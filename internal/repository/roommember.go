package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrMembershipNotFound = errors.New("repository: membership not found")

type RoomMember interface {
	Upsert(ctx context.Context, in entity.NewRoomMembership) error
	Get(ctx context.Context, roomNID int64, userID string) (entity.RoomMembership, error)
	SetForgotten(ctx context.Context, roomNID int64, userID string, forgotten bool) error
	ListForRoom(ctx context.Context, roomNID int64, membership string) ([]entity.RoomMembership, error)
	ListForUser(ctx context.Context, scope entity.TenantScope, userID, membership string) ([]entity.RoomMembership, error)
	CountForRoom(ctx context.Context, roomNID int64, membership string) (int64, error)
	CountForRooms(ctx context.Context, roomNIDs []int64) (map[int64]entity.MemberCounts, error)
	ListForSync(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.SyncRoom, error)
	ListForRooms(ctx context.Context, roomNIDs []int64, userIDs []string) ([]entity.RoomMembership, error)
	Heroes(ctx context.Context, roomNIDs []int64, exclude string, limit int) (map[int64][]entity.RoomMembership, error)
	SharedWith(ctx context.Context, scope entity.TenantScope, caller string, userIDs []string) ([]string, error)
}
