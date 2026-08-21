package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrRefreshTokenNotFound = errors.New("repository: refresh token not found")

type RefreshToken interface {
	Insert(ctx context.Context, in entity.NewRefreshToken) (entity.RefreshToken, error)
	GetByHash(ctx context.Context, tokenHash []byte) (entity.RefreshToken, error)
	MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeForDevice(ctx context.Context, scope entity.TenantScope, userID, deviceID string, at time.Time) (int64, error)
	RevokeForUser(ctx context.Context, scope entity.TenantScope, userID string, at time.Time) (int64, error)
}
