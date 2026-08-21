package service

import (
	"context"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Events interface {
	CreateRoom(ctx context.Context, scope entity.TenantScope, in entity.NewRoomRequest) (entity.Room, []entity.StoredEvent, error)
	Send(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, error)
	Room(ctx context.Context, roomID string) (entity.Room, error)
	Timeline(ctx context.Context, roomID string) ([]entity.StoredEvent, error)
	CurrentState(ctx context.Context, roomID string) (entity.StateMap, error)
	StateBefore(ctx context.Context, eventID string) (entity.StateMap, error)
}
