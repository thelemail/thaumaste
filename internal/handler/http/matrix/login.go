package matrix

import (
	"errors"
	"net/http"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/service"
)

type identifier struct {
	Type string `json:"type"`
	User string `json:"user"`
}

type loginRequest struct {
	Type                     string     `json:"type"`
	Identifier               identifier `json:"identifier"`
	User                     string     `json:"user"`
	Password                 string     `json:"password"`
	Token                    string     `json:"token"`
	DeviceID                 string     `json:"device_id"`
	InitialDeviceDisplayName string     `json:"initial_device_display_name"`
	RefreshToken             bool       `json:"refresh_token"`
}

type loginFlow struct {
	Type string `json:"type"`
}

func (h *Handler) loginFlows(w http.ResponseWriter, r *http.Request) {
	types := h.users.LoginFlows(r.Context())
	flows := make([]loginFlow, 0, len(types))
	for _, t := range types {
		flows = append(flows, loginFlow{Type: t})
	}
	writeJSON(w, http.StatusOK, map[string]any{"flows": flows})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return
	}

	var in loginRequest
	if !readJSON(w, r, &in) {
		return
	}
	if in.Type == "" {
		writeError(w, http.StatusBadRequest, codeUnknown, "Bad login type")
		return
	}

	// The deprecated top-level `user` still arrives from older clients, and the identifier object
	// supersedes it when both are present.
	subject := in.User
	if in.Identifier.User != "" {
		if in.Identifier.Type != "" && in.Identifier.Type != entity.IdentifierTypeUser {
			writeError(w, http.StatusForbidden, codeForbidden, "Unsupported identifier type")
			return
		}
		subject = in.Identifier.User
	}

	guard := subject
	if guard == "" {
		guard = clientIP(r)
	}
	if err := h.users.GuardAttempt(r.Context(), tenant.Scope(), guard, entity.AttemptLogin); err != nil {
		writeRateLimited(w, h.users.RetryAfter(r.Context(), tenant.Scope(), guard, entity.AttemptLogin))
		return
	}

	session, err := h.users.Login(r.Context(), tenant.Scope(), service.LoginInput{
		Type:                     in.Type,
		Identifier:               subject,
		Password:                 in.Password,
		Token:                    in.Token,
		DeviceID:                 in.DeviceID,
		InitialDeviceDisplayName: in.InitialDeviceDisplayName,
		WithRefreshToken:         in.RefreshToken,
	})
	switch {
	case err == nil:
	case errors.Is(err, entity.ErrUserDeactivated):
		writeError(w, http.StatusForbidden, codeDeactivated, "This account has been deactivated")
		return
	case errors.Is(err, entity.ErrBadCredentials), errors.Is(err, entity.ErrUserNotFound),
		errors.Is(err, entity.ErrRegistrationShut):
		if recordErr := h.users.RecordFailure(r.Context(), tenant.Scope(), guard, entity.AttemptLogin); recordErr != nil {
			writeInternal(r.Context(), w, "Could not record the failed attempt", recordErr)
			return
		}
		writeError(w, http.StatusForbidden, codeForbidden, "Invalid username or password")
		return
	default:
		writeInternal(r.Context(), w, "Could not complete the login", err)
		return
	}

	if err := h.users.ClearFailures(r.Context(), tenant.Scope(), guard, entity.AttemptLogin); err != nil {
		writeInternal(r.Context(), w, "Could not clear the failed attempts", err)
		return
	}

	writeJSON(w, http.StatusOK, sessionResponse{
		UserID:       session.UserID,
		AccessToken:  session.AccessToken,
		DeviceID:     session.DeviceID,
		RefreshToken: session.RefreshToken,
		ExpiresInMS:  session.ExpiresIn.Milliseconds(),
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return
	}

	var in refreshRequest
	if !readJSON(w, r, &in) {
		return
	}
	if in.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, codeMissingParam, "Missing refresh_token")
		return
	}

	session, err := h.users.Refresh(r.Context(), tenant.Scope(), in.RefreshToken)
	switch {
	case err == nil:
	case errors.Is(err, entity.ErrRefreshTokenNotFound), errors.Is(err, entity.ErrRefreshTokenUsed):
		writeUnknownToken(w, "Unknown refresh token")
		return
	default:
		writeInternal(r.Context(), w, "Could not refresh the token", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
		"expires_in_ms": session.ExpiresIn.Milliseconds(),
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	if err := h.users.Logout(r.Context(), tenant.Scope(), caller); err != nil {
		writeInternal(r.Context(), w, "Could not log out", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	if err := h.users.LogoutAll(r.Context(), tenant.Scope(), caller.UserID); err != nil {
		writeInternal(r.Context(), w, "Could not log out", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}
