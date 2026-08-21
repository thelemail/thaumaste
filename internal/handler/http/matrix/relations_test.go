package matrix_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

func TestRedactingAChildBreaksTheRelation(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	reply := s.mustSend(t, "alpha.test", token, roomID, "reply", threaded(root, "in the thread"))
	if got := s.relationCount(t, roomID, entity.RelThread); got != 1 {
		t.Fatalf("thread relations before the redaction = %d, want 1", got)
	}

	s.mustRedact(t, "alpha.test", token, roomID, reply, "one")

	if got := s.relationCount(t, roomID, entity.RelThread); got != 0 {
		t.Fatalf("a redacted child left %d relations behind", got)
	}
}

func TestRedactingAnAnnotationLetsTheSameOneBeSentAgain(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))

	first := s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "first", annotation(root, "👍"))
	s.mustRedact(t, "alpha.test", token, roomID, first, "one")

	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "again", annotation(root, "👍"))
}

func TestRedactingAParentLeavesItsChildrenInPlace(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	s.mustSend(t, "alpha.test", token, roomID, "reply", threaded(root, "in the thread"))

	s.mustRedact(t, "alpha.test", token, roomID, root, "one")

	if got := s.relationCount(t, roomID, entity.RelThread); got != 1 {
		t.Fatalf("redacting the parent removed %d child relations", 1-got)
	}
}

func relations(t *testing.T, event map[string]any) map[string]any {
	t.Helper()
	unsigned, _ := event["unsigned"].(map[string]any)
	out, _ := unsigned["m.relations"].(map[string]any)
	return out
}

func TestTheMostRecentValidEditIsBundledAndTheOriginalIsLeftAlone(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	original := s.mustSend(t, "alpha.test", token, roomID, "original", text("I really like cake"))
	s.mustSend(t, "alpha.test", token, roomID, "first-edit", edit(original, "I really like carrot cake"))
	latest := s.mustSend(t, "alpha.test", token, roomID, "second-edit", edit(original, "I really like chocolate cake"))

	served := s.event(t, "alpha.test", token, roomID, original)
	content, _ := served["content"].(map[string]any)
	if content["body"] != "I really like cake" {
		t.Fatalf("the server rewrote the original content: %v", content)
	}

	replace, _ := relations(t, served)["m.replace"].(map[string]any)
	if replace["event_id"] != latest {
		t.Fatalf("m.replace bundles %v, want the latest edit %s", replace["event_id"], latest)
	}
	replaceContent, _ := replace["content"].(map[string]any)
	newContent, _ := replaceContent["m.new_content"].(map[string]any)
	if newContent["body"] != "I really like chocolate cake" {
		t.Fatalf("the bundled edit is not the latest: %v", replaceContent)
	}
}

func TestAnEditThatBreaksTheRulesIsIgnored(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"preset": entity.PresetPublicChat})
	s.joinAs(t, tenant, roomID, bob.UserID)

	original := s.mustSend(t, "alpha.test", token, roomID, "original", text("mine"))

	s.mustSend(t, "alpha.test", bob.AccessToken, roomID, "other-sender", edit(original, "not yours to edit"))

	wrongType := text("* different type")
	wrongType["m.new_content"] = text("different type")
	wrongType["m.relates_to"] = relatesTo(entity.RelReplace, original)
	s.mustSendType(t, "alpha.test", token, roomID, "m.note", "other-type", wrongType)

	missing := text("* no new content")
	missing["m.relates_to"] = relatesTo(entity.RelReplace, original)
	s.mustSend(t, "alpha.test", token, roomID, "no-new-content", missing)

	served := s.event(t, "alpha.test", token, roomID, original)
	if got := relations(t, served)["m.replace"]; got != nil {
		t.Fatalf("an invalid edit was bundled: %v", got)
	}
}

func TestAnEditOfAnEditIsIgnored(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	original := s.mustSend(t, "alpha.test", token, roomID, "original", text("first"))
	first := s.mustSend(t, "alpha.test", token, roomID, "first-edit", edit(original, "second"))
	s.mustSend(t, "alpha.test", token, roomID, "edit-of-edit", edit(first, "third"))

	served := s.event(t, "alpha.test", token, roomID, first)
	if got := relations(t, served)["m.replace"]; got != nil {
		t.Fatalf("an edit of an edit was bundled: %v", got)
	}
}

func TestRedactingTheOriginalDropsTheEditBundleAndRedactingTheLatestEditReverts(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	original := s.mustSend(t, "alpha.test", token, roomID, "original", text("first"))
	earlier := s.mustSend(t, "alpha.test", token, roomID, "first-edit", edit(original, "second"))
	latest := s.mustSend(t, "alpha.test", token, roomID, "second-edit", edit(original, "third"))

	s.mustRedact(t, "alpha.test", token, roomID, latest, "one")
	replace, _ := relations(t, s.event(t, "alpha.test", token, roomID, original))["m.replace"].(map[string]any)
	if replace["event_id"] != earlier {
		t.Fatalf("redacting the latest edit did not revert to %s: %v", earlier, replace)
	}

	s.mustRedact(t, "alpha.test", token, roomID, original, "two")
	if got := relations(t, s.event(t, "alpha.test", token, roomID, original))["m.replace"]; got != nil {
		t.Fatalf("a redacted original still bundles an edit: %v", got)
	}
}

