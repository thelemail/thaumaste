package typing

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/repository"
)

const (
	reclaimAfter     = 5 * time.Minute
	versionRetention = 24 * time.Hour
)

type repo struct {
	live *valkey.Client
}

func New(live *valkey.Client) repository.Typing {
	return &repo{live: live}
}

func (r *repo) Set(ctx context.Context, in entity.NewTyping, now time.Time) error {
	key := entity.TypingKey(in.TenantID, in.RoomNID)
	if !in.Typing {
		if err := r.live.SortedRemove(ctx, key, in.UserID); err != nil {
			return err
		}
		return r.bump(ctx, entity.NewTenantScope(in.TenantID, ""))
	}
	expires := now.Add(in.Timeout)
	if err := r.live.SortedAdd(ctx, key, expires.UnixMilli(), in.UserID, in.Timeout+reclaimAfter); err != nil {
		return err
	}
	if err := r.live.SortedTrim(ctx, key, now.UnixMilli()); err != nil {
		return err
	}
	return r.bump(ctx, entity.NewTenantScope(in.TenantID, ""))
}

func (r *repo) ForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64,
	now time.Time,
) ([]string, error) {
	return r.live.SortedRange(ctx, entity.TypingKey(scope.ID(), roomNID), now.UnixMilli())
}

func (r *repo) ForRooms(ctx context.Context, scope entity.TenantScope, roomNIDs []int64,
	now time.Time,
) (map[int64][]string, error) {
	out := make(map[int64][]string, len(roomNIDs))
	for _, roomNID := range roomNIDs {
		members, err := r.ForRoom(ctx, scope, roomNID, now)
		if err != nil {
			return nil, err
		}
		if len(members) > 0 {
			out[roomNID] = members
		}
	}
	return out, nil
}

func (r *repo) Version(ctx context.Context, scope entity.TenantScope) (int64, error) {
	return r.live.Counter(ctx, entity.TypingVersionKey(scope.ID()))
}

func (r *repo) bump(ctx context.Context, scope entity.TenantScope) error {
	_, err := r.live.Increment(ctx, entity.TypingVersionKey(scope.ID()), versionRetention)
	return err
}
