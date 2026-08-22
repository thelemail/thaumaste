//go:generate go tool wire

package internal

import (
	"context"
	"time"

	"github.com/google/wire"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/repository/accesstoken"
	accountdatarepo "github.com/thelemail/thaumaste/internal/repository/accountdata"
	"github.com/thelemail/thaumaste/internal/repository/alias"
	"github.com/thelemail/thaumaste/internal/repository/authattempt"
	"github.com/thelemail/thaumaste/internal/repository/connection"
	"github.com/thelemail/thaumaste/internal/repository/credential"
	"github.com/thelemail/thaumaste/internal/repository/device"
	devicelistrepo "github.com/thelemail/thaumaste/internal/repository/devicelist"
	"github.com/thelemail/thaumaste/internal/repository/event"
	filterrepo "github.com/thelemail/thaumaste/internal/repository/filter"
	"github.com/thelemail/thaumaste/internal/repository/key"
	presencerepo "github.com/thelemail/thaumaste/internal/repository/presence"
	receiptrepo "github.com/thelemail/thaumaste/internal/repository/receipt"
	"github.com/thelemail/thaumaste/internal/repository/refreshtoken"
	"github.com/thelemail/thaumaste/internal/repository/relation"
	"github.com/thelemail/thaumaste/internal/repository/room"
	"github.com/thelemail/thaumaste/internal/repository/roommember"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/state"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	todevicerepo "github.com/thelemail/thaumaste/internal/repository/todevice"
	"github.com/thelemail/thaumaste/internal/repository/transaction"
	typingrepo "github.com/thelemail/thaumaste/internal/repository/typing"
	"github.com/thelemail/thaumaste/internal/repository/uiasession"
	"github.com/thelemail/thaumaste/internal/repository/user"
	"github.com/thelemail/thaumaste/internal/service"
	"github.com/thelemail/thaumaste/internal/service/accountdata"
	"github.com/thelemail/thaumaste/internal/service/devicelists"
	"github.com/thelemail/thaumaste/internal/service/directory"
	"github.com/thelemail/thaumaste/internal/service/events"
	"github.com/thelemail/thaumaste/internal/service/filters"
	"github.com/thelemail/thaumaste/internal/service/keys"
	"github.com/thelemail/thaumaste/internal/service/legacysync"
	"github.com/thelemail/thaumaste/internal/service/presence"
	"github.com/thelemail/thaumaste/internal/service/receipts"
	"github.com/thelemail/thaumaste/internal/service/rooms"
	"github.com/thelemail/thaumaste/internal/service/sync"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/service/timeline"
	"github.com/thelemail/thaumaste/internal/service/todevice"
	"github.com/thelemail/thaumaste/internal/service/tokens"
	"github.com/thelemail/thaumaste/internal/service/typing"
	"github.com/thelemail/thaumaste/internal/service/users"
)

func provideServerConfig(c config.Config) config.Server       { return c.Server }
func providePostgresConfig(c config.Config) config.Postgres   { return c.Postgres }
func provideSigningConfig(c config.Config) config.Signing     { return c.Signing }
func provideValkeyConfig(c config.Config) config.Valkey       { return c.Valkey }
func provideLimitsConfig(c config.Config) config.Limits       { return c.Limits }
func provideSyncConfig(c config.Config) config.Sync           { return c.Sync }
func provideKeysConfig(c config.Config) config.Keys           { return c.Keys }
func provideDirectoryConfig(c config.Config) config.Directory { return c.Directory }
func provideToDeviceConfig(c config.Config) config.ToDevice   { return c.ToDevice }

var ConfigSet = wire.NewSet(provideSyncConfig, provideKeysConfig, provideDirectoryConfig, provideToDeviceConfig, provideServerConfig, providePostgresConfig, provideSigningConfig,
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
	deviceLists service.DeviceLists,
	tx repository.Transactor,
	cfg config.Auth,
	clock func() time.Time,
) service.Users {
	return users.New(usersRepo, credentials, devices, refresh, sessions, attempts,
		tokens, tenants, deviceLists, tx, cfg, clock, nil)
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
	relations repository.Relation,
	txns repository.Transaction,
	tenants service.Tenants,
	tx repository.Transactor,
	stream *postgres.Stream,
	locks *valkey.Client,
	notifier *notify.Notifier,
	gate *serialiser.Serialiser,
	cfg config.Server,
	clock func() time.Time,
) service.Events {
	return events.New(rooms, eventRepo, stateRepo, members, relations, txns, tenants, tx, stream, locks,
		notifier, gate, cfg.InstanceName, clock, nil)
}

