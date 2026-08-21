package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var (
	ErrAliasNotFound = errors.New("repository: alias not found")
	ErrAliasInUse    = errors.New("repository: alias already exists")
)

type Alias interface {
	Create(ctx context.Context, in entity.NewRoomAlias) (entity.RoomAlias, error)
	Get(ctx context.Context, scope entity.TenantScope, alias string) (entity.RoomAlias, error)
	Delete(ctx context.Context, scope entity.TenantScope, alias string) error
	ListForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64) ([]entity.RoomAlias, error)
}
