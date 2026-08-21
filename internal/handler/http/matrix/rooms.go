package matrix

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

type initialStateRequest struct {
	Type     string         `json:"type"`
	StateKey string         `json:"state_key"`
	Content  map[string]any `json:"content"`
}

type createRoomRequest struct {
	Visibility                string                `json:"visibility"`
	RoomAliasName             string                `json:"room_alias_name"`
	Name                      string                `json:"name"`
	Topic                     string                `json:"topic"`
	Invite                    []string              `json:"invite"`
	RoomVersion               json.RawMessage       `json:"room_version"`
	CreationContent           map[string]any        `json:"creation_content"`
	InitialState              []initialStateRequest `json:"initial_state"`
	Preset                    string                `json:"preset"`
	IsDirect                  bool                  `json:"is_direct"`
	PowerLevelContentOverride map[string]any        `json:"power_level_content_override"`
}

func (h *Handler) createRoom(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var in createRoomRequest
	if !readJSON(w, r, &in) {
		return
	}

	version := entity.DefaultRoomVersion
	if len(in.RoomVersion) > 0 {
		var named string
		if err := json.Unmarshal(in.RoomVersion, &named); err != nil {
			writeError(w, http.StatusBadRequest, codeBadJSON, "room_version must be a string")
			return
		}
		if _, err := entity.LookupRoomVersion(entity.RoomVersionID(named)); err != nil {
			writeError(w, http.StatusBadRequest, codeBadRoomVer, "Unsupported room version")
			return
		}
		version = entity.RoomVersionID(named)
	}

	visibility := in.Visibility
	if visibility == "" {
		visibility = entity.VisibilityPrivate
	}

	initial := make([]entity.InitialState, 0, len(in.InitialState))
	for _, item := range in.InitialState {
		initial = append(initial, entity.InitialState{
			Type: item.Type, StateKey: item.StateKey, Content: item.Content,
		})
	}

	room, err := h.rooms.Create(r.Context(), tenant.Scope(), entity.NewRoomRequest{
		Creator:                   caller.UserID,
		Version:                   version,
		Visibility:                visibility,
		Preset:                    in.Preset,
		AliasLocalpart:            in.RoomAliasName,
		Name:                      in.Name,
		Topic:                     in.Topic,
		Invite:                    in.Invite,
		CreationContent:           in.CreationContent,
		InitialState:              initial,
		PowerLevelContentOverride: in.PowerLevelContentOverride,
	})
	if err != nil {
		writeRoomError(r, w, err, "Could not create the room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room_id": room.RoomID})
}

func (h *Handler) roomState(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	state, err := h.rooms.State(r.Context(), tenant.Scope(), caller.UserID, chi.URLParam(r, "roomID"))
	if err != nil {
		writeRoomError(r, w, err, "Could not read the room state")
		return
	}
	out := make([]json.RawMessage, 0, len(state))
	for _, e := range state {
		out = append(out, e.JSON())
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) roomStateEvent(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	e, err := h.rooms.StateEvent(r.Context(), tenant.Scope(), caller.UserID,
		chi.URLParam(r, "roomID"), chi.URLParam(r, "eventType"), chi.URLParam(r, "stateKey"))
	if err != nil {
		writeRoomError(r, w, err, "Could not read the state event")
		return
	}
	if r.URL.Query().Get("format") == "event" {
		writeJSON(w, http.StatusOK, json.RawMessage(e.JSON()))
		return
	}
	writeJSON(w, http.StatusOK, e.Content())
}

func (h *Handler) setRoomState(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var content map[string]any
	if !readJSON(w, r, &content) {
		return
	}
	stateKey := chi.URLParam(r, "stateKey")
	eventID, err := h.rooms.SetState(r.Context(), tenant.Scope(), entity.NewEvent{
		RoomID:   chi.URLParam(r, "roomID"),
		Type:     chi.URLParam(r, "eventType"),
		StateKey: &stateKey,
		Sender:   caller.UserID,
		Content:  content,
	})
	if err != nil {
		writeRoomError(r, w, err, "Could not set the state event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_id": eventID})
}

func (h *Handler) joinedMembers(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	members, err := h.rooms.JoinedMembers(r.Context(), tenant.Scope(), caller.UserID, chi.URLParam(r, "roomID"))
	if err != nil {
		writeRoomError(r, w, err, "Could not list the members")
		return
	}
	joined := make(map[string]any, len(members))
	for _, m := range members {
		joined[m.UserID] = map[string]any{
			"display_name": m.DisplayName,
			"avatar_url":   m.AvatarURL,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"joined": joined})
}

func (h *Handler) joinedRooms(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	rooms, err := h.rooms.JoinedRooms(r.Context(), tenant.Scope(), caller.UserID)
	if err != nil {
		writeRoomError(r, w, err, "Could not list the rooms")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"joined_rooms": rooms})
}

