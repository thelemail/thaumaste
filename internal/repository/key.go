package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrKeyNotFound = errors.New("repository: key not found")

type Key interface {
	Lock(ctx context.Context, scope entity.TenantScope, userID, deviceID string) error
	UpsertDevice(ctx context.Context, in entity.NewDeviceKey) error
	ListDevices(ctx context.Context, scope entity.TenantScope, userIDs []string) ([]entity.DeviceKey, error)

	AddOneTime(ctx context.Context, keys []entity.NewOneTimeKey) error
	ExistingOneTime(ctx context.Context, scope entity.TenantScope, userID, deviceID string,
		ids []entity.KeyIdentifier) (map[entity.KeyIdentifier][]byte, error)
	CountOneTime(ctx context.Context, scope entity.TenantScope, userID, deviceID string) (map[string]int, error)
	ClaimOneTime(ctx context.Context, scope entity.TenantScope, userID, deviceID, algorithm string) (entity.ClaimedKey, error)

	SetFallback(ctx context.Context, in entity.NewFallbackKey) error
	ClaimFallback(ctx context.Context, scope entity.TenantScope, userID, deviceID, algorithm string) (entity.ClaimedKey, error)
	UnusedFallbackAlgorithms(ctx context.Context, scope entity.TenantScope, userID, deviceID string) ([]string, error)
}
