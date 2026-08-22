package repository

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Receipt interface {
	Set(ctx context.Context, in entity.NewReceipt, stream int64) error
	ForUser(ctx context.Context, scope entity.TenantScope, roomNID int64, userID string) ([]entity.Receipt, error)
	ForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64, caller string) ([]entity.Receipt, error)
	Since(ctx context.Context, scope entity.TenantScope, roomNIDs []int64, caller string, after int64) ([]entity.Receipt, error)
	UnreadSince(ctx context.Context, roomNID, position int64, exclude string) (int, error)
}
