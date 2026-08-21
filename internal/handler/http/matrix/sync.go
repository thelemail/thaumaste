package matrix

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

const syncWriteSlack = 10 * time.Second

type syncRequest struct {
	ConnID            string                        `json:"conn_id"`
	Pos               string                        `json:"pos"`
	Timeout           *int                          `json:"timeout"`
	SetPresence       string                        `json:"set_presence"`
	Lists             map[string]syncListRequest    `json:"lists"`
	RoomSubscriptions map[string]syncRoomSubRequest `json:"room_subscriptions"`
	Extensions        map[string]json.RawMessage    `json:"extensions"`
}

type syncListRequest struct {
	Range         *[2]int              `json:"range"`
	Filters       *syncFilterRequest   `json:"filters"`
	TimelineLimit int                  `json:"timeline_limit"`
	RequiredState requiredStateRequest `json:"required_state"`
}

type syncRoomSubRequest struct {
	TimelineLimit int                  `json:"timeline_limit"`
	RequiredState requiredStateRequest `json:"required_state"`
}

type requiredStateRequest struct {
	Include     []stateSelectorRequest `json:"include"`
	Exclude     []stateSelectorRequest `json:"exclude"`
	LazyMembers bool                   `json:"lazy_members"`
}

type stateSelectorRequest struct {
	Type     *string `json:"type"`
	StateKey *string `json:"state_key"`
}

type syncFilterRequest struct {
	IsDM         *bool     `json:"is_dm"`
	Spaces       []string  `json:"spaces"`
	IsEncrypted  *bool     `json:"is_encrypted"`
	IsInvited    *bool     `json:"is_invited"`
	RoomTypes    []*string `json:"room_types"`
	NotRoomTypes []*string `json:"not_room_types"`
	Tags         []string  `json:"tags"`
	NotTags      []string  `json:"not_tags"`
}

type syncResponse struct {
	Pos        string                    `json:"pos"`
	Lists      map[string]syncListResult `json:"lists"`
	Rooms      map[string]syncRoomResult `json:"rooms"`
	Extensions map[string]any            `json:"extensions"`
}

type syncListResult struct {
	Count int `json:"count"`
}

type syncRoomResult struct {
	Membership       string            `json:"membership"`
	BumpStamp        int64             `json:"bump_stamp,omitempty"`
	Lists            []string          `json:"lists,omitempty"`
	Initial          bool              `json:"initial,omitempty"`
	Name             json.RawMessage   `json:"name,omitempty"`
	Avatar           json.RawMessage   `json:"avatar,omitempty"`
	Heroes           []syncHero        `json:"heroes,omitempty"`
	JoinedCount      *int              `json:"joined_count,omitempty"`
	InvitedCount     *int              `json:"invited_count,omitempty"`
	RequiredState    []json.RawMessage `json:"required_state,omitempty"`
	Timeline         []json.RawMessage `json:"timeline,omitempty"`
	StrippedState    []json.RawMessage `json:"stripped_state,omitempty"`
	PrevBatch        string            `json:"prev_batch,omitempty"`
	Limited          bool              `json:"limited,omitempty"`
	NumLive          int               `json:"num_live,omitempty"`
	ExpandedTimeline bool              `json:"expanded_timeline,omitempty"`
}

type syncHero struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"displayname,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}
	var body syncRequest
	if !readJSON(w, r, &body) {
		return
	}

	in := body.entity(r)
	deadline := h.clock().Add(time.Duration(in.Timeout)*time.Millisecond + syncWriteSlack)
	if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		writeInternal(r.Context(), w, "Could not hold the sync connection open", err)
		return
	}

	result, err := h.syncSvc.Sync(r.Context(), tenant.Scope(), caller.UserID, caller.DeviceID, in)
	if err != nil {
		writeSyncError(r, w, err)
		return
	}

	response, err := renderSync(result)
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the sync response", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (b syncRequest) entity(r *http.Request) entity.SyncRequest {
	query := r.URL.Query()
	in := entity.SyncRequest{
		ConnID:      firstOf(b.ConnID, query.Get("conn_id")),
		Pos:         firstOf(b.Pos, query.Get("pos")),
		SetPresence: b.SetPresence,
		Extensions:  b.Extensions,
	}
	switch {
	case b.Timeout != nil:
		in.Timeout = *b.Timeout
	default:
		in.Timeout, _ = strconv.Atoi(query.Get("timeout"))
	}

	if len(b.Lists) > 0 {
		in.Lists = make(map[string]entity.SyncList, len(b.Lists))
		for name, list := range b.Lists {
			converted := entity.SyncList{
				TimelineLimit: list.TimelineLimit,
				RequiredState: list.RequiredState.entity(),
				Filters:       list.Filters.entity(),
			}
			if list.Range != nil {
				converted.Range = &entity.ListRange{Start: list.Range[0], End: list.Range[1]}
			}
			in.Lists[name] = converted
		}
	}
	if len(b.RoomSubscriptions) > 0 {
		in.RoomSubscriptions = make(map[string]entity.RoomSubscription, len(b.RoomSubscriptions))
		for roomID, sub := range b.RoomSubscriptions {
			in.RoomSubscriptions[roomID] = entity.RoomSubscription{
				TimelineLimit: sub.TimelineLimit,
				RequiredState: sub.RequiredState.entity(),
			}
		}
	}
	return in
}

