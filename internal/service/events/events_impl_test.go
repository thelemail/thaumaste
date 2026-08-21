package events_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/repository/event"
	"github.com/thelemail/thaumaste/internal/repository/room"
	"github.com/thelemail/thaumaste/internal/repository/roommember"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/state"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	"github.com/thelemail/thaumaste/internal/repository/transaction"
	"github.com/thelemail/thaumaste/internal/service"
	"github.com/thelemail/thaumaste/internal/service/events"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
)

type harness struct {
	events  service.Events
	tenants service.Tenants
	stream  *postgres.Stream
	db      *postgres.Client
}

type heldTx struct {
	inner   repository.Transactor
	armed   *atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newHeldTx() heldTx {
	return heldTx{armed: new(atomic.Bool), entered: make(chan struct{}), release: make(chan struct{})}
}

func (h heldTx) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return h.inner.WithTx(ctx, func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		if h.armed.CompareAndSwap(true, false) {
			close(h.entered)
			<-h.release
		}
		return nil
	})
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return buildHarness(t, nil)
}

func buildHarness(t *testing.T, wrap func(repository.Transactor) repository.Transactor) *harness {
	t.Helper()
	pg := pgtest.Connect(t, "tenants")

	sealer, err := keyseal.NewWithKey(make([]byte, keyseal.MasterKeySize))
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	tenantSvc := tenants.New(tenant.New(pg), signingkey.New(pg), sealer, pg, nil, nil)

	eventRepo := event.New(pg)
	stream, err := postgres.NewStream(t.Context(), pg, postgres.StreamConfig{
		Name: "events", Instance: "test", Sequence: "events_stream_seq",
	})
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	var tx repository.Transactor = pg
	if wrap != nil {
		tx = wrap(tx)
	}
	eventSvc := events.New(room.New(pg, eventRepo), eventRepo, state.New(pg), roommember.New(pg), transaction.New(pg),
		tenantSvc, tx, stream, nil, serialiser.New(), "test", nil, nil)

	return &harness{events: eventSvc, tenants: tenantSvc, stream: stream, db: pg}
}

func (h *harness) tenant(t *testing.T, serverName string) entity.Tenant {
	t.Helper()
	created, err := h.tenants.Create(t.Context(), entity.NewTenant{ServerName: serverName})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return created
}

func (h *harness) room(t *testing.T, of entity.Tenant, version entity.RoomVersionID) (entity.Room, string, []entity.StoredEvent) {
	t.Helper()
	creator := "@creator:" + of.ServerName
	created, chain, err := h.events.CreateRoom(t.Context(), of.Scope(), entity.NewRoomRequest{
		Creator:    creator,
		ServerName: of.ServerName,
		Version:    version,
		Visibility: entity.VisibilityPrivate,
		Preset:     entity.PresetPrivateChat,
	})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	return created, creator, chain
}

func (h *harness) send(t *testing.T, of entity.Tenant, in entity.NewEvent) entity.StoredEvent {
	t.Helper()
	stored, err := h.events.Send(t.Context(), of.Scope(), in)
	if err != nil {
		t.Fatalf("Send %s: %v", in.Type, err)
	}
	return stored
}

func eachVersion(t *testing.T, fn func(t *testing.T, version entity.RoomVersionID)) {
	t.Helper()
	for _, id := range entity.SupportedRoomVersions() {
		t.Run(string(id), func(t *testing.T) { fn(t, id) })
	}
}

