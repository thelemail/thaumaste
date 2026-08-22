package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Filters interface {
	Store(ctx context.Context, scope entity.TenantScope, caller, target string, document []byte) (string, error)
	Get(ctx context.Context, scope entity.TenantScope, caller, target, filterID string) (entity.Filter, error)
}
