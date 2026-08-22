package repository

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type DeviceList interface {
	Record(ctx context.Context, in entity.NewDeviceListChange, stream int64) error
	ChangedSince(ctx context.Context, scope entity.TenantScope, after, upTo int64) ([]string, error)
}
