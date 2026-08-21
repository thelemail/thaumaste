package matrix_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) redact(t *testing.T, host, token, roomID, eventID, txnID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/redact/"+url.PathEscape(eventID)+"/"+url.PathEscape(txnID),
		token, body)
}

func (s *server) mustRedact(t *testing.T, host, token, roomID, eventID, txnID string) string {
	t.Helper()
	rec := s.redact(t, host, token, roomID, eventID, txnID, map[string]any{"reason": "spam"})
	if rec.Code != http.StatusOK {
		t.Fatalf("redact = %d: %s", rec.Code, rec.Body)
	}
	return decode[struct {
		EventID string `json:"event_id"`
	}](t, rec).EventID
}

func (s *server) storedJSON(t *testing.T, eventID string) []byte {
	t.Helper()
	var raw []byte
	err := s.db.QueryRowContext(t.Context(),
		`SELECT event_json FROM events WHERE event_id = $1`, eventID).Scan(&raw)
	if err != nil {
		t.Fatalf("read stored event: %v", err)
	}
	return raw
}

func (s *server) event(t *testing.T, host, token, roomID, eventID string) map[string]any {
	t.Helper()
	rec := s.get(t, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/event/"+url.PathEscape(eventID), token)
	if rec.Code != http.StatusOK {
		t.Fatalf("event = %d: %s", rec.Code, rec.Body)
	}
	return decode[map[string]any](t, rec)
}

func TestRedactingYourOwnEventRemovesTheContentFromTheStoredBytes(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	target := s.mustSend(t, "alpha.test", token, roomID, "target", text("something regrettable"))

	if !strings.Contains(string(s.storedJSON(t, target)), "something regrettable") {
		t.Fatal("the fixture did not store the body it claims to")
	}

	s.mustRedact(t, "alpha.test", token, roomID, target, "one")

	after := s.storedJSON(t, target)
	if strings.Contains(string(after), "something regrettable") {
		t.Fatalf("the redacted event still carries its body: %s", after)
	}

	rendered := s.event(t, "alpha.test", token, roomID, target)
	content, _ := rendered["content"].(map[string]any)
	if len(content) != 0 {
		t.Fatalf("a redacted event served content: %v", content)
	}
	if rendered["event_id"] != target {
		t.Fatalf("redaction changed the event id: %v", rendered["event_id"])
	}
}

func TestARedactedEventCarriesTheRedactionThatCausedIt(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	target := s.mustSend(t, "alpha.test", token, roomID, "target", text("hello"))

	redaction := s.mustRedact(t, "alpha.test", token, roomID, target, "one")

	rendered := s.event(t, "alpha.test", token, roomID, target)
	unsigned, _ := rendered["unsigned"].(map[string]any)
	because, _ := unsigned["redacted_because"].(map[string]any)
	if because["event_id"] != redaction {
		t.Fatalf("redacted_because names %v, want %s", because["event_id"], redaction)
	}

	served := s.event(t, "alpha.test", token, roomID, redaction)
	if served["redacts"] != target {
		t.Fatalf("the redaction does not carry a top-level redacts: %v", served)
	}
	content, _ := served["content"].(map[string]any)
	if content["redacts"] != target {
		t.Fatalf("the redaction does not carry content.redacts: %v", content)
	}
}

func TestAModeratorMayRedactSomeoneElsesEventAndAnOrdinaryMemberMayNot(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	carol := s.register(t, "alpha.test", "carol", goodPassword)
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{
		"preset":                       entity.PresetPublicChat,
		"power_level_content_override": map[string]any{"users": map[string]any{bob.UserID: 50}},
	})
	s.joinAs(t, tenant, roomID, bob.UserID)
	s.joinAs(t, tenant, roomID, carol.UserID)

	first := s.mustSend(t, "alpha.test", token, roomID, "first", text("one"))
	second := s.mustSend(t, "alpha.test", token, roomID, "second", text("two"))

	s.mustRedact(t, "alpha.test", bob.AccessToken, roomID, first, "by-moderator")

	rec := s.redact(t, "alpha.test", carol.AccessToken, roomID, second, "by-member", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a member redacting someone else = %d: %s", rec.Code, rec.Body)
	}
}

func TestARedactionOfAnUnknownOrForeignEventIsNotFound(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	other := s.createRoom(t, "alpha.test", token, map[string]any{})
	elsewhere := s.mustSend(t, "alpha.test", token, other, "elsewhere", text("elsewhere"))

	unknown := s.redact(t, "alpha.test", token, roomID, "$nope", "one", nil)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("redacting an unknown event = %d: %s", unknown.Code, unknown.Body)
	}
	foreign := s.redact(t, "alpha.test", token, roomID, elsewhere, "two", nil)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("redacting another room's event = %d: %s", foreign.Code, foreign.Body)
	}
}

func TestTheEncryptionOfARoomCannotBeRedactedAway(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	var encryption string
	err := s.db.QueryRowContext(t.Context(), `
		SELECT e.event_id FROM events e
		JOIN rooms r ON r.room_nid = e.room_nid
		JOIN event_types t ON t.event_type_nid = e.event_type_nid
		WHERE r.room_id = $1 AND t.event_type = $2`, roomID, entity.EventTypeEncryption).Scan(&encryption)
	if err != nil {
		t.Fatalf("find the encryption event: %v", err)
	}

	rec := s.redact(t, "alpha.test", token, roomID, encryption, "one", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("redacting the encryption event = %d: %s", rec.Code, rec.Body)
	}
}

func TestRedactingAMembershipEventLeavesTheUserJoined(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"preset": entity.PresetPublicChat})
	s.joinAs(t, tenant, roomID, bob.UserID)

	var join string
	err := s.db.QueryRowContext(t.Context(), `
		SELECT e.event_id FROM events e
		JOIN rooms r ON r.room_nid = e.room_nid
		JOIN event_types t ON t.event_type_nid = e.event_type_nid
		JOIN event_state_keys k ON k.event_state_key_nid = e.event_state_key_nid
		WHERE r.room_id = $1 AND t.event_type = $2 AND k.event_state_key = $3`,
		roomID, entity.EventTypeMember, bob.UserID).Scan(&join)
	if err != nil {
		t.Fatalf("find the join event: %v", err)
	}

	s.mustRedact(t, "alpha.test", bob.AccessToken, roomID, join, "one")

	content := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeMember, bob.UserID)
	if content["membership"] != entity.MembershipJoin {
		t.Fatalf("a redacted join no longer reads as a join: %v", content)
	}
	if _, leaked := content["displayname"]; leaked {
		t.Fatalf("a redacted join kept a field the algorithm strips: %v", content)
	}
}

func TestRedactingTwiceUnderOneTransactionIsIdempotent(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	target := s.mustSend(t, "alpha.test", token, roomID, "target", text("hello"))

	first := s.mustRedact(t, "alpha.test", token, roomID, target, "same")
	second := s.mustRedact(t, "alpha.test", token, roomID, target, "same")
	if first != second {
		t.Fatalf("a replayed redaction made a second event: %s then %s", first, second)
	}
}

func TestARedactedEventIsStillPagedWithEmptyContent(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	target := s.mustSend(t, "alpha.test", token, roomID, "target", text("hello"))
	s.mustRedact(t, "alpha.test", token, roomID, target, "one")

	swept := s.sweep(t, "alpha.test", token, roomID, "b")
	if !contains(swept, target) {
		t.Fatalf("a redacted event vanished from the timeline")
	}
}
