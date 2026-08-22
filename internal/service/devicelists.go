package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type DeviceLists interface {
	Record(ctx context.Context, scope entity.TenantScope, userID string) error
	ChangedSince(ctx context.Context, scope entity.TenantScope, caller string,
		from, to entity.SyncCursors) (entity.DeviceLists, error)
}
