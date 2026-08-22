package entity

import (
	"errors"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

const (
	PresenceOnline      = "online"
	PresenceUnavailable = "unavailable"
	PresenceOffline     = "offline"

	MaxStatusMessageBytes = 2048
)

var (
	ErrPresenceUnknown  = errors.New("entity: unknown presence state")
	ErrPresenceForeign  = errors.New("entity: presence belongs to another user")
	ErrPresenceDisabled = errors.New("entity: presence is not enabled for this domain")
)

func PresenceState(state string) bool {
	return state == PresenceOnline || state == PresenceUnavailable || state == PresenceOffline
}

type Presence struct {
	UserID       string
	State        string
	StatusMsg    string
	LastActiveAt time.Time
}

func (Presence) Validate() error { return nil }

func (p Presence) Currently() string {
	if p.State == "" {
		return PresenceOffline
	}
	return p.State
}

type NewPresence struct {
	TenantID  uuid.UUID
	UserID    string
	State     string
	StatusMsg string
}

func (n NewPresence) Validate() error {
	if !PresenceState(n.State) {
		return ErrPresenceUnknown
	}
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.StatusMsg, validation.Length(0, MaxStatusMessageBytes)),
	)
}
