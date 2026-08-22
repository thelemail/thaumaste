package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Receipts interface {
	Send(ctx context.Context, scope entity.TenantScope, caller, roomID, receiptType, eventID, threadID string) error
	Mark(ctx context.Context, scope entity.TenantScope, caller, roomID string, in entity.ReadMarker) error
	ForRoom(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]entity.Receipt, error)
	ReadUpTo(ctx context.Context, scope entity.TenantScope, caller, roomID, threadID string) (int64, error)
	Unread(ctx context.Context, scope entity.TenantScope, caller, roomID, threadID string) (int, error)
}
