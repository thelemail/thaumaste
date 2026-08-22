package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type Stores struct {
	ToDevice    repository.ToDevice
	AccountData repository.AccountData
	Receipts    repository.Receipt
	Typing      repository.Typing
	Keys        repository.Key
}

type Streams struct {
	ToDevice    *postgres.Stream
	DeviceLists *postgres.Stream
	AccountData *postgres.Stream
	Receipts    *postgres.Stream
}

type srv struct {
	connections repository.Connection
	members     repository.RoomMember
	events      repository.Event
	timeline    service.Timeline
	deviceLists service.DeviceLists
	stores      Stores
	streams     Streams
	tx          repository.Transactor
	stream      *postgres.Stream
	notifier    *notify.Notifier
	gate        *serialiser.Serialiser
	cfg         config.Sync
	clock       func() time.Time
}

func New(
	connections repository.Connection,
	members repository.RoomMember,
	events repository.Event,
	timeline service.Timeline,
	deviceLists service.DeviceLists,
	stores Stores,
	streams Streams,
	tx repository.Transactor,
	stream *postgres.Stream,
	notifier *notify.Notifier,
	gate *serialiser.Serialiser,
	cfg config.Sync,
	clock func() time.Time,
) service.Sync {
	if clock == nil {
		clock = time.Now
	}
	return &srv{connections: connections, members: members, events: events, timeline: timeline,
		deviceLists: deviceLists, stores: stores, streams: streams, tx: tx, stream: stream, notifier: notifier,
		gate: gate, cfg: cfg, clock: clock}
}

type session struct {
	scope      entity.TenantScope
	caller     string
	deviceID   string
	request    entity.SyncRequest
	wanted     entity.SyncExtensionRequest
	initial    bool
	connection entity.Connection
	known      map[int64]entity.RoomStatus
	delivered  map[int64]string
	ceiling    int64
	ceilings   entity.SyncCursors
}

func (s *srv) Sync(ctx context.Context, scope entity.TenantScope, caller, deviceID string, in entity.SyncRequest) (entity.SyncResult, error) {
	if err := in.Validate(); err != nil {
		return entity.SyncResult{}, err
	}
	if deviceID == "" {
		return entity.SyncResult{}, entity.ErrDeviceRequired
	}

	var result entity.SyncResult
	key := fmt.Sprintf("sync:%s:%s:%s:%s", scope.ID(), caller, deviceID, in.ConnID)
	err := s.gate.Do(ctx, key, func(ctx context.Context) error {
		var err error
		result, err = s.run(ctx, scope, caller, deviceID, in)
		return err
	})
	return result, err
}

func (s *srv) SweepConnections(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.connections.DeleteBefore(ctx, cutoff)
}

func (s *srv) run(ctx context.Context, scope entity.TenantScope, caller, deviceID string, in entity.SyncRequest) (entity.SyncResult, error) {
	wanted, err := entity.ParseSyncExtensions(in.Extensions)
	if err != nil {
		return entity.SyncResult{}, err
	}
	sess := session{scope: scope, caller: caller, deviceID: deviceID, request: in, wanted: wanted}

	connection, initial, err := s.resolve(ctx, scope, caller, deviceID, in)
	if err != nil {
		return entity.SyncResult{}, err
	}
	sess.connection, sess.initial = connection, initial

	deadline := s.clock().Add(s.timeout(in.Timeout))
	for {
		result, staged, err := s.attempt(ctx, &sess)
		if err != nil {
			return entity.SyncResult{}, err
		}
		if initial || len(result.Rooms) > 0 || result.Extensions.Carries() {
			return s.commit(ctx, &sess, result, staged)
		}
		if !s.clock().Before(deadline) {
			result.Extensions = s.quiet(&sess)
			result.Pos = sess.connection.Position(sess.connection.Confirmed)
			return result, s.connections.Touch(ctx, sess.connection.NID, s.clock().UTC())
		}
		if err := s.park(ctx, &sess, deadline); err != nil {
			return entity.SyncResult{}, err
		}
	}
}

