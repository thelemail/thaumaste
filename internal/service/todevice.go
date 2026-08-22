package service

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

type ToDevice interface {
	Send(ctx context.Context, scope entity.TenantScope, in entity.ToDeviceSend) error
	Sweep(ctx context.Context, cutoff time.Time) (int64, error)
}
