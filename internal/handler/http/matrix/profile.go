package matrix

import (
	"errors"
	"net/http"

	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/entity"
)

type profileResponse struct {
	DisplayName string `json:"displayname,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := h.profileTarget(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, profileResponse{DisplayName: user.DisplayName, AvatarURL: user.AvatarURL})
}

func (h *Handler) getProfileField(w http.ResponseWriter, r *http.Request) {
	user, ok := h.profileTarget(w, r)
	if !ok {
		return
	}
	key := chi.URLParam(r, "keyName")
	value, ok := profileField(user, key)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "Unknown profile field")
		return
	}
	if value == "" {
		writeError(w, http.StatusNotFound, codeNotFound, "This profile field is not set")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{key: value})
}

func (h *Handler) setProfileField(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	key := chi.URLParam(r, "keyName")
	if key != "displayname" && key != "avatar_url" {
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Unknown profile field")
		return
	}

	var body map[string]any
	if !readJSON(w, r, &body) {
		return
	}
	value, present := body[key]
	if !present {
		writeError(w, http.StatusBadRequest, codeMissingParam, "Missing "+key)
		return
	}
	text, isText := value.(string)
	if !isText {
		writeError(w, http.StatusBadRequest, codeInvalidParam, key+" must be a string")
		return
	}

	var in entity.UpdateProfile
	if key == "displayname" {
		in.DisplayName = &text
	} else {
		in.AvatarURL = &text
	}

	h.applyProfile(w, r, tenant, caller.UserID, roomParam(r, "userID"), in)
}

func (h *Handler) setProfile(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body struct {
		DisplayName *string `json:"displayname"`
		AvatarURL   *string `json:"avatar_url"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	h.applyProfile(w, r, tenant, caller.UserID,
		roomParam(r, "userID"), entity.UpdateProfile{DisplayName: body.DisplayName, AvatarURL: body.AvatarURL})
}

func (h *Handler) applyProfile(w http.ResponseWriter, r *http.Request, tenant entity.Tenant,
	caller, target string, in entity.UpdateProfile,
) {
	if _, err := h.users.UpdateProfile(r.Context(), tenant.Scope(), caller, target, in); err != nil {
		switch {
		case errors.Is(err, entity.ErrProfileNotAllowed):
			writeError(w, http.StatusForbidden, codeForbidden, "Cannot change another user's profile")
		case errors.Is(err, entity.ErrUserNotFound):
			writeError(w, http.StatusNotFound, codeNotFound, "Unknown user")
		case errors.As(err, &validation.Errors{}):
			writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
		default:
			writeInternal(r.Context(), w, "Could not update the profile", err)
		}
		return
	}
	if err := h.rooms.PropagateProfile(r.Context(), tenant.Scope(), target); err != nil {
		writeInternal(r.Context(), w, "Could not tell the rooms about the profile", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) profileTarget(w http.ResponseWriter, r *http.Request) (entity.User, bool) {
	tenant, ok := tenantFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return entity.User{}, false
	}
	user, err := h.users.Get(r.Context(), tenant.Scope(), chi.URLParam(r, "userID"))
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "Unknown user")
			return entity.User{}, false
		}
		writeInternal(r.Context(), w, "Could not read the profile", err)
		return entity.User{}, false
	}
	return user, true
}

func profileField(user entity.User, key string) (string, bool) {
	switch key {
	case "displayname":
		return user.DisplayName, true
	case "avatar_url":
		return user.AvatarURL, true
	default:
		return "", false
	}
}