func provideNotifier(locks *valkey.Client, cfg config.Valkey) *notify.Notifier {
	return notify.New(locks, cfg.KeyPrefix+":sync")
}

func provideSync(
	connections repository.Connection,
	members repository.RoomMember,
	eventRepo repository.Event,
	timelineSvc service.Timeline,
	tx repository.Transactor,
	stream *postgres.Stream,
	notifier *notify.Notifier,
	gate *serialiser.Serialiser,
	stores sync.Stores,
	streams sync.Streams,
	cfg config.Sync,
	clock func() time.Time,
) service.Sync {
	return sync.New(connections, members, eventRepo, timelineSvc, stores, streams, tx, stream, notifier, gate, cfg, clock)
}

func provideKeys(keyRepo repository.Key, members repository.RoomMember, tx repository.Transactor,
	deviceLists service.DeviceLists, cfg config.Keys,
) service.Keys {
	return keys.New(keyRepo, members, tx, deviceLists, cfg)
}

type AccountDataStream struct{ *postgres.Stream }

func provideAccountDataStream(ctx context.Context, db *postgres.Client, cfg config.Server) (*AccountDataStream, error) {
	stream, err := postgres.NewStream(ctx, db, postgres.StreamConfig{
		Name:     "account_data",
		Instance: cfg.InstanceName,
		Sequence: "account_data_stream_seq",
	})
	if err != nil {
		return nil, err
	}
	return &AccountDataStream{Stream: stream}, nil
}

func provideAccountData(data repository.AccountData, roomRepo repository.Room, tx repository.Transactor,
	stream *AccountDataStream, notifier *notify.Notifier,
) service.AccountData {
	return accountdata.New(data, roomRepo, tx, stream.Stream, notifier)
}

func provideDirectory(userRepo repository.User, roomRepo repository.Room, eventRepo repository.Event,
	cfg config.Directory,
) service.Directory {
	return directory.New(userRepo, roomRepo, eventRepo, cfg)
}

type ReceiptStream struct{ *postgres.Stream }

func provideReceiptStream(ctx context.Context, db *postgres.Client, cfg config.Server) (*ReceiptStream, error) {
	stream, err := postgres.NewStream(ctx, db, postgres.StreamConfig{
		Name:     "receipts",
		Instance: cfg.InstanceName,
		Sequence: "receipts_stream_seq",
	})
	if err != nil {
		return nil, err
	}
	return &ReceiptStream{Stream: stream}, nil
}

func provideReceipts(receiptRepo repository.Receipt, members repository.RoomMember, events service.Events,
	data service.AccountData, tx repository.Transactor, stream *ReceiptStream,
	notifier *notify.Notifier, clock func() time.Time,
) service.Receipts {
	return receipts.New(receiptRepo, members, events, data, tx, stream.Stream, notifier, clock)
}

func providePresence(presenceRepo repository.Presence, members repository.RoomMember,
	clock func() time.Time,
) service.Presence {
	return presence.New(presenceRepo, members, clock)
}

type ToDeviceStream struct{ *postgres.Stream }

func provideToDeviceStream(ctx context.Context, db *postgres.Client, cfg config.Server) (*ToDeviceStream, error) {
	stream, err := postgres.NewStream(ctx, db, postgres.StreamConfig{
		Name:     "to_device",
		Instance: cfg.InstanceName,
		Sequence: "to_device_stream_seq",
	})
	if err != nil {
		return nil, err
	}
	return &ToDeviceStream{Stream: stream}, nil
}

type DeviceListStream struct{ *postgres.Stream }

func provideDeviceListStream(ctx context.Context, db *postgres.Client, cfg config.Server) (*DeviceListStream, error) {
	stream, err := postgres.NewStream(ctx, db, postgres.StreamConfig{
		Name:     "device_lists",
		Instance: cfg.InstanceName,
		Sequence: "device_list_stream_seq",
	})
	if err != nil {
		return nil, err
	}
	return &DeviceListStream{Stream: stream}, nil
}

