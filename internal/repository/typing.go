package repository

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Typing interface {
	Set(ctx context.Context, in entity.NewTyping, now time.Time) error
	ForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64, now time.Time) ([]string, error)
	ForRooms(ctx context.Context, scope entity.TenantScope, roomNIDs []int64, now time.Time) (map[int64][]string, error)
	Version(ctx context.Context, scope entity.TenantScope) (int64, error)
}
