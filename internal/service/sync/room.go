package sync

import (
	"bytes"
	"context"
	"slices"

	"github.com/thelemail/thaumaste/internal/entity"
)

var summaryKeys = []entity.StateKey{
	{Type: entity.EventTypeName, StateKey: ""},
	{Type: entity.EventTypeAvatar, StateKey: ""},
}

var strippedKeys = []entity.StateKey{
	{Type: entity.EventTypeCreate, StateKey: ""},
	{Type: entity.EventTypeName, StateKey: ""},
	{Type: entity.EventTypeAvatar, StateKey: ""},
	{Type: entity.EventTypeTopic, StateKey: ""},
	{Type: entity.EventTypeJoinRules, StateKey: ""},
	{Type: entity.EventTypeCanonicalAlias, StateKey: ""},
	{Type: entity.EventTypeEncryption, StateKey: ""},
}

type delivery struct {
	*candidate
	initial  bool
	expanded bool
	since    int64
	liveFrom int64
	history  entity.HistoryFilter
	timeline []entity.StoredEvent
	limited  bool
	state    []entity.StoredEvent
}

func (s *srv) build(ctx context.Context, sess *session) (entity.SyncResult, []entity.NewRoomStatus, error) {
	chosen, lists, err := s.candidates(ctx, sess)
	if err != nil {
		return entity.SyncResult{}, nil, err
	}
	result := entity.SyncResult{Lists: lists, Rooms: make(map[string]entity.RoomResult)}
	sess.delivered = map[int64]string{}

	sending := s.due(sess, chosen)
	if len(sending) == 0 {
		return result, nil, nil
	}

	if err := s.gather(ctx, sess, sending); err != nil {
		return entity.SyncResult{}, nil, err
	}

	staged := make([]entity.NewRoomStatus, 0, len(sending))
	for _, entry := range sending {
		room, ok, err := s.assemble(ctx, sess, entry)
		if err != nil {
			return entity.SyncResult{}, nil, err
		}
		if !ok {
			continue
		}
		staged = append(staged, entity.NewRoomStatus{
			RoomNID:       entry.room.RoomNID,
			SentTo:        sess.ceiling,
			TimelineLimit: entry.limit,
			RequiredState: entry.required.Canonical(),
		})
		result.Rooms[entry.room.RoomID] = room
		sess.delivered[entry.room.RoomNID] = entry.room.RoomID
	}
	if len(result.Rooms) == 0 {
		return result, nil, nil
	}
	return result, staged, nil
}

func (s *srv) due(sess *session, chosen map[int64]*candidate) []*delivery {
	var sending []*delivery
	for _, entry := range chosen {
		status, known := sess.known[entry.room.RoomNID]
		if !known && left(entry.room.Membership) {
			continue
		}
		switch {
		case !known:
			sending = append(sending, &delivery{candidate: entry, initial: true})
		case entry.room.LastStream > status.SentTo:
			sending = append(sending, &delivery{candidate: entry, since: status.SentTo, liveFrom: status.SentTo})
		case entry.limit > status.TimelineLimit:
			sending = append(sending, &delivery{candidate: entry, expanded: true, liveFrom: status.SentTo})
		case !bytes.Equal(entry.required.Canonical(), status.RequiredState):
			sending = append(sending, &delivery{candidate: entry, expanded: true, liveFrom: status.SentTo})
		}
	}
	slices.SortFunc(sending, func(a, b *delivery) int { return int(a.room.RoomNID - b.room.RoomNID) })
	return sending
}

func left(membership string) bool {
	return membership == entity.MembershipLeave || membership == entity.MembershipBan
}

func (s *srv) gather(ctx context.Context, sess *session, sending []*delivery) error {
	nids := make([]int64, 0, len(sending))
	windows := make([]entity.RoomWindow, 0, len(sending))
	for _, entry := range sending {
		nids = append(nids, entry.room.RoomNID)
		windows = append(windows, entity.RoomWindow{RoomNID: entry.room.RoomNID, After: entry.since})
	}

	visibility, err := s.events.StateHistory(ctx, nids, entity.EventTypeHistoryVisibility, "")
	if err != nil {
		return err
	}
	memberships, err := s.events.StateHistory(ctx, nids, entity.EventTypeMember, sess.caller)
	if err != nil {
		return err
	}
	for _, entry := range sending {
		entry.history = entity.NewHistoryFilter(sess.caller,
			visibility[entry.room.RoomNID], memberships[entry.room.RoomNID])
	}

	if err := s.gatherTimelines(ctx, sess, sending, windows); err != nil {
		return err
	}
	return s.gatherState(ctx, sess, sending)
}

