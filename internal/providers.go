//go:generate go tool wire

package internal

import (
	"context"

	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
)

func provideServerConfig(c config.Config) config.Server     { return c.Server }
func providePostgresConfig(c config.Config) config.Postgres { return c.Postgres }

var ConfigSet = wire.NewSet(provideServerConfig, providePostgresConfig)

func providePostgres(ctx context.Context, cfg config.Postgres) (*postgres.Client, func(), error) {
	pg, err := postgres.New(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return pg, func() { _ = pg.Close() }, nil
}

var PostgresSet = wire.NewSet(providePostgres)
