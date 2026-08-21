package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type AccountData interface {
	Set(ctx context.Context, scope entity.TenantScope, caller, target, roomID, dataType string, content []byte) error
	Get(ctx context.Context, scope entity.TenantScope, caller, target, roomID, dataType string) (entity.AccountData, error)
	Tags(ctx context.Context, scope entity.TenantScope, caller, target, roomID string) (entity.RoomTags, error)
	SetTag(ctx context.Context, scope entity.TenantScope, caller, target, roomID, tag string, order []byte) error
	DeleteTag(ctx context.Context, scope entity.TenantScope, caller, target, roomID, tag string) error
}