func (s *srv) gatherTimelines(ctx context.Context, sess *session, sending []*delivery, windows []entity.RoomWindow) error {
	limit := 0
	for _, entry := range sending {
		limit = max(limit, entry.limit)
	}
	if limit == 0 {
		return nil
	}

	fetched, err := s.events.Since(ctx, windows, sess.ceiling, limit+1)
	if err != nil {
		return err
	}
	for _, entry := range sending {
		events := fetched[entry.room.RoomNID]
		if len(events) > entry.limit {
			entry.limited = true
			events = events[len(events)-entry.limit:]
		}
		entry.timeline = events
	}
	return nil
}

func (s *srv) gatherState(ctx context.Context, sess *session, sending []*delivery) error {
	var fresh, changed []*delivery
	for _, entry := range sending {
		if entry.initial || entry.expanded {
			fresh = append(fresh, entry)
			continue
		}
		changed = append(changed, entry)
	}

	if len(fresh) > 0 {
		nids := make([]int64, 0, len(fresh))
		selectors := summaryKeys
		wildcard := false
		for _, entry := range fresh {
			nids = append(nids, entry.room.RoomNID)
			for _, selector := range entry.required.Include {
				if !selector.HasType || !selector.HasKey {
					wildcard = true
					continue
				}
				selectors = append(selectors, entity.StateKey{Type: selector.Type, StateKey: selector.StateKey})
			}
			if invited(entry.room.Membership) {
				selectors = append(selectors, strippedKeys...)
				selectors = append(selectors,
					entity.StateKey{Type: entity.EventTypeMember, StateKey: sess.caller})
			}
		}
		if wildcard {
			selectors = nil
		}
		state, err := s.events.LatestState(ctx, nids, selectors)
		if err != nil {
			return err
		}
		for _, entry := range fresh {
			entry.state = state[entry.room.RoomNID]
		}
	}

	if len(changed) > 0 {
		windows := make([]entity.RoomWindow, 0, len(changed))
		for _, entry := range changed {
			windows = append(windows, entity.RoomWindow{RoomNID: entry.room.RoomNID, After: entry.since})
		}
		state, err := s.events.StateSince(ctx, windows, sess.ceiling)
		if err != nil {
			return err
		}
		for _, entry := range changed {
			entry.state = state[entry.room.RoomNID]
		}
	}
	return nil
}

func invited(membership string) bool {
	return membership == entity.MembershipInvite || membership == entity.MembershipKnock
}

func (s *srv) assemble(ctx context.Context, sess *session, entry *delivery) (entity.RoomResult, bool, error) {
	view := entity.TimelineView{
		Scope:    sess.scope,
		Caller:   sess.caller,
		DeviceID: sess.deviceID,
		RoomID:   entry.room.RoomID,
		History:  entry.history,
	}
	if !entry.history.EverEntitled() {
		return entity.RoomResult{}, false, nil
	}

	room := entity.RoomResult{
		Initial:          entry.initial,
		Membership:       entry.room.Membership,
		Lists:            entry.lists,
		BumpStamp:        entry.room.BumpStream,
		Limited:          entry.limited,
		ExpandedTimeline: entry.expanded,
	}

	if invited(entry.room.Membership) {
		room.StrippedState = entity.StrippedState(events(entry.state), sess.caller)
		return room, true, nil
	}

	timeline, err := s.timeline.Render(ctx, view, entry.timeline)
	if err != nil {
		return entity.RoomResult{}, false, err
	}
	room.Timeline = timeline
	if !entry.initial {
		room.NumLive = entry.live(timeline)
	}
	if len(entry.timeline) > 0 {
		room.PrevBatch = entity.Anchor(entity.PositionOf(entry.timeline[0])).String()
	}

	state, err := s.requiredState(ctx, sess, entry, view)
	if err != nil {
		return entity.RoomResult{}, false, err
	}
	room.RequiredState = state

	if err := s.summarise(ctx, sess, entry, &room); err != nil {
		return entity.RoomResult{}, false, err
	}
	return room, true, nil
}

