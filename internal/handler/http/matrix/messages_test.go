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

type page struct {
	Chunk []json.RawMessage `json:"chunk"`
	Start string            `json:"start"`
	End   *string           `json:"end"`
}

func (s *server) messages(t *testing.T, host, token, roomID, query string) *httptest.ResponseRecorder {
	t.Helper()
	return s.get(t, host, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/messages?"+query, token)
}

func (s *server) mustPage(t *testing.T, host, token, roomID, query string) page {
	t.Helper()
	rec := s.messages(t, host, token, roomID, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages?%s = %d: %s", query, rec.Code, rec.Body)
	}
	return decode[page](t, rec)
}

func eventIDs(t *testing.T, chunk []json.RawMessage) []string {
	t.Helper()
	out := make([]string, 0, len(chunk))
	for _, raw := range chunk {
		var e struct {
			EventID string `json:"event_id"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("decode chunk entry: %v", err)
		}
		if e.EventID == "" {
			t.Fatalf("a chunk entry carries no event_id: %s", raw)
		}
		out = append(out, e.EventID)
	}
	return out
}

func (s *server) sweep(t *testing.T, host, token, roomID, dir string) []string {
	t.Helper()
	var all []string
	from := ""
	for range 50 {
		query := "dir=" + dir + "&limit=3"
		if from != "" {
			query += "&from=" + url.QueryEscape(from)
		}
		got := s.mustPage(t, host, token, roomID, query)
		all = append(all, eventIDs(t, got.Chunk)...)
		if got.End == nil {
			return all
		}
		from = *got.End
	}
	t.Fatal("pagination did not terminate")
	return nil
}

func (s *server) chatter(t *testing.T, host, token, roomID string, count int) []string {
	t.Helper()
	sent := make([]string, 0, count)
	for i := range count {
		sent = append(sent, s.mustSend(t, host, token, roomID, "chatter-"+strconv.Itoa(i), text(strconv.Itoa(i))))
	}
	return sent
}

func TestPagingBackwardsReachesTheStartOfTheRoomAndStops(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	sent := s.chatter(t, "alpha.test", token, roomID, 7)

	swept := s.sweep(t, "alpha.test", token, roomID, "b")
	for _, id := range sent {
		if !contains(swept, id) {
			t.Fatalf("a backwards sweep missed %s", id)
		}
	}
	if got := unique(swept); len(got) != len(swept) {
		t.Fatalf("a backwards sweep repeated an event: %d entries, %d distinct", len(swept), len(got))
	}
}

func TestPagingForwardsReturnsEveryEventExactlyOnce(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	sent := s.chatter(t, "alpha.test", token, roomID, 7)

	forwards := s.sweep(t, "alpha.test", token, roomID, "f")
	if got := unique(forwards); len(got) != len(forwards) {
		t.Fatalf("a forwards sweep repeated an event: %d entries, %d distinct", len(forwards), len(got))
	}

	seen := 0
	for _, id := range forwards {
		if contains(sent, id) {
			if id != sent[seen] {
				t.Fatalf("forwards order broke at %d: got %s, want %s", seen, id, sent[seen])
			}
			seen++
		}
	}
	if seen != len(sent) {
		t.Fatalf("a forwards sweep saw %d of %d messages", seen, len(sent))
	}
}

func TestBothSweepsSeeTheSameRoom(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	s.chatter(t, "alpha.test", token, roomID, 5)

	backwards := unique(s.sweep(t, "alpha.test", token, roomID, "b"))
	forwards := unique(s.sweep(t, "alpha.test", token, roomID, "f"))

	if len(backwards) != len(forwards) {
		t.Fatalf("backwards saw %d events, forwards saw %d", len(backwards), len(forwards))
	}
	for _, id := range forwards {
		if !contains(backwards, id) {
			t.Fatalf("%s appears going forwards but not going backwards", id)
		}
	}
}

func TestTheLimitIsHonouredDefaultedAndCapped(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	s.chatter(t, "alpha.test", token, roomID, 20)

	if got := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=4"); len(got.Chunk) != 4 {
		t.Fatalf("limit=4 returned %d events", len(got.Chunk))
	}
	if got := s.mustPage(t, "alpha.test", token, roomID, "dir=b"); len(got.Chunk) != entity.DefaultPageLimit {
		t.Fatalf("an absent limit returned %d events, want %d", len(got.Chunk), entity.DefaultPageLimit)
	}
	if got := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=100000"); len(got.Chunk) == 0 {
		t.Fatal("an enormous limit returned nothing")
	}
	if got := s.mustPage(t, "alpha.test", token, roomID, `dir=b&filter={"limit":2}`); len(got.Chunk) != 2 {
		t.Fatalf("a filter limit returned %d events", len(got.Chunk))
	}
}

func TestEndIsOmittedOnceTheTimelineRunsOut(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	s.chatter(t, "alpha.test", token, roomID, 2)

	full := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=1000")
	if full.End != nil {
		t.Fatalf("end was present after reading the whole room: %q", *full.End)
	}
	if full.Start == "" {
		t.Fatal("start is required and was empty")
	}
	partial := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=2")
	if partial.End == nil {
		t.Fatal("end was omitted while more events remained")
	}
}

func TestAToBoundStopsBeforeTheTokenItNames(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	s.chatter(t, "alpha.test", token, roomID, 6)

	first := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=3")
	if first.End == nil {
		t.Fatal("no end token to bound with")
	}
	head := eventIDs(t, first.Chunk)

	bounded := s.mustPage(t, "alpha.test", token, roomID,
		"dir=b&limit=1000&to="+url.QueryEscape(*first.End))
	if got := eventIDs(t, bounded.Chunk); len(got) != len(head)-1 {
		t.Fatalf("a page bounded at %s returned %v, want the %d events above it", *first.End, got, len(head)-1)
	}
	for _, id := range eventIDs(t, bounded.Chunk) {
		if id == head[len(head)-1] {
			t.Fatal("a to bound returned the event it names")
		}
	}

	rest := s.mustPage(t, "alpha.test", token, roomID,
		"dir=b&limit=1000&from="+url.QueryEscape(*first.End))
	for _, id := range eventIDs(t, rest.Chunk) {
		if contains(head, id) {
			t.Fatalf("%s was returned on both sides of the token", id)
		}
	}

	forwards := s.mustPage(t, "alpha.test", token, roomID,
		"dir=f&limit=1000&to="+url.QueryEscape(first.Start))
	if len(forwards.Chunk) == 0 {
		t.Fatal("a forwards page bounded by the newest position returned nothing")
	}
	for _, id := range eventIDs(t, forwards.Chunk) {
		if id == head[0] {
			t.Fatal("a forwards to bound returned the event it names")
		}
	}
}

func TestAnUnparseableTokenIsRefused(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	for _, query := range []string{"dir=b&from=nonsense", "dir=b&to=nonsense", "dir=sideways", ""} {
		rec := s.messages(t, "alpha.test", token, roomID, query)
		if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_INVALID_PARAM" {
			t.Fatalf("messages?%s = %d %s", query, rec.Code, rec.Body)
		}
	}
	rec := s.messages(t, "alpha.test", token, roomID, `dir=b&filter={"senders":["nope"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed filter = %d: %s", rec.Code, rec.Body)
	}
}

func TestAStreamTokenPointsAtTheSamePlaceAsATopologicalOne(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	s.chatter(t, "alpha.test", token, roomID, 6)

	var stream int64
	err := s.db.QueryRowContext(t.Context(), `
		SELECT e.stream_ordering FROM events e
		JOIN rooms r ON r.room_nid = e.room_nid
		WHERE r.room_id = $1 ORDER BY e.stream_ordering DESC LIMIT 1 OFFSET 2`, roomID).Scan(&stream)
	if err != nil {
		t.Fatalf("read a stream position: %v", err)
	}

	byStream := s.mustPage(t, "alpha.test", token, roomID,
		"dir=b&limit=1000&from="+url.QueryEscape("s"+strconv.FormatInt(stream, 10)))
	byTopological := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=1000")

	if len(byStream.Chunk) == 0 {
		t.Fatal("a stream token returned nothing")
	}
	if len(byStream.Chunk) >= len(byTopological.Chunk) {
		t.Fatalf("a stream token did not bound the page: %d of %d", len(byStream.Chunk), len(byTopological.Chunk))
	}
}

func TestATokenStillResolvesAfterARestart(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	s.chatter(t, "alpha.test", token, roomID, 6)

	first := s.mustPage(t, "alpha.test", token, roomID, "dir=b&limit=2")
	if first.End == nil {
		t.Fatal("no end token to carry across the restart")
	}

	restarted := reopen(t, s)
	after := restarted.mustPage(t, "alpha.test", token, roomID,
		"dir=b&limit=2&from="+url.QueryEscape(*first.End))
	if len(after.Chunk) != 2 {
		t.Fatalf("a token issued before the restart returned %d events", len(after.Chunk))
	}
	for _, id := range eventIDs(t, after.Chunk) {
		if contains(eventIDs(t, first.Chunk), id) {
			t.Fatalf("%s was returned on both sides of the restart", id)
		}
	}
}

func TestAStrangerCannotReadARoom(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	s.chatter(t, "alpha.test", token, roomID, 2)

	stranger := s.register(t, "alpha.test", "mallory", goodPassword)
	if rec := s.messages(t, "alpha.test", stranger.AccessToken, roomID, "dir=b"); rec.Code != http.StatusForbidden {
		t.Fatalf("a stranger read the timeline = %d: %s", rec.Code, rec.Body)
	}
}

func TestAnUnknownRoomIsRefusedTheSameWay(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	rec := s.messages(t, "alpha.test", token, "!does-not-exist:alpha.test", "dir=b")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an unknown room = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func contains(haystack []string, needle string) bool {
	for _, each := range haystack {
		if each == needle {
			return true
		}
	}
	return false
}

func unique(all []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(all))
	for _, each := range all {
		if !seen[each] {
			seen[each] = true
			out = append(out, each)
		}
	}
	return out
}
