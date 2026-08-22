package legacysync

import (
	"context"
	"encoding/json"
	"slices"
	"sort"

	"github.com/thelemail/thaumaste/internal/entity"
)

const maxTimelineScan = 500

type roomDelivery struct {
	room     entity.SyncRoom
	initial  bool
	since    int64
	ceiling  int64
	history  entity.HistoryFilter
	timeline []entity.StoredEvent
	limited  bool
	state    []entity.StoredEvent
	stripped []entity.StoredEvent
	ephemera []json.RawMessage
	data     []json.RawMessage
}

func (s *srv) build(ctx context.Context, sess *session) (entity.LegacySyncResult, error) {
	out := entity.LegacySyncResult{
		Join: map[string]entity.LegacyRoom{}, Invite: map[string]entity.LegacyRoom{},
		Knock: map[string]entity.LegacyRoom{}, Leave: map[string]entity.LegacyRoom{},
	}
	if err := s.global(ctx, sess, &out); err != nil {
		return entity.LegacySyncResult{}, err
	}
	if err := s.rooms(ctx, sess, &out); err != nil {
		return entity.LegacySyncResult{}, err
	}
	return out, nil
}

func (s *srv) rooms(ctx context.Context, sess *session, out *entity.LegacySyncResult) error {
	filter := sess.request.Filter
	membershipAt, err := s.membershipPositions(ctx, sess)
	if err != nil {
		return err
	}

	var joined, invited, knocked, departed []*roomDelivery
	for _, room := range sess.rooms {
		if !filter.SelectsRoom(room.RoomID) {
			continue
		}
		entry := &roomDelivery{room: room, since: sess.since.Events, initial: sess.initial,
			ceiling: sess.upTo.Events}
		fresh := membershipAt[room.RoomNID] > sess.since.Events

		switch room.Membership {
		case entity.MembershipJoin:
			joined = append(joined, entry)
		case entity.MembershipInvite:
			if sess.initial || sess.request.FullState || fresh {
				invited = append(invited, entry)
			}
		case entity.MembershipKnock:
			if sess.initial || sess.request.FullState || fresh {
				knocked = append(knocked, entry)
			}
		default:
			entry.ceiling = min(entry.ceiling, membershipAt[room.RoomNID])
			if fresh || (filter.IncludeLeave() && (sess.initial || sess.request.FullState)) {
				departed = append(departed, entry)
			}
		}
	}

	if err := s.stripped(ctx, slices.Concat(invited, knocked)); err != nil {
		return err
	}
	for _, entry := range invited {
		out.Invite[entry.room.RoomID] = entity.LegacyRoom{
			Stripped: entity.StrippedState(events(entry.stripped), sess.caller),
		}
	}
	for _, entry := range knocked {
		out.Knock[entry.room.RoomID] = entity.LegacyRoom{
			Stripped: entity.StrippedState(events(entry.stripped), sess.caller),
		}
	}

	timelined := slices.Concat(joined, departed)
	if err := s.gather(ctx, sess, timelined); err != nil {
		return err
	}
	if err := s.ephemeral(ctx, sess, joined); err != nil {
		return err
	}
	if err := s.roomData(ctx, sess, timelined); err != nil {
		return err
	}

	for _, entry := range timelined {
		room, ok, err := s.assemble(ctx, sess, entry)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if entry.room.Membership == entity.MembershipJoin {
			out.Join[entry.room.RoomID] = room
			continue
		}
		out.Leave[entry.room.RoomID] = room
	}
	return nil
}

func (s *srv) membershipPositions(ctx context.Context, sess *session) (map[int64]int64, error) {
	nids := make([]int64, 0, len(sess.rooms))
	for _, room := range sess.rooms {
		nids = append(nids, room.EventNID)
	}
	events, err := s.stores.Events.GetManyByNID(ctx, nids)
	if err != nil {
		return nil, err
	}
	byNID := make(map[int64]int64, len(events))
	for _, stored := range events {
		byNID[stored.NID] = stored.StreamOrdering
	}
	out := make(map[int64]int64, len(sess.rooms))
	for _, room := range sess.rooms {
		out[room.RoomNID] = byNID[room.EventNID]
	}
	return out, nil
}

func (s *srv) stripped(ctx context.Context, entries []*roomDelivery) error {
	if len(entries) == 0 {
		return nil
	}
	nids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		nids = append(nids, entry.room.RoomNID)
	}
	state, err := s.stores.Events.LatestState(ctx, nids, nil)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entry.stripped = state[entry.room.RoomNID]
	}
	return nil
}

