package service

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Sync interface {
	Sync(ctx context.Context, scope entity.TenantScope, caller, deviceID string, in entity.SyncRequest) (entity.SyncResult, error)
	SweepConnections(ctx context.Context, cutoff time.Time) (int64, error)
}
