package sync

import (
	"context"
	"maps"
	"slices"

	"github.com/thelemail/thaumaste/internal/entity"
)

type candidate struct {
	room     entity.SyncRoom
	lists    []string
	limit    int
	required entity.RequiredState
	merged   bool
}

func (s *srv) candidates(ctx context.Context, sess *session) (map[int64]*candidate, map[string]entity.ListResult, error) {
	rooms, err := s.members.ListForSync(ctx, sess.scope, sess.caller)
	if err != nil {
		return nil, nil, err
	}
	rooms = slices.DeleteFunc(rooms, func(room entity.SyncRoom) bool { return room.Forgotten })

	traits, err := s.traits(ctx, sess, rooms)
	if err != nil {
		return nil, nil, err
	}

	chosen := make(map[int64]*candidate)
	lists := make(map[string]entity.ListResult, len(sess.request.Lists))

	for _, name := range slices.Sorted(maps.Keys(sess.request.Lists)) {
		list := sess.request.Lists[name]
		matched := make([]entity.SyncRoom, 0, len(rooms))
		for _, room := range rooms {
			trait := traits[room.RoomNID]
			if list.Filters.Matches(trait.roomType, trait.encrypted, room.Membership == entity.MembershipInvite) {
				matched = append(matched, room)
			}
		}
		lists[name] = entity.ListResult{Count: len(matched)}

		for index, room := range matched {
			if list.Range != nil && !list.Range.Contains(index) {
				continue
			}
			entry := admit(chosen, room, list.TimelineLimit, list.RequiredState)
			entry.lists = append(entry.lists, name)
		}
	}

	byID := make(map[string]entity.SyncRoom, len(rooms))
	for _, room := range rooms {
		byID[room.RoomID] = room
	}
	for _, roomID := range slices.Sorted(maps.Keys(sess.request.RoomSubscriptions)) {
		subscription := sess.request.RoomSubscriptions[roomID]
		room, ok := byID[roomID]
		if !ok {
			continue
		}
		admit(chosen, room, subscription.TimelineLimit, subscription.RequiredState)
	}

	return s.cap(chosen), lists, nil
}

func (s *srv) cap(chosen map[int64]*candidate) map[int64]*candidate {
	if s.cfg.MaxRoomsPerSync <= 0 || len(chosen) <= s.cfg.MaxRoomsPerSync {
		return chosen
	}
	ordered := slices.SortedFunc(maps.Values(chosen), func(a, b *candidate) int {
		if a.room.LastStream != b.room.LastStream {
			return int(b.room.LastStream - a.room.LastStream)
		}
		return int(b.room.RoomNID - a.room.RoomNID)
	})
	out := make(map[int64]*candidate, s.cfg.MaxRoomsPerSync)
	for _, entry := range ordered[:s.cfg.MaxRoomsPerSync] {
		out[entry.room.RoomNID] = entry
	}
	return out
}

func admit(chosen map[int64]*candidate, room entity.SyncRoom, limit int, required entity.RequiredState) *candidate {
	entry, ok := chosen[room.RoomNID]
	if !ok {
		entry = &candidate{room: room, limit: limit, required: required}
		chosen[room.RoomNID] = entry
		return entry
	}
	entry.limit = max(entry.limit, limit)
	entry.required = merge(entry.required, required)
	entry.merged = true
	return entry
}

func merge(a, b entity.RequiredState) entity.RequiredState {
	out := entity.RequiredState{
		Include:     union(a.Include, b.Include),
		LazyMembers: a.LazyMembers && b.LazyMembers,
	}
	for _, selector := range a.Exclude {
		if slices.Contains(b.Exclude, selector) {
			out.Exclude = append(out.Exclude, selector)
		}
	}
	return out
}

func union(a, b []entity.StateSelector) []entity.StateSelector {
	out := slices.Clone(a)
	for _, selector := range b {
		if !slices.Contains(out, selector) {
			out = append(out, selector)
		}
	}
	return out
}

type roomTraits struct {
	roomType  *string
	encrypted bool
}

func (s *srv) traits(ctx context.Context, sess *session, rooms []entity.SyncRoom) (map[int64]roomTraits, error) {
	out := make(map[int64]roomTraits, len(rooms))
	if !filtersNeedState(sess.request) || len(rooms) == 0 {
		return out, nil
	}

	nids := make([]int64, 0, len(rooms))
	for _, room := range rooms {
		nids = append(nids, room.RoomNID)
	}
	state, err := s.events.LatestState(ctx, nids, []entity.StateKey{
		{Type: entity.EventTypeCreate, StateKey: ""},
		{Type: entity.EventTypeEncryption, StateKey: ""},
	})
	if err != nil {
		return nil, err
	}

	for roomNID, events := range state {
		trait := roomTraits{}
		for _, stored := range events {
			switch stored.Event.Type() {
			case entity.EventTypeCreate:
				if value, ok := stored.Event.Content()["type"].(string); ok {
					trait.roomType = &value
				}
			case entity.EventTypeEncryption:
				_, trait.encrypted = stored.Event.Content()["algorithm"].(string)
			}
		}
		out[roomNID] = trait
	}
	return out, nil
}

func filtersNeedState(in entity.SyncRequest) bool {
	for _, list := range in.Lists {
		if list.Filters == nil {
			continue
		}
		if list.Filters.IsEncrypted != nil || len(list.Filters.RoomTypes) > 0 || len(list.Filters.NotRoomTypes) > 0 {
			return true
		}
	}
	return false
}
