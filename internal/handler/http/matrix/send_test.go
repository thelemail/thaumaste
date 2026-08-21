package matrix_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) send(t *testing.T, host, token, roomID, txnID string, content map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/send/m.room.message/"+url.PathEscape(txnID),
		token, content)
}

func (s *server) mustSend(t *testing.T, host, token, roomID, txnID string, content map[string]any) string {
	t.Helper()
	rec := s.send(t, host, token, roomID, txnID, content)
	if rec.Code != http.StatusOK {
		t.Fatalf("send = %d: %s", rec.Code, rec.Body)
	}
	return decode[struct {
		EventID string `json:"event_id"`
	}](t, rec).EventID
}

func (s *server) loginAlice(t *testing.T, deviceID string) sessionBody {
	t.Helper()
	body := map[string]any{
		"type":       entity.LoginTypePassword,
		"identifier": map[string]any{"type": entity.IdentifierTypeUser, "user": "alice"},
		"password":   goodPassword,
	}
	if deviceID != "" {
		body["device_id"] = deviceID
	}
	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/login", "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body)
	}
	return decode[sessionBody](t, rec)
}

func text(body string) map[string]any {
	return map[string]any{"msgtype": "m.text", "body": body}
}

func (s *server) messageCount(t *testing.T, roomID string) int {
	t.Helper()
	var count int
	err := s.db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM events e
		JOIN rooms r ON r.room_nid = e.room_nid
		JOIN event_types t ON t.event_type_nid = e.event_type_nid
		WHERE r.room_id = $1 AND t.event_type = $2`, roomID, entity.EventTypeMessage).Scan(&count)
	if err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return count
}

func TestARetriedTransactionReturnsTheOriginalEvent(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	first := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))
	second := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))

	if first != second {
		t.Fatalf("event ids %s and %s differ for one transaction", first, second)
	}
	if got := s.messageCount(t, roomID); got != 1 {
		t.Fatalf("%d messages in the room, want 1", got)
	}
}

func TestARetriedTransactionIgnoresTheContent(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	first := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))
	second := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("something else entirely"))

	if first != second {
		t.Fatalf("changing the content produced a second event: %s then %s", first, second)
	}
	if got := s.messageCount(t, roomID); got != 1 {
		t.Fatalf("%d messages in the room, want 1", got)
	}
}

func TestATransactionIsScopedToItsRoom(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	here := s.createRoom(t, "alpha.test", token, map[string]any{})
	there := s.createRoom(t, "alpha.test", token, map[string]any{})

	first := s.mustSend(t, "alpha.test", token, here, "lorem", text("hello"))
	second := s.mustSend(t, "alpha.test", token, there, "lorem", text("hello"))

	if first == second {
		t.Fatalf("the same transaction in two rooms produced one event: %s", first)
	}
}

func TestATransactionIsScopedToTheDeviceRatherThanTheToken(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	first := s.register(t, "alpha.test", "alice", goodPassword)
	roomID := s.createRoom(t, "alpha.test", first.AccessToken, map[string]any{})

	_ = tenant
	original := s.mustSend(t, "alpha.test", first.AccessToken, roomID, "abcdef", text("hello"))

	same := s.loginAlice(t, first.DeviceID)
	other := s.loginAlice(t, "")

	if same.DeviceID != first.DeviceID {
		t.Fatalf("login kept device %q, want %q", same.DeviceID, first.DeviceID)
	}
	if other.DeviceID == first.DeviceID {
		t.Fatal("a login without a device id reused the first device")
	}

	shared := s.mustSend(t, "alpha.test", same.AccessToken, roomID, "abcdef", text("hello"))
	separate := s.mustSend(t, "alpha.test", other.AccessToken, roomID, "abcdef", text("hello"))

	if shared != original {
		t.Fatalf("two tokens on one device produced %s and %s", original, shared)
	}
	if separate == original {
		t.Fatalf("a second device replayed the first device's transaction: %s", separate)
	}
}

func TestASentEventIsReadableBackWithItsTransaction(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	eventID := s.mustSend(t, "alpha.test", token, roomID, "abcdefg", text("hello"))

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/event/"+url.PathEscape(eventID), token)
	if rec.Code != http.StatusOK {
		t.Fatalf("read back = %d: %s", rec.Code, rec.Body)
	}

	body := decode[struct {
		EventID  string          `json:"event_id"`
		RoomID   string          `json:"room_id"`
		Sender   string          `json:"sender"`
		Content  json.RawMessage `json:"content"`
		Unsigned struct {
			TransactionID string `json:"transaction_id"`
			Age           *int64 `json:"age"`
		} `json:"unsigned"`
	}](t, rec)

	if body.EventID != eventID {
		t.Fatalf("event_id = %q, want %q", body.EventID, eventID)
	}
	if body.RoomID != roomID {
		t.Fatalf("room_id = %q, want %q", body.RoomID, roomID)
	}
	if body.Unsigned.TransactionID != "abcdefg" {
		t.Fatalf("unsigned.transaction_id = %q, want abcdefg", body.Unsigned.TransactionID)
	}
	if body.Unsigned.Age == nil {
		t.Fatal("unsigned carries no age")
	}
}

func TestAnotherUserCannotSeeTheSendersTransaction(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"preset": entity.PresetPublicChat})

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)

	eventID := s.mustSend(t, "alpha.test", token, roomID, "abcdefg", text("hello"))

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/event/"+url.PathEscape(eventID), bob.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("read back = %d: %s", rec.Code, rec.Body)
	}
	if got := decode[struct {
		Unsigned struct {
			TransactionID string `json:"transaction_id"`
		} `json:"unsigned"`
	}](t, rec).Unsigned.TransactionID; got != "" {
		t.Fatalf("another user saw the sender's transaction id %q", got)
	}
}

func TestAStrangerCannotReadAnEvent(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	eventID := s.mustSend(t, "alpha.test", token, roomID, "abcdefg", text("hello"))

	stranger := s.register(t, "alpha.test", "mallory", goodPassword)
	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/event/"+url.PathEscape(eventID), stranger.AccessToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a stranger reading an event = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestANonMemberCannotSend(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	stranger := s.register(t, "alpha.test", "mallory", goodPassword)
	rec := s.send(t, "alpha.test", stranger.AccessToken, roomID, "lorem", text("hello"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a stranger sending = %d, want 403: %s", rec.Code, rec.Body)
	}
	if got := s.messageCount(t, roomID); got != 0 {
		t.Fatalf("%d messages after a refused send", got)
	}
}

func TestAnOversizedEventIsRefusedAsTooLarge(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	rec := s.send(t, "alpha.test", token, roomID, "lorem", text(strings.Repeat("and they dont stop coming ", 2700)))
	if rec.Code != http.StatusRequestEntityTooLarge || errcode(t, rec) != "M_TOO_LARGE" {
		t.Fatalf("an oversized message = %d %s", rec.Code, rec.Body)
	}
}

func TestNonCanonicalNumbersAreRefusedAsBadJSON(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	for name, raw := range map[string]string{
		"above-the-safe-range": `{"body": 9007199254740992}`,
		"below-the-safe-range": `{"body": -9007199254740992}`,
		"a-float":              `{"body": 1.1}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := s.raw(t, http.MethodPut, "alpha.test",
				"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/send/complement.dummy/"+name,
				token, raw)
			if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_BAD_JSON" {
				t.Fatalf("%s = %d %s", name, rec.Code, rec.Body)
			}
		})
	}
}

