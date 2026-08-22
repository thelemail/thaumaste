package entity

import (
	"encoding/json"
	"errors"
)

const (
	DefaultLegacyTimelineLimit = 10
	MaxLegacyTimelineLimit     = 100
)

var ErrFilterNotStored = errors.New("entity: no such filter")

type LegacySyncRequest struct {
	Since       string
	Timeout     int
	FullState   bool
	SetPresence string
	Filter      Filter
}

func (r LegacySyncRequest) Validate() error { return nil }

func (r LegacySyncRequest) TimelineLimit() int {
	limit := r.Filter.Timeline().Limit
	if limit <= 0 {
		limit = DefaultLegacyTimelineLimit
	}
	if limit > MaxLegacyTimelineLimit {
		limit = MaxLegacyTimelineLimit
	}
	return limit
}

type LegacyRoom struct {
	Timeline      []ClientEvent
	PrevBatch     string
	Limited       bool
	State         []ClientEvent
	Stripped      []Event
	Ephemeral     []json.RawMessage
	AccountData   []json.RawMessage
	Summary       RoomSummary
	HasSummary    bool
	Notifications NotificationCounts
}

type RoomSummary struct {
	Heroes       []Hero
	JoinedCount  int
	InvitedCount int
}

type NotificationCounts struct {
	Notification int
	Highlight    int
}

type LegacySyncResult struct {
	NextBatch     SyncToken
	Join          map[string]LegacyRoom
	Invite        map[string]LegacyRoom
	Knock         map[string]LegacyRoom
	Leave         map[string]LegacyRoom
	Presence      []json.RawMessage
	AccountData   []json.RawMessage
	ToDevice      []json.RawMessage
	DeviceLists   DeviceLists
	HasDeviceList bool
	OneTimeKeys   map[string]int
	FallbackTypes []string
}

func (r LegacySyncResult) Carries() bool {
	switch {
	case len(r.Join) > 0, len(r.Invite) > 0, len(r.Knock) > 0, len(r.Leave) > 0:
		return true
	case len(r.Presence) > 0, len(r.AccountData) > 0, len(r.ToDevice) > 0:
		return true
	case r.HasDeviceList && !r.DeviceLists.Empty():
		return true
	}
	return false
}
