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

	chunk, err := s.render(ctx, view{scope, caller, deviceID, in.RoomID, history, filter}, scanned)
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

	v := view{scope, caller, deviceID, in.RoomID, history, filter}
	target, err := s.single(ctx, v, stored)
	if err != nil {
		return entity.Context{}, err
	}
	renderedBefore, err := s.render(ctx, v, before)
	if err != nil {
		return entity.Context{}, err
	}
	renderedAfter, err := s.render(ctx, v, after)
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

type view struct {
	scope    entity.TenantScope
	caller   string
	deviceID string
	roomID   string
	history  entity.HistoryFilter
	filter   entity.RoomEventFilter
}

type enrichment struct {
	redactions map[int64]entity.ClientEvent
	bundles    map[string]*entity.Aggregation
}

func (s *srv) render(ctx context.Context, v view, scanned []entity.StoredEvent) ([]entity.ClientEvent, error) {
	kept := make([]entity.StoredEvent, 0, len(scanned))
	for _, stored := range scanned {
		if v.history.Visible(stored) && v.filter.Keeps(stored.Event) {
			kept = append(kept, stored)
		}
	}
	return s.enriched(ctx, v, kept)
}

func (s *srv) single(ctx context.Context, v view, stored entity.StoredEvent) (entity.ClientEvent, error) {
	out, err := s.enriched(ctx, v, []entity.StoredEvent{stored})
	if err != nil {
		return entity.ClientEvent{}, err
	}
	return out[0], nil
}

func (s *srv) enriched(ctx context.Context, v view, kept []entity.StoredEvent) ([]entity.ClientEvent, error) {
	extra, err := s.enrich(ctx, v, kept)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ClientEvent, 0, len(kept))
	for _, stored := range kept {
		out = append(out, s.clientEvent(ctx, v, extra, stored))
	}
	return out, nil
}

func (s *srv) enrich(ctx context.Context, v view, kept []entity.StoredEvent) (enrichment, error) {
	redactions, err := s.redactions(ctx, v, kept)
	if err != nil {
		return enrichment{}, err
	}
	bundles, err := s.bundles(ctx, v, kept, true)
	if err != nil {
		return enrichment{}, err
	}
	return enrichment{redactions: redactions, bundles: bundles}, nil
}

func (s *srv) bundles(ctx context.Context, v view, kept []entity.StoredEvent, nested bool) (map[string]*entity.Aggregation, error) {
	parents := make(map[string]entity.StoredEvent, len(kept))
	ids := make([]string, 0, len(kept))
	for _, stored := range kept {
		if stored.Event.IsState() {
			continue
		}
		parents[stored.Event.ID()] = stored
		ids = append(ids, stored.Event.ID())
	}
	if len(ids) == 0 {
		return nil, nil
	}

	refs, err := s.events.Relations(ctx, v.roomID, entity.RelationQuery{ParentIDs: ids})
	if err != nil {
		return nil, err
	}

	grouped := make(map[string][]entity.RelationRef)
	for _, ref := range refs {
		if v.history.VisibleAt(ref.Position, ref.Disposition) {
			grouped[ref.ParentID] = append(grouped[ref.ParentID], ref)
		}
	}
	if len(grouped) == 0 {
		return nil, nil
	}

	plans := make(map[string]entity.BundlePlan, len(grouped))
	var wanted []int64
	for parentID, refs := range grouped {
		plan := entity.PlanBundle(parents[parentID], v.caller, refs)
		if plan.Empty() {
			continue
		}
		plans[parentID] = plan
		wanted = append(wanted, plan.Wanted()...)
	}
	if len(plans) == 0 {
		return nil, nil
	}

	fetched, err := s.events.Many(ctx, wanted)
	if err != nil {
		return nil, err
	}
	byNID := make(map[int64]entity.StoredEvent, len(fetched))
	for _, stored := range fetched {
		byNID[stored.NID] = stored
	}

	inner := map[string]*entity.Aggregation(nil)
	if nested {
		latest := make([]entity.StoredEvent, 0, len(plans))
		for _, plan := range plans {
			if stored, ok := byNID[plan.ThreadLatest]; ok {
				latest = append(latest, stored)
			}
		}
		if inner, err = s.bundles(ctx, v, latest, false); err != nil {
			return nil, err
		}
	}

	now := s.clock().UTC().UnixMilli()
	out := make(map[string]*entity.Aggregation, len(plans))
	for parentID, plan := range plans {
		aggregation := &entity.Aggregation{Reference: plan.Reference}

		candidates := make([]entity.StoredEvent, 0, len(plan.Replacements))
		for _, nid := range plan.Replacements {
			if stored, ok := byNID[nid]; ok {
				candidates = append(candidates, stored)
			}
		}
		if best, ok := entity.ChooseReplacement(parents[parentID].Event, candidates); ok {
			replacement := entity.ClientEvent{Event: best.Event, Age: now - best.Event.OriginServerTS()}
			aggregation.Replace = &replacement
		}

		if stored, ok := byNID[plan.ThreadLatest]; ok {
			aggregation.Thread = &entity.ThreadSummary{
				Latest: entity.ClientEvent{
					Event:     stored.Event,
					Age:       now - stored.Event.OriginServerTS(),
					Relations: inner[stored.Event.ID()],
				},
				Count:        plan.ThreadCount,
				Participated: plan.ThreadParticipated,
			}
		}
		out[parentID] = aggregation
	}
	return out, nil
}

func (s *srv) redactions(ctx context.Context, v view, kept []entity.StoredEvent) (map[int64]entity.ClientEvent, error) {
	var nids []int64
	for _, stored := range kept {
		if stored.RedactedByNID != 0 {
			nids = append(nids, stored.RedactedByNID)
		}
	}
	if len(nids) == 0 {
		return nil, nil
	}

	found, err := s.events.Many(ctx, nids)
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC().UnixMilli()
	out := make(map[int64]entity.ClientEvent, len(found))
	for _, stored := range found {
		if !v.history.Visible(stored) {
			continue
		}
		out[stored.NID] = entity.ClientEvent{
			Event: stored.Event,
			Age:   now - stored.Event.OriginServerTS(),
		}
	}
	return out, nil
}

func (s *srv) clientEvent(ctx context.Context, v view, extra enrichment, stored entity.StoredEvent) entity.ClientEvent {
	client := entity.ClientEvent{
		Event:      stored.Event,
		Age:        s.clock().UTC().UnixMilli() - stored.Event.OriginServerTS(),
		Membership: v.history.MembershipAt(entity.PositionOf(stored)),
	}
	if v.deviceID != "" && stored.Event.Sender() == v.caller {
		client.TransactionID, _ = s.events.TransactionFor(ctx, entity.TransactionSender{
			TenantID: v.scope.ID(),
			UserID:   v.caller,
			DeviceID: v.deviceID,
		}, stored.Event.ID())
	}
	if because, ok := extra.redactions[stored.RedactedByNID]; ok {
		client.RedactedBecause = &because
	}
	client.Relations = extra.bundles[stored.Event.ID()]
	return client
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
