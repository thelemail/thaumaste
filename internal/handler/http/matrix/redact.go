package matrix

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (h *Handler) redactEvent(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &body) {
		return
	}

	eventID, err := h.rooms.Redact(r.Context(), tenant.Scope(), entity.NewRedaction{
		RoomID:   roomParam(r, "roomID"),
		EventID:  roomParam(r, "eventID"),
		Sender:   caller.UserID,
		DeviceID: caller.DeviceID,
		TxnID:    chi.URLParam(r, "txnID"),
		Reason:   body.Reason,
	})
	if err != nil {
		writeRoomError(r, w, err, "Could not redact the event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_id": eventID})
}
