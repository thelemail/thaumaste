package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrPresenceNotFound = errors.New("repository: presence not found")

type Presence interface {
	Set(ctx context.Context, in entity.NewPresence) error
	Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.Presence, error)
}
