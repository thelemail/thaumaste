package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

var (
	ErrTenantNotFound      = errors.New("repository: tenant not found")
	ErrTenantAlreadyExists = errors.New("repository: tenant already exists")
	ErrHostAlreadyClaimed  = errors.New("repository: host already claimed")
)

type Tenant interface {
	Create(ctx context.Context, in entity.NewTenant) (entity.Tenant, error)
	GetByID(ctx context.Context, id uuid.UUID) (entity.Tenant, error)
	GetByServerName(ctx context.Context, serverName string) (entity.Tenant, error)
	GetByHost(ctx context.Context, host string) (entity.Tenant, error)
	List(ctx context.Context) ([]entity.Tenant, error)
	SetState(ctx context.Context, id uuid.UUID, state entity.TenantState) (entity.Tenant, error)
	Delete(ctx context.Context, id uuid.UUID) error
	AddHost(ctx context.Context, scope entity.TenantScope, host string) error
	ListHosts(ctx context.Context, scope entity.TenantScope) ([]string, error)
}
