package matrix

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

type authDict struct {
	Type       string     `json:"type"`
	Session    string     `json:"session"`
	Password   string     `json:"password"`
	User       string     `json:"user"`
	Identifier identifier `json:"identifier"`
}

func (a authDict) subject() string {
	if a.Identifier.User != "" {
		return a.Identifier.User
	}
	return a.User
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

func (h *Handler) requireUIA(w http.ResponseWriter, r *http.Request, auth *authDict, tenant entity.Tenant, kind entity.UIAKind, userID string) (entity.UIASession, bool) {
	if auth == nil {
		session, err := h.users.BeginAuth(r.Context(), tenant.Scope(), kind, userID)
		if err != nil {
			writeInternal(r.Context(), w, "Could not start authentication", err)
			return entity.UIASession{}, false
		}
		h.writeChallenge(w, session, "", "")
		return entity.UIASession{}, false
	}

	// A stage that carries its own evidence, such as a password, needs no session: answering it
	// is proof in itself. The dummy stage proves nothing, so there the session is the only
	// evidence the client went through the flow at all, and it is required.
	var sessionID uuid.UUID
	switch {
	case auth.Session == "" && auth.Type != entity.LoginTypeDummy && auth.Type != "":
		opened, err := h.users.BeginAuth(r.Context(), tenant.Scope(), kind, userID)
		if err != nil {
			writeInternal(r.Context(), w, "Could not start authentication", err)
			return entity.UIASession{}, false
		}
		sessionID = opened.ID
	case auth.Session == "":
		opened, err := h.users.BeginAuth(r.Context(), tenant.Scope(), kind, userID)
		if err != nil {
			writeInternal(r.Context(), w, "Could not start authentication", err)
			return entity.UIASession{}, false
		}
		h.writeChallenge(w, opened, "", "")
		return entity.UIASession{}, false
	default:
		parsed, err := uuid.Parse(auth.Session)
		if err != nil {
			writeError(w, http.StatusUnauthorized, codeForbidden, "Unknown authentication session")
			return entity.UIASession{}, false
		}
		sessionID = parsed
	}

	stage := auth.Type
	if stage == "" {
		stage = kind.Stages()[0]
	}
	session, err := h.users.SubmitAuth(r.Context(), tenant.Scope(), kind, sessionID, stage, auth.subject(), auth.Password)
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