func (s *srv) gather(ctx context.Context, sess *session, entries []*roomDelivery) error {
	if len(entries) == 0 {
		return nil
	}
	nids := make([]int64, 0, len(entries))
	windows := make([]entity.RoomWindow, 0, len(entries))
	for _, entry := range entries {
		nids = append(nids, entry.room.RoomNID)
		windows = append(windows, entity.RoomWindow{RoomNID: entry.room.RoomNID, After: entry.since})
	}

	visibility, err := s.stores.Events.StateHistory(ctx, nids, entity.EventTypeHistoryVisibility, "")
	if err != nil {
		return err
	}
	memberships, err := s.stores.Events.StateHistory(ctx, nids, entity.EventTypeMember, sess.caller)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entry.history = entity.NewHistoryFilter(sess.caller,
			visibility[entry.room.RoomNID], memberships[entry.room.RoomNID])
	}

	if err := s.gatherTimelines(ctx, sess, entries, windows); err != nil {
		return err
	}
	return s.gatherState(ctx, sess, entries, nids, windows)
}

func (s *srv) gatherTimelines(ctx context.Context, sess *session, entries []*roomDelivery,
	windows []entity.RoomWindow,
) error {
	limit := sess.request.TimelineLimit()
	filter := sess.request.Filter.Timeline()

	scan := limit + 1
	if !filter.Trivial() {
		scan = maxTimelineScan
	}
	fetched, err := s.stores.Events.Since(ctx, windows, sess.upTo.Events, scan)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		raw := fetched[entry.room.RoomNID]
		entry.limited = len(raw) >= scan
		kept := make([]entity.StoredEvent, 0, len(raw))
		for _, stored := range raw {
			if stored.StreamOrdering > entry.ceiling {
				continue
			}
			if filter.Keeps(stored.Event) {
				kept = append(kept, stored)
			}
		}
		if len(kept) > limit {
			entry.limited = true
			kept = kept[len(kept)-limit:]
		}
		entry.timeline = kept
	}
	return nil
}

func (s *srv) gatherState(ctx context.Context, sess *session, entries []*roomDelivery,
	nids []int64, windows []entity.RoomWindow,
) error {
	full := sess.initial || sess.request.FullState
	if full {
		state, err := s.stores.Events.LatestState(ctx, nids, nil)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			entry.state = state[entry.room.RoomNID]
		}
		return nil
	}

	state, err := s.stores.Events.StateSince(ctx, windows, sess.upTo.Events)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entry.state = slices.DeleteFunc(state[entry.room.RoomNID],
			func(stored entity.StoredEvent) bool { return stored.StreamOrdering > entry.ceiling })
	}
	return nil
}

func (s *srv) ephemeral(ctx context.Context, sess *session, entries []*roomDelivery) error {
	if len(entries) == 0 {
		return nil
	}
	filter := sess.request.Filter.Ephemeral()
	byNID := make(map[int64]*roomDelivery, len(entries))
	nids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		byNID[entry.room.RoomNID] = entry
		nids = append(nids, entry.room.RoomNID)
	}

	if filter.KeepsType(entity.EventTypeReceipt) {
		found, err := s.stores.Receipts.Since(ctx, sess.scope, nids, sess.caller, sess.since.Receipts)
		if err != nil {
			return err
		}
		byRoom := map[string]map[string]map[string]map[string]any{}
		for _, receipt := range found {
			events := byRoom[receipt.RoomID]
			if events == nil {
				events = map[string]map[string]map[string]any{}
				byRoom[receipt.RoomID] = events
			}
			types := events[receipt.EventID]
			if types == nil {
				types = map[string]map[string]any{}
				events[receipt.EventID] = types
			}
			users := types[receipt.Type]
			if users == nil {
				users = map[string]any{}
				types[receipt.Type] = users
			}
			entry := map[string]any{"ts": receipt.Timestamp}
			if receipt.ThreadID != entity.ThreadUnthreaded {
				entry["thread_id"] = receipt.ThreadID
			}
			users[receipt.UserID] = entry
		}
		for _, entry := range entries {
			content, ok := byRoom[entry.room.RoomID]
			if !ok {
				continue
			}
			raw, err := marshalEvent(entity.EventTypeReceipt, content)
			if err != nil {
				return err
			}
			entry.ephemera = append(entry.ephemera, raw)
		}
	}

	if !filter.KeepsType(entity.EventTypeTyping) {
		return nil
	}
	sets, err := s.stores.Typing.ChangedSince(ctx, sess.scope, nids, sess.since.Typing, s.clock().UTC())
	if err != nil {
		return err
	}
	for roomNID, users := range sets {
		entry, ok := byNID[roomNID]
		if !ok {
			continue
		}
		sort.Strings(users)
		raw, err := marshalEvent(entity.EventTypeTyping, map[string]any{"user_ids": users})
		if err != nil {
			return err
		}
		entry.ephemera = append(entry.ephemera, raw)
	}
	return nil
}

