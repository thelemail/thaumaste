//go:build wireinject

package internal

import (
	"context"

	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
)

func InitializeServe(ctx context.Context, cfg config.Config) (*ServeRuntime, func(), error) {
	wire.Build(ConfigSet, PostgresSet, ServeSet)
	return nil, nil, nil
}