func TestARoomChainRoundTripsThroughStorage(t *testing.T) {
	eachVersion(t, func(t *testing.T, version entity.RoomVersionID) {
		h := newHarness(t)
		alpha := h.tenant(t, "alpha.test")
		created, creator, chain := h.room(t, alpha, version)
		empty := ""
		h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypePowerLevels, StateKey: &empty, Sender: creator,
			Content: map[string]any{"users_default": 0, "state_default": 50},
		})
		last := h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypeMessage, Sender: creator,
			Content: map[string]any{"msgtype": "m.text", "body": "hello"},
		})

		timeline, err := h.events.Timeline(t.Context(), created.RoomID)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if len(timeline) != len(chain)+2 {
			t.Fatalf("timeline = %d events, want %d", len(timeline), len(chain)+2)
		}

		keys, err := h.tenants.Keys(t.Context(), alpha.Scope())
		if err != nil || len(keys) != 1 {
			t.Fatalf("Keys: %v (%d)", err, len(keys))
		}
		roomVersion, err := entity.LookupRoomVersion(version)
		if err != nil {
			t.Fatalf("LookupRoomVersion: %v", err)
		}

		for i, stored := range timeline {
			err := stored.Event.VerifySignature(
				alpha.ServerName, entity.KeyID(keys[0].KeyID), keys[0].PublicKey, roomVersion)
			if err != nil {
				t.Fatalf("event %d does not verify: %v", i, err)
			}
			if err := stored.Event.VerifyContentHash(); err != nil {
				t.Fatalf("event %d content hash: %v", i, err)
			}

			sum, err := entity.ReferenceHash(stored.Event.Fields(), roomVersion)
			if err != nil {
				t.Fatalf("ReferenceHash: %v", err)
			}
			if stored.Event.ID() != "$"+entity.EventIDEncoding().EncodeToString(sum[:]) {
				t.Fatalf("event %d id does not match its reference hash", i)
			}

			if !stored.SenderIsLocal {
				t.Fatalf("event %d was not recorded as local", i)
			}
			if stored.InstanceName != "test" {
				t.Fatalf("event %d instance = %q", i, stored.InstanceName)
			}
			if stored.Disposition != entity.DispositionAccepted {
				t.Fatalf("event %d disposition = %q", i, stored.Disposition)
			}
		}

		for i := 1; i < len(timeline); i++ {
			if timeline[i].StreamOrdering <= timeline[i-1].StreamOrdering {
				t.Fatalf("stream ordering did not increase: %d then %d",
					timeline[i-1].StreamOrdering, timeline[i].StreamOrdering)
			}
			if timeline[i].Event.Depth() != timeline[i-1].Event.Depth()+1 {
				t.Fatalf("depth did not follow its parent at %d", i)
			}
		}

		for i := 1; i < len(timeline); i++ {
			prev := timeline[i].Event.PrevEvents()
			if len(prev) != 1 || prev[0] != timeline[i-1].Event.ID() {
				t.Fatalf("event %d names %v as its parent, want %s", i, prev, timeline[i-1].Event.ID())
			}
		}

		if got := timeline[len(timeline)-1].Event.ID(); got != last.Event.ID() {
			t.Fatalf("the last stored event is %s, want %s", got, last.Event.ID())
		}
	})
}

func TestTheStateBeforeAnEventIsWhatTheFoldSays(t *testing.T) {
	eachVersion(t, func(t *testing.T, version entity.RoomVersionID) {
		h := newHarness(t)
		alpha := h.tenant(t, "alpha.test")
		created, creator, chain := h.room(t, alpha, version)
		join := chain[1]

		beforeJoin, err := h.events.StateBefore(t.Context(), join.Event.ID())
		if err != nil {
			t.Fatalf("StateBefore: %v", err)
		}
		if _, ok := beforeJoin.Get(entity.EventTypeCreate, ""); !ok {
			t.Fatal("the state before the join does not contain the create event")
		}
		if got := beforeJoin.Membership(creator); got != "" {
			t.Fatalf("the creator is already %q before their own join", got)
		}

		empty := ""
		levels := h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypePowerLevels, StateKey: &empty, Sender: creator,
			Content: map[string]any{"users_default": 0},
		})

		beforeLevels, err := h.events.StateBefore(t.Context(), levels.Event.ID())
		if err != nil {
			t.Fatalf("StateBefore: %v", err)
		}
		if got := beforeLevels.Membership(creator); got != entity.MembershipJoin {
			t.Fatalf("membership before the power levels = %q, want join", got)
		}

		current, err := h.events.CurrentState(t.Context(), created.RoomID)
		if err != nil {
			t.Fatalf("CurrentState: %v", err)
		}
		if _, ok := current.Get(entity.EventTypePowerLevels, ""); !ok {
			t.Fatal("the current state does not include the power levels")
		}
		if got := current.Membership(creator); got != entity.MembershipJoin {
			t.Fatalf("current membership = %q", got)
		}
	})
}

func TestTheDagEdgesAreStoredNotJustDeclared(t *testing.T) {
	eachVersion(t, func(t *testing.T, version entity.RoomVersionID) {
		h := newHarness(t)
		alpha := h.tenant(t, "alpha.test")
		created, creator, _ := h.room(t, alpha, version)

		message := h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypeMessage, Sender: creator,
			Content: map[string]any{"msgtype": "m.text", "body": "hello"},
		})

		repo := event.New(h.db)
		parents, err := repo.ParentsOf(t.Context(), message.NID)
		if err != nil {
			t.Fatalf("ParentsOf: %v", err)
		}
		if !slices.Equal(parents, message.Event.PrevEvents()) {
			t.Fatalf("stored prev edges %v, event declares %v", parents, message.Event.PrevEvents())
		}

		authParents, err := repo.AuthParentsOf(t.Context(), message.NID)
		if err != nil {
			t.Fatalf("AuthParentsOf: %v", err)
		}
		declared := slices.Clone(message.Event.AuthEvents())
		slices.Sort(declared)
		if !slices.Equal(authParents, declared) {
			t.Fatalf("stored auth edges %v, event declares %v", authParents, declared)
		}
		if len(authParents) == 0 {
			t.Fatal("the message declared no auth events at all")
		}
	})
}

