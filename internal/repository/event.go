package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var (
	ErrEventNotFound = errors.New("repository: event not found")
	ErrEventExists   = errors.New("repository: event already exists")
)

type Event interface {
	Insert(ctx context.Context, in entity.NewStoredEvent) (entity.StoredEvent, error)
	GetByEventID(ctx context.Context, eventID string) (entity.StoredEvent, error)
	GetByNID(ctx context.Context, eventNID int64) (entity.StoredEvent, error)
	GetManyByEventID(ctx context.Context, eventIDs []string) ([]entity.StoredEvent, error)
	ListForRoom(ctx context.Context, roomNID int64) ([]entity.StoredEvent, error)
	Page(ctx context.Context, roomNID int64, in entity.PageRequest) ([]entity.StoredEvent, error)
	ListStateOfType(ctx context.Context, roomNID int64, eventType, stateKey string) ([]entity.StoredEvent, error)
	AtStream(ctx context.Context, roomNID, stream int64) (entity.StoredEvent, error)
	SetDisposition(ctx context.Context, eventNID int64, disposition entity.Disposition) error
	SetStateSnapshot(ctx context.Context, eventNID, snapshotNID int64) error
	ParentsOf(ctx context.Context, eventNID int64) ([]string, error)
	AuthParentsOf(ctx context.Context, eventNID int64) ([]string, error)
}
