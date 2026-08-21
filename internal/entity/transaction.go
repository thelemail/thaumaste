package entity

import (
	"errors"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var ErrTransactionNotFound = errors.New("entity: transaction not found")

const (
	MaxTransactionIDBytes = 255
	MaxEndpointBytes      = 64

	EndpointSend = "send"
)

type EventTransaction struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	Endpoint string
	RoomID   string
	TxnID    string
	EventID  string
	Recorded time.Time
}

func (EventTransaction) Validate() error { return nil }

type NewEventTransaction struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	Endpoint string
	RoomID   string
	TxnID    string
	EventID  string
}

func (n NewEventTransaction) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.Required),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.Endpoint, validation.Required, validation.Length(1, MaxEndpointBytes)),
		validation.Field(&n.RoomID, validation.Required, validation.Length(1, MaxRoomIDBytes)),
		validation.Field(&n.TxnID, validation.Required, validation.Length(1, MaxTransactionIDBytes)),
		validation.Field(&n.EventID, validation.Required, validation.Length(1, MaxEventIDBytes)),
	)
}

type TransactionSender struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
}

type TransactionKey struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	Endpoint string
	RoomID   string
	TxnID    string
}

func (n NewEventTransaction) Key() TransactionKey {
	return TransactionKey{
		TenantID: n.TenantID,
		UserID:   n.UserID,
		DeviceID: n.DeviceID,
		Endpoint: n.Endpoint,
		RoomID:   n.RoomID,
		TxnID:    n.TxnID,
	}
}