func TestTheFirstJoinNamesCreateOnlyWhereTheVersionRequiresIt(t *testing.T) {
	eachVersion(t, func(t *testing.T, version entity.RoomVersionID) {
		h := newHarness(t)
		alpha := h.tenant(t, "alpha.test")
		created, _, chain := h.room(t, alpha, version)
		join := chain[1]

		timeline, err := h.events.Timeline(t.Context(), created.RoomID)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		createID := timeline[0].Event.ID()

		named := slices.Contains(join.Event.AuthEvents(), createID)
		roomVersion, err := entity.LookupRoomVersion(version)
		if err != nil {
			t.Fatalf("LookupRoomVersion: %v", err)
		}
		if named != roomVersion.CreateInAuthEvents {
			t.Fatalf("version %s names create in auth_events = %v, want %v",
				version, named, roomVersion.CreateInAuthEvents)
		}
		if !roomVersion.CreateInAuthEvents && len(join.Event.AuthEvents()) != 0 {
			t.Fatalf("v12 first join declared %v", join.Event.AuthEvents())
		}
	})
}

func TestARoomIDNamesItsCreateEventUnderVersionTwelve(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")

	created, _, _ := h.room(t, alpha, entity.RoomVersion12)

	timeline, err := h.events.Timeline(t.Context(), created.RoomID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	create := timeline[0].Event
	if created.RoomID != "!"+create.ID()[1:] {
		t.Fatalf("room id %s does not name the create event %s", created.RoomID, create.ID())
	}
	if create.RoomID() != "" {
		t.Fatalf("a v12 create event must carry no room id, got %q", create.RoomID())
	}
}

func TestARoomIDCarriesADomainUnderVersionEleven(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")

	created, _, _ := h.room(t, alpha, entity.RoomVersion11)
	if !hasSuffix(created.RoomID, ":alpha.test") {
		t.Fatalf("room id = %s, want a domain suffix", created.RoomID)
	}

	timeline, err := h.events.Timeline(t.Context(), created.RoomID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if timeline[0].Event.RoomID() != created.RoomID {
		t.Fatal("a v11 create event must carry its room id")
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func TestAnUnauthorisedEventIsRefusedAndNotStored(t *testing.T) {
	eachVersion(t, func(t *testing.T, version entity.RoomVersionID) {
		h := newHarness(t)
		alpha := h.tenant(t, "alpha.test")
		created, _, _ := h.room(t, alpha, version)

		before, err := h.events.Timeline(t.Context(), created.RoomID)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}

		_, err = h.events.Send(t.Context(), alpha.Scope(), entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypeMessage, Sender: "@stranger:alpha.test",
			Content: map[string]any{"body": "hello"},
		})
		if !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}

		after, err := h.events.Timeline(t.Context(), created.RoomID)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("a refused event was stored anyway: %d then %d", len(before), len(after))
		}
	})
}

func TestTheRoomKeepsExactlyOneForwardExtremity(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	created, creator, _ := h.room(t, alpha, entity.DefaultRoomVersion)

	for i := range 5 {
		h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypeMessage, Sender: creator,
			Content: map[string]any{"msgtype": "m.text", "body": "message"},
		})
		_ = i
	}

	repo := room.New(h.db, event.New(h.db))
	extremities, err := repo.Extremities(t.Context(), created.NID)
	if err != nil {
		t.Fatalf("Extremities: %v", err)
	}
	if len(extremities) != 1 {
		t.Fatalf("extremities = %d, want exactly 1", len(extremities))
	}

	timeline, err := h.events.Timeline(t.Context(), created.RoomID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if extremities[0].Event.ID() != timeline[len(timeline)-1].Event.ID() {
		t.Fatal("the extremity is not the most recent event")
	}
}

