package entity

import "slices"

type visibilityChange struct {
	at    Position
	value string
}

type membershipChange struct {
	at    Position
	value string
}

type HistoryFilter struct {
	caller      string
	visibility  []visibilityChange
	memberships []membershipChange
}

func NewHistoryFilter(caller string, visibility, memberships []StoredEvent) HistoryFilter {
	f := HistoryFilter{caller: caller}

	for _, e := range visibility {
		value, _ := e.Event.Content()["history_visibility"].(string)
		if !slices.Contains(historyVisibilities, value) {
			value = HistoryVisibilityShared
		}
		f.visibility = append(f.visibility, visibilityChange{at: PositionOf(e), value: value})
	}
	for _, e := range memberships {
		value, _ := e.Event.Content()["membership"].(string)
		f.memberships = append(f.memberships, membershipChange{at: PositionOf(e), value: value})
	}

	slices.SortFunc(f.visibility, func(a, b visibilityChange) int { return comparePositions(a.at, b.at) })
	slices.SortFunc(f.memberships, func(a, b membershipChange) int { return comparePositions(a.at, b.at) })
	return f
}

func PositionOf(e StoredEvent) Position {
	return Position{Topological: e.TopologicalOrdering, Stream: e.StreamOrdering}
}

func comparePositions(a, b Position) int {
	switch {
	case a.Before(b):
		return -1
	case b.Before(a):
		return 1
	default:
		return 0
	}
}

func (f HistoryFilter) visibilityBefore(at Position) string {
	value := HistoryVisibilityShared
	for _, change := range f.visibility {
		if !change.at.Before(at) {
			break
		}
		value = change.value
	}
	return value
}

func (f HistoryFilter) membershipBefore(at Position) string {
	value := ""
	for _, change := range f.memberships {
		if !change.at.Before(at) {
			break
		}
		value = change.value
	}
	return value
}

func (f HistoryFilter) joinedAtOrAfter(at Position) bool {
	for _, change := range f.memberships {
		if change.value == MembershipJoin && !change.at.Before(at) {
			return true
		}
	}
	return false
}

func (f HistoryFilter) allows(visibility, membership string, at Position) bool {
	switch {
	case visibility == HistoryVisibilityWorldReadable:
		return true
	case membership == MembershipJoin:
		return true
	case visibility == HistoryVisibilityShared && f.joinedAtOrAfter(at):
		return true
	case visibility == HistoryVisibilityInvited && membership == MembershipInvite:
		return true
	default:
		return false
	}
}

func (f HistoryFilter) Visible(e StoredEvent) bool {
	if !e.Disposition.Deliverable() {
		return false
	}

	at := PositionOf(e)
	visibility := f.visibilityBefore(at)
	membership := f.membershipBefore(at)

	if f.allows(visibility, membership, at) {
		return true
	}

	stateKey, isState := e.Event.StateKey()
	switch {
	case isState && e.Event.Type() == EventTypeHistoryVisibility && stateKey == "":
		after, _ := e.Event.Content()["history_visibility"].(string)
		return f.allows(after, membership, at)
	case isState && e.Event.Type() == EventTypeMember && stateKey == f.caller:
		after, _ := e.Event.Content()["membership"].(string)
		return f.allows(visibility, after, at)
	default:
		return false
	}
}

func (f HistoryFilter) MembershipAt(at Position) string {
	value := ""
	for _, change := range f.memberships {
		if at.Before(change.at) {
			break
		}
		value = change.value
	}
	if value == "" {
		return MembershipLeave
	}
	return value
}
