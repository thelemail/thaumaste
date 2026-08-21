package entity

import (
	"errors"
	"slices"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrUnorderedWrite    = errors.New("entity: event written outside the stream order")
	ErrRoomNotFound      = errors.New("entity: room not found")
	ErrRoomAlreadyExists = errors.New("entity: room already exists")
	ErrEventNotFound     = errors.New("entity: event not found")
	ErrEventExists       = errors.New("entity: event already exists")
)

type Disposition string

const (
	DispositionAccepted   Disposition = "accepted"
	DispositionRedacted   Disposition = "redacted"
	DispositionRejected   Disposition = "rejected"
	DispositionSoftFailed Disposition = "soft_failed"
	DispositionOutlier    Disposition = "outlier"
)

func (d Disposition) Valid() bool {
	switch d {
	case DispositionAccepted, DispositionRedacted, DispositionRejected,
		DispositionSoftFailed, DispositionOutlier:
		return true
	default:
		return false
	}
}

func (d Disposition) Deliverable() bool {
	return d == DispositionAccepted || d == DispositionRedacted
}

type Room struct {
	NID        int64
	TenantID   uuid.UUID
	RoomID     string
	Version    RoomVersionID
	Visibility string
	CreatedAt  time.Time
}

func (r Room) Public() bool { return r.Visibility == VisibilityPublic }

func (Room) Validate() error { return nil }

type NewRoom struct {
	TenantID uuid.UUID
	RoomID   string
	Version  RoomVersionID
}

func (n NewRoom) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.RoomID, validation.Required, validation.Length(1, MaxRoomIDBytes)),
	); err != nil {
		return err
	}
	if _, err := LookupRoomVersion(n.Version); err != nil {
		return err
	}
	return nil
}

type StoredEvent struct {
	NID                 int64
	RoomNID             int64
	Event               Event
	SenderIsLocal       bool
	StreamOrdering      int64
	TopologicalOrdering int64
	InstanceName        string
	StateSnapshotNID    int64
	Disposition         Disposition
	RedactedByNID       int64
}

func (StoredEvent) Validate() error { return nil }

type NewStoredEvent struct {
	RoomNID             int64
	Event               Event
	SenderIsLocal       bool
	StreamOrdering      int64
	TopologicalOrdering int64
	InstanceName        string
	StateSnapshotNID    int64
	Disposition         Disposition
}

func (n NewStoredEvent) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.InstanceName, validation.Required, validation.Length(1, 255)),
	); err != nil {
		return err
	}
	if n.RoomNID == 0 {
		return validation.Errors{"roomNID": ErrRoomNotFound}
	}
	if n.Event.ID() == "" {
		return ErrEventMalformed
	}
	if !n.Disposition.Valid() {
		return validation.Errors{"disposition": errors.New("must be a known disposition")}
	}
	return nil
}

type TransactionRef struct {
	DeviceID string
	Endpoint string
	TxnID    string
}

type NewEvent struct {
	RoomID   string
	Type     string
	StateKey *string
	Sender   string
	Content  map[string]any
	Txn      *TransactionRef
}

func (n NewEvent) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.RoomID, validation.Required, validation.Length(1, MaxRoomIDBytes)),
		validation.Field(&n.Type, validation.Required, validation.Length(1, MaxEventTypeSize)),
		validation.Field(&n.Sender, validation.Required, validation.Length(1, MaxUserIDBytes)),
	)
}

type RoomMembership struct {
	TenantID   uuid.UUID
	RoomNID    int64
	RoomID     string
	UserID     string
	Membership string
	EventNID   int64
	Forgotten  bool
}

func (RoomMembership) Validate() error { return nil }

type NewRoomMembership struct {
	TenantID   uuid.UUID
	RoomNID    int64
	UserID     string
	Membership string
	EventNID   int64
	Forgotten  bool
}

func (n NewRoomMembership) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.RoomNID, validation.Required),
		validation.Field(&n.EventNID, validation.Required),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
	); err != nil {
		return err
	}
	if !slices.Contains(memberships, n.Membership) {
		return ErrEventMalformed
	}
	return nil
}

var ErrNotInRoom = errors.New("entity: user is not in the room")

type RoomMember struct {
	UserID      string
	DisplayName string
	AvatarURL   string
}

func (RoomMember) Validate() error { return nil }

type PublicRoom struct {
	RoomID           string
	Name             string
	Topic            string
	CanonicalAlias   string
	AvatarURL        string
	RoomType         string
	JoinRule         string
	NumJoinedMembers int64
	WorldReadable    bool
	GuestCanJoin     bool
}

func (PublicRoom) Validate() error { return nil }

func (p PublicRoom) Matches(term string) bool {
	if term == "" {
		return true
	}
	term = strings.ToLower(term)
	for _, field := range []string{p.Name, p.Topic, p.CanonicalAlias} {
		if strings.Contains(strings.ToLower(field), term) {
			return true
		}
	}
	return false
}

type SyncRoom struct {
	RoomNID    int64
	RoomID     string
	Version    RoomVersionID
	Membership string
	EventNID   int64
	Forgotten  bool
	LastStream int64
	BumpStream int64
}

func (SyncRoom) Validate() error { return nil }

type RoomWindow struct {
	RoomNID int64
	After   int64
}

type MemberCounts struct {
	Joined  int
	Invited int
}
