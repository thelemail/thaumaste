package entity

import (
	"errors"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrTokenNotFound = errors.New("entity: access token not found")
	ErrTokenExpired  = errors.New("entity: access token has expired")
	ErrTokenRevoked  = errors.New("entity: access token has been revoked")
	ErrTokenForeign  = errors.New("entity: access token belongs to another tenant")
)

const AccessTokenHashSize = 32

type AccessToken struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    string
	DeviceID  string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

func (AccessToken) Validate() error { return nil }

func (t AccessToken) Usable(now time.Time) error {
	if t.RevokedAt != nil {
		return ErrTokenRevoked
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return ErrTokenExpired
	}
	return nil
}

type NewAccessToken struct {
	TenantID       uuid.UUID
	UserID         string
	DeviceID       string
	TokenHash      []byte
	ExpiresAt      *time.Time
	RefreshTokenID *uuid.UUID
}

func (n NewAccessToken) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, 255)),
		validation.Field(&n.TokenHash, validation.Length(AccessTokenHashSize, AccessTokenHashSize)),
	)
}
