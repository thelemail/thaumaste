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

type relationPage struct {
	Chunk     []json.RawMessage `json:"chunk"`
	NextBatch string            `json:"next_batch"`
	PrevBatch string            `json:"prev_batch"`
	Depth     *int              `json:"recursion_depth"`
}

func (s *server) relationsAt(t *testing.T, host, token, roomID, eventID, path, query string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/_matrix/client/v1/rooms/" + url.PathEscape(roomID) + "/relations/" + url.PathEscape(eventID) + path
	if query != "" {
		url += "?" + query
	}
	return s.get(t, host, url, token)
}

func (s *server) mustRelations(t *testing.T, host, token, roomID, eventID, path, query string) relationPage {
	t.Helper()
	rec := s.relationsAt(t, host, token, roomID, eventID, path, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("relations%s?%s = %d: %s", path, query, rec.Code, rec.Body)
	}
	return decode[relationPage](t, rec)
}

func (s *server) threads(t *testing.T, host, token, roomID, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/_matrix/client/v1/rooms/" + url.PathEscape(roomID) + "/threads"
	if query != "" {
		path += "?" + query
	}
	return s.get(t, host, path, token)
}

func (s *server) mustThreads(t *testing.T, host, token, roomID, query string) relationPage {
	t.Helper()
	rec := s.threads(t, host, token, roomID, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("threads?%s = %d: %s", query, rec.Code, rec.Body)
	}
	return decode[relationPage](t, rec)
}

func TestRelationsReturnsTheChildrenMostRecentFirst(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	first := s.mustSend(t, "alpha.test", token, roomID, "one", threaded(root, "one"))
	second := s.mustSend(t, "alpha.test", token, roomID, "two", threaded(root, "two"))
	reaction := s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "react", annotation(root, "👍"))

	all := eventIDs(t, s.mustRelations(t, "alpha.test", token, roomID, root, "", "").Chunk)
	if len(all) != 3 || all[0] != reaction || all[1] != second || all[2] != first {
		t.Fatalf("relations returned %v, want most recent first", all)
	}

	forward := eventIDs(t, s.mustRelations(t, "alpha.test", token, roomID, root, "", "dir=f").Chunk)
	if len(forward) != 3 || forward[0] != first {
		t.Fatalf("forward relations returned %v", forward)
	}
}

func TestRelationsFiltersByRelationTypeAndEventType(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	reply := s.mustSend(t, "alpha.test", token, roomID, "one", threaded(root, "one"))
	s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "react", annotation(root, "👍"))
	noted := text("a note in the thread")
	noted["m.relates_to"] = relatesTo(entity.RelThread, root)
	note := s.mustSendType(t, "alpha.test", token, roomID, "m.note", "note", noted)

	byRel := eventIDs(t, s.mustRelations(t, "alpha.test", token, roomID, root, "/m.thread", "").Chunk)
	if len(byRel) != 2 || !contains(byRel, reply) || !contains(byRel, note) {
		t.Fatalf("filtering by rel_type returned %v", byRel)
	}

	byType := eventIDs(t, s.mustRelations(t, "alpha.test", token, roomID, root, "/m.thread/m.note", "").Chunk)
	if len(byType) != 1 || byType[0] != note {
		t.Fatalf("filtering by event type returned %v", byType)
	}
}

func TestRelationsPaginatesAndStopsWhenThereIsNoMore(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))

	sent := make([]string, 0, 7)
	for i := range 7 {
		sent = append(sent, s.mustSend(t, "alpha.test", token, roomID, "reply-"+strconv.Itoa(i),
			threaded(root, strconv.Itoa(i))))
	}

	var seen []string
	from := ""
	for range 20 {
		query := "limit=2"
		if from != "" {
			query += "&from=" + url.QueryEscape(from)
		}
		got := s.mustRelations(t, "alpha.test", token, roomID, root, "", query)
		seen = append(seen, eventIDs(t, got.Chunk)...)
		if got.NextBatch == "" {
			break
		}
		from = got.NextBatch
	}

	if len(unique(seen)) != len(sent) {
		t.Fatalf("paging saw %d distinct children, want %d", len(unique(seen)), len(sent))
	}
	for _, id := range sent {
		if !contains(seen, id) {
			t.Fatalf("paging missed %s", id)
		}
	}
}

