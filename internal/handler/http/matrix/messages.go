package matrix

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (h *Handler) roomMessages(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	found, err := h.rooms.Messages(r.Context(), tenant.Scope(), caller.UserID, caller.DeviceID,
		entity.MessagesRequest{
			RoomID:    roomParam(r, "roomID"),
			Direction: query.Get("dir"),
			From:      query.Get("from"),
			To:        query.Get("to"),
			Limit:     optionalInt(query.Get("limit")),
			Filter:    query.Get("filter"),
		})
	if err != nil {
		writeRoomError(r, w, err, "Could not read the room")
		return
	}

	chunk, err := renderEvents(found.Chunk)
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the timeline", err)
		return
	}
	body := map[string]any{"chunk": chunk, "start": found.Start}
	if found.End != "" {
		body["end"] = found.End
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) roomContext(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	found, err := h.rooms.Context(r.Context(), tenant.Scope(), caller.UserID, caller.DeviceID,
		entity.ContextRequest{
			RoomID:  roomParam(r, "roomID"),
			EventID: roomParam(r, "eventID"),
			Limit:   optionalInt(query.Get("limit")),
			Filter:  query.Get("filter"),
		})
	if err != nil {
		writeRoomError(r, w, err, "Could not read around the event")
		return
	}

	event, err := found.Event.JSON()
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the event", err)
		return
	}
	before, err := renderEvents(found.Before)
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the timeline", err)
		return
	}
	after, err := renderEvents(found.After)
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the timeline", err)
		return
	}
	state, err := entity.ClientEvents(found.State, h.clock().UTC().UnixMilli())
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the state", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"event":         json.RawMessage(event),
		"events_before": before,
		"events_after":  after,
		"state":         state,
		"start":         found.Start,
		"end":           found.End,
	})
}

func renderEvents(events []entity.ClientEvent) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(events))
	for _, e := range events {
		raw, err := e.JSON()
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

func optionalInt(raw string) *int {
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &value
}
