//go:generate go tool wire

package internal

import (
	"context"
	"time"

	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/repository/accesstoken"
	"github.com/thelemail/thaumaste/internal/repository/alias"
	"github.com/thelemail/thaumaste/internal/repository/authattempt"
	"github.com/thelemail/thaumaste/internal/repository/credential"
	"github.com/thelemail/thaumaste/internal/repository/device"
	"github.com/thelemail/thaumaste/internal/repository/event"
	"github.com/thelemail/thaumaste/internal/repository/refreshtoken"
	"github.com/thelemail/thaumaste/internal/repository/room"
	"github.com/thelemail/thaumaste/internal/repository/roommember"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/state"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	"github.com/thelemail/thaumaste/internal/repository/uiasession"
	"github.com/thelemail/thaumaste/internal/repository/user"
	"github.com/thelemail/thaumaste/internal/service"
	"github.com/thelemail/thaumaste/internal/service/events"
	"github.com/thelemail/thaumaste/internal/service/rooms"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/service/tokens"
	"github.com/thelemail/thaumaste/internal/service/users"
)

func provideServerConfig(c config.Config) config.Server     { return c.Server }
func providePostgresConfig(c config.Config) config.Postgres { return c.Postgres }
func provideSigningConfig(c config.Config) config.Signing   { return c.Signing }
func provideValkeyConfig(c config.Config) config.Valkey     { return c.Valkey }
func provideLimitsConfig(c config.Config) config.Limits     { return c.Limits }

var ConfigSet = wire.NewSet(provideServerConfig, providePostgresConfig, provideSigningConfig,
	provideValkeyConfig, provideLimitsConfig, provideAuthConfig)

func providePostgres(ctx context.Context, cfg config.Postgres) (*postgres.Client, func(), error) {
	pg, err := postgres.New(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return pg, func() { _ = pg.Close() }, nil
}

func provideTransactor(pg *postgres.Client) repository.Transactor { return pg }

var PostgresSet = wire.NewSet(providePostgres, provideTransactor)

func provideValkey(ctx context.Context, cfg config.Valkey, limits config.Limits) (*valkey.Client, func(), error) {
	client, err := valkey.New(ctx, cfg, limits)
	if err != nil {
		return nil, nil, err
	}
	return client, client.Close, nil
}

var ValkeySet = wire.NewSet(provideValkey)

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

func provideAuthConfig(c config.Config) config.Auth { return c.Auth }

func provideUsers(
	usersRepo repository.User,
	credentials repository.Credential,
	devices repository.Device,
	refresh repository.RefreshToken,
	sessions repository.UIASession,
	attempts repository.AuthAttempt,
	tokens service.Tokens,
	tenants service.Tenants,
	tx repository.Transactor,
	cfg config.Auth,
	clock func() time.Time,
) service.Users {
	return users.New(usersRepo, credentials, devices, refresh, sessions, attempts,
		tokens, tenants, tx, cfg, clock, nil)
}

func provideUIASessions(db *postgres.Client, clock func() time.Time) repository.UIASession {
	return uiasession.New(db, clock)
}

func provideEventStream(ctx context.Context, db *postgres.Client, cfg config.Server) (*postgres.Stream, error) {
	return postgres.NewStream(ctx, db, postgres.StreamConfig{
		Name:     "events",
		Instance: cfg.InstanceName,
		Sequence: "events_stream_seq",
	})
}

func provideSerialiser() *serialiser.Serialiser { return serialiser.New() }

func provideEvents(
	rooms repository.Room,
	eventRepo repository.Event,
	stateRepo repository.State,
	members repository.RoomMember,
	tenants service.Tenants,
	tx repository.Transactor,
	stream *postgres.Stream,
	locks *valkey.Client,
	gate *serialiser.Serialiser,
	cfg config.Server,
	clock func() time.Time,
) service.Events {
	return events.New(rooms, eventRepo, stateRepo, members, tenants, tx, stream, locks, gate,
		cfg.InstanceName, clock, nil)
}

var DomainSet = wire.NewSet(
	provideClock,
	keyseal.New,
	tenant.New,
	signingkey.New,
	accesstoken.New,
	provideTenants,
	provideTokens,
	provideEventStream,
	provideSerialiser,
	room.New,
	event.New,
	state.New,
	alias.New,
	roommember.New,
	provideEvents,
	rooms.New,
	user.New,
	credential.New,
	device.New,
	refreshtoken.New,
	provideUIASessions,
	authattempt.New,
	provideUsers,
)