func TestTheThreadBundleCountsRepliesAndNamesTheLatest(t *testing.T) {
	s := newServer(t)
	tenant, token, alice := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"preset": entity.PresetPublicChat})
	s.joinAs(t, tenant, roomID, bob.UserID)

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	s.mustSend(t, "alpha.test", bob.AccessToken, roomID, "first", threaded(root, "one"))
	last := s.mustSend(t, "alpha.test", bob.AccessToken, roomID, "second", threaded(root, "two"))

	thread, _ := relations(t, s.event(t, "alpha.test", token, roomID, root))["m.thread"].(map[string]any)
	if thread["count"] != float64(2) {
		t.Fatalf("thread count = %v, want 2", thread["count"])
	}
	latest, _ := thread["latest_event"].(map[string]any)
	if latest["event_id"] != last {
		t.Fatalf("latest_event = %v, want %s", latest["event_id"], last)
	}
	if thread["current_user_participated"] != true {
		t.Fatalf("the root's own sender %s does not count as a participant", alice)
	}

	fromBob, _ := relations(t, s.event(t, "alpha.test", bob.AccessToken, roomID, root))["m.thread"].(map[string]any)
	if fromBob["current_user_participated"] != true {
		t.Fatal("a user who replied in the thread does not count as a participant")
	}
}

func TestTheLatestThreadEventCarriesItsOwnBundle(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	reply := s.mustSend(t, "alpha.test", token, roomID, "reply", threaded(root, "one"))
	editOfReply := s.mustSend(t, "alpha.test", token, roomID, "edit", edit(reply, "one, corrected"))

	thread, _ := relations(t, s.event(t, "alpha.test", token, roomID, root))["m.thread"].(map[string]any)
	latest, _ := thread["latest_event"].(map[string]any)
	if latest["event_id"] != reply {
		t.Fatalf("latest_event = %v, want %s", latest["event_id"], reply)
	}
	nested, _ := relations(t, latest)["m.replace"].(map[string]any)
	if nested["event_id"] != editOfReply {
		t.Fatalf("latest_event carries no bundle of its own: %v", latest)
	}
}

func TestReferencesAreListedAndAnnotationsAreNot(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	referring := s.mustSendType(t, "alpha.test", token, roomID, "m.key.verification.start", "ref",
		map[string]any{"m.relates_to": relatesTo(entity.RelReference, root)})
	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "react", annotation(root, "👍"))

	bundled := relations(t, s.event(t, "alpha.test", token, roomID, root))
	if got := bundled["m.annotation"]; got != nil {
		t.Fatalf("annotations were aggregated by the server: %v", got)
	}
	reference, _ := bundled["m.reference"].(map[string]any)
	chunk, _ := reference["chunk"].([]any)
	if len(chunk) != 1 {
		t.Fatalf("m.reference chunk = %v, want one entry", chunk)
	}
	first, _ := chunk[0].(map[string]any)
	if first["event_id"] != referring {
		t.Fatalf("m.reference names %v, want %s", first["event_id"], referring)
	}
}

func TestStateEventsCarryNoBundle(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"name": "named"})

	var name string
	err := s.db.QueryRowContext(t.Context(), `
		SELECT e.event_id FROM events e
		JOIN rooms r ON r.room_nid = e.room_nid
		JOIN event_types t ON t.event_type_nid = e.event_type_nid
		WHERE r.room_id = $1 AND t.event_type = $2`, roomID, entity.EventTypeName).Scan(&name)
	if err != nil {
		t.Fatalf("find the name event: %v", err)
	}
	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "react", annotation(name, "👍"))
	s.mustSendType(t, "alpha.test", token, roomID, "m.key.verification.start", "ref",
		map[string]any{"m.relates_to": relatesTo(entity.RelReference, name)})

	if got := relations(t, s.event(t, "alpha.test", token, roomID, name)); got != nil {
		t.Fatalf("a state event carries a bundle: %v", got)
	}
}

