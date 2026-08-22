package repository

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

type ToDevice interface {
	Add(ctx context.Context, messages []entity.NewToDeviceMessage, positions []int64) error
	Since(ctx context.Context, scope entity.TenantScope, userID, deviceID string,
		after int64, limit int) ([]entity.ToDeviceMessage, error)
	DeleteUpTo(ctx context.Context, scope entity.TenantScope, userID, deviceID string, upTo int64) error
	Recorded(ctx context.Context, scope entity.TenantScope, userID, deviceID, txnID string) (bool, error)
	Record(ctx context.Context, scope entity.TenantScope, userID, deviceID, txnID string) error
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
