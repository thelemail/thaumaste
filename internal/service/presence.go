package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Presence interface {
	Set(ctx context.Context, tenant entity.Tenant, caller, target, state, statusMsg string) error
	Get(ctx context.Context, tenant entity.Tenant, caller, target string) (entity.Presence, error)
}
