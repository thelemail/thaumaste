package matrix

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/service"
)

type joinRequest struct {
	Reason      string  `json:"reason"`
	DisplayName *string `json:"displayname"`
	AvatarURL   *string `json:"avatar_url"`
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

type targetRequest struct {
	UserID string `json:"user_id"`
	Reason string `json:"reason"`
}

type targetAction func(ctx context.Context, scope entity.TenantScope, caller, roomID, target, reason string) error

func roomParam(r *http.Request, name string) string {
	raw := chi.URLParam(r, name)
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}

func (h *Handler) joinRoom(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in joinRequest
	if !readJSON(w, r, &in) {
		return
	}
	roomID, err := h.rooms.Join(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomIDOrAlias"),
		service.JoinInput{DisplayName: in.DisplayName, AvatarURL: in.AvatarURL, Reason: in.Reason})
	if err != nil {
		writeRoomError(r, w, err, "Could not join the room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room_id": roomID})
}

func (h *Handler) knockRoom(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in reasonRequest
	if !readJSON(w, r, &in) {
		return
	}
	roomID, err := h.rooms.Knock(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomIDOrAlias"), in.Reason)
	if err != nil {
		writeRoomError(r, w, err, "Could not knock on the room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room_id": roomID})
}

func (h *Handler) leaveRoom(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in reasonRequest
	if !readJSON(w, r, &in) {
		return
	}
	if err := h.rooms.Leave(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomID"), in.Reason); err != nil {
		writeRoomError(r, w, err, "Could not leave the room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) forgetRoom(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	if err := h.rooms.Forget(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomID")); err != nil {
		writeRoomError(r, w, err, "Could not forget the room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) inviteUser(w http.ResponseWriter, r *http.Request) {
	h.actOnTarget(w, r, h.rooms.Invite, "Could not invite the user")
}

func (h *Handler) kickUser(w http.ResponseWriter, r *http.Request) {
	h.actOnTarget(w, r, h.rooms.Kick, "Could not kick the user")
}

func (h *Handler) banUser(w http.ResponseWriter, r *http.Request) {
	h.actOnTarget(w, r, h.rooms.Ban, "Could not ban the user")
}

func (h *Handler) unbanUser(w http.ResponseWriter, r *http.Request) {
	h.actOnTarget(w, r, h.rooms.Unban, "Could not unban the user")
}

func (h *Handler) actOnTarget(w http.ResponseWriter, r *http.Request, act targetAction, msg string) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in targetRequest
	if !readJSON(w, r, &in) {
		return
	}
	if in.UserID == "" {
		writeError(w, http.StatusBadRequest, codeMissingParam, "Missing user_id")
		return
	}
	if err := act(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomID"), in.UserID, in.Reason); err != nil {
		writeRoomError(r, w, err, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	found, err := h.rooms.Members(r.Context(), tenant.Scope(), caller.UserID, roomParam(r, "roomID"),
		entity.MembersFilter{
			Membership:    query.Get("membership"),
			NotMembership: query.Get("not_membership"),
			At:            query.Get("at"),
		})
	if err != nil {
		writeRoomError(r, w, err, "Could not list the members")
		return
	}
	chunk, err := entity.ClientEvents(found, h.clock().UTC().UnixMilli())
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the members", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chunk": chunk})
}