func TestConcurrentSendsToOneRoomStayLinear(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	created, creator, chain := h.room(t, alpha, entity.DefaultRoomVersion)

	const writers = 6
	done := make(chan error, writers)
	for range writers {
		go func() {
			_, err := h.events.Send(t.Context(), alpha.Scope(), entity.NewEvent{
				RoomID: created.RoomID, Type: entity.EventTypeMessage, Sender: creator,
				Content: map[string]any{"msgtype": "m.text", "body": "concurrent"},
			})
			done <- err
		}()
	}
	for range writers {
		if err := <-done; err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	timeline, err := h.events.Timeline(t.Context(), created.RoomID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline) != writers+len(chain) {
		t.Fatalf("timeline = %d events, want %d", len(timeline), writers+len(chain))
	}

	seen := map[string]bool{}
	for _, stored := range timeline[1:] {
		prev := stored.Event.PrevEvents()
		if len(prev) != 1 {
			t.Fatalf("%s names %d parents", stored.Event.ID(), len(prev))
		}
		if seen[prev[0]] {
			t.Fatalf("two events both name %s as their parent, so the room forked", prev[0])
		}
		seen[prev[0]] = true
	}
}

func TestStateIsSharedRatherThanRewrittenForAQuietRoom(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	created, creator, chain := h.room(t, alpha, entity.DefaultRoomVersion)

	var snapshots []int64
	for range 4 {
		stored := h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypeMessage, Sender: creator,
			Content: map[string]any{"msgtype": "m.text", "body": "quiet"},
		})
		snapshots = append(snapshots, stored.StateSnapshotNID)
	}

	for i := 1; i < len(snapshots); i++ {
		if snapshots[i] != snapshots[0] {
			t.Fatalf("message %d wrote a fresh snapshot %d instead of reusing %d",
				i, snapshots[i], snapshots[0])
		}
	}

	var rows int
	err := h.db.QueryRowContext(t.Context(), "SELECT count(*) FROM state_snapshots").Scan(&rows)
	if err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if rows > len(chain)+1 {
		t.Fatalf("snapshots = %d for a room with %d creation events", rows, len(chain))
	}
}

