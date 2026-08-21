//go:generate go tool wire

package internal

import (
	"context"
	"time"

	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/repository/accesstoken"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	"github.com/thelemail/thaumaste/internal/service"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/service/tokens"
)

func provideServerConfig(c config.Config) config.Server     { return c.Server }
func providePostgresConfig(c config.Config) config.Postgres { return c.Postgres }
func provideSigningConfig(c config.Config) config.Signing   { return c.Signing }

var ConfigSet = wire.NewSet(provideServerConfig, providePostgresConfig, provideSigningConfig)

func providePostgres(ctx context.Context, cfg config.Postgres) (*postgres.Client, func(), error) {
	pg, err := postgres.New(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return pg, func() { _ = pg.Close() }, nil
}

func provideTransactor(pg *postgres.Client) repository.Transactor { return pg }

var PostgresSet = wire.NewSet(providePostgres, provideTransactor)

func provideClock() func() time.Time { return time.Now }

func provideTenants(
	tenantRepo repository.Tenant,
	keyRepo repository.SigningKey,
	sealer keyseal.Sealer,
	tx repository.Transactor,
	clock func() time.Time,
) service.Tenants {
	return tenants.New(tenantRepo, keyRepo, sealer, tx, clock, nil)
}

func provideTokens(tokenRepo repository.AccessToken, clock func() time.Time) service.Tokens {
	return tokens.New(tokenRepo, clock, nil)
}

var DomainSet = wire.NewSet(
	provideClock,
	keyseal.New,
	tenant.New,
	signingkey.New,
	accesstoken.New,
	provideTenants,
	provideTokens,
)
