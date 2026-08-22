package typing

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/repository"
)

const reclaimAfter = 5 * time.Minute

type repo struct {
	live *valkey.Client
}

func New(live *valkey.Client) repository.Typing {
	return &repo{live: live}
}

func (r *repo) Set(ctx context.Context, in entity.NewTyping, now time.Time) error {
	key := entity.TypingKey(in.TenantID, in.RoomNID)
	if !in.Typing {
		return r.live.SortedRemove(ctx, key, in.UserID)
	}
	expires := now.Add(in.Timeout)
	if err := r.live.SortedAdd(ctx, key, expires.UnixMilli(), in.UserID, in.Timeout+reclaimAfter); err != nil {
		return err
	}
	return r.live.SortedTrim(ctx, key, now.UnixMilli())
}

func (r *repo) ForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64,
	now time.Time,
) ([]string, error) {
	return r.live.SortedRange(ctx, entity.TypingKey(scope.ID(), roomNID), now.UnixMilli())
}
