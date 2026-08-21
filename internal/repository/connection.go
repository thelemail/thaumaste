package repository

import (
	"context"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrConnectionNotFound = errors.New("repository: sync connection not found")

type Connection interface {
	Open(ctx context.Context, in entity.NewConnection) (entity.Connection, error)
	Get(ctx context.Context, connectionNID int64) (entity.Connection, error)
	Rooms(ctx context.Context, connectionNID int64, pending bool) ([]entity.RoomStatus, error)
	Acknowledge(ctx context.Context, connectionNID, generation, stream int64) error
	Discard(ctx context.Context, connectionNID int64) error
	Reset(ctx context.Context, connectionNID int64) error
	Stage(ctx context.Context, connectionNID, generation, stream int64, rooms []entity.NewRoomStatus) error
	Touch(ctx context.Context, connectionNID int64, at time.Time) error
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
