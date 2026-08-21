package matrix

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/service"
)

type aliasRequest struct {
	RoomID string `json:"room_id"`
}

type visibilityRequest struct {
	Visibility string `json:"visibility"`
}

type publicRoomsRequest struct {
	Limit  int `json:"limit"`
	Filter struct {
		GenericSearchTerm string `json:"generic_search_term"`
	} `json:"filter"`
}

func aliasParam(r *http.Request) string {
	raw := chi.URLParam(r, "roomAlias")
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}

func (h *Handler) resolveAlias(w http.ResponseWriter, r *http.Request) {
	tenant, _, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	found, err := h.rooms.ResolveAlias(r.Context(), tenant.Scope(), aliasParam(r))
	if err != nil {
		writeRoomError(r, w, err, "Could not resolve the alias")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"room_id": found.RoomID,
		"servers": []string{tenant.ServerName},
	})
}

func (h *Handler) createAlias(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in aliasRequest
	if !readJSON(w, r, &in) {
		return
	}
	if in.RoomID == "" {
		writeError(w, http.StatusBadRequest, codeMissingParam, "Missing room_id")
		return
	}
	if err := h.rooms.CreateAlias(r.Context(), tenant.Scope(), caller.UserID, aliasParam(r), in.RoomID); err != nil {
		writeRoomError(r, w, err, "Could not create the alias")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) deleteAlias(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	if err := h.rooms.DeleteAlias(r.Context(), tenant.Scope(), caller.UserID, aliasParam(r)); err != nil {
		writeRoomError(r, w, err, "Could not delete the alias")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) getVisibility(w http.ResponseWriter, r *http.Request) {
	tenant, _, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	visibility, err := h.rooms.Visibility(r.Context(), tenant.Scope(), chi.URLParam(r, "roomID"))
	if err != nil {
		writeVisibilityError(r, w, err, "Could not read the room visibility")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"visibility": visibility})
}

func (h *Handler) setVisibility(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var in visibilityRequest
	if !readJSON(w, r, &in) {
		return
	}
	err := h.rooms.SetVisibility(r.Context(), tenant.Scope(), caller.UserID, chi.URLParam(r, "roomID"), in.Visibility)
	if err != nil {
		writeVisibilityError(r, w, err, "Could not set the room visibility")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) publicRooms(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	h.writePublicRooms(w, r, service.PublicRoomsFilter{Limit: limit})
}

func (h *Handler) searchPublicRooms(w http.ResponseWriter, r *http.Request) {
	var in publicRoomsRequest
	if !readJSON(w, r, &in) {
		return
	}
	h.writePublicRooms(w, r, service.PublicRoomsFilter{
		SearchTerm: in.Filter.GenericSearchTerm,
		Limit:      in.Limit,
	})
}

func (h *Handler) writePublicRooms(w http.ResponseWriter, r *http.Request, filter service.PublicRoomsFilter) {
	tenant, _, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	found, err := h.rooms.PublicRooms(r.Context(), tenant.Scope(), filter)
	if err != nil {
		writeRoomError(r, w, err, "Could not list the public rooms")
		return
	}

	chunk := make([]map[string]any, 0, len(found.Chunk))
	for _, room := range found.Chunk {
		entry := map[string]any{
			"room_id":            room.RoomID,
			"num_joined_members": room.NumJoinedMembers,
			"world_readable":     room.WorldReadable,
			"guest_can_join":     room.GuestCanJoin,
			"join_rule":          room.JoinRule,
		}
		for key, value := range map[string]string{
			"name":            room.Name,
			"topic":           room.Topic,
			"canonical_alias": room.CanonicalAlias,
			"avatar_url":      room.AvatarURL,
			"room_type":       room.RoomType,
		} {
			if value != "" {
				entry[key] = value
			}
		}
		chunk = append(chunk, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chunk":                     chunk,
		"total_room_count_estimate": found.TotalRooms,
	})
}

func writeVisibilityError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, entity.ErrRoomNotFound) {
		writeError(w, http.StatusNotFound, codeNotFound, "Unknown room")
		return
	}
	writeRoomError(r, w, err, msg)
}
