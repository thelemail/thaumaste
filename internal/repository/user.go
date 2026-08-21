package repository

import (
	"context"
	"errors"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

var (
	ErrUserNotFound = errors.New("repository: user not found")
	ErrUserInUse    = errors.New("repository: user id is already taken")
)

type User interface {
	Create(ctx context.Context, in entity.NewUser) (entity.User, error)
	Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.User, error)
	Exists(ctx context.Context, scope entity.TenantScope, localpart string) (bool, error)
	UpdateProfile(ctx context.Context, scope entity.TenantScope, userID string, in entity.UpdateProfile) (entity.User, error)
	Deactivate(ctx context.Context, scope entity.TenantScope, userID string, at time.Time) error
}
