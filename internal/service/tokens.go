package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Tokens interface {
	Mint(ctx context.Context, scope entity.TenantScope, userID string, ttl time.Duration) (string, entity.AccessToken, error)
	Resolve(ctx context.Context, token string) (entity.AccessToken, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	RevokeForUser(ctx context.Context, scope entity.TenantScope, userID string) (int64, error)
}