func (s *srv) attempt(ctx context.Context, sess *session) (entity.SyncResult, []entity.NewRoomStatus, error) {
	var (
		result entity.SyncResult
		staged []entity.NewRoomStatus
	)
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		known, err := s.connections.Rooms(ctx, sess.connection.NID, false)
		if err != nil {
			return err
		}
		sess.known = make(map[int64]entity.RoomStatus, len(known))
		for _, status := range known {
			sess.known[status.RoomNID] = status
		}
		if sess.ceiling, err = s.stream.Published(ctx); err != nil {
			return err
		}
		if err := s.ceilings(ctx, sess); err != nil {
			return err
		}

		result, staged, err = s.build(ctx, sess)
		if err != nil {
			return err
		}
		result.Extensions, err = s.extensions(ctx, sess)
		return err
	})
	return result, staged, err
}

func (s *srv) commit(ctx context.Context, sess *session, result entity.SyncResult, staged []entity.NewRoomStatus) (entity.SyncResult, error) {
	generation := sess.connection.Confirmed + 1
	if err := s.connections.Stage(ctx, sess.connection.NID, generation, sess.ceilings, staged); err != nil {
		return entity.SyncResult{}, err
	}
	result.Pos = sess.connection.Position(generation)
	return result, nil
}

func (s *srv) park(ctx context.Context, sess *session, deadline time.Time) error {
	keys := []string{entity.UserWakeKey(sess.caller), entity.DeviceWakeKey(sess.caller, sess.deviceID)}
	rooms, err := s.members.ListForSync(ctx, sess.scope, sess.caller)
	if err != nil {
		return err
	}
	for _, room := range rooms {
		keys = append(keys, entity.RoomWakeKey(room.RoomID))
	}

	woken, release := s.notifier.Wait(keys)
	defer release()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	case <-woken:
	}
	return nil
}

func (s *srv) resolve(ctx context.Context, scope entity.TenantScope, caller, deviceID string, in entity.SyncRequest) (entity.Connection, bool, error) {
	if in.Pos == "" {
		connection, err := s.connections.Open(ctx, entity.NewConnection{
			TenantID: scope.ID(), UserID: caller, DeviceID: deviceID, ConnID: in.ConnID,
		})
		if err != nil {
			return entity.Connection{}, false, err
		}
		if err := s.connections.Reset(ctx, connection.NID); err != nil {
			return entity.Connection{}, false, err
		}
		connection.Confirmed = 0
		connection.ConfirmedStream = 0
		connection.ConfirmedCursors = entity.SyncCursors{}
		connection.Pending = nil
		connection.PendingStream = nil
		connection.PendingCursors = entity.SyncCursors{}
		return connection, true, nil
	}

	position, err := entity.ParseSyncPosition(in.Pos)
	if err != nil {
		return entity.Connection{}, false, err
	}
	connection, err := s.connections.Get(ctx, position.ConnectionNID)
	if err != nil {
		if errors.Is(err, repository.ErrConnectionNotFound) {
			return entity.Connection{}, false, entity.ErrUnknownPos
		}
		return entity.Connection{}, false, err
	}
	if !connection.Owns(scope, caller, deviceID) || connection.ConnID != in.ConnID {
		return entity.Connection{}, false, entity.ErrUnknownPos
	}

	switch {
	case connection.Pending != nil && *connection.Pending == position.Generation:
		if err := s.connections.Acknowledge(ctx, connection.NID, position.Generation, connection.PendingCursors); err != nil {
			return entity.Connection{}, false, err
		}
		connection.Confirmed = position.Generation
		connection.ConfirmedStream = *connection.PendingStream
		connection.ConfirmedCursors = connection.PendingCursors
		connection.Pending = nil
		connection.PendingStream = nil
		connection.PendingCursors = entity.SyncCursors{}
	case connection.Confirmed == position.Generation:
		if err := s.connections.Discard(ctx, connection.NID); err != nil {
			return entity.Connection{}, false, err
		}
		connection.Pending = nil
		connection.PendingStream = nil
	default:
		return entity.Connection{}, false, entity.ErrUnknownPos
	}
	return connection, false, nil
}

func (s *srv) timeout(requested int) time.Duration {
	if requested <= 0 {
		return 0
	}
	timeout := time.Duration(requested) * time.Millisecond
	if timeout > s.cfg.MaxTimeout {
		return s.cfg.MaxTimeout
	}
	return timeout
}
