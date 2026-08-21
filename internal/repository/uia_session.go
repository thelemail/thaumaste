package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrUIASessionNotFound = errors.New("repository: authentication session not found")

type UIASession interface {
	Create(ctx context.Context, in entity.NewUIASession) (entity.UIASession, error)
	Get(ctx context.Context, scope entity.TenantScope, id uuid.UUID) (entity.UIASession, error)
	Complete(ctx context.Context, scope entity.TenantScope, id uuid.UUID, stage string) (entity.UIASession, error)
	Delete(ctx context.Context, scope entity.TenantScope, id uuid.UUID) error
}