func (h *Handler) roomAliases(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	aliases, err := h.rooms.Aliases(r.Context(), tenant.Scope(), caller.UserID, chi.URLParam(r, "roomID"))
	if err != nil {
		writeRoomError(r, w, err, "Could not list the aliases")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"aliases": aliases})
}

func writeForbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, codeForbidden, "You are not allowed to do that")
}

func writeRoomError(r *http.Request, w http.ResponseWriter, err error, msg string) {
	var limited entity.RateLimited
	if errors.As(err, &limited) {
		writeRateLimited(w, limited.RetryAfter)
		return
	}
	switch {
	case errors.Is(err, entity.ErrRoomNotFound), errors.Is(err, entity.ErrNotInRoom),
		errors.Is(err, entity.ErrAuthFailed), errors.Is(err, entity.ErrForeignUser):
		writeForbidden(w)
	case errors.Is(err, entity.ErrEventNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "Unknown state event")
	case errors.Is(err, entity.ErrCannotGrantJoin):
		writeError(w, http.StatusForbidden, codeCannotGrant, "No local user can authorise this join")
	case errors.Is(err, entity.ErrNotBanned), errors.Is(err, entity.ErrNotForgettable):
		writeError(w, http.StatusBadRequest, codeBadState, "The room is not in a state that allows that")
	case errors.Is(err, entity.ErrUnknownMembership), errors.Is(err, entity.ErrPointInTime):
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Unsupported membership query")
	case errors.Is(err, entity.ErrBadToken), errors.Is(err, entity.ErrBadDirection),
		errors.Is(err, entity.ErrBadFilter):
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Invalid pagination request")
	case errors.Is(err, entity.ErrAliasNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "Unknown room alias")
	case errors.Is(err, entity.ErrAliasInUse):
		writeError(w, http.StatusConflict, codeRoomInUse, "Room alias is already taken")
	case errors.Is(err, entity.ErrAliasNotOwned), errors.Is(err, entity.ErrAliasUnusable):
		writeError(w, http.StatusBadRequest, codeBadAlias, "Alias is not usable for this room")
	case errors.Is(err, entity.ErrAliasInvalid), errors.Is(err, entity.ErrAliasForeign):
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Invalid room alias")
	case errors.Is(err, entity.ErrEncryptionRequired):
		writeError(w, http.StatusForbidden, codeForbidden, "Rooms on this server are always encrypted")
	case errors.Is(err, entity.ErrUnsupportedRoomVersion):
		writeError(w, http.StatusBadRequest, codeBadRoomVer, "Unsupported room version")
	case errors.Is(err, entity.ErrInvalidVisibility), errors.Is(err, entity.ErrInvalidPreset):
		writeError(w, http.StatusBadRequest, codeInvalidParam, "Invalid room visibility or preset")
	case errors.Is(err, entity.ErrInvalidRoomState), errors.Is(err, entity.ErrPowerLevelsMalformed),
		errors.Is(err, entity.ErrPowerLevelsNameCreator):
		writeError(w, http.StatusBadRequest, codeBadRoomState, "Invalid room state content")
	case errors.Is(err, entity.ErrEventTooLarge), errors.Is(err, entity.ErrEventFieldTooLong):
		writeError(w, http.StatusRequestEntityTooLarge, codeTooLarge, "Event exceeds the maximum size")
	case errors.Is(err, entity.ErrCanonicalFloat), errors.Is(err, entity.ErrCanonicalIntegerRange),
		errors.Is(err, entity.ErrCanonicalUTF8), errors.Is(err, entity.ErrCanonicalType),
		errors.Is(err, entity.ErrEventMalformed), errors.Is(err, entity.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, codeBadJSON, "The request is not a valid event")
	case errors.Is(err, entity.ErrTransactionMissing):
		writeError(w, http.StatusBadRequest, codeMissingParam, "Missing transaction id")
	case errors.As(err, &validation.Errors{}):
		writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
	default:
		writeInternal(r.Context(), w, msg, err)
	}
}
