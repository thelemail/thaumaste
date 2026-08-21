package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
)

type Tenants interface {
	Create(ctx context.Context, in entity.NewTenant) (entity.Tenant, error)
	List(ctx context.Context) ([]entity.Tenant, error)
	ByHost(ctx context.Context, host string) (entity.Tenant, error)
	ByServerName(ctx context.Context, serverName string) (entity.Tenant, error)
	Hosts(ctx context.Context, scope entity.TenantScope) ([]string, error)
	AddHost(ctx context.Context, scope entity.TenantScope, host string) error
	Suspend(ctx context.Context, id uuid.UUID) (entity.Tenant, error)
	Resume(ctx context.Context, id uuid.UUID) (entity.Tenant, error)
	Delete(ctx context.Context, id uuid.UUID) error
	SetRegistration(ctx context.Context, id uuid.UUID, mode entity.RegistrationMode) (entity.Tenant, error)
	Keys(ctx context.Context, scope entity.TenantScope) ([]entity.SigningKey, error)
	RotateKey(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, error)
	SignAs(ctx context.Context, scope entity.TenantScope, document []byte) ([]byte, error)
	ResealKeys(ctx context.Context, next keyseal.Sealer) (int, error)
}
