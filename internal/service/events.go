package service

import (
	"context"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Events interface {
	CreateRoom(ctx context.Context, scope entity.TenantScope, in entity.NewRoomRequest) (entity.Room, []entity.StoredEvent, error)
	Send(ctx context.Context, scope entity.TenantScope, in entity.NewEvent) (entity.StoredEvent, error)
	Redact(ctx context.Context, scope entity.TenantScope, in entity.NewRedaction) (entity.StoredEvent, error)
	Room(ctx context.Context, roomID string) (entity.Room, error)
	CurrentState(ctx context.Context, roomID string) (entity.StateMap, error)
	StateBefore(ctx context.Context, eventID string) (entity.StateMap, error)
	StateAfter(ctx context.Context, eventNID int64) (entity.StateMap, error)
	Event(ctx context.Context, eventID string) (entity.StoredEvent, error)
	Many(ctx context.Context, eventNIDs []int64) ([]entity.StoredEvent, error)
	ManyByID(ctx context.Context, eventIDs []string) ([]entity.StoredEvent, error)
	Page(ctx context.Context, roomID string, in entity.PageRequest) ([]entity.StoredEvent, error)
	Relations(ctx context.Context, roomID string, q entity.RelationQuery) ([]entity.RelationRef, error)
	VisibilityFor(ctx context.Context, roomID, caller string) (entity.HistoryFilter, error)
	PositionAtStream(ctx context.Context, roomID string, stream int64) (entity.Position, error)
	TransactionsFor(ctx context.Context, sender entity.TransactionSender, eventIDs []string) (map[string]string, error)
	SweepTransactions(ctx context.Context, cutoff time.Time) (int64, error)
}
