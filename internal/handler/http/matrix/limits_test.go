package matrix_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
	"github.com/thelemail/thaumaste/internal/testutil/valkeytest"
)

func TestTheSenderIsToldHowLongToWait(t *testing.T) {
	s := newLimitedServer(t, entity.SendLimits{PerUser: 3, Window: 2 * time.Second})
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	for i := range 3 {
		if rec := s.send(t, "alpha.test", token, roomID, "txn-"+strconv.Itoa(i), text("hello")); rec.Code != http.StatusOK {
			t.Fatalf("send %d = %d: %s", i, rec.Code, rec.Body)
		}
	}

	rec := s.send(t, "alpha.test", token, roomID, "txn-over", text("hello"))
	if rec.Code != http.StatusTooManyRequests || errcode(t, rec) != "M_LIMIT_EXCEEDED" {
		t.Fatalf("the fourth send = %d %s", rec.Code, rec.Body)
	}

	header := rec.Header().Get("Retry-After")
	if header == "" {
		t.Fatal("no Retry-After header for the client to back off with")
	}
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 1 {
		t.Fatalf("Retry-After = %q", header)
	}

	body := decode[struct {
		RetryAfterMS int64 `json:"retry_after_ms"`
	}](t, rec)
	if body.RetryAfterMS <= 0 {
		t.Fatalf("retry_after_ms = %d", body.RetryAfterMS)
	}

	if got := s.messageCount(t, roomID); got != 3 {
		t.Fatalf("%d messages in the room, want 3: a refused send was written anyway", got)
	}
}

func TestALimitedSenderRecoversAfterTheWindow(t *testing.T) {
	s := newLimitedServer(t, entity.SendLimits{PerUser: 2, Window: 500 * time.Millisecond})
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	for i := range 2 {
		s.mustSend(t, "alpha.test", token, roomID, "txn-"+strconv.Itoa(i), text("hello"))
	}
	if rec := s.send(t, "alpha.test", token, roomID, "txn-over", text("hello")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the third send = %d: %s", rec.Code, rec.Body)
	}

	time.Sleep(600 * time.Millisecond)

	if rec := s.send(t, "alpha.test", token, roomID, "txn-later", text("hello")); rec.Code != http.StatusOK {
		t.Fatalf("send after the window = %d: %s", rec.Code, rec.Body)
	}
}

func TestOneSenderExhaustingTheirLimitDoesNotStopAnother(t *testing.T) {
	s := newLimitedServer(t, entity.SendLimits{PerUser: 2, Window: 5 * time.Second})
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"preset": entity.PresetPublicChat})

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)

	for i := range 2 {
		s.mustSend(t, "alpha.test", token, roomID, "alice-"+strconv.Itoa(i), text("hello"))
	}
	if rec := s.send(t, "alpha.test", token, roomID, "alice-over", text("hello")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("alice past her limit = %d: %s", rec.Code, rec.Body)
	}
	if rec := s.send(t, "alpha.test", bob.AccessToken, roomID, "bob-0", text("hello")); rec.Code != http.StatusOK {
		t.Fatalf("bob was refused because alice was noisy: %d %s", rec.Code, rec.Body)
	}
}

func TestARetryOfALimitedTransactionIsStillDeduplicated(t *testing.T) {
	s := newLimitedServer(t, entity.SendLimits{PerUser: 4, Window: 5 * time.Second})
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	first := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))
	second := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))

	if first != second {
		t.Fatalf("a retry under a rate limit produced %s and %s", first, second)
	}
	if got := s.messageCount(t, roomID); got != 1 {
		t.Fatalf("%d messages in the room, want 1", got)
	}
}

func TestAnUnreachableLimiterLetsSendsThrough(t *testing.T) {
	s := wireServer(t, nil, pgtest.Connect(t, "tenants"), valkeytest.Unreachable(t),
		entity.SendLimits{PerUser: 1, Window: time.Minute})
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	for i := range 3 {
		if rec := s.send(t, "alpha.test", token, roomID, "txn-"+strconv.Itoa(i), text("hello")); rec.Code != http.StatusOK {
			t.Fatalf("send %d with valkey down = %d: %s", i, rec.Code, rec.Body)
		}
	}
	if got := s.messageCount(t, roomID); got != 3 {
		t.Fatalf("%d messages in the room, want 3", got)
	}
}

func TestSpentTransactionsAreSweptOnceTheyAreOldEnough(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	original := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))

	kept, err := s.events.SweepTransactions(t.Context(), time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("SweepTransactions: %v", err)
	}
	if kept != 0 {
		t.Fatalf("the sweep deleted %d fresh transactions", kept)
	}
	if replayed := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello")); replayed != original {
		t.Fatal("a fresh transaction stopped replaying")
	}

	swept, err := s.events.SweepTransactions(t.Context(), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("SweepTransactions: %v", err)
	}
	if swept == 0 {
		t.Fatal("the sweep deleted nothing past the cutoff")
	}
	if s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello")) == original {
		t.Fatal("a swept transaction still replayed")
	}
}
