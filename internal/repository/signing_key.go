package repository

import (
	"context"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrSigningKeyNotFound = errors.New("repository: signing key not found")

type SigningKey interface {
	Insert(ctx context.Context, in entity.NewSigningKey) (entity.SigningKey, error)
	Active(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, error)
	ActivePrivate(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, []byte, error)
	List(ctx context.Context, scope entity.TenantScope) ([]entity.SigningKey, error)
	Expire(ctx context.Context, scope entity.TenantScope, keyID string, at time.Time) error
	AllSealed(ctx context.Context) ([]entity.SealedSigningKey, error)
	Reseal(ctx context.Context, key entity.SealedSigningKey) error
}
