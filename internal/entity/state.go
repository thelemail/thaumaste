package entity

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
)

var ErrStateMissing = errors.New("entity: required state is missing")

const (
	MembershipJoin   = "join"
	MembershipInvite = "invite"
	MembershipLeave  = "leave"
	MembershipBan    = "ban"
	MembershipKnock  = "knock"
)

const (
	JoinRulePublic          = "public"
	JoinRuleInvite          = "invite"
	JoinRuleKnock           = "knock"
	JoinRuleRestricted      = "restricted"
	JoinRuleKnockRestricted = "knock_restricted"
	JoinRulePrivate         = "private"
)

type StateKey struct {
	Type     string
	StateKey string
}

type StateMap map[StateKey]Event

func (s StateMap) Get(eventType, stateKey string) (Event, bool) {
	e, ok := s[StateKey{Type: eventType, StateKey: stateKey}]
	return e, ok
}

func (s StateMap) Clone() StateMap {
	out := make(StateMap, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// Apply is the whole of state resolution while every event has one parent: the state before an
// event is its parent's state after.
func (s StateMap) Apply(e Event) StateMap {
	stateKey, ok := e.StateKey()
	if !ok {
		return s
	}
	out := s.Clone()
	out[StateKey{Type: e.Type(), StateKey: stateKey}] = e
	return out
}

func (s StateMap) Membership(userID string) string {
	e, ok := s.Get(EventTypeMember, userID)
	if !ok {
		return ""
	}
	membership, _ := e.Content()["membership"].(string)
	return membership
}

func (s StateMap) JoinRule() string {
	e, ok := s.Get(EventTypeJoinRules, "")
	if !ok {
		return JoinRuleInvite
	}
	rule, _ := e.Content()["join_rule"].(string)
	return rule
}

func (s StateMap) Create() (Event, bool) {
	return s.Get(EventTypeCreate, "")
}

func (s StateMap) Creators(version RoomVersion) []string {
	create, ok := s.Create()
	if !ok {
		return nil
	}
	out := []string{create.Sender()}
	if !version.AdditionalCreators {
		return out
	}
	extra, _ := create.Content()["additional_creators"].([]any)
	for _, item := range extra {
		if id, ok := item.(string); ok && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (s StateMap) PowerLevels(version RoomVersion) (PowerLevels, error) {
	creators := s.Creators(version)
	e, ok := s.Get(EventTypePowerLevels, "")
	if !ok {
		levels := DefaultPowerLevels(version, creators)
		if create, ok := s.Create(); ok && !version.CreatorsOutrankPowerLevels {
			levels.Users = map[string]int64{create.Sender(): 100}
		}
		return levels, nil
	}
	return ParsePowerLevels(e.Content(), version, creators)
}

func (s StateMap) Keys() []StateKey {
	out := make([]StateKey, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b StateKey) int {
		if a.Type != b.Type {
			if a.Type < b.Type {
				return -1
			}
			return 1
		}
		if a.StateKey == b.StateKey {
			return 0
		}
		if a.StateKey < b.StateKey {
			return -1
		}
		return 1
	})
	return out
}

// SnapshotHash addresses a state map by its content, so state a room already holds is a lookup
// rather than another row.
func (s StateMap) SnapshotHash() [32]byte {
	h := sha256.New()
	var length [8]byte
	for _, key := range s.Keys() {
		for _, part := range []string{key.Type, key.StateKey, s[key].ID()} {
			binary.BigEndian.PutUint64(length[:], uint64(len(part)))
			_, _ = h.Write(length[:])
			_, _ = h.Write([]byte(part))
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
