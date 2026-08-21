package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrAccountDataNotFound = errors.New("repository: account data not found")

type AccountData interface {
	Set(ctx context.Context, in entity.NewAccountData, stream int64) error
	Get(ctx context.Context, scope entity.TenantScope, userID string, roomNID int64, dataType string) (entity.AccountData, error)
	Lock(ctx context.Context, scope entity.TenantScope, userID string) error
}
