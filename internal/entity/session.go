package entity

import (
	"errors"
	"slices"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrRefreshTokenNotFound = errors.New("entity: refresh token not found")
	ErrRefreshTokenUsed     = errors.New("entity: refresh token has already been used")
	ErrUIASessionNotFound   = errors.New("entity: authentication session not found")
	ErrUIAStageUnknown      = errors.New("entity: unknown authentication stage")
	ErrUIAIncomplete        = errors.New("entity: authentication is not complete")
)

const (
	LoginTypePassword = "m.login.password"
	LoginTypeToken    = "m.login.token"
	LoginTypeDummy    = "m.login.dummy"

	IdentifierTypeUser = "m.id.user"
)

// UIAKind names what a session is authorising. A session opened to register an account must not be
// replayed against a password change, so the kind is part of the lookup rather than advisory.
type UIAKind string

const (
	UIAKindRegister     UIAKind = "register"
	UIAKindPassword     UIAKind = "password"
	UIAKindDeactivate   UIAKind = "deactivate"
	UIAKindDeleteDevice UIAKind = "delete_device"
)

func (k UIAKind) Valid() bool {
	switch k {
	case UIAKindRegister, UIAKindPassword, UIAKindDeactivate, UIAKindDeleteDevice:
		return true
	default:
		return false
	}
}

// Stages returns the flow this server offers for a kind. Registration offers only the dummy stage,
// which the spec names as the way to have effectively open registration while still issuing the
// challenge every client expects.
func (k UIAKind) Stages() []string {
	if k == UIAKindRegister {
		return []string{LoginTypeDummy}
	}
	return []string{LoginTypePassword}
}

type UIASession struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Kind      UIAKind
	UserID    string
	Completed []string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (UIASession) Validate() error { return nil }

func (s UIASession) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }

func (s UIASession) Done() bool {
	for _, stage := range s.Kind.Stages() {
		if !slices.Contains(s.Completed, stage) {
			return false
		}
	}
	return true
}

// Next names the stage the caller still owes. A stage already in Completed is never asked for
// again, because the spec forbids retrying a completed stage.
func (s UIASession) Next() (string, bool) {
	for _, stage := range s.Kind.Stages() {
		if !slices.Contains(s.Completed, stage) {
			return stage, true
		}
	}
	return "", false
}

type NewUIASession struct {
	TenantID uuid.UUID
	Kind     UIAKind
	UserID   string
	TTL      time.Duration
}

func (n NewUIASession) Validate() error {
	if err := validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
	); err != nil {
		return err
	}
	if !n.Kind.Valid() {
		return ErrUIAStageUnknown
	}
	return nil
}

type RefreshToken struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    string
	DeviceID  string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
	UsedAt    *time.Time
}

func (RefreshToken) Validate() error { return nil }

// Usable follows the spec's rotation rule: a refresh token stays valid until the tokens it produced
// are themselves used, so a client that loses the response can safely present it again.
func (t RefreshToken) Usable(now time.Time) error {
	if t.RevokedAt != nil {
		return ErrRefreshTokenNotFound
	}
	if t.UsedAt != nil {
		return ErrRefreshTokenUsed
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return ErrRefreshTokenNotFound
	}
	return nil
}

type NewRefreshToken struct {
	TenantID  uuid.UUID
	UserID    string
	DeviceID  string
	TokenHash []byte
	ExpiresAt *time.Time
}

func (n NewRefreshToken) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.TokenHash, validation.Length(AccessTokenHashSize, AccessTokenHashSize)),
	)
}
