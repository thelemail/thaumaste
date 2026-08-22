package legacysync

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/notify"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type Stores struct {
	Members     repository.RoomMember
	Events      repository.Event
	ToDevice    repository.ToDevice
	AccountData repository.AccountData
	Receipts    repository.Receipt
	Typing      repository.Typing
	Keys        repository.Key
}

type Streams struct {
	Events      *postgres.Stream
	ToDevice    *postgres.Stream
	DeviceLists *postgres.Stream
	AccountData *postgres.Stream
	Receipts    *postgres.Stream
}

type srv struct {
	stores      Stores
	streams     Streams
	timeline    service.Timeline
	deviceLists service.DeviceLists
	tx       repository.Transactor
	notifier *notify.Notifier
	cfg      config.Sync
	clock    func() time.Time
}

func New(
	stores Stores,
	streams Streams,
	timeline service.Timeline,
	deviceLists service.DeviceLists,
	tx repository.Transactor,
	notifier *notify.Notifier,
	cfg config.Sync,
	clock func() time.Time,
) service.LegacySync {
	if clock == nil {
		clock = time.Now
	}
	return &srv{stores: stores, streams: streams, timeline: timeline, deviceLists: deviceLists, tx: tx,
		notifier: notifier, cfg: cfg, clock: clock}
}

type session struct {
	scope    entity.TenantScope
	caller   string
	deviceID string
	request  entity.LegacySyncRequest
	initial  bool
	since    entity.SyncToken
	upTo     entity.SyncToken
	rooms    []entity.SyncRoom
}

func (s *srv) Sync(ctx context.Context, scope entity.TenantScope, caller, deviceID string,
	in entity.LegacySyncRequest,
) (entity.LegacySyncResult, error) {
	if err := in.Validate(); err != nil {
		return entity.LegacySyncResult{}, err
	}
	if deviceID == "" {
		return entity.LegacySyncResult{}, entity.ErrDeviceRequired
	}
	since, err := entity.ParseSyncToken(in.Since)
	if err != nil {
		return entity.LegacySyncResult{}, err
	}
	sess := session{scope: scope, caller: caller, deviceID: deviceID, request: in,
		initial: in.Since == "", since: since}

	deadline := s.clock().Add(s.timeout(in))
	for {
		result, err := s.attempt(ctx, &sess)
		if err != nil {
			return entity.LegacySyncResult{}, err
		}
		if sess.initial || in.FullState || result.Carries() || !s.clock().Before(deadline) {
			return result, nil
		}
		if err := s.park(ctx, &sess, deadline); err != nil {
			return entity.LegacySyncResult{}, err
		}
	}
}

func (s *srv) attempt(ctx context.Context, sess *session) (entity.LegacySyncResult, error) {
	var result entity.LegacySyncResult
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.ceilings(ctx, sess); err != nil {
			return err
		}
		rooms, err := s.stores.Members.ListForSync(ctx, sess.scope, sess.caller)
		if err != nil {
			return err
		}
		sess.rooms = slices.DeleteFunc(rooms, func(room entity.SyncRoom) bool { return room.Forgotten })

		result, err = s.build(ctx, sess)
		return err
	})
	if err != nil {
		return entity.LegacySyncResult{}, err
	}
	result.NextBatch = sess.upTo
	return result, nil
}

func (s *srv) ceilings(ctx context.Context, sess *session) error {
	var err error
	sess.upTo = sess.since
	if sess.upTo.Events, err = s.streams.Events.Published(ctx); err != nil {
		return err
	}
	if sess.upTo.AccountData, err = s.streams.AccountData.Published(ctx); err != nil {
		return err
	}
	if sess.upTo.Receipts, err = s.streams.Receipts.Published(ctx); err != nil {
		return err
	}
	if sess.upTo.DeviceLists, err = s.streams.DeviceLists.Published(ctx); err != nil {
		return err
	}
	if sess.upTo.Typing, err = s.stores.Typing.Version(ctx, sess.scope); err != nil {
		return err
	}
	return nil
}

func (s *srv) park(ctx context.Context, sess *session, deadline time.Time) error {
	keys := []string{
		entity.UserWakeKey(sess.caller),
		entity.DeviceWakeKey(sess.caller, sess.deviceID),
	}
	for _, room := range sess.rooms {
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

func (s *srv) timeout(in entity.LegacySyncRequest) time.Duration {
	if in.FullState || in.Timeout <= 0 {
		return 0
	}
	timeout := time.Duration(in.Timeout) * time.Millisecond
	if timeout > s.cfg.MaxTimeout {
		return s.cfg.MaxTimeout
	}
	return timeout
}

func marshalEvent(eventType string, content any) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"type": eventType, "content": content})
}