func (s requiredStateRequest) entity() entity.RequiredState {
	return entity.RequiredState{
		Include:     selectors(s.Include),
		Exclude:     selectors(s.Exclude),
		LazyMembers: s.LazyMembers,
	}
}

func selectors(in []stateSelectorRequest) []entity.StateSelector {
	if len(in) == 0 {
		return nil
	}
	out := make([]entity.StateSelector, 0, len(in))
	for _, selector := range in {
		converted := entity.StateSelector{}
		if selector.Type != nil {
			converted.Type, converted.HasType = *selector.Type, true
		}
		if selector.StateKey != nil {
			converted.StateKey, converted.HasKey = *selector.StateKey, true
		}
		out = append(out, converted)
	}
	return out
}

func (f *syncFilterRequest) entity() *entity.RoomFilter {
	if f == nil {
		return nil
	}
	out := &entity.RoomFilter{
		IsEncrypted:  f.IsEncrypted,
		IsInvited:    f.IsInvited,
		RoomTypes:    f.RoomTypes,
		NotRoomTypes: f.NotRoomTypes,
	}
	if f.IsDM != nil {
		out.Unsupported = append(out.Unsupported, "is_dm")
	}
	if len(f.Spaces) > 0 {
		out.Unsupported = append(out.Unsupported, "spaces")
	}
	if len(f.Tags) > 0 {
		out.Unsupported = append(out.Unsupported, "tags")
	}
	if len(f.NotTags) > 0 {
		out.Unsupported = append(out.Unsupported, "not_tags")
	}
	return out
}

func renderSync(result entity.SyncResult) (syncResponse, error) {
	response := syncResponse{
		Pos:        result.Pos.String(),
		Lists:      make(map[string]syncListResult, len(result.Lists)),
		Rooms:      make(map[string]syncRoomResult, len(result.Rooms)),
		Extensions: map[string]any{},
	}
	for name, list := range result.Lists {
		response.Lists[name] = syncListResult{Count: list.Count}
	}
	for roomID, room := range result.Rooms {
		rendered, err := renderSyncRoom(room)
		if err != nil {
			return syncResponse{}, err
		}
		response.Rooms[roomID] = rendered
	}
	return response, nil
}

func renderSyncRoom(room entity.RoomResult) (syncRoomResult, error) {
	out := syncRoomResult{
		Membership:       room.Membership,
		BumpStamp:        room.BumpStamp,
		Lists:            room.Lists,
		Initial:          room.Initial,
		PrevBatch:        room.PrevBatch,
		Limited:          room.Limited,
		NumLive:          room.NumLive,
		ExpandedTimeline: room.ExpandedTimeline,
	}
	var err error
	if out.Name, err = optionalName(room.Name); err != nil {
		return syncRoomResult{}, err
	}
	if out.Avatar, err = optionalName(room.Avatar); err != nil {
		return syncRoomResult{}, err
	}
	if room.JoinedCount.Present {
		out.JoinedCount = &room.JoinedCount.Value
	}
	if room.InvitedCount.Present {
		out.InvitedCount = &room.InvitedCount.Value
	}
	if room.HasHeroes {
		out.Heroes = make([]syncHero, 0, len(room.Heroes))
		for _, hero := range room.Heroes {
			out.Heroes = append(out.Heroes, syncHero{
				UserID: hero.UserID, DisplayName: hero.DisplayName, AvatarURL: hero.AvatarURL,
			})
		}
	}

	if out.Timeline, err = renderEvents(room.Timeline); err != nil {
		return syncRoomResult{}, err
	}
	if out.RequiredState, err = renderEvents(room.RequiredState); err != nil {
		return syncRoomResult{}, err
	}
	if out.StrippedState, err = entity.StrippedEvents(room.StrippedState); err != nil {
		return syncRoomResult{}, err
	}
	return out, nil
}

func optionalName(value entity.OptionalString) (json.RawMessage, error) {
	if !value.Present {
		return nil, nil
	}
	if value.Cleared() {
		return json.RawMessage("null"), nil
	}
	raw, err := json.Marshal(value.Value)
	if err != nil {
		return nil, fmt.Errorf("matrix: render room name: %w", err)
	}
	return raw, nil
}

func firstOf(preferred, fallback string) string {
	if preferred != "" {
		return preferred
	}
	return fallback
}

func writeSyncError(r *http.Request, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entity.ErrUnknownPos):
		writeError(w, http.StatusBadRequest, codeUnknownPos, "Unknown sync position")
	case errors.Is(err, entity.ErrDeviceRequired):
		writeError(w, http.StatusForbidden, codeForbidden, "Sync requires a device")
	case errors.Is(err, entity.ErrFilterUnsupported), errors.Is(err, entity.ErrTooManyLists),
		errors.Is(err, entity.ErrBadRange), errors.Is(err, entity.ErrBadTimelineLimit):
		writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
	default:
		writeRoomError(r, w, err, "Could not sync")
	}
}
