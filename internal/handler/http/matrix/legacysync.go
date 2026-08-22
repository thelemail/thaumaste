package matrix

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

const defaultLegacyTimeout = 0

type legacySyncResponse struct {
	NextBatch              string             `json:"next_batch"`
	Rooms                  legacyRooms        `json:"rooms"`
	Presence               legacyEvents       `json:"presence"`
	AccountData            legacyEvents       `json:"account_data"`
	ToDevice               legacyEvents       `json:"to_device"`
	DeviceLists            *legacyDeviceLists `json:"device_lists,omitempty"`
	OneTimeKeyCount        map[string]int     `json:"device_one_time_keys_count"`
	UnusedFallbackKeyTypes []string           `json:"device_unused_fallback_key_types"`
}

type legacyRooms struct {
	Join   map[string]legacyJoinedRoom  `json:"join"`
	Invite map[string]legacyInvitedRoom `json:"invite"`
	Knock  map[string]legacyKnockedRoom `json:"knock"`
	Leave  map[string]legacyLeftRoom    `json:"leave"`
}

type legacyEvents struct {
	Events []json.RawMessage `json:"events"`
}

type legacyTimeline struct {
	Events    []json.RawMessage `json:"events"`
	PrevBatch string            `json:"prev_batch,omitempty"`
	Limited   bool              `json:"limited"`
}

type legacyJoinedRoom struct {
	Timeline    legacyTimeline `json:"timeline"`
	State       legacyEvents   `json:"state"`
	Ephemeral   legacyEvents   `json:"ephemeral"`
	AccountData legacyEvents   `json:"account_data"`
	Summary     *legacySummary `json:"summary,omitempty"`
}

type legacyInvitedRoom struct {
	InviteState legacyEvents `json:"invite_state"`
}

type legacyKnockedRoom struct {
	KnockState legacyEvents `json:"knock_state"`
}

type legacyLeftRoom struct {
	Timeline    legacyTimeline `json:"timeline"`
	State       legacyEvents   `json:"state"`
	AccountData legacyEvents   `json:"account_data"`
}

type legacySummary struct {
	Heroes       []string `json:"m.heroes,omitempty"`
	JoinedCount  int      `json:"m.joined_member_count"`
	InvitedCount int      `json:"m.invited_member_count"`
}

type legacyDeviceLists struct {
	Changed []string `json:"changed"`
	Left    []string `json:"left"`
}

func (h *Handler) legacySync(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	in, ok := h.legacySyncRequest(w, r, tenant.Scope(), caller.UserID)
	if !ok {
		return
	}

	deadline := h.clock().Add(time.Duration(in.Timeout)*time.Millisecond + syncWriteSlack)
	if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		writeInternal(r.Context(), w, "Could not hold the sync connection open", err)
		return
	}

	result, err := h.legacy.Sync(r.Context(), tenant.Scope(), caller.UserID, caller.DeviceID, in)
	if err != nil {
		switch {
		case errors.Is(err, entity.ErrUnknownToken):
			writeError(w, http.StatusBadRequest, codeInvalidParam, "Unknown since token")
		case errors.Is(err, entity.ErrDeviceRequired):
			writeError(w, http.StatusForbidden, codeForbidden, "Sync requires a device")
		default:
			writeRoomError(r, w, err, "Could not sync")
		}
		return
	}

	response, err := renderLegacySync(result)
	if err != nil {
		writeInternal(r.Context(), w, "Could not render the sync response", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) legacySyncRequest(w http.ResponseWriter, r *http.Request,
	scope entity.TenantScope, caller string,
) (entity.LegacySyncRequest, bool) {
	query := r.URL.Query()
	in := entity.LegacySyncRequest{
		Since:       query.Get("since"),
		FullState:   query.Get("full_state") == "true",
		SetPresence: query.Get("set_presence"),
	}
	if raw := query.Get("timeout"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidParam, "timeout is not a number")
			return entity.LegacySyncRequest{}, false
		}
		in.Timeout = parsed
	} else {
		in.Timeout = defaultLegacyTimeout
	}

	raw := query.Get("filter")
	switch {
	case raw == "":
	case strings.HasPrefix(raw, "{"):
		filter, err := entity.ParseFilter([]byte(raw))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidParam, "filter is not a valid filter")
			return entity.LegacySyncRequest{}, false
		}
		in.Filter = filter
	default:
		filter, err := h.filters.Get(r.Context(), scope, caller, caller, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidParam, "Unknown filter")
			return entity.LegacySyncRequest{}, false
		}
		in.Filter = filter
	}
	return in, true
}

