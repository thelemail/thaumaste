package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var (
	ErrRoomNotFound      = errors.New("repository: room not found")
	ErrRoomAlreadyExists = errors.New("repository: room already exists")
)

type Room interface {
	Create(ctx context.Context, in entity.NewRoom) (entity.Room, error)
	GetByRoomID(ctx context.Context, roomID string) (entity.Room, error)
	SetCreateEvent(ctx context.Context, roomNID, eventNID int64) error
	ListForTenant(ctx context.Context, scope entity.TenantScope) ([]entity.Room, error)
	Extremities(ctx context.Context, roomNID int64) ([]entity.StoredEvent, error)
	ReplaceExtremities(ctx context.Context, roomNID int64, eventNIDs []int64) error
}