func TestMessagesContextAndEventAgreeOnTheBundle(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	reply := s.mustSend(t, "alpha.test", token, roomID, "reply", threaded(root, "one"))

	fromEvent := relations(t, s.event(t, "alpha.test", token, roomID, root))

	page := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=50")
	fromMessages := findBundle(t, page.Chunk, root)

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/context/"+url.PathEscape(root)+"?limit=4", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("context = %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Event map[string]any `json:"event"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	fromContext := relations(t, got.Event)

	for name, bundle := range map[string]map[string]any{"messages": fromMessages, "context": fromContext} {
		thread, _ := bundle["m.thread"].(map[string]any)
		latest, _ := thread["latest_event"].(map[string]any)
		if latest["event_id"] != reply {
			t.Fatalf("%s disagrees with /event: %v", name, bundle)
		}
	}
	if thread, _ := fromEvent["m.thread"].(map[string]any); thread["count"] != float64(1) {
		t.Fatalf("/event bundle = %v", fromEvent)
	}
}

func findBundle(t *testing.T, chunk []json.RawMessage, eventID string) map[string]any {
	t.Helper()
	for _, raw := range chunk {
		var event map[string]any
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatalf("decode chunk entry: %v", err)
		}
		if event["event_id"] == eventID {
			return relations(t, event)
		}
	}
	t.Fatalf("%s is not in the chunk", eventID)
	return nil
}

func TestABundleHidesChildrenTheCallerMayNotSee(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityWorldReadable)

	root := s.mustSend(t, "alpha.test", alice, roomID, "root", text("root"))
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityJoined)

	s.mustSend(t, "alpha.test", alice, roomID, "hidden-reply", threaded(root, "before bob"))
	s.mustSend(t, "alpha.test", alice, roomID, "hidden-edit", edit(root, "before bob"))
	s.mustSendType(t, "alpha.test", alice, roomID, "m.key.verification.start", "hidden-ref",
		map[string]any{"m.relates_to": relatesTo(entity.RelReference, root)})

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)

	bundled := relations(t, s.event(t, "alpha.test", bob.AccessToken, roomID, root))
	if got := bundled["m.replace"]; got != nil {
		t.Fatalf("an edit bob may not read was bundled for him: %v", got)
	}
	if got := bundled["m.reference"]; got != nil {
		t.Fatalf("a reference bob may not read was listed for him: %v", got)
	}
	if got := bundled["m.thread"]; got != nil {
		t.Fatalf("a thread reply bob may not read was counted for him: %v", got)
	}

	visibleReply := s.mustSend(t, "alpha.test", alice, roomID, "visible-reply", threaded(root, "after bob"))
	visibleEdit := s.mustSend(t, "alpha.test", alice, roomID, "visible-edit", edit(root, "after bob"))

	bundled = relations(t, s.event(t, "alpha.test", bob.AccessToken, roomID, root))
	thread, _ := bundled["m.thread"].(map[string]any)
	if thread["count"] != float64(1) {
		t.Fatalf("thread count for bob = %v, want only the reply he may read", thread["count"])
	}
	latest, _ := thread["latest_event"].(map[string]any)
	if latest["event_id"] != visibleReply {
		t.Fatalf("latest_event for bob = %v, want %s", latest["event_id"], visibleReply)
	}
	replace, _ := bundled["m.replace"].(map[string]any)
	if replace["event_id"] != visibleEdit {
		t.Fatalf("m.replace for bob = %v, want %s", replace["event_id"], visibleEdit)
	}

	forAlice, _ := relations(t, s.event(t, "alpha.test", alice, roomID, root))["m.thread"].(map[string]any)
	if forAlice["count"] != float64(2) {
		t.Fatalf("alice sees a thread count of %v, want 2", forAlice["count"])
	}
}

func TestARedactionTheCallerMayNotSeeIsNotAttributed(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityWorldReadable)

	target := s.mustSend(t, "alpha.test", alice, roomID, "target", text("regrettable"))
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityJoined)
	redaction := s.mustRedact(t, "alpha.test", alice, roomID, target, "one")

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)

	served := s.event(t, "alpha.test", bob.AccessToken, roomID, target)
	unsigned, _ := served["unsigned"].(map[string]any)
	if got := unsigned["redacted_because"]; got != nil {
		t.Fatalf("bob was told who redacted an event in history he cannot read: %v", got)
	}

	forAlice := s.event(t, "alpha.test", alice, roomID, target)
	aliceUnsigned, _ := forAlice["unsigned"].(map[string]any)
	because, _ := aliceUnsigned["redacted_because"].(map[string]any)
	if because["event_id"] != redaction {
		t.Fatalf("alice lost the attribution: %v", aliceUnsigned)
	}
}

func TestBundlingAPageCostsTheSameWhateverItsSize(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	for i := range 20 {
		root := s.mustSend(t, "alpha.test", token, roomID, "root-"+strconv.Itoa(i), text("root"))
		s.mustSend(t, "alpha.test", token, roomID, "reply-"+strconv.Itoa(i), threaded(root, "one"))
		s.mustSend(t, "alpha.test", token, roomID, "edit-"+strconv.Itoa(i), edit(root, "two"))
	}

	small := s.countQueries(t, func() { s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=3") })
	large := s.countQueries(t, func() { s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=60") })

	if small != large {
		t.Fatalf("a page of 3 cost %d relation lookups and a page of 60 cost %d", small, large)
	}
	if large == 0 {
		t.Fatal("the counter never fired, so this proves nothing")
	}
}
