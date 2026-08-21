package matrix

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (h *Handler) roomRelations(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	direction := query.Get("dir")
	if direction == "" {
		direction = entity.DirectionBackward
	}

	found, err := h.rooms.Relations(r.Context(), tenant.Scope(), caller.UserID, caller.DeviceID,
		entity.RelationsRequest{
			RoomID:    roomParam(r, "roomID"),
			EventID:   roomParam(r, "eventID"),
			RelType:   chi.URLParam(r, "relType"),
			EventType: chi.URLParam(r, "eventType"),
			Direction: direction,
			From:      query.Get("from"),
			To:        query.Get("to"),
			Limit:     optionalInt(query.Get("limit")),
			Recurse:   query.Get("recurse") == "true",
		})
	if err != nil {
		writeRelationError(r, w, err, "Could not read the relations")
		return
	}

	chunk, err := renderEvents(found.Chunk)
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the relations", err)
		return
	}
	body := map[string]any{"chunk": chunk}
	if found.NextBatch != "" {
		body["next_batch"] = found.NextBatch
	}
	if found.PrevBatch != "" {
		body["prev_batch"] = found.PrevBatch
	}
	if found.Depth != nil {
		body["recursion_depth"] = *found.Depth
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) roomThreads(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	include := query.Get("include")
	if include == "" {
		include = entity.ThreadsAll
	}

	found, err := h.rooms.Threads(r.Context(), tenant.Scope(), caller.UserID, caller.DeviceID,
		entity.ThreadsRequest{
			RoomID:  roomParam(r, "roomID"),
			Include: include,
			From:    query.Get("from"),
			Limit:   optionalInt(query.Get("limit")),
		})
	if err != nil {
		writeRoomError(r, w, err, "Could not list the threads")
		return
	}

	chunk, err := renderEvents(found.Chunk)
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the threads", err)
		return
	}
	body := map[string]any{"chunk": chunk}
	if found.NextBatch != "" {
		body["next_batch"] = found.NextBatch
	}
	writeJSON(w, http.StatusOK, body)
}

func writeRelationError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, entity.ErrEventNotFound) {
		writeError(w, http.StatusNotFound, codeNotFound, "Event not found")
		return
	}
	writeRoomError(r, w, err, msg)
}
