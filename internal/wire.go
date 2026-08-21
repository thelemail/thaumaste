//go:build wireinject

package internal

import (
	"context"

	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/service"
)

func InitializeServe(ctx context.Context, cfg config.Config) (*ServeRuntime, func(), error) {
	wire.Build(ConfigSet, PostgresSet, DomainSet, ServeSet)
	return nil, nil, nil
}

func InitializeTenants(ctx context.Context, cfg config.Config) (service.Tenants, func(), error) {
	wire.Build(ConfigSet, PostgresSet, DomainSet)
	return nil, nil, nil
}
