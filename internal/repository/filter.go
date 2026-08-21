package repository

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Filter interface {
	Store(ctx context.Context, in entity.NewFilter) (string, error)
	Get(ctx context.Context, scope entity.TenantScope, userID, filterID string) (entity.Filter, error)
}
