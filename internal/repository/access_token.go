package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrAccessTokenNotFound = errors.New("repository: access token not found")

type AccessToken interface {
	Insert(ctx context.Context, in entity.NewAccessToken) (entity.AccessToken, error)
	GetByHash(ctx context.Context, tokenHash []byte) (entity.AccessToken, error)
	ListForUser(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.AccessToken, error)
	Revoke(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeForUser(ctx context.Context, scope entity.TenantScope, userID string, at time.Time) (int64, error)
}