func TestTwoDomainsCanEachHoldTheirOwnRoom(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	beta := h.tenant(t, "beta.test")

	alphaRoom, _, _ := h.room(t, alpha, entity.DefaultRoomVersion)
	betaRoom, _, _ := h.room(t, beta, entity.DefaultRoomVersion)

	if alphaRoom.RoomID == betaRoom.RoomID {
		t.Fatal("two domains produced the same room id")
	}
	alphaKeys, err := h.tenants.Keys(t.Context(), alpha.Scope())
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	version, err := entity.LookupRoomVersion(entity.DefaultRoomVersion)
	if err != nil {
		t.Fatalf("LookupRoomVersion: %v", err)
	}

	betaTimeline, err := h.events.Timeline(t.Context(), betaRoom.RoomID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	err = betaTimeline[0].Event.VerifySignature(
		"beta.test", entity.KeyID(alphaKeys[0].KeyID), alphaKeys[0].PublicKey, version)
	if err == nil {
		t.Fatal("beta's events verify under alpha's key")
	}
}

func TestSendingToAnUnknownRoomIsRefused(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")

	_, err := h.events.Send(t.Context(), alpha.Scope(), entity.NewEvent{
		RoomID: "!nobody:alpha.test", Type: entity.EventTypeMessage, Sender: "@a:alpha.test",
	})
	if !errors.Is(err, entity.ErrRoomNotFound) {
		t.Fatalf("error = %v, want ErrRoomNotFound", err)
	}
}

func TestAForeignSenderIsRecordedAsNotLocal(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	created, creator, _ := h.room(t, alpha, entity.DefaultRoomVersion)

	empty := ""
	h.send(t, alpha, entity.NewEvent{
		RoomID: created.RoomID, Type: entity.EventTypeJoinRules, StateKey: &empty, Sender: creator,
		Content: map[string]any{"join_rule": entity.JoinRulePublic},
	})

	guest := "@guest:elsewhere.test"
	stored := h.send(t, alpha, entity.NewEvent{
		RoomID: created.RoomID, Type: entity.EventTypeMember, StateKey: &guest, Sender: guest,
		Content: map[string]any{"membership": entity.MembershipJoin},
	})

	if stored.SenderIsLocal {
		t.Fatal("a sender from another domain was recorded as local")
	}

	reread, err := event.New(h.db).GetByEventID(t.Context(), stored.Event.ID())
	if err != nil {
		t.Fatalf("GetByEventID: %v", err)
	}
	if reread.SenderIsLocal {
		t.Fatal("locality was not persisted")
	}
}

func TestTheClockIsTheOriginServerTimestamp(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	created, _, _ := h.room(t, alpha, entity.DefaultRoomVersion)

	timeline, err := h.events.Timeline(t.Context(), created.RoomID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	ts := time.UnixMilli(timeline[0].Event.OriginServerTS())
	if time.Since(ts) > time.Minute || ts.After(time.Now().Add(time.Minute)) {
		t.Fatalf("origin_server_ts = %s", ts)
	}
}

func TestTheWatermarkNeverPassesAnUncommittedEvent(t *testing.T) {
	held := newHeldTx()
	h := buildHarness(t, func(inner repository.Transactor) repository.Transactor {
		held.inner = inner
		return held
	})

	of := h.tenant(t, "watermark.example")
	room, creator, _ := h.room(t, of, entity.RoomVersion11)

	settled := h.stream.Current()
	held.armed.Store(true)

	done := make(chan error, 1)
	go func() {
		_, err := h.events.Send(t.Context(), of.Scope(), entity.NewEvent{
			RoomID: room.RoomID, Type: entity.EventTypeMessage, Sender: creator,
			Content: map[string]any{"msgtype": "m.text", "body": "in flight"},
		})
		done <- err
	}()

	select {
	case <-held.entered:
	case err := <-done:
		t.Fatalf("Send finished without holding the transaction: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Send never reached the transaction")
	}

	if got := h.stream.Current(); got != settled {
		t.Fatalf("watermark = %d, want %d: it moved to a position whose transaction had not committed", got, settled)
	}

	close(held.release)
	if err := <-done; err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := h.stream.Current(); got <= settled {
		t.Fatalf("watermark = %d, want above %d once the write committed", got, settled)
	}
}

func TestARefusedSendDoesNotStallTheWatermark(t *testing.T) {
	h := newHarness(t)
	of := h.tenant(t, "stall.example")
	room, creator, _ := h.room(t, of, entity.RoomVersion11)

	_, err := h.events.Send(t.Context(), of.Scope(), entity.NewEvent{
		RoomID: room.RoomID, Type: entity.EventTypeMessage, Sender: "@stranger:" + of.ServerName,
		Content: map[string]any{"msgtype": "m.text", "body": "not mine to send"},
	})
	if !errors.Is(err, entity.ErrAuthFailed) {
		t.Fatalf("Send error = %v, want %v", err, entity.ErrAuthFailed)
	}

	refused := h.stream.Current()
	stored := h.send(t, of, entity.NewEvent{
		RoomID: room.RoomID, Type: entity.EventTypeMessage, Sender: creator,
		Content: map[string]any{"msgtype": "m.text", "body": "mine to send"},
	})

	if got := h.stream.Current(); got < stored.StreamOrdering {
		t.Fatalf("watermark = %d, want at least %d: the refused write pinned the stream", got, stored.StreamOrdering)
	}
	if stored.StreamOrdering <= refused {
		t.Fatalf("stream ordering = %d, want above %d", stored.StreamOrdering, refused)
	}
}

func TestConcurrentSendsAcrossRoomsReuseNoPosition(t *testing.T) {
	h := newHarness(t)
	of := h.tenant(t, "positions.example")

	const rooms, each = 4, 6
	created := make([]entity.Room, rooms)
	creators := make([]string, rooms)
	for i := range rooms {
		created[i], creators[i], _ = h.room(t, of, entity.RoomVersion11)
	}

	var wg sync.WaitGroup
	for i := range rooms {
		for j := range each {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := h.events.Send(t.Context(), of.Scope(), entity.NewEvent{
					RoomID: created[i].RoomID, Type: entity.EventTypeMessage, Sender: creators[i],
					Content: map[string]any{"msgtype": "m.text", "body": strconv.Itoa(j)},
				})
				if err != nil {
					t.Errorf("Send: %v", err)
				}
			}()
		}
	}
	wg.Wait()

	rows, err := h.db.QueryContext(t.Context(),
		`SELECT stream_ordering, count(*) FROM events GROUP BY stream_ordering HAVING count(*) > 1`)
	if err != nil {
		t.Fatalf("scan stream orderings: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var position, times int64
		if err := rows.Scan(&position, &times); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Fatalf("stream position %d was handed out %d times", position, times)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("scan stream orderings: %v", err)
	}

	var highest int64
	if err := h.db.QueryRowContext(t.Context(), `SELECT max(stream_ordering) FROM events`).Scan(&highest); err != nil {
		t.Fatalf("highest position: %v", err)
	}
	if got := h.stream.Current(); got != highest {
		t.Fatalf("watermark = %d, want %d once every write has settled", got, highest)
	}
}
