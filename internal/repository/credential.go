package repository

import (
	"context"
	"errors"

	"github.com/thelemail/thaumaste/internal/entity"
)

var ErrCredentialNotFound = errors.New("repository: credential not found")

type Credential interface {
	Upsert(ctx context.Context, scope entity.TenantScope, in entity.Credential) error
	Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.Credential, error)
	Delete(ctx context.Context, scope entity.TenantScope, userID string) error
}