func (d *delivery) live(rendered []entity.ClientEvent) int {
	fresh := make(map[string]struct{}, len(d.timeline))
	for _, stored := range d.timeline {
		if stored.StreamOrdering > d.liveFrom {
			fresh[stored.Event.ID()] = struct{}{}
		}
	}
	live := 0
	for _, event := range rendered {
		if _, ok := fresh[event.Event.ID()]; ok {
			live++
		}
	}
	return live
}

func events(stored []entity.StoredEvent) []entity.Event {
	out := make([]entity.Event, 0, len(stored))
	for _, candidate := range stored {
		out = append(out, candidate.Event)
	}
	return out
}

func (s *srv) requiredState(ctx context.Context, sess *session, entry *delivery, view entity.TimelineView) ([]entity.ClientEvent, error) {
	wanted := make([]entity.StoredEvent, 0, len(entry.state))
	for _, stored := range entry.state {
		key, ok := stored.Event.StateKey()
		if !ok {
			continue
		}
		if entry.required.Selects(stored.Event.Type(), key) {
			wanted = append(wanted, stored)
		}
	}

	lazy, err := s.lazyMembers(ctx, sess, entry)
	if err != nil {
		return nil, err
	}
	for _, stored := range lazy {
		if !slices.ContainsFunc(wanted, func(have entity.StoredEvent) bool { return have.NID == stored.NID }) {
			wanted = append(wanted, stored)
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	slices.SortFunc(wanted, func(a, b entity.StoredEvent) int { return int(a.StreamOrdering - b.StreamOrdering) })
	return s.timeline.Render(ctx, view, wanted)
}

func (s *srv) lazyMembers(ctx context.Context, sess *session, entry *delivery) ([]entity.StoredEvent, error) {
	if !entry.required.LazyMembers {
		return nil, nil
	}
	senders := []string{sess.caller}
	for _, stored := range entry.timeline {
		if !slices.Contains(senders, stored.Event.Sender()) {
			senders = append(senders, stored.Event.Sender())
		}
		if key, ok := stored.Event.StateKey(); ok && stored.Event.Type() == entity.EventTypeMember {
			if !slices.Contains(senders, key) {
				senders = append(senders, key)
			}
		}
	}

	memberships, err := s.members.ListForRooms(ctx, []int64{entry.room.RoomNID}, senders)
	if err != nil {
		return nil, err
	}
	nids := make([]int64, 0, len(memberships))
	for _, membership := range memberships {
		nids = append(nids, membership.EventNID)
	}
	return s.events.GetManyByNID(ctx, nids)
}

func (s *srv) summarise(ctx context.Context, sess *session, entry *delivery, room *entity.RoomResult) error {
	named := false
	for _, stored := range entry.state {
		key, ok := stored.Event.StateKey()
		if !ok || key != "" {
			continue
		}
		switch stored.Event.Type() {
		case entity.EventTypeName:
			value, _ := stored.Event.Content()["name"].(string)
			room.Name = entity.SetString(value)
			named = value != ""
		case entity.EventTypeAvatar:
			value, _ := stored.Event.Content()["url"].(string)
			room.Avatar = entity.SetString(value)
		}
	}

	membershipChanged := slices.ContainsFunc(entry.state, func(stored entity.StoredEvent) bool {
		return stored.Event.Type() == entity.EventTypeMember
	})
	if !entry.initial && !membershipChanged {
		return nil
	}

	counts, err := s.members.CountForRooms(ctx, []int64{entry.room.RoomNID})
	if err != nil {
		return err
	}
	count := counts[entry.room.RoomNID]
	room.JoinedCount = entity.SetInt(count.Joined)
	room.InvitedCount = entity.SetInt(count.Invited)

	if named {
		return nil
	}
	heroes, err := s.members.Heroes(ctx, []int64{entry.room.RoomNID}, sess.caller, entity.MaxSyncHeroes)
	if err != nil {
		return err
	}
	found := heroes[entry.room.RoomNID]
	if len(found) == 0 {
		return nil
	}
	nids := make([]int64, 0, len(found))
	for _, member := range found {
		nids = append(nids, member.EventNID)
	}
	events, err := s.events.GetManyByNID(ctx, nids)
	if err != nil {
		return err
	}
	room.HasHeroes = true
	for _, stored := range events {
		key, ok := stored.Event.StateKey()
		if !ok {
			continue
		}
		hero := entity.Hero{UserID: key}
		hero.DisplayName, _ = stored.Event.Content()["displayname"].(string)
		hero.AvatarURL, _ = stored.Event.Content()["avatar_url"].(string)
		room.Heroes = append(room.Heroes, hero)
	}
	return nil
}
