package events_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/pkg/serialiser"
	"github.com/thelemail/thaumaste/internal/pkg/valkey"
	"github.com/thelemail/thaumaste/internal/repository/event"
	"github.com/thelemail/thaumaste/internal/repository/room"
	"github.com/thelemail/thaumaste/internal/repository/roommember"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/state"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	"github.com/thelemail/thaumaste/internal/service/events"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
	"github.com/thelemail/thaumaste/internal/testutil/valkeytest"
)

func lockLimits() config.Limits {
	return config.Limits{SendPerUser: 1, SendWindow: time.Second}
}

func instance(t *testing.T, name string, cfg config.Valkey) *harness {
	t.Helper()

	pg := pgtest.Connect(t, "tenants")

	locks, err := valkey.New(t.Context(), cfg, lockLimits())
	if err != nil {
		t.Fatalf("valkey: %v", err)
	}
	t.Cleanup(locks.Close)
	if err := locks.Ping(t.Context()); err != nil {
		t.Fatalf("valkey: %v", err)
	}

	sealer, err := keyseal.NewWithKey(make([]byte, keyseal.MasterKeySize))
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	tenantSvc := tenants.New(tenant.New(pg), signingkey.New(pg), sealer, pg, nil, nil)

	eventRepo := event.New(pg)
	stream, err := postgres.NewStream(t.Context(), pg, postgres.StreamConfig{
		Name: "events", Instance: name, Sequence: "events_stream_seq",
	})
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	eventSvc := events.New(room.New(pg, eventRepo), eventRepo, state.New(pg), roommember.New(pg),
		tenantSvc, pg, stream, locks, serialiser.New(), name, nil, nil)

	return &harness{events: eventSvc, tenants: tenantSvc, stream: stream, db: pg}
}

func TestTwoServersWritingOneRoomKeepItLinear(t *testing.T) {
	shared := valkeytest.Settings(t)
	valkeytest.Require(t, shared)

	first := instance(t, "one", shared)
	second := instance(t, "two", shared)

	of := first.tenant(t, "twoservers.example")
	created, creator, _ := first.room(t, of, entity.RoomVersion11)

	const each = 12
	var wg sync.WaitGroup
	errs := make(chan error, each*2)

	for _, h := range []*harness{first, second} {
		for i := range each {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := h.events.Send(t.Context(), of.Scope(), entity.NewEvent{
					RoomID: created.RoomID, Type: entity.EventTypeMessage, Sender: creator,
					Content: map[string]any{"msgtype": "m.text", "body": strconv.Itoa(i)},
				})
				errs <- err
			}()
		}
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	assertLinear(t, second, created.RoomID, each*2)
}

func assertLinear(t *testing.T, h *harness, roomID string, sent int) {
	t.Helper()

	timeline, err := h.events.Timeline(t.Context(), roomID)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	messages := 0
	for i, e := range timeline {
		if e.Event.Type() == entity.EventTypeMessage {
			messages++
		}
		if i == 0 {
			continue
		}
		prev := e.Event.PrevEvents()
		if len(prev) != 1 {
			t.Fatalf("%s has %d prev events, want 1", e.Event.ID(), len(prev))
		}
		if prev[0] != timeline[i-1].Event.ID() {
			t.Fatalf("%s follows %s, but the previous event in the timeline is %s",
				e.Event.ID(), prev[0], timeline[i-1].Event.ID())
		}
		if e.StreamOrdering <= timeline[i-1].StreamOrdering {
			t.Fatalf("stream ordering went backwards: %d then %d",
				timeline[i-1].StreamOrdering, e.StreamOrdering)
		}
	}
	if messages != sent {
		t.Fatalf("%d messages in the room, want %d", messages, sent)
	}
}
