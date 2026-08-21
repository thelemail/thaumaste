package entity

import (
	"cmp"
	"slices"
)

type ThreadSummary struct {
	Latest       ClientEvent
	Count        int
	Participated bool
}

type Aggregation struct {
	Replace   *ClientEvent
	Thread    *ThreadSummary
	Reference []string
}

func (a *Aggregation) json() (map[string]any, error) {
	if a == nil {
		return nil, nil
	}

	out := map[string]any{}
	if a.Replace != nil {
		raw, err := a.Replace.JSON()
		if err != nil {
			return nil, err
		}
		out[RelReplace] = raw
	}
	if a.Thread != nil {
		latest, err := a.Thread.Latest.JSON()
		if err != nil {
			return nil, err
		}
		out[RelThread] = map[string]any{
			"latest_event":              latest,
			"count":                     a.Thread.Count,
			"current_user_participated": a.Thread.Participated,
		}
	}
	if len(a.Reference) > 0 {
		chunk := make([]map[string]any, 0, len(a.Reference))
		for _, id := range a.Reference {
			chunk = append(chunk, map[string]any{"event_id": id})
		}
		out[RelReference] = map[string]any{"chunk": chunk}
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func ChooseReplacement(parent Event, candidates []StoredEvent) (StoredEvent, bool) {
	var best StoredEvent
	found := false
	for _, candidate := range candidates {
		if !ValidReplacement(parent, candidate.Event) {
			continue
		}
		if !found || MoreRecent(candidate.Event, best.Event) {
			best, found = candidate, true
		}
	}
	return best, found
}

const (
	MaxReplaceCandidates = 10
	MaxReferenceChunk    = 50
)

type BundlePlan struct {
	Replacements       []int64
	ThreadLatest       int64
	ThreadCount        int
	ThreadParticipated bool
	Reference          []string
}

func (p BundlePlan) Wanted() []int64 {
	out := append([]int64(nil), p.Replacements...)
	if p.ThreadLatest != 0 {
		out = append(out, p.ThreadLatest)
	}
	return out
}

func (p BundlePlan) Empty() bool {
	return len(p.Replacements) == 0 && p.ThreadLatest == 0 && len(p.Reference) == 0
}

func PlanBundle(parent StoredEvent, caller string, refs []RelationRef) BundlePlan {
	var plan BundlePlan
	var replacements, threads, references []RelationRef

	for _, ref := range refs {
		switch ref.RelType {
		case RelReplace:
			if ref.Sender == parent.Event.Sender() {
				replacements = append(replacements, ref)
			}
		case RelThread:
			threads = append(threads, ref)
		case RelReference:
			references = append(references, ref)
		}
	}

	if parent.Disposition != DispositionRedacted {
		slices.SortFunc(replacements, func(a, b RelationRef) int { return compareRecency(b, a) })
		for _, ref := range replacements[:min(len(replacements), MaxReplaceCandidates)] {
			plan.Replacements = append(plan.Replacements, ref.ChildNID)
		}
	}

	if len(threads) > 0 {
		latest := threads[0]
		plan.ThreadCount = len(threads)
		plan.ThreadParticipated = parent.Event.Sender() == caller
		for _, ref := range threads {
			if latest.Position.Before(ref.Position) {
				latest = ref
			}
			if ref.Sender == caller {
				plan.ThreadParticipated = true
			}
		}
		plan.ThreadLatest = latest.ChildNID
	}

	slices.SortFunc(references, func(a, b RelationRef) int { return comparePositions(a.Position, b.Position) })
	for _, ref := range references[:min(len(references), MaxReferenceChunk)] {
		plan.Reference = append(plan.Reference, ref.EventID)
	}
	return plan
}

func compareRecency(a, b RelationRef) int {
	if a.OriginServerTS != b.OriginServerTS {
		return cmp.Compare(a.OriginServerTS, b.OriginServerTS)
	}
	return cmp.Compare(a.EventID, b.EventID)
}
