package entity

import (
	"errors"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

const (
	EventTypeReceipt = "m.receipt"

	ReceiptRead        = "m.read"
	ReceiptReadPrivate = "m.read.private"

	ThreadMain       = "main"
	ThreadUnthreaded = ""

	MaxThreadIDBytes = 255
)

var (
	ErrReceiptTypeUnknown = errors.New("entity: unknown receipt type")
	ErrThreadUnknown      = errors.New("entity: thread id does not name a thread in this room")
	ErrReceiptNotFound    = errors.New("entity: receipt not found")
)

func ReceiptType(name string) bool {
	return name == ReceiptRead || name == ReceiptReadPrivate
}

type Receipt struct {
	RoomID    string
	UserID    string
	Type      string
	ThreadID  string
	EventID   string
	EventNID  int64
	Position  int64
	Timestamp int64
	StreamID  int64
}

func (Receipt) Validate() error { return nil }

type NewReceipt struct {
	TenantID  uuid.UUID
	RoomNID   int64
	UserID    string
	Type      string
	ThreadID  string
	EventNID  int64
	Timestamp int64
}

func (n NewReceipt) Validate() error {
	if !ReceiptType(n.Type) {
		return ErrReceiptTypeUnknown
	}
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.ThreadID, validation.Length(0, MaxThreadIDBytes)),
		validation.Field(&n.EventNID, validation.Required),
	)
}

type ReadMarker struct {
	FullyRead   string
	Read        string
	ReadPrivate string
}

func (m ReadMarker) Validate() error {
	if m.FullyRead == "" && m.Read == "" && m.ReadPrivate == "" {
		return validation.Errors{"m.fully_read": ErrEventNotFound}
	}
	return nil
}

func ReadUpTo(receipts []Receipt) int64 {
	var furthest int64
	for _, receipt := range receipts {
		if receipt.Position > furthest {
			furthest = receipt.Position
		}
	}
	return furthest
}
