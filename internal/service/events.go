package service

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Events interface {
	CreateRoom(ctx context.Context, scope entity.TenantScope, in entity.NewRoomRequest) (entity.Room, []entity.StoredEvent, error)
	Send(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, error)
	Room(ctx context.Context, roomID string) (entity.Room, error)
	Timeline(ctx context.Context, roomID string) ([]entity.StoredEvent, error)
	CurrentState(ctx context.Context, roomID string) (entity.StateMap, error)
	StateBefore(ctx context.Context, eventID string) (entity.StateMap, error)
	StateAfter(ctx context.Context, eventNID int64) (entity.StateMap, error)
	Event(ctx context.Context, eventID string) (entity.StoredEvent, error)
	Page(ctx context.Context, roomID string, in entity.PageRequest) ([]entity.StoredEvent, error)
	VisibilityFor(ctx context.Context, roomID, caller string) (entity.HistoryFilter, error)
	PositionAtStream(ctx context.Context, roomID string, stream int64) (entity.Position, error)
	TransactionFor(ctx context.Context, sender entity.TransactionSender, eventID string) (string, error)
	SweepTransactions(ctx context.Context, cutoff time.Time) (int64, error)
}
