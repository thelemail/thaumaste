package entity

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
