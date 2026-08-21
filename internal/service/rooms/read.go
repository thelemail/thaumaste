package rooms

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
)

func (s *srv) Messages(ctx context.Context, scope entity.TenantScope, caller, deviceID string, in entity.MessagesRequest) (entity.Messages, error) {
	if err := in.Validate(); err != nil {
		return entity.Messages{}, err
	}
	filter, err := entity.ParseRoomEventFilter(in.Filter)
	if err != nil {
		return entity.Messages{}, err
	}
	history, err := s.readableRoom(ctx, scope, caller, in.RoomID)
	if err != nil {
		return entity.Messages{}, err
	}

	from, err := s.at(ctx, in.RoomID, in.From)
	if err != nil {
		return entity.Messages{}, err
	}
	to, err := s.at(ctx, in.RoomID, in.To)
	if err != nil {
		return entity.Messages{}, err
	}

	limit := entity.PageLimit(in.Limit, filter.Limit)
	scanned, err := s.events.Page(ctx, in.RoomID, entity.PageRequest{
		From: from, To: to, Backwards: in.Backwards(), Limit: limit,
	})
	if err != nil {
		return entity.Messages{}, err
	}

	chunk, err := s.timeline.Render(ctx, entity.TimelineView{Scope: scope, Caller: caller, DeviceID: deviceID, RoomID: in.RoomID, History: history, Filter: filter}, scanned)
	if err != nil {
		return entity.Messages{}, err
	}

	out := entity.Messages{Chunk: chunk}
	switch {
	case from != nil:
		out.Start = entity.Anchor(*from).String()
	case len(scanned) > 0:
		out.Start = entity.Anchor(entity.PositionOf(scanned[0])).String()
	}
	if len(scanned) > 0 && len(scanned) == limit {
		out.End = entity.Anchor(entity.PositionOf(scanned[len(scanned)-1])).String()
	}
	return out, nil
}

func (s *srv) Context(ctx context.Context, scope entity.TenantScope, caller, deviceID string, in entity.ContextRequest) (entity.Context, error) {
	if err := in.Validate(); err != nil {
		return entity.Context{}, err
	}
	filter, err := entity.ParseRoomEventFilter(in.Filter)
	if err != nil {
		return entity.Context{}, err
	}
	history, err := s.readableRoom(ctx, scope, caller, in.RoomID)
	if err != nil {
		return entity.Context{}, errNotFound(err)
	}

	stored, err := s.events.Event(ctx, in.EventID)
	if err != nil {
		return entity.Context{}, err
	}
	if stored.Event.RoomID() != in.RoomID {
		return entity.Context{}, entity.ErrEventNotFound
	}
	if !history.Visible(stored) {
		return entity.Context{}, entity.ErrEventNotFound
	}

	at := entity.PositionOf(stored)
	half := entity.PageLimit(in.Limit, 0) / 2
	before, err := s.events.Page(ctx, in.RoomID, entity.PageRequest{From: &at, Backwards: true, Limit: half})
	if err != nil {
		return entity.Context{}, err
	}
	after, err := s.events.Page(ctx, in.RoomID, entity.PageRequest{From: &at, Limit: half})
	if err != nil {
		return entity.Context{}, err
	}

	v := entity.TimelineView{Scope: scope, Caller: caller, DeviceID: deviceID, RoomID: in.RoomID, History: history, Filter: filter}
	target, err := s.timeline.Single(ctx, v, stored)
	if err != nil {
		return entity.Context{}, err
	}
	renderedBefore, err := s.timeline.Render(ctx, v, before)
	if err != nil {
		return entity.Context{}, err
	}
	renderedAfter, err := s.timeline.Render(ctx, v, after)
	if err != nil {
		return entity.Context{}, err
	}

	out := entity.Context{
		Event:  target,
		Before: renderedBefore,
		After:  renderedAfter,
		Start:  entity.Anchor(at).String(),
		End:    entity.Anchor(at).String(),
	}
	if len(before) > 0 {
		out.Start = entity.Anchor(entity.PositionOf(before[len(before)-1])).String()
	}

	last := stored
	if len(after) > 0 {
		last = after[len(after)-1]
		out.End = entity.Anchor(entity.PositionOf(last)).String()
	}
	state, err := s.events.StateAfter(ctx, last.NID)
	if err != nil {
		return entity.Context{}, err
	}
	for _, key := range state.Keys() {
		out.State = append(out.State, state[key])
	}
	return out, nil
}

func (s *srv) at(ctx context.Context, roomID, raw string) (*entity.Position, error) {
	if raw == "" {
		return nil, nil
	}
	token, err := entity.ParseToken(raw)
	if err != nil {
		return nil, err
	}
	if token.HasTopological {
		return &token.Position, nil
	}
	at, err := s.events.PositionAtStream(ctx, roomID, token.Position.Stream)
	if err != nil {
		if errors.Is(err, entity.ErrEventNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &at, nil
}

func (s *srv) readableRoom(ctx context.Context, scope entity.TenantScope, caller, roomID string) (entity.HistoryFilter, error) {
	room, err := s.room(ctx, scope, roomID)
	if err != nil {
		return entity.HistoryFilter{}, err
	}

	membership, err := s.members.Get(ctx, room.NID, caller)
	switch {
	case err == nil:
		if membership.Forgotten {
			return entity.HistoryFilter{}, entity.ErrNotInRoom
		}
	case !errors.Is(err, repository.ErrMembershipNotFound):
		return entity.HistoryFilter{}, err
	}

	history, err := s.events.VisibilityFor(ctx, roomID, caller)
	if err != nil {
		return entity.HistoryFilter{}, err
	}
	if !history.EverEntitled() {
		return entity.HistoryFilter{}, entity.ErrNotInRoom
	}
	return history, nil
}

func errNotFound(err error) error {
	if errors.Is(err, entity.ErrRoomNotFound) || errors.Is(err, entity.ErrNotInRoom) {
		return entity.ErrEventNotFound
	}
	return err
}
