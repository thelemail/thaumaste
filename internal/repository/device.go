package repository

import (
	"context"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrDeviceNotFound = errors.New("repository: device not found")

type Device interface {
	Upsert(ctx context.Context, in entity.NewDevice) (entity.Device, error)
	Get(ctx context.Context, scope entity.TenantScope, userID, deviceID string) (entity.Device, error)
	ListForUser(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.Device, error)
	Rename(ctx context.Context, scope entity.TenantScope, userID, deviceID, displayName string) error
	Delete(ctx context.Context, scope entity.TenantScope, userID, deviceID string) error
	DeleteAllForUser(ctx context.Context, scope entity.TenantScope, userID string) (int64, error)
	Touch(ctx context.Context, scope entity.TenantScope, userID, deviceID, ip string, at time.Time) error
}
