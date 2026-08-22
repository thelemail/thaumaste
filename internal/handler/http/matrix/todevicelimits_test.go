package matrix_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

func TestAnUnknownToDeviceBatchTokenIsRefused(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	_ = s.register(t, tenant.ServerName, "alice", goodPassword)
	phone := s.loginAlice(t, "PHONE")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	s.mustSendToDevice(t, tenant.ServerName, bob, "m.room_key", "one", map[string]any{
		phone.UserID: map[string]any{phone.DeviceID: map[string]any{"session": "first"}},
	})

	for _, token := range []string{"not-a-number", "-1", "12x"} {
		request := withExtensions(window(1, 0, 9), toDeviceEnabled(token))
		rec := s.syncRaw(t, tenant.ServerName, phone.AccessToken, "", request)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("an unknown batch token %q = %d, want 400: %s", token, rec.Code, rec.Body)
		}
	}

	accepted := s.syncOnce(t, tenant.ServerName, phone.AccessToken, "",
		withExtensions(window(1, 0, 9), toDeviceEnabled("")))
	batch := extension[toDeviceExtension](t, accepted, "to_device")
	if len(batch.Events) != 1 {
		t.Fatalf("the queue was not delivered after the refusals: %v", batch.Events)
	}
}

func TestASenderOverTheToDeviceLimitIsToldToWaitAndRecovers(t *testing.T) {
	s := newToDeviceLimitedServer(t, entity.SendLimits{PerUser: 2, Window: 300 * time.Millisecond})
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	target := map[string]any{
		bob.UserID: map[string]any{bob.DeviceID: map[string]any{"session": "one"}},
	}
	s.mustSendToDevice(t, tenant.ServerName, alice, "m.room_key", "a", target)
	s.mustSendToDevice(t, tenant.ServerName, alice, "m.room_key", "b", target)

	refused := s.sendToDevice(t, tenant.ServerName, alice, "m.room_key", "c", target)
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("a sender over the limit = %d, want 429: %s", refused.Code, refused.Body)
	}
	if got := errcode(t, refused); got != "M_LIMIT_EXCEEDED" {
		t.Fatalf("the refusal said %s", got)
	}
	told := decode[struct {
		RetryAfterMS int64 `json:"retry_after_ms"`
	}](t, refused)
	if told.RetryAfterMS <= 0 {
		t.Fatalf("the refusal did not say how long to wait: %+v", told)
	}

	time.Sleep(400 * time.Millisecond)
	s.mustSendToDevice(t, tenant.ServerName, alice, "m.room_key", "d", target)
}

func TestOneSenderExhaustingTheToDeviceLimitDoesNotStopAnother(t *testing.T) {
	s := newToDeviceLimitedServer(t, entity.SendLimits{PerUser: 1, Window: 5 * time.Second})
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	carol := s.register(t, tenant.ServerName, "carol", goodPassword)

	target := map[string]any{
		carol.UserID: map[string]any{carol.DeviceID: map[string]any{"session": "one"}},
	}
	s.mustSendToDevice(t, tenant.ServerName, alice, "m.room_key", "a", target)
	if rec := s.sendToDevice(t, tenant.ServerName, alice, "m.room_key", "b", target); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the exhausted sender = %d, want 429: %s", rec.Code, rec.Body)
	}
	s.mustSendToDevice(t, tenant.ServerName, bob, "m.room_key", "a", target)
}

func TestTheSendResponseNeverVariesWithTheRecipientQueueDepth(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	idle := s.register(t, tenant.ServerName, "idle", goodPassword)
	busy := s.register(t, tenant.ServerName, "busy", goodPassword)

	for i := range 40 {
		s.mustSendToDevice(t, tenant.ServerName, alice, "m.room_key", "fill-"+strconv.Itoa(i),
			map[string]any{busy.UserID: map[string]any{busy.DeviceID: map[string]any{"n": i}}})
	}

	deep := s.sendToDevice(t, tenant.ServerName, alice, "m.room_key", "probe-deep",
		map[string]any{busy.UserID: map[string]any{busy.DeviceID: map[string]any{"n": "deep"}}})
	shallow := s.sendToDevice(t, tenant.ServerName, alice, "m.room_key", "probe-shallow",
		map[string]any{idle.UserID: map[string]any{idle.DeviceID: map[string]any{"n": "shallow"}}})

	if deep.Code != shallow.Code || deep.Body.String() != shallow.Body.String() {
		t.Fatalf("the send response leaks the recipient queue depth: deep %d %s, shallow %d %s",
			deep.Code, deep.Body, shallow.Code, shallow.Body)
	}
}
