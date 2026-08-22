package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type LegacySync interface {
	Sync(ctx context.Context, scope entity.TenantScope, caller, deviceID string,
		in entity.LegacySyncRequest) (entity.LegacySyncResult, error)
}
