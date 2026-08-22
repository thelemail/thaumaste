package matrix

import (
	"errors"
	"net/http"

	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

type typingRequest struct {
	Typing  bool `json:"typing"`
	Timeout int  `json:"timeout"`
}

func (h *Handler) setTyping(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body typingRequest
	if !readJSON(w, r, &body) {
		return
	}

	err := h.typing.Set(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "userID"),
		roomParam(r, "roomID"), body.Typing, body.Timeout)
	if err != nil {
		switch {
		case errors.Is(err, entity.ErrProfileNotAllowed):
			writeError(w, http.StatusForbidden, codeForbidden, "Cannot set another user's typing state")
		case errors.Is(err, entity.ErrNotInRoom):
			writeError(w, http.StatusForbidden, codeForbidden, err.Error())
		case errors.Is(err, entity.ErrRoomNotFound):
			writeError(w, http.StatusNotFound, codeNotFound, err.Error())
		case errors.Is(err, entity.ErrTypingTimeout):
			writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
		case errors.As(err, &validation.Errors{}):
			writeError(w, http.StatusBadRequest, codeBadJSON, err.Error())
		default:
			writeInternal(r.Context(), w, "Could not set the typing state", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
