package entity

import (
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

type SyncCursors struct {
	Events      int64
	AccountData int64
	Receipts    int64
	DeviceLists int64
	Typing      int64
}

func (SyncCursors) Validate() error { return nil }

type Connection struct {
	NID              int64
	TenantID         uuid.UUID
	UserID           string
	DeviceID         string
	ConnID           string
	Confirmed        int64
	ConfirmedStream  int64
	ConfirmedCursors SyncCursors
	Pending          *int64
	PendingStream    *int64
	PendingCursors   SyncCursors
	LastSeenAt       time.Time
}

func (Connection) Validate() error { return nil }

func (c Connection) Position(generation int64) SyncPosition {
	return SyncPosition{ConnectionNID: c.NID, Generation: generation}
}

func (c Connection) Owns(scope TenantScope, userID, deviceID string) bool {
	return c.TenantID == scope.ID() && c.UserID == userID && c.DeviceID == deviceID
}

type NewConnection struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	ConnID   string
}

func (n NewConnection) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.ConnID, validation.Length(0, MaxConnIDBytes)),
	)
}

type RoomStatus struct {
	RoomNID       int64
	SentTo        int64
	TimelineLimit int
	RequiredState []byte
}

func (RoomStatus) Validate() error { return nil }

type NewRoomStatus struct {
	RoomNID       int64
	SentTo        int64
	TimelineLimit int
	RequiredState []byte
}

func (n NewRoomStatus) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.RoomNID, validation.Required),
		validation.Field(&n.TimelineLimit, validation.Min(0), validation.Max(MaxSyncTimelineLimit)),
	)
}
