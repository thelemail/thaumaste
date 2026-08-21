package matrix

import (
	"errors"
	"net/http"
	"strings"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/service"
)

type registerRequest struct {
	Auth                     *authDict `json:"auth"`
	Username                 string    `json:"username"`
	Password                 string    `json:"password"`
	DeviceID                 string    `json:"device_id"`
	InitialDeviceDisplayName string    `json:"initial_device_display_name"`
	InhibitLogin             bool      `json:"inhibit_login"`
	RefreshToken             bool      `json:"refresh_token"`
}

type sessionResponse struct {
	UserID       string `json:"user_id"`
	AccessToken  string `json:"access_token,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresInMS  int64  `json:"expires_in_ms,omitempty"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return
	}
	if kind := r.URL.Query().Get("kind"); kind == "guest" {
		writeError(w, http.StatusForbidden, codeForbidden, "Guest accounts are not supported")
		return
	}

	var in registerRequest
	if !readJSON(w, r, &in) {
		return
	}

	// The username is judged before the challenge is issued. The spec requires this order, and a
	// client that re-posts without auth must still be told the name is taken rather than re-asked.
	if in.Username != "" {
		if err := h.users.CheckUsername(r.Context(), tenant.Scope(), in.Username); err != nil {
			writeRegisterError(w, err)
			return
		}
	}

	if _, done := h.requireUIA(w, r, in.Auth, tenant, entity.UIAKindRegister, ""); !done {
		return
	}

	user, session, err := h.users.Register(r.Context(), tenant.Scope(), service.RegisterInput{
		Username:                 in.Username,
		Password:                 in.Password,
		DeviceID:                 in.DeviceID,
		InitialDeviceDisplayName: in.InitialDeviceDisplayName,
		InhibitLogin:             in.InhibitLogin,
		WithRefreshToken:         in.RefreshToken,
	})
	if err != nil {
		writeRegisterError(w, err)
		return
	}

	out := sessionResponse{UserID: user.UserID}
	if !in.InhibitLogin {
		out.AccessToken = session.AccessToken
		out.DeviceID = session.DeviceID
		out.RefreshToken = session.RefreshToken
		out.ExpiresInMS = session.ExpiresIn.Milliseconds()
	}
	writeJSON(w, http.StatusOK, out)
}

func writeRegisterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entity.ErrUserInUse):
		writeError(w, http.StatusBadRequest, codeUserInUse, "Desired user ID is already taken")
	case errors.Is(err, entity.ErrInvalidUsername), errors.Is(err, entity.ErrInvalidServerName):
		writeError(w, http.StatusBadRequest, codeInvalidUser, "Desired user ID is not a valid user name")
	case errors.Is(err, entity.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, codeWeakPassword, "Password is too short")
	case errors.Is(err, entity.ErrRegistrationShut):
		writeError(w, http.StatusForbidden, codeForbidden, "Registration is disabled")
	default:
		writeError(w, http.StatusBadRequest, codeUnknown, "Could not register the account")
	}
}

func (h *Handler) registerAvailable(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return
	}
	username := r.URL.Query().Get("username")
	if username == "" {
		writeError(w, http.StatusBadRequest, codeMissingParam, "Missing username")
		return
	}
	if err := h.users.CheckUsername(r.Context(), tenant.Scope(), username); err != nil {
		writeRegisterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true})
}

type passwordRequest struct {
	Auth          *authDict `json:"auth"`
	NewPassword   string    `json:"new_password"`
	LogoutDevices *bool     `json:"logout_devices"`
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var in passwordRequest
	if !readJSON(w, r, &in) {
		return
	}
	if _, done := h.requireUIA(w, r, in.Auth, tenant, entity.UIAKindPassword, caller.UserID); !done {
		return
	}

	logout := true
	if in.LogoutDevices != nil {
		logout = *in.LogoutDevices
	}
	if err := h.users.ChangePassword(r.Context(), tenant.Scope(), caller, in.NewPassword, logout); err != nil {
		if errors.Is(err, entity.ErrWeakPassword) {
			writeError(w, http.StatusBadRequest, codeWeakPassword, "Password is too short")
			return
		}
		writeInternal(r.Context(), w, "Could not change the password", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

type deactivateRequest struct {
	Auth *authDict `json:"auth"`
}

func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var in deactivateRequest
	if !readJSON(w, r, &in) {
		return
	}
	if _, done := h.requireUIA(w, r, in.Auth, tenant, entity.UIAKindDeactivate, caller.UserID); !done {
		return
	}

	if err := h.users.Deactivate(r.Context(), tenant.Scope(), caller.UserID); err != nil {
		writeInternal(r.Context(), w, "Could not deactivate the account", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id_server_unbind_result": "no-support"})
}

func (h *Handler) callerAndTenant(w http.ResponseWriter, r *http.Request) (entity.Tenant, entity.AccessToken, bool) {
	tenant, ok := tenantFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return entity.Tenant{}, entity.AccessToken{}, false
	}
	caller, ok := callerFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, codeMissingToken, "Missing access token")
		return entity.Tenant{}, entity.AccessToken{}, false
	}
	return tenant, caller, true
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		return strings.TrimSpace(first)
	}
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}
