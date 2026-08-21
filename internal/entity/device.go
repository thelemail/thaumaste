package entity

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrDeviceNotFound = errors.New("entity: device not found")
	ErrInvalidDevice  = errors.New("entity: invalid device id")
)

const (
	MaxDeviceIDBytes         = 255
	MaxDeviceDisplayNameSize = 256

	generatedDeviceIDLength = 10
)

var deviceIDAlphabet = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ")

type Device struct {
	TenantID    uuid.UUID
	UserID      string
	DeviceID    string
	DisplayName string
	LastSeenIP  string
	LastSeenTS  *time.Time
	CreatedAt   time.Time
}

func (Device) Validate() error { return nil }

type NewDevice struct {
	TenantID    uuid.UUID
	UserID      string
	DeviceID    string
	DisplayName string
}

func (n NewDevice) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.DisplayName, validation.Length(0, MaxDeviceDisplayNameSize)),
	); err != nil {
		return err
	}
	return nil
}

// GenerateDeviceID produces the identifier a client gets when it does not choose one. The alphabet
// matches what other servers emit, which matters only because device ids are shown to users.
func GenerateDeviceID(rnd io.Reader) (string, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	raw := make([]byte, generatedDeviceIDLength)
	if _, err := io.ReadFull(rnd, raw); err != nil {
		return "", fmt.Errorf("entity: device id: %w", err)
	}
	for i, b := range raw {
		raw[i] = deviceIDAlphabet[int(b)%len(deviceIDAlphabet)]
	}
	return string(raw), nil
}

func ValidateDeviceID(deviceID string) error {
	if deviceID == "" || len(deviceID) > MaxDeviceIDBytes {
		return ErrInvalidDevice
	}
	return nil
}
