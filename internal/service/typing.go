package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Typing interface {
	Set(ctx context.Context, scope entity.TenantScope, caller, target, roomID string, typing bool, timeoutMS int) error
	ForRoom(ctx context.Context, scope entity.TenantScope, caller, roomID string) ([]string, error)
}
