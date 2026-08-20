package entity

import (
	"crypto/ed25519"
	"errors"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

var (
	ErrSigningKeyNotFound = errors.New("entity: signing key not found")
	ErrNoActiveSigningKey = errors.New("entity: tenant has no active signing key")
)

type SigningKey struct {
	TenantID  uuid.UUID
	KeyID     string
	PublicKey ed25519.PublicKey
	CreatedAt time.Time
	ExpiredAt *time.Time
}

func (SigningKey) Validate() error { return nil }

func (k SigningKey) Active() bool { return k.ExpiredAt == nil }

type SealedSigningKey struct {
	TenantID   uuid.UUID
	KeyID      string
	PrivateKey []byte
}

func (SealedSigningKey) Validate() error { return nil }

type NewSigningKey struct {
	TenantID   uuid.UUID
	KeyID      string
	PublicKey  ed25519.PublicKey
	PrivateKey []byte
}

func (n NewSigningKey) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.KeyID, validation.Required),
		validation.Field(&n.PublicKey, validation.Length(ed25519.PublicKeySize, ed25519.PublicKeySize)),
		validation.Field(&n.PrivateKey, validation.Required),
	)
}
