package matrix

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var content map[string]any
	if !readJSON(w, r, &content) {
		return
	}
	eventID, err := h.rooms.SendMessage(r.Context(), tenant.Scope(), entity.NewMessage{
		RoomID:   roomParam(r, "roomID"),
		Type:     chi.URLParam(r, "eventType"),
		Sender:   caller.UserID,
		DeviceID: caller.DeviceID,
		TxnID:    chi.URLParam(r, "txnID"),
		Content:  content,
	})
	if err != nil {
		writeRoomError(r, w, err, "Could not send the event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_id": eventID})
}

func (h *Handler) roomEvent(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	found, err := h.rooms.Event(r.Context(), tenant.Scope(), caller.UserID, caller.DeviceID,
		roomParam(r, "roomID"), roomParam(r, "eventID"))
	if err != nil {
		writeRoomError(r, w, err, "Could not read the event")
		return
	}
	raw, err := found.JSON()
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the event", err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}