func TestRelationsAcceptsASyncShapedToken(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	for i := range 3 {
		s.mustSend(t, "alpha.test", token, roomID, "reply-"+strconv.Itoa(i), threaded(root, strconv.Itoa(i)))
	}

	var stream int64
	err := s.db.QueryRowContext(t.Context(),
		`SELECT stream_ordering FROM events WHERE event_id = $1`, root).Scan(&stream)
	if err != nil {
		t.Fatalf("read the root position: %v", err)
	}

	got := s.mustRelations(t, "alpha.test", token, roomID, root, "",
		"dir=f&from="+url.QueryEscape("s"+strconv.FormatInt(stream, 10)))
	if len(got.Chunk) != 3 {
		t.Fatalf("a sync-shaped token returned %d children, want 3", len(got.Chunk))
	}
}

func TestRelationsRecursesAndReportsHowFar(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))
	reply := s.mustSend(t, "alpha.test", token, roomID, "reply", threaded(root, "one"))
	editOfReply := s.mustSend(t, "alpha.test", token, roomID, "edit", edit(reply, "one, corrected"))
	reactionToEdit := s.mustSendType(t, "alpha.test", token, roomID, entity.EventTypeReaction, "react",
		annotation(editOfReply, "👍"))

	shallow := eventIDs(t, s.mustRelations(t, "alpha.test", token, roomID, root, "", "limit=50").Chunk)
	if len(shallow) != 1 || shallow[0] != reply {
		t.Fatalf("a direct query returned %v, want only the direct child", shallow)
	}

	deep := s.mustRelations(t, "alpha.test", token, roomID, root, "", "recurse=true&limit=50")
	ids := eventIDs(t, deep.Chunk)
	for _, want := range []string{reply, editOfReply, reactionToEdit} {
		if !contains(ids, want) {
			t.Fatalf("recursion missed %s: %v", want, ids)
		}
	}
	if deep.Depth == nil || *deep.Depth != entity.RecursionDepth {
		t.Fatalf("recursion_depth = %v, want %d", deep.Depth, entity.RecursionDepth)
	}
	if shallowDepth := s.mustRelations(t, "alpha.test", token, roomID, root, "", "limit=50").Depth; shallowDepth != nil {
		t.Fatalf("recursion_depth was reported without recurse: %v", shallowDepth)
	}
}

func TestRecursionDoesNotReachThroughAnEventTheCallerCannotSee(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityWorldReadable)

	root := s.mustSend(t, "alpha.test", alice, roomID, "root", text("root"))
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityJoined)
	hidden := s.mustSend(t, "alpha.test", alice, roomID, "hidden", threaded(root, "hidden"))
	grandchild := s.mustSend(t, "alpha.test", alice, roomID, "grandchild", edit(hidden, "still hidden"))

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)

	ids := eventIDs(t, s.mustRelations(t, "alpha.test", bob.AccessToken, roomID, root, "", "recurse=true&limit=50").Chunk)
	if contains(ids, hidden) {
		t.Fatalf("recursion returned an event bob cannot see: %v", ids)
	}
	if contains(ids, grandchild) {
		t.Fatalf("recursion reached a grandchild through an event bob cannot see: %v", ids)
	}

	forAlice := eventIDs(t, s.mustRelations(t, "alpha.test", alice, roomID, root, "", "recurse=true&limit=50").Chunk)
	if !contains(forAlice, grandchild) {
		t.Fatalf("alice cannot reach the grandchild either, so this proves nothing: %v", forAlice)
	}
}

