package entity

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	MaxSyncLists         = 100
	MaxSyncSubscriptions = 100
	MaxSyncTimelineLimit = 1000
	MaxConnIDBytes       = 255
	MaxSyncHeroes        = 5
	MaxSyncRangeIndex    = 1 << 20

	wakeKeySeparator = ":"
)

var (
	ErrTooManyLists       = errors.New("entity: too many lists or room subscriptions")
	ErrBadRange           = errors.New("entity: range must be two ascending non-negative integers")
	ErrBadTimelineLimit   = errors.New("entity: timeline_limit is out of range")
	ErrFilterUnsupported  = errors.New("entity: filter is not supported by this server")
	ErrConnectionNotFound = errors.New("entity: sync connection not found")
)

var bumpingEventTypes = []string{
	EventTypeCreate,
	EventTypeMessage,
	EventTypeEncrypted,
	EventTypeSticker,
	EventTypeCallInvite,
	EventTypePollStart,
	EventTypeBeaconInfo,
}

func Bumping(eventType string) bool { return slices.Contains(bumpingEventTypes, eventType) }

func BumpingEventTypes() []string { return slices.Clone(bumpingEventTypes) }

type StateSelector struct {
	Type     string
	HasType  bool
	StateKey string
	HasKey   bool
}

func (s StateSelector) matches(eventType, stateKey string) bool {
	if s.HasType && s.Type != eventType {
		return false
	}
	if s.HasKey && s.StateKey != stateKey {
		return false
	}
	return true
}

func (s StateSelector) canonical() string {
	out := "*"
	if s.HasType {
		out = s.Type
	}
	if s.HasKey {
		return out + "\x1f" + s.StateKey
	}
	return out + "\x1f*"
}

type RequiredState struct {
	Include     []StateSelector
	Exclude     []StateSelector
	LazyMembers bool
}

func (r RequiredState) Selects(eventType, stateKey string) bool {
	if !slices.ContainsFunc(r.Include, func(s StateSelector) bool { return s.matches(eventType, stateKey) }) {
		return false
	}
	return !slices.ContainsFunc(r.Exclude, func(s StateSelector) bool { return s.matches(eventType, stateKey) })
}

func (r RequiredState) Canonical() []byte {
	parts := make([]string, 0, len(r.Include)+len(r.Exclude)+1)
	for _, s := range r.Include {
		parts = append(parts, "+"+s.canonical())
	}
	slices.Sort(parts)
	excludes := make([]string, 0, len(r.Exclude))
	for _, s := range r.Exclude {
		excludes = append(excludes, "-"+s.canonical())
	}
	slices.Sort(excludes)
	parts = append(parts, excludes...)
	if r.LazyMembers {
		parts = append(parts, "lazy")
	}
	return []byte(strings.Join(parts, "\x1e"))
}

type ListRange struct {
	Start int
	End   int
}

func (r ListRange) Contains(index int) bool { return index >= r.Start && index <= r.End }

type RoomFilter struct {
	IsEncrypted  *bool
	IsInvited    *bool
	RoomTypes    []*string
	NotRoomTypes []*string
	Unsupported  []string
}

func (f *RoomFilter) Matches(roomType *string, encrypted, invited bool) bool {
	if f == nil {
		return true
	}
	if f.IsEncrypted != nil && *f.IsEncrypted != encrypted {
		return false
	}
	if f.IsInvited != nil && *f.IsInvited != invited {
		return false
	}
	if len(f.RoomTypes) > 0 && !containsRoomType(f.RoomTypes, roomType) {
		return false
	}
	return !containsRoomType(f.NotRoomTypes, roomType)
}

func containsRoomType(wanted []*string, roomType *string) bool {
	return slices.ContainsFunc(wanted, func(w *string) bool {
		if w == nil || roomType == nil {
			return w == nil && roomType == nil
		}
		return *w == *roomType
	})
}

type RoomSubscription struct {
	TimelineLimit int
	RequiredState RequiredState
}

type SyncList struct {
	Range         *ListRange
	Filters       *RoomFilter
	TimelineLimit int
	RequiredState RequiredState
}

type SyncRequest struct {
	ConnID            string
	Pos               string
	Timeout           int
	SetPresence       string
	Lists             map[string]SyncList
	RoomSubscriptions map[string]RoomSubscription
	Extensions        map[string]json.RawMessage
}

func (r SyncRequest) Validate() error {
	if err := validation.ValidateStruct(&r,
		validation.Field(&r.ConnID, validation.Length(0, MaxConnIDBytes)),
	); err != nil {
		return err
	}
	if len(r.Lists) > MaxSyncLists || len(r.RoomSubscriptions) > MaxSyncSubscriptions {
		return ErrTooManyLists
	}
	for key, list := range r.Lists {
		if err := validateTimelineLimit(list.TimelineLimit); err != nil {
			return fmt.Errorf("%w in list %q", err, key)
		}
		if list.Range != nil && !validRange(*list.Range) {
			return fmt.Errorf("%w in list %q", ErrBadRange, key)
		}
		if list.Filters != nil && len(list.Filters.Unsupported) > 0 {
			return fmt.Errorf("%w: %s", ErrFilterUnsupported, strings.Join(list.Filters.Unsupported, ", "))
		}
	}
	for roomID, sub := range r.RoomSubscriptions {
		if roomID == "" || len(roomID) > MaxRoomIDBytes {
			return ErrRoomNotFound
		}
		if err := validateTimelineLimit(sub.TimelineLimit); err != nil {
			return fmt.Errorf("%w in subscription %q", err, roomID)
		}
	}
	return nil
}

func validRange(r ListRange) bool {
	return r.Start >= 0 && r.End >= r.Start && r.End <= MaxSyncRangeIndex
}

func validateTimelineLimit(limit int) error {
	if limit < 0 || limit > MaxSyncTimelineLimit {
		return ErrBadTimelineLimit
	}
	return nil
}

func RoomWakeKey(roomID string) string { return "room" + wakeKeySeparator + roomID }

func UserWakeKey(userID string) string { return "user" + wakeKeySeparator + userID }

var ErrDeviceRequired = errors.New("entity: sync requires a device")
