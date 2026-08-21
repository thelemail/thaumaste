package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrStateSnapshotNotFound = errors.New("repository: state snapshot not found")

type State interface {
	Save(ctx context.Context, roomNID int64, state entity.StateMap) (int64, error)
	Load(ctx context.Context, snapshotNID int64) (entity.StateMap, error)
}