func provideToDevice(messages repository.ToDevice, devices repository.Device, tx repository.Transactor,
	stream *ToDeviceStream, notifier *notify.Notifier,
) service.ToDevice {
	return todevice.New(messages, devices, tx, stream.Stream, notifier)
}

func provideDeviceLists(changes repository.DeviceList, members repository.RoomMember,
	tx repository.Transactor, stream *DeviceListStream, notifier *notify.Notifier,
) service.DeviceLists {
	return devicelists.New(changes, members, tx, stream.Stream, notifier)
}

func provideSyncStores(toDeviceRepo repository.ToDevice, deviceListRepo repository.DeviceList,
	data repository.AccountData, receiptRepo repository.Receipt, typingRepo repository.Typing,
	keyRepo repository.Key,
) sync.Stores {
	return sync.Stores{
		ToDevice: toDeviceRepo, DeviceLists: deviceListRepo, AccountData: data,
		Receipts: receiptRepo, Typing: typingRepo, Keys: keyRepo,
	}
}

func provideSyncStreams(toDevice *ToDeviceStream, deviceLists *DeviceListStream,
	data *AccountDataStream, receipts *ReceiptStream,
) sync.Streams {
	return sync.Streams{
		ToDevice: toDevice.Stream, DeviceLists: deviceLists.Stream,
		AccountData: data.Stream, Receipts: receipts.Stream,
	}
}

func provideLegacyStores(members repository.RoomMember, eventRepo repository.Event,
	toDeviceRepo repository.ToDevice, deviceListRepo repository.DeviceList,
	data repository.AccountData, receiptRepo repository.Receipt, typingRepo repository.Typing,
	keyRepo repository.Key,
) legacysync.Stores {
	return legacysync.Stores{
		Members: members, Events: eventRepo, ToDevice: toDeviceRepo,
		DeviceLists: deviceListRepo, AccountData: data, Receipts: receiptRepo,
		Typing: typingRepo, Keys: keyRepo,
	}
}

func provideLegacyStreams(events *postgres.Stream, toDevice *ToDeviceStream,
	deviceLists *DeviceListStream, data *AccountDataStream, receipts *ReceiptStream,
) legacysync.Streams {
	return legacysync.Streams{
		Events: events, ToDevice: toDevice.Stream, DeviceLists: deviceLists.Stream,
		AccountData: data.Stream, Receipts: receipts.Stream,
	}
}

func provideLegacySync(stores legacysync.Stores, streams legacysync.Streams,
	timelineSvc service.Timeline, tx repository.Transactor, notifier *notify.Notifier,
	cfg config.Sync, clock func() time.Time,
) service.LegacySync {
	return legacysync.New(stores, streams, timelineSvc, tx, notifier, cfg, clock)
}

func provideSendLimits(cfg config.Limits) entity.SendLimits {
	return entity.SendLimits{
		PerUser:   cfg.SendPerUser,
		PerRoom:   cfg.SendPerRoom,
		PerTenant: cfg.SendPerTenant,
		Window:    cfg.SendWindow,
	}
}

var DomainSet = wire.NewSet(
	provideSendLimits,
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
	relation.New,
	roommember.New,
	transaction.New,
	provideNotifier,
	provideEvents,
	timeline.New,
	rooms.New,
	connection.New,
	provideSync,
	key.New,
	provideKeys,
	accountdatarepo.New,
	receiptrepo.New,
	typingrepo.New,
	presencerepo.New,
	provideReceiptStream,
	provideReceipts,
	todevicerepo.New,
	devicelistrepo.New,
	provideToDeviceStream,
	provideDeviceListStream,
	provideToDevice,
	provideDeviceLists,
	provideSyncStores,
	provideSyncStreams,
	provideLegacyStores,
	provideLegacyStreams,
	provideLegacySync,
	typing.New,
	providePresence,
	filterrepo.New,
	filters.New,
	provideAccountDataStream,
	provideAccountData,
	provideDirectory,
	user.New,
	credential.New,
	device.New,
	refreshtoken.New,
	provideUIASessions,
	authattempt.New,
	provideUsers,
)
