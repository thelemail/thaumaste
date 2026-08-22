package repository

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Typing interface {
	Set(ctx context.Context, in entity.NewTyping, now time.Time) error
	ForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64, now time.Time) ([]string, error)
}