func TestRelationsRefusesAParentTheCallerCannotRead(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	other := s.register(t, "alpha.test", "bob", goodPassword)
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	root := s.mustSend(t, "alpha.test", token, roomID, "root", text("root"))

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a stranger":       s.relationsAt(t, "alpha.test", other.AccessToken, roomID, root, "", ""),
		"an unknown room":  s.relationsAt(t, "alpha.test", token, "!nope:alpha.test", root, "", ""),
		"an unknown event": s.relationsAt(t, "alpha.test", token, roomID, "$nope", "", ""),
	} {
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d: %s", name, rec.Code, rec.Body)
		}
		if got := errcode(t, rec); got != "M_NOT_FOUND" {
			t.Fatalf("%s answered %s", name, got)
		}
	}
}

func TestThreadsListsRootsByLatestActivity(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	older := s.mustSend(t, "alpha.test", token, roomID, "older", text("older root"))
	newer := s.mustSend(t, "alpha.test", token, roomID, "newer", text("newer root"))
	s.mustSend(t, "alpha.test", token, roomID, "newer-reply", threaded(newer, "one"))
	s.mustSend(t, "alpha.test", token, roomID, "older-reply", threaded(older, "one"))

	roots := eventIDs(t, s.mustThreads(t, "alpha.test", token, roomID, "").Chunk)
	if len(roots) != 2 || roots[0] != older || roots[1] != newer {
		t.Fatalf("threads returned %v, want the most recently active first", roots)
	}

	bundled := relations(t, decodeEvent(t, s.mustThreads(t, "alpha.test", token, roomID, "").Chunk[0]))
	if bundled["m.thread"] == nil {
		t.Fatalf("a thread root came back without its bundle: %v", bundled)
	}
}

func TestThreadsCanBeNarrowedToOnesTheCallerJoinedIn(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.joinAs(t, tenant, roomID, bob.UserID)

	aliceRoot := s.mustSend(t, "alpha.test", alice, roomID, "alice-root", text("alice"))
	s.mustSend(t, "alpha.test", alice, roomID, "alice-reply", threaded(aliceRoot, "one"))

	bobRoot := s.mustSend(t, "alpha.test", bob.AccessToken, roomID, "bob-root", text("bob"))
	s.mustSend(t, "alpha.test", alice, roomID, "bob-reply", threaded(bobRoot, "one"))

	all := eventIDs(t, s.mustThreads(t, "alpha.test", bob.AccessToken, roomID, "").Chunk)
	if len(all) != 2 {
		t.Fatalf("threads returned %v, want both", all)
	}

	mine := eventIDs(t, s.mustThreads(t, "alpha.test", bob.AccessToken, roomID, "include=participated").Chunk)
	if len(mine) != 1 || mine[0] != bobRoot {
		t.Fatalf("participated threads returned %v, want only %s", mine, bobRoot)
	}
}

func TestThreadsPaginates(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	roots := make([]string, 0, 5)
	for i := range 5 {
		root := s.mustSend(t, "alpha.test", token, roomID, "root-"+strconv.Itoa(i), text("root"))
		s.mustSend(t, "alpha.test", token, roomID, "reply-"+strconv.Itoa(i), threaded(root, "one"))
		roots = append(roots, root)
	}

	var seen []string
	from := ""
	for range 20 {
		query := "limit=2"
		if from != "" {
			query += "&from=" + url.QueryEscape(from)
		}
		got := s.mustThreads(t, "alpha.test", token, roomID, query)
		seen = append(seen, eventIDs(t, got.Chunk)...)
		if got.NextBatch == "" {
			break
		}
		from = got.NextBatch
	}

	if len(unique(seen)) != len(roots) {
		t.Fatalf("paging saw %v, want %v", unique(seen), roots)
	}
}

func TestThreadsRefusesARoomTheCallerCannotView(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	other := s.register(t, "alpha.test", "bob", goodPassword)
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	rec := s.threads(t, "alpha.test", other.AccessToken, roomID, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("threads for a stranger = %d: %s", rec.Code, rec.Body)
	}
}

func decodeEvent(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	return out
}