func (s *srv) assemble(ctx context.Context, sess *session, entry *roomDelivery) (entity.LegacyRoom, bool, error) {
	if !entry.history.EverEntitled() {
		return entity.LegacyRoom{}, false, nil
	}
	carries := len(entry.timeline) > 0 || len(entry.state) > 0 ||
		len(entry.ephemera) > 0 || len(entry.data) > 0
	if !sess.initial && !sess.request.FullState && !carries {
		return entity.LegacyRoom{}, false, nil
	}

	view := entity.TimelineView{
		Scope:    sess.scope,
		Caller:   sess.caller,
		DeviceID: sess.deviceID,
		RoomID:   entry.room.RoomID,
		History:  entry.history,
	}
	timeline, err := s.timeline.Render(ctx, view, entry.timeline)
	if err != nil {
		return entity.LegacyRoom{}, false, err
	}

	room := entity.LegacyRoom{
		Timeline:    timeline,
		Limited:     entry.limited,
		Ephemeral:   entry.ephemera,
		AccountData: entry.data,
	}
	if len(entry.timeline) > 0 {
		room.PrevBatch = entity.Anchor(entity.PositionOf(entry.timeline[0])).String()
	}

	state, err := s.state(ctx, sess, entry, view)
	if err != nil {
		return entity.LegacyRoom{}, false, err
	}
	room.State = state

	if err := s.summarise(ctx, sess, entry, &room); err != nil {
		return entity.LegacyRoom{}, false, err
	}
	return room, true, nil
}

func (s *srv) state(ctx context.Context, sess *session, entry *roomDelivery,
	view entity.TimelineView,
) ([]entity.ClientEvent, error) {
	filter := sess.request.Filter.State()
	lazy := sess.request.Filter.LazyLoadMembers()
	senders := timelineSenders(sess.caller, entry.timeline)

	wanted := make([]entity.StoredEvent, 0, len(entry.state))
	for _, stored := range entry.state {
		key, ok := stored.Event.StateKey()
		if !ok {
			continue
		}
		if !filter.Keeps(stored.Event) {
			continue
		}
		if lazy && stored.Event.Type() == entity.EventTypeMember && !slices.Contains(senders, key) {
			continue
		}
		wanted = append(wanted, stored)
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	slices.SortFunc(wanted, func(a, b entity.StoredEvent) int {
		return int(a.StreamOrdering - b.StreamOrdering)
	})
	return s.timeline.Render(ctx, view, wanted)
}

func timelineSenders(caller string, timeline []entity.StoredEvent) []string {
	senders := []string{caller}
	for _, stored := range timeline {
		if !slices.Contains(senders, stored.Event.Sender()) {
			senders = append(senders, stored.Event.Sender())
		}
		if key, ok := stored.Event.StateKey(); ok && stored.Event.Type() == entity.EventTypeMember {
			if !slices.Contains(senders, key) {
				senders = append(senders, key)
			}
		}
	}
	return senders
}

func (s *srv) summarise(ctx context.Context, sess *session, entry *roomDelivery,
	room *entity.LegacyRoom,
) error {
	if !entry.initial {
		return nil
	}
	counts, err := s.stores.Members.CountForRooms(ctx, []int64{entry.room.RoomNID})
	if err != nil {
		return err
	}
	count := counts[entry.room.RoomNID]
	room.HasSummary = true
	room.Summary = entity.RoomSummary{JoinedCount: count.Joined, InvitedCount: count.Invited}

	named := slices.ContainsFunc(entry.state, func(stored entity.StoredEvent) bool {
		key, ok := stored.Event.StateKey()
		return ok && key == "" && stored.Event.Type() == entity.EventTypeName
	})
	if named {
		return nil
	}
	heroes, err := s.stores.Members.Heroes(ctx, []int64{entry.room.RoomNID}, sess.caller, entity.MaxSyncHeroes)
	if err != nil {
		return err
	}
	for _, member := range heroes[entry.room.RoomNID] {
		room.Summary.Heroes = append(room.Summary.Heroes, entity.Hero{UserID: member.UserID})
	}
	return nil
}

func events(stored []entity.StoredEvent) []entity.Event {
	out := make([]entity.Event, 0, len(stored))
	for _, candidate := range stored {
		out = append(out, candidate.Event)
	}
	return out
}