func renderLegacySync(result entity.LegacySyncResult) (legacySyncResponse, error) {
	out := legacySyncResponse{
		NextBatch: result.NextBatch.String(),
		Rooms: legacyRooms{
			Join:   map[string]legacyJoinedRoom{},
			Invite: map[string]legacyInvitedRoom{},
			Knock:  map[string]legacyKnockedRoom{},
			Leave:  map[string]legacyLeftRoom{},
		},
		Presence:               legacyEvents{Events: orEmpty(result.Presence)},
		AccountData:            legacyEvents{Events: orEmpty(result.AccountData)},
		ToDevice:               legacyEvents{Events: orEmpty(result.ToDevice)},
		OneTimeKeyCount:        result.OneTimeKeys,
		UnusedFallbackKeyTypes: orEmptyStrings(result.FallbackTypes),
	}
	if out.OneTimeKeyCount == nil {
		out.OneTimeKeyCount = map[string]int{}
	}
	if result.HasDeviceList {
		out.DeviceLists = &legacyDeviceLists{
			Changed: orEmptyStrings(result.DeviceLists.Changed),
			Left:    orEmptyStrings(result.DeviceLists.Left),
		}
	}

	for roomID, room := range result.Join {
		timeline, state, err := renderLegacyBody(room)
		if err != nil {
			return legacySyncResponse{}, err
		}
		joined := legacyJoinedRoom{
			Timeline:    timeline,
			State:       state,
			Ephemeral:   legacyEvents{Events: orEmpty(room.Ephemeral)},
			AccountData: legacyEvents{Events: orEmpty(room.AccountData)},
		}
		if room.HasSummary {
			summary := &legacySummary{
				JoinedCount:  room.Summary.JoinedCount,
				InvitedCount: room.Summary.InvitedCount,
			}
			for _, hero := range room.Summary.Heroes {
				summary.Heroes = append(summary.Heroes, hero.UserID)
			}
			joined.Summary = summary
		}
		out.Rooms.Join[roomID] = joined
	}

	for roomID, room := range result.Leave {
		timeline, state, err := renderLegacyBody(room)
		if err != nil {
			return legacySyncResponse{}, err
		}
		out.Rooms.Leave[roomID] = legacyLeftRoom{
			Timeline:    timeline,
			State:       state,
			AccountData: legacyEvents{Events: orEmpty(room.AccountData)},
		}
	}

	for roomID, room := range result.Invite {
		events, err := entity.StrippedEvents(room.Stripped)
		if err != nil {
			return legacySyncResponse{}, err
		}
		out.Rooms.Invite[roomID] = legacyInvitedRoom{InviteState: legacyEvents{Events: events}}
	}
	for roomID, room := range result.Knock {
		events, err := entity.StrippedEvents(room.Stripped)
		if err != nil {
			return legacySyncResponse{}, err
		}
		out.Rooms.Knock[roomID] = legacyKnockedRoom{KnockState: legacyEvents{Events: events}}
	}
	return out, nil
}

func renderLegacyBody(room entity.LegacyRoom) (legacyTimeline, legacyEvents, error) {
	events, err := renderEvents(room.Timeline)
	if err != nil {
		return legacyTimeline{}, legacyEvents{}, err
	}
	state, err := renderEvents(room.State)
	if err != nil {
		return legacyTimeline{}, legacyEvents{}, err
	}
	return legacyTimeline{Events: events, PrevBatch: room.PrevBatch, Limited: room.Limited},
		legacyEvents{Events: state}, nil
}
