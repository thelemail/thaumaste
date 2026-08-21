package matrix

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

type authDict struct {
	Type     string `json:"type"`
	Session  string `json:"session"`
	Password string `json:"password"`
	User     string `json:"user"`
}

type flow struct {
	Stages []string `json:"stages"`
}

type uiaChallenge struct {
	Flows     []flow         `json:"flows"`
	Params    map[string]any `json:"params"`
	Session   string         `json:"session"`
	Completed []string       `json:"completed,omitempty"`
	ErrCode   errorCode      `json:"errcode,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// requireUIA drives one interactive stage and reports whether the caller may proceed. A request
// carrying no auth is always challenged, even when the only stage is the dummy one: the spec is
// explicit that such a server "must still give a 401 response to requests with no auth data".
func (h *Handler) requireUIA(w http.ResponseWriter, r *http.Request, auth *authDict, tenant entity.Tenant, kind entity.UIAKind, userID string) (entity.UIASession, bool) {
	if auth == nil || auth.Session == "" {
		session, err := h.users.BeginAuth(r.Context(), tenant.Scope(), kind, userID)
		if err != nil {
			writeInternal(r.Context(), w, "Could not start authentication", err)
			return entity.UIASession{}, false
		}
		h.writeChallenge(w, session, "", "")
		return entity.UIASession{}, false
	}

	sessionID, err := uuid.Parse(auth.Session)
	if err != nil {
		writeError(w, http.StatusUnauthorized, codeForbidden, "Unknown authentication session")
		return entity.UIASession{}, false
	}

	stage := auth.Type
	if stage == "" {
		stage = kind.Stages()[0]
	}
	session, err := h.users.SubmitAuth(r.Context(), tenant.Scope(), kind, sessionID, stage, auth.User, auth.Password)
	switch {
	case err == nil:
	case errors.Is(err, entity.ErrUIASessionNotFound):
		writeError(w, http.StatusUnauthorized, codeForbidden, "Unknown authentication session")
		return entity.UIASession{}, false
	case errors.Is(err, entity.ErrUIAStageUnknown):
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Unknown authentication stage")
		return entity.UIASession{}, false
	case errors.Is(err, entity.ErrBadCredentials):
		reloaded, loadErr := h.users.BeginAuth(r.Context(), tenant.Scope(), kind, userID)
		if loadErr != nil {
			writeInternal(r.Context(), w, "Could not start authentication", loadErr)
			return entity.UIASession{}, false
		}
		h.writeChallenge(w, reloaded, codeForbidden, "Invalid password")
		return entity.UIASession{}, false
	default:
		writeInternal(r.Context(), w, "Could not check authentication", err)
		return entity.UIASession{}, false
	}

	if !session.Done() {
		h.writeChallenge(w, session, "", "")
		return entity.UIASession{}, false
	}
	return session, true
}

func (h *Handler) writeChallenge(w http.ResponseWriter, session entity.UIASession, code errorCode, message string) {
	body := uiaChallenge{
		Flows:     []flow{{Stages: session.Kind.Stages()}},
		Params:    map[string]any{},
		Session:   session.ID.String(),
		Completed: session.Completed,
		ErrCode:   code,
		Error:     message,
	}
	writeJSON(w, http.StatusUnauthorized, body)
}
