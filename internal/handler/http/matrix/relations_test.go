package matrix_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) sendType(t *testing.T, host, token, roomID, eventType, txnID string, content map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/send/"+url.PathEscape(eventType)+"/"+url.PathEscape(txnID),
		token, content)
}

func (s *server) mustSendType(t *testing.T, host, token, roomID, eventType, txnID string, content map[string]any) string {
	t.Helper()
	rec := s.sendType(t, host, token, roomID, eventType, txnID, content)
	if rec.Code != http.StatusOK {
		t.Fatalf("send %s = %d: %s", eventType, rec.Code, rec.Body)
	}
	return decode[struct {
		EventID string `json:"event_id"`
	}](t, rec).EventID
}

func relatesTo(relType, parentID string) map[string]any {
	return map[string]any{"rel_type": relType, "event_id": parentID}
}

func annotation(parentID, key string) map[string]any {
	return map[string]any{"m.relates_to": map[string]any{
		"rel_type": entity.RelAnnotation, "event_id": parentID, "key": key,
	}}
}

func threaded(rootID, body string) map[string]any {
	content := text(body)
	content["m.relates_to"] = relatesTo(entity.RelThread, rootID)
	return content
}

func edit(originalID, body string) map[string]any {
	content := text("* " + body)
	content["m.new_content"] = text(body)
	content["m.relates_to"] = relatesTo(entity.RelReplace, originalID)
	return content
}

func (s *server) relationCount(t *testing.T, roomID, relType string) int {
	t.Helper()
	var count int
	err := s.db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM event_relations er
		JOIN rooms r ON r.room_nid = er.room_nid
		WHERE r.room_id = $1 AND er.rel_type = $2`, roomID, relType).Scan(&count)
	if err != nil {
		t.Fatalf("count relations: %v", err)
	}
	return count
}

func TestARelatedEventIsRecordedAsARelation(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	s.mustSend(t, "alpha.test", token, roomID, "reply", threaded(root, "in the thread"))
	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "react", annotation(root, "👍"))
	s.mustSend(t, "alpha.test", token, roomID, "edited", edit(root, "root, corrected"))

	for relType, want := range map[string]int{
		entity.RelThread:     1,
		entity.RelAnnotation: 1,
		entity.RelReplace:    1,
	} {
		if got := s.relationCount(t, roomID, relType); got != want {
			t.Fatalf("%s relations = %d, want %d", relType, got, want)
		}
	}
}

func TestAnUnrelatedEventIsNotRecordedAsARelation(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	reply := text("a plain rich reply")
	reply["m.relates_to"] = map[string]any{"m.in_reply_to": map[string]any{"event_id": root}}
	s.mustSend(t, "alpha.test", token, roomID, "reply", reply)

	var count int
	err := s.db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM event_relations er
		JOIN rooms r ON r.room_nid = er.room_nid WHERE r.room_id = $1`, roomID).Scan(&count)
	if err != nil {
		t.Fatalf("count relations: %v", err)
	}
	if count != 0 {
		t.Fatalf("a rich reply was recorded as %d relations, and it has no rel_type", count)
	}
}

func TestASecondIdenticalAnnotationIsRefused(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))

	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "first", annotation(root, "👍"))
	rec := s.sendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "second", annotation(root, "👍"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a duplicate annotation = %d: %s", rec.Code, rec.Body)
	}
	if got := errcode(t, rec); got != "M_DUPLICATE_ANNOTATION" {
		t.Fatalf("a duplicate annotation answered %s", got)
	}
}

func TestAnAnnotationThatDiffersInAnyWayIsNotADuplicate(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	other := s.register(t, "alpha.test", "bob", goodPassword)
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"preset": entity.PresetPublicChat})
	s.joinAs(t, tenant, roomID, other.UserID)

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "first", annotation(root, "👍"))

	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "other-key", annotation(root, "🎉"))
	s.mustSendType(t, "alpha.test", other.AccessToken, roomID, entity.EventTypeReaction, "other-sender", annotation(root, "👍"))
	s.mustSendType(t, "alpha.test", token, roomID, "m.vote", "other-type", annotation(root, "👍"))

	if got := s.relationCount(t, roomID, entity.RelAnnotation); got != 4 {
		t.Fatalf("annotations recorded = %d, want 4", got)
	}
}

func TestAThreadCannotStartFromAnEventThatIsItselfRelated(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	reply := s.mustSend(t, "alpha.test", token, roomID, "reply", threaded(root, "in the thread"))

	rec := s.send(t, "alpha.test", token, roomID, "nested", threaded(reply, "a thread off a thread"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a nested thread = %d: %s", rec.Code, rec.Body)
	}
	if got := errcode(t, rec); got != "M_UNKNOWN" {
		t.Fatalf("a nested thread answered %s", got)
	}
}
