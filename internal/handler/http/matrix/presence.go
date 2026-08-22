package matrix

import (
	"errors"
	"net/http"

	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

type presenceRequest struct {
	Presence  string `json:"presence"`
	StatusMsg string `json:"status_msg"`
}

type presenceResponse struct {
	Presence        string `json:"presence"`
	StatusMsg       string `json:"status_msg,omitempty"`
	LastActiveAgo   int64  `json:"last_active_ago,omitempty"`
	CurrentlyActive bool   `json:"currently_active,omitempty"`
}

func (h *Handler) setPresence(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body presenceRequest
	if !readJSON(w, r, &body) {
		return
	}

	err := h.presence.Set(r.Context(), tenant, caller.UserID, roomParam(r, "userID"),
		body.Presence, body.StatusMsg)
	if err != nil {
		writePresenceError(r, w, err, "Could not set the presence")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) getPresence(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	found, err := h.presence.Get(r.Context(), tenant, caller.UserID, roomParam(r, "userID"))
	if err != nil {
		writePresenceError(r, w, err, "Could not read the presence")
		return
	}

	out := presenceResponse{Presence: found.Currently(), StatusMsg: found.StatusMsg}
	if !found.LastActiveAt.IsZero() {
		out.LastActiveAgo = h.clock().UTC().Sub(found.LastActiveAt).Milliseconds()
		out.CurrentlyActive = found.State == entity.PresenceOnline
	}
	writeJSON(w, http.StatusOK, out)
}

func writePresenceError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	switch {
	case errors.Is(err, entity.ErrPresenceForeign):
		writeError(w, http.StatusForbidden, codeForbidden, err.Error())
	case errors.Is(err, entity.ErrPresenceUnknown):
		writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
	case errors.As(err, &validation.Errors{}):
		writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
	default:
		writeInternal(r.Context(), w, msg, err)
	}
}
