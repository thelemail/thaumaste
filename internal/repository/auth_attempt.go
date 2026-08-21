package repository

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type AuthAttempt interface {
	Get(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) (entity.AuthAttempt, error)
	Save(ctx context.Context, in entity.AuthAttempt) error
	Clear(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error
}
