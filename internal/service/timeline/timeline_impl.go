package timeline

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	events service.Events
	clock  func() time.Time
}

func New(events service.Events, clock func() time.Time) service.Timeline {
	if clock == nil {
		clock = time.Now
	}
	return &srv{events: events, clock: clock}
}

type enrichment struct {
	redactions   map[int64]entity.ClientEvent
	bundles      map[string]*entity.Aggregation
	transactions map[string]string
}

func (s *srv) Render(ctx context.Context, v entity.TimelineView, scanned []entity.StoredEvent) ([]entity.ClientEvent, error) {
	kept := make([]entity.StoredEvent, 0, len(scanned))
	for _, stored := range scanned {
		if v.History.Visible(stored) && v.Filter.Keeps(stored.Event) {
			kept = append(kept, stored)
		}
	}
	return s.Enriched(ctx, v, kept)
}

func (s *srv) Single(ctx context.Context, v entity.TimelineView, stored entity.StoredEvent) (entity.ClientEvent, error) {
	out, err := s.Enriched(ctx, v, []entity.StoredEvent{stored})
	if err != nil {
		return entity.ClientEvent{}, err
	}
	return out[0], nil
}

func (s *srv) Enriched(ctx context.Context, v entity.TimelineView, kept []entity.StoredEvent) ([]entity.ClientEvent, error) {
	extra, err := s.enrich(ctx, v, kept)
	if err != nil {
		return nil, err
	}
	out := make([]entity.ClientEvent, 0, len(kept))
	for _, stored := range kept {
		out = append(out, s.clientEvent(v, extra, stored))
	}
	return out, nil
}

func (s *srv) enrich(ctx context.Context, v entity.TimelineView, kept []entity.StoredEvent) (enrichment, error) {
	redactions, err := s.redactions(ctx, v, kept)
	if err != nil {
		return enrichment{}, err
	}
	bundles, err := s.bundles(ctx, v, kept, true)
	if err != nil {
		return enrichment{}, err
	}
	transactions, err := s.transactions(ctx, v, kept)
	if err != nil {
		return enrichment{}, err
	}
	return enrichment{redactions: redactions, bundles: bundles, transactions: transactions}, nil
}

func (s *srv) bundles(ctx context.Context, v entity.TimelineView, kept []entity.StoredEvent, nested bool) (map[string]*entity.Aggregation, error) {
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

	refs, err := s.events.Relations(ctx, v.RoomID, entity.RelationQuery{ParentIDs: ids})
	if err != nil {
		return nil, err
	}

	grouped := make(map[string][]entity.RelationRef)
	for _, ref := range refs {
		if v.History.VisibleAt(ref.Position, ref.Disposition) {
			grouped[ref.ParentID] = append(grouped[ref.ParentID], ref)
		}
	}
	if len(grouped) == 0 {
		return nil, nil
	}

	plans := make(map[string]entity.BundlePlan, len(grouped))
	var wanted []int64
	for parentID, refs := range grouped {
		plan := entity.PlanBundle(parents[parentID], v.Caller, refs)
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

func (s *srv) redactions(ctx context.Context, v entity.TimelineView, kept []entity.StoredEvent) (map[int64]entity.ClientEvent, error) {
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
		if !v.History.Visible(stored) {
			continue
		}
		out[stored.NID] = entity.ClientEvent{
			Event: stored.Event,
			Age:   now - stored.Event.OriginServerTS(),
		}
	}
	return out, nil
}

func (s *srv) transactions(ctx context.Context, v entity.TimelineView, kept []entity.StoredEvent) (map[string]string, error) {
	if v.DeviceID == "" {
		return nil, nil
	}
	ids := make([]string, 0, len(kept))
	for _, stored := range kept {
		if stored.Event.Sender() == v.Caller {
			ids = append(ids, stored.Event.ID())
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return s.events.TransactionsFor(ctx, entity.TransactionSender{
		TenantID: v.Scope.ID(),
		UserID:   v.Caller,
		DeviceID: v.DeviceID,
	}, ids)
}

func (s *srv) clientEvent(v entity.TimelineView, extra enrichment, stored entity.StoredEvent) entity.ClientEvent {
	client := entity.ClientEvent{
		Event:         stored.Event,
		Age:           s.clock().UTC().UnixMilli() - stored.Event.OriginServerTS(),
		Membership:    v.History.MembershipAt(entity.PositionOf(stored)),
		TransactionID: extra.transactions[stored.Event.ID()],
	}
	if because, ok := extra.redactions[stored.RedactedByNID]; ok {
		client.RedactedBecause = &because
	}
	client.Relations = extra.bundles[stored.Event.ID()]
	return client
}
