package rooms

import (
	"context"
	"slices"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *srv) Relations(ctx context.Context, scope entity.TenantScope, caller, deviceID string,
	in entity.RelationsRequest,
) (entity.Relations, error) {
	if err := in.Validate(); err != nil {
		return entity.Relations{}, err
	}
	history, err := s.readableRoom(ctx, scope, caller, in.RoomID)
	if err != nil {
		return entity.Relations{}, errNotFound(err)
	}

	parent, err := s.events.Event(ctx, in.EventID)
	if err != nil {
		return entity.Relations{}, err
	}
	if parent.Event.RoomID() != in.RoomID || !history.Visible(parent) {
		return entity.Relations{}, entity.ErrEventNotFound
	}

	from, err := s.at(ctx, in.RoomID, in.From)
	if err != nil {
		return entity.Relations{}, err
	}
	to, err := s.at(ctx, in.RoomID, in.To)
	if err != nil {
		return entity.Relations{}, err
	}

	v := view{scope: scope, caller: caller, deviceID: deviceID, roomID: in.RoomID, history: history}
	limit := entity.PageLimit(in.Limit, 0)
	query := entity.RelationQuery{
		ParentIDs: []string{in.EventID},
		RelType:   in.RelType,
		EventType: in.EventType,
		From:      from,
		To:        to,
		Backwards: in.Backwards(),
		Limit:     limit,
	}

	refs, exhausted, err := s.children(ctx, v, query, in.Recurse)
	if err != nil {
		return entity.Relations{}, err
	}

	chunk, err := s.byRef(ctx, v, refs)
	if err != nil {
		return entity.Relations{}, err
	}

	out := entity.Relations{Chunk: chunk}
	if in.Recurse {
		depth := entity.RecursionDepth
		out.Depth = &depth
	}
	if from != nil {
		out.PrevBatch = entity.Anchor(*from).String()
	}
	if !exhausted && len(refs) > 0 {
		out.NextBatch = entity.Anchor(refs[len(refs)-1].Position).String()
	}
	return out, nil
}

func (s *srv) children(ctx context.Context, v view, query entity.RelationQuery, recurse bool) ([]entity.RelationRef, bool, error) {
	if !recurse {
		found, err := s.events.Relations(ctx, v.roomID, query)
		if err != nil {
			return nil, false, err
		}
		exhausted := len(found) < query.Limit
		return visibleRefs(v, found), exhausted, nil
	}

	seen := map[string]bool{}
	frontier := query.ParentIDs
	var all []entity.RelationRef

	for range entity.RecursionDepth {
		if len(frontier) == 0 {
			break
		}
		found, err := s.events.Relations(ctx, v.roomID, entity.RelationQuery{
			ParentIDs: frontier,
			RelType:   query.RelType,
			EventType: query.EventType,
		})
		if err != nil {
			return nil, false, err
		}

		frontier = nil
		for _, ref := range visibleRefs(v, found) {
			if seen[ref.EventID] {
				continue
			}
			seen[ref.EventID] = true
			all = append(all, ref)
			frontier = append(frontier, ref.EventID)
		}
	}

	slices.SortFunc(all, func(a, b entity.RelationRef) int {
		if query.Backwards {
			return entity.ComparePositions(b.Position, a.Position)
		}
		return entity.ComparePositions(a.Position, b.Position)
	})

	bounded := all[:0]
	for _, ref := range all {
		if !within(ref.Position, query) {
			continue
		}
		bounded = append(bounded, ref)
	}
	if len(bounded) <= query.Limit {
		return bounded, true, nil
	}
	return bounded[:query.Limit], false, nil
}

func within(at entity.Position, query entity.RelationQuery) bool {
	after, before := query.From, query.To
	if query.Backwards {
		after, before = query.To, query.From
	}
	if after != nil && !after.Before(at) {
		return false
	}
	if before != nil && !at.Before(*before) {
		return false
	}
	return true
}

func visibleRefs(v view, refs []entity.RelationRef) []entity.RelationRef {
	out := make([]entity.RelationRef, 0, len(refs))
	for _, ref := range refs {
		if v.history.VisibleAt(ref.Position, ref.Disposition) {
			out = append(out, ref)
		}
	}
	return out
}

func (s *srv) byRef(ctx context.Context, v view, refs []entity.RelationRef) ([]entity.ClientEvent, error) {
	if len(refs) == 0 {
		return []entity.ClientEvent{}, nil
	}

	nids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		nids = append(nids, ref.ChildNID)
	}
	found, err := s.events.Many(ctx, nids)
	if err != nil {
		return nil, err
	}

	byNID := make(map[int64]entity.StoredEvent, len(found))
	for _, stored := range found {
		byNID[stored.NID] = stored
	}
	ordered := make([]entity.StoredEvent, 0, len(refs))
	for _, ref := range refs {
		if stored, ok := byNID[ref.ChildNID]; ok {
			ordered = append(ordered, stored)
		}
	}
	return s.enriched(ctx, v, ordered)
}

func (s *srv) Threads(ctx context.Context, scope entity.TenantScope, caller, deviceID string,
	in entity.ThreadsRequest,
) (entity.Threads, error) {
	if err := in.Validate(); err != nil {
		return entity.Threads{}, err
	}
	history, err := s.readableRoom(ctx, scope, caller, in.RoomID)
	if err != nil {
		return entity.Threads{}, err
	}
	from, err := s.at(ctx, in.RoomID, in.From)
	if err != nil {
		return entity.Threads{}, err
	}

	v := view{scope: scope, caller: caller, deviceID: deviceID, roomID: in.RoomID, history: history}
	refs, err := s.events.Relations(ctx, in.RoomID, entity.RelationQuery{RelType: entity.RelThread})
	if err != nil {
		return entity.Threads{}, err
	}

	roots := map[string]entity.Position{}
	joined := map[string]bool{}
	for _, ref := range visibleRefs(v, refs) {
		if latest, seen := roots[ref.ParentID]; !seen || latest.Before(ref.Position) {
			roots[ref.ParentID] = ref.Position
		}
		if ref.Sender == caller {
			joined[ref.ParentID] = true
		}
	}

	ordered := make([]string, 0, len(roots))
	for parentID, latest := range roots {
		if from != nil && !latest.Before(*from) {
			continue
		}
		ordered = append(ordered, parentID)
	}
	slices.SortFunc(ordered, func(a, b string) int { return entity.ComparePositions(roots[b], roots[a]) })

	limit := entity.PageLimit(in.Limit, 0)
	selected := make([]entity.StoredEvent, 0, limit)
	var last entity.Position
	exhausted := true

	for start := 0; start < len(ordered) && exhausted; start += limit {
		batch := ordered[start:min(start+limit, len(ordered))]
		found, err := s.events.ManyByID(ctx, batch)
		if err != nil {
			return entity.Threads{}, err
		}
		byID := make(map[string]entity.StoredEvent, len(found))
		for _, stored := range found {
			byID[stored.Event.ID()] = stored
		}

		for _, parentID := range batch {
			stored, ok := byID[parentID]
			if !ok || !history.Visible(stored) {
				continue
			}
			if in.Include == entity.ThreadsParticipated && !joined[parentID] && stored.Event.Sender() != caller {
				continue
			}
			if len(selected) == limit {
				exhausted = false
				break
			}
			selected = append(selected, stored)
			last = roots[parentID]
		}
	}

	chunk, err := s.enriched(ctx, v, selected)
	if err != nil {
		return entity.Threads{}, err
	}

	out := entity.Threads{Chunk: chunk}
	if !exhausted {
		out.NextBatch = entity.Anchor(last).String()
	}
	return out, nil
}
