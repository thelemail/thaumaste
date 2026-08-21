package entity

import (
	"errors"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrRoomNotFound      = errors.New("entity: room not found")
	ErrRoomAlreadyExists = errors.New("entity: room already exists")
	ErrEventNotFound     = errors.New("entity: event not found")
	ErrEventExists       = errors.New("entity: event already exists")
)

// Disposition is what happened to an event on the way in. The spec has four distinct outcomes and
// collapsing them into a boolean loses the difference between "we hold a redacted copy" and "this
// failed authorisation", which federation needs to tell apart.
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

// Deliverable reports whether an event may reach clients or be named as a parent. A rejected event
// is stored but never relayed and never becomes a prev_event.
func (d Disposition) Deliverable() bool {
	return d == DispositionAccepted || d == DispositionRedacted
}

type Room struct {
	NID       int64
	TenantID  uuid.UUID
	RoomID    string
	Version   RoomVersionID
	CreatedAt time.Time
}

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

// StoredEvent is an event as it sits in the database: the canonical bytes, plus the properties the
// storage layer indexes on. SenderIsLocal is one of them rather than something recomputed on read,
// so no query can assume every participant shares the room's tenant.
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

type NewRoomEvent struct {
	Version RoomVersionID
	Creator string
	Content map[string]any
}

func (n NewRoomEvent) Validate() error {
	if _, err := LookupRoomVersion(n.Version); err != nil {
		return err
	}
	if !isUserID(n.Creator) {
		return ErrEventMalformed
	}
	return nil
}

type NewEvent struct {
	RoomID   string
	Type     string
	StateKey *string
	Sender   string
	Content  map[string]any
}

func (n NewEvent) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.RoomID, validation.Required, validation.Length(1, MaxRoomIDBytes)),
		validation.Field(&n.Type, validation.Required, validation.Length(1, MaxEventTypeSize)),
		validation.Field(&n.Sender, validation.Required, validation.Length(1, MaxUserIDBytes)),
	)
}
