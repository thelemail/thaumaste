package entity

import (
	"errors"
	"strconv"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

const (
	MaxTypingTimeout     = 2 * time.Minute
	DefaultTypingTimeout = 30 * time.Second

	typingKeyPrefix = "typing"
)

var ErrTypingTimeout = errors.New("entity: typing timeout is out of range")

type NewTyping struct {
	TenantID uuid.UUID
	RoomNID  int64
	UserID   string
	Typing   bool
	Timeout  time.Duration
}

func (n NewTyping) Validate() error {
	if n.Typing && (n.Timeout <= 0 || n.Timeout > MaxTypingTimeout) {
		return ErrTypingTimeout
	}
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.RoomNID, validation.Required),
	)
}

func TypingVersionKey(tenantID uuid.UUID) string {
	return typingKeyPrefix + wakeKeySeparator + "version" + wakeKeySeparator + tenantID.String()
}

func TypingKey(tenantID uuid.UUID, roomNID int64) string {
	return typingKeyPrefix + wakeKeySeparator + tenantID.String() +
		wakeKeySeparator + strconv.FormatInt(roomNID, 10)
}
