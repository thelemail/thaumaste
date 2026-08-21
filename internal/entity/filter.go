package entity

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

const (
	MaxStoredFilterBytes = 65536
	MaxFilterIDBytes     = 64

	EventFormatClient     = "client"
	EventFormatFederation = "federation"

	roomIDSigil = '!'
)

var (
	ErrFilterNotFound = errors.New("entity: filter not found")
	ErrFilterTooLarge = errors.New("entity: filter is too large")
)

type eventFilterDoc struct {
	Limit      *int      `json:"limit"`
	Types      *[]string `json:"types"`
	NotTypes   *[]string `json:"not_types"`
	Senders    *[]string `json:"senders"`
	NotSenders *[]string `json:"not_senders"`
}

type roomEventFilterDoc struct {
	Limit                     *int      `json:"limit"`
	Types                     *[]string `json:"types"`
	NotTypes                  *[]string `json:"not_types"`
	Senders                   *[]string `json:"senders"`
	NotSenders                *[]string `json:"not_senders"`
	Rooms                     *[]string `json:"rooms"`
	NotRooms                  *[]string `json:"not_rooms"`
	ContainsURL               *bool     `json:"contains_url"`
	LazyLoadMembers           *bool     `json:"lazy_load_members"`
	IncludeRedundantMembers   *bool     `json:"include_redundant_members"`
	UnreadThreadNotifications *bool     `json:"unread_thread_notifications"`
}

type roomFilterDoc struct {
	Rooms        *[]string           `json:"rooms"`
	NotRooms     *[]string           `json:"not_rooms"`
	Timeline     *roomEventFilterDoc `json:"timeline"`
	State        *roomEventFilterDoc `json:"state"`
	Ephemeral    *roomEventFilterDoc `json:"ephemeral"`
	AccountData  *roomEventFilterDoc `json:"account_data"`
	IncludeLeave *bool               `json:"include_leave"`
}

type filterDoc struct {
	Room        *roomFilterDoc  `json:"room"`
	Presence    *eventFilterDoc `json:"presence"`
	AccountData *eventFilterDoc `json:"account_data"`
	EventFields *[]string       `json:"event_fields"`
	EventFormat *string         `json:"event_format"`
}

type Filter struct {
	ID       string
	Document json.RawMessage
	doc      filterDoc
}

func (f Filter) Validate() error { return nil }

func ParseFilter(raw []byte) (Filter, error) {
	if len(raw) > MaxStoredFilterBytes {
		return Filter{}, ErrFilterTooLarge
	}
	var doc filterDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Filter{}, ErrBadFilter
	}
	if err := doc.check(); err != nil {
		return Filter{}, err
	}
	stored := make([]byte, len(raw))
	copy(stored, raw)
	return Filter{Document: stored, doc: doc}, nil
}

func (d filterDoc) check() error {
	if d.EventFormat != nil && *d.EventFormat != EventFormatClient && *d.EventFormat != EventFormatFederation {
		return ErrBadFilter
	}
	if err := bounded(d.EventFields); err != nil {
		return err
	}
	if err := d.Presence.check(); err != nil {
		return err
	}
	if err := d.AccountData.check(); err != nil {
		return err
	}
	if d.Room == nil {
		return nil
	}
	if err := identifiers(d.Room.Rooms, isRoomID); err != nil {
		return err
	}
	if err := identifiers(d.Room.NotRooms, isRoomID); err != nil {
		return err
	}
	for _, section := range []*roomEventFilterDoc{d.Room.Timeline, d.Room.State, d.Room.Ephemeral, d.Room.AccountData} {
		if err := section.check(); err != nil {
			return err
		}
	}
	return nil
}

func (d *eventFilterDoc) check() error {
	if d == nil {
		return nil
	}
	if err := bounded(d.Types); err != nil {
		return err
	}
	if err := bounded(d.NotTypes); err != nil {
		return err
	}
	return errors.Join(identifiers(d.Senders, isUserID), identifiers(d.NotSenders, isUserID))
}

func (d *roomEventFilterDoc) check() error {
	if d == nil {
		return nil
	}
	if err := bounded(d.Types); err != nil {
		return err
	}
	if err := bounded(d.NotTypes); err != nil {
		return err
	}
	return errors.Join(
		identifiers(d.Senders, isUserID), identifiers(d.NotSenders, isUserID),
		identifiers(d.Rooms, isRoomID), identifiers(d.NotRooms, isRoomID),
	)
}

func bounded(list *[]string) error {
	if list != nil && len(*list) > MaxFilterEntries {
		return ErrBadFilter
	}
	return nil
}

func identifiers(list *[]string, valid func(string) bool) error {
	if err := bounded(list); err != nil {
		return err
	}
	if list == nil {
		return nil
	}
	for _, id := range *list {
		if !valid(id) {
			return ErrBadFilter
		}
	}
	return nil
}

func isRoomID(id string) bool {
	if len(id) < 2 || len(id) > MaxRoomIDBytes || id[0] != roomIDSigil {
		return false
	}
	return strings.Contains(id[1:], ":")
}

func (f Filter) Timeline() RoomEventFilter {
	if f.doc.Room == nil || f.doc.Room.Timeline == nil {
		return RoomEventFilter{}
	}
	timeline := f.doc.Room.Timeline
	out := RoomEventFilter{ContainsURL: timeline.ContainsURL}
	if timeline.Types != nil {
		out.Types = *timeline.Types
	}
	if timeline.NotTypes != nil {
		out.NotTypes = *timeline.NotTypes
	}
	if timeline.Senders != nil {
		out.Senders = *timeline.Senders
	}
	if timeline.NotSenders != nil {
		out.NotSenders = *timeline.NotSenders
	}
	if timeline.Limit != nil {
		out.Limit = *timeline.Limit
	}
	return out
}

func (f Filter) Hash() ([]byte, error) {
	normal, err := NormalJSON(f.Document)
	if err != nil {
		return nil, ErrBadFilter
	}
	sum := sha256.Sum256(normal)
	return sum[:], nil
}

type NewFilter struct {
	TenantID uuid.UUID
	UserID   string
	Filter   Filter
}

func (n NewFilter) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
	)
}
