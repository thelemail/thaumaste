package entity

import (
	"encoding/json"
	"errors"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

const (
	AllDevices = "*"

	DefaultToDeviceLimit = 100
	MaxToDeviceLimit     = 1000
	MaxToDeviceBytes     = 65536

	deviceKeyPrefix = "device"
)

var (
	ErrToDeviceEmpty   = errors.New("entity: a to-device request carries no messages")
	ErrToDeviceTooBig  = errors.New("entity: to-device content is too large")
	ErrToDeviceUnknown = errors.New("entity: unknown to-device batch token")
)

type ToDeviceMessage struct {
	Sender   string
	Type     string
	Content  json.RawMessage
	StreamID int64
}

func (ToDeviceMessage) Validate() error { return nil }

type NewToDeviceMessage struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	Sender   string
	Type     string
	Content  []byte
}

func (n NewToDeviceMessage) Validate() error {
	if len(n.Content) > MaxToDeviceBytes {
		return ErrToDeviceTooBig
	}
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.Sender, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.Type, validation.Required, validation.Length(1, MaxEventTypeSize)),
		validation.Field(&n.Content, validation.Required),
	)
}

type ToDeviceSend struct {
	TenantID uuid.UUID
	Sender   string
	DeviceID string
	Type     string
	TxnID    string
	Messages map[string]map[string]json.RawMessage
}

func (s ToDeviceSend) Validate() error {
	if len(s.Messages) == 0 {
		return ErrToDeviceEmpty
	}
	for _, devices := range s.Messages {
		for _, content := range devices {
			if len(content) > MaxToDeviceBytes {
				return ErrToDeviceTooBig
			}
		}
	}
	return validation.ValidateStruct(&s,
		validation.Field(&s.TenantID, validation.By(notNilUUID)),
		validation.Field(&s.Sender, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&s.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&s.Type, validation.Required, validation.Length(1, MaxEventTypeSize)),
		validation.Field(&s.TxnID, validation.Required, validation.Length(1, MaxTransactionIDBytes)),
	)
}

func DeviceWakeKey(userID, deviceID string) string {
	return deviceKeyPrefix + wakeKeySeparator + userID + wakeKeySeparator + deviceID
}