func TestJSONSpecialValuesAreRefused(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	for i, raw := range []string{`{"body": Infinity}`, `{"body": -Infinity}`, `{"body": NaN}`} {
		rec := s.raw(t, http.MethodPut, "alpha.test",
			fmt.Sprintf("/_matrix/client/v3/rooms/%s/send/complement.dummy/special-%d", url.PathEscape(roomID), i),
			token, raw)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", raw, rec.Code, rec.Body)
		}
	}
}

func TestATransactionSurvivesARestart(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	original := s.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))

	restarted := reopen(t, s)
	replayed := restarted.mustSend(t, "alpha.test", token, roomID, "lorem", text("hello"))

	if replayed != original {
		t.Fatalf("after a restart the transaction produced %s, want %s", replayed, original)
	}
	if got := restarted.messageCount(t, roomID); got != 1 {
		t.Fatalf("%d messages in the room, want 1", got)
	}
}

func TestConcurrentRetriesOfOneTransactionWriteOneEvent(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	const attempts = 8
	ids := make(chan string, attempts)
	var wg sync.WaitGroup

	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := s.send(t, "alpha.test", token, roomID, "lorem", text("hello"))
			if rec.Code != http.StatusOK {
				t.Errorf("send = %d: %s", rec.Code, rec.Body)
				return
			}
			ids <- decode[struct {
				EventID string `json:"event_id"`
			}](t, rec).EventID
		}()
	}
	wg.Wait()
	close(ids)

	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("%d distinct event ids for one transaction: %v", len(seen), seen)
	}
	if got := s.messageCount(t, roomID); got != 1 {
		t.Fatalf("%d messages in the room, want 1", got)
	}
}
