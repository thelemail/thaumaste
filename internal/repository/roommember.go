package repository

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type RoomMember interface {
	Upsert(ctx context.Context, in entity.NewRoomMembership) error
	ListForRoom(ctx context.Context, roomNID int64, membership string) ([]entity.RoomMembership, error)
	ListForUser(ctx context.Context, scope entity.TenantScope, userID, membership string) ([]entity.RoomMembership, error)
	CountForRoom(ctx context.Context, roomNID int64, membership string) (int64, error)
}
