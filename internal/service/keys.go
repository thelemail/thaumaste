package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Keys interface {
	Upload(ctx context.Context, scope entity.TenantScope, in entity.KeyUpload) (map[string]int, error)
	Query(ctx context.Context, scope entity.TenantScope, caller string, in entity.KeyQuery) (map[string]map[string]entity.DeviceKey, error)
	Claim(ctx context.Context, scope entity.TenantScope, in entity.KeyClaim) ([]entity.ClaimedKey, error)
	FallbackAlgorithms(ctx context.Context, scope entity.TenantScope, userID, deviceID string) ([]string, error)
}
