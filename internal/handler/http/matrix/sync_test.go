package matrix_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
	"github.com/thelemail/thaumaste/internal/testutil/valkeytest"
)

const syncPath = "/_matrix/client/unstable/org.matrix.simplified_msc3575/sync"

type syncBody struct {
	Pos        string                     `json:"pos"`
	Lists      map[string]syncListBody    `json:"lists"`
	Rooms      map[string]syncRoomBody    `json:"rooms"`
	Extensions map[string]json.RawMessage `json:"extensions"`
}

type syncListBody struct {
	Count int `json:"count"`
}

type syncRoomBody struct {
	Membership       string            `json:"membership"`
	BumpStamp        int64             `json:"bump_stamp"`
	Lists            []string          `json:"lists"`
	Initial          bool              `json:"initial"`
	Name             *string           `json:"name"`
	Avatar           *string           `json:"avatar"`
	Heroes           []heroBody        `json:"heroes"`
	JoinedCount      *int              `json:"joined_count"`
	InvitedCount     *int              `json:"invited_count"`
	RequiredState    []json.RawMessage `json:"required_state"`
	Timeline         []json.RawMessage `json:"timeline"`
	StrippedState    []json.RawMessage `json:"stripped_state"`
	PrevBatch        string            `json:"prev_batch"`
	Limited          bool              `json:"limited"`
	NumLive          int               `json:"num_live"`
	ExpandedTimeline bool              `json:"expanded_timeline"`
}

type heroBody struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"displayname"`
	AvatarURL   string `json:"avatar_url"`
}

func window(limit int, start, end int) map[string]any {
	return map[string]any{
		"lists": map[string]any{
			"all": map[string]any{
				"ranges":         nil,
				"range":          []int{start, end},
				"timeline_limit": limit,
				"required_state": map[string]any{
					"include": []map[string]any{{"type": entity.EventTypeCreate, "state_key": ""}},
				},
			},
		},
	}
}

func (s *server) syncOnce(t *testing.T, host, token, pos string, request map[string]any) syncBody {
	t.Helper()
	rec := s.syncRaw(t, host, token, pos, request)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync = %d: %s", rec.Code, rec.Body)
	}
	return decode[syncBody](t, rec)
}

func (s *server) syncRaw(t *testing.T, host, token, pos string, request map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"conn_id": "test"}
	for key, value := range request {
		body[key] = value
	}
	if pos != "" {
		body["pos"] = pos
	}
	return s.do(t, http.MethodPost, host, syncPath, token, body)
}

func timelineBodies(t *testing.T, raw []json.RawMessage) []string {
	t.Helper()
	out := make([]string, 0, len(raw))
	for _, event := range raw {
		var parsed struct {
			Type    string `json:"type"`
			Content struct {
				Body string `json:"body"`
			} `json:"content"`
		}
		if err := json.Unmarshal(event, &parsed); err != nil {
			t.Fatalf("decode timeline event: %v", err)
		}
		if parsed.Type == entity.EventTypeMessage {
			out = append(out, parsed.Content.Body)
		}
	}
	return out
}

func TestAnInitialSyncCarriesEveryJoinedRoomAndAUsablePos(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	first := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	second := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "two"})
	s.mustSend(t, tenant.ServerName, alice.AccessToken, first, "1", text("hello"))

	body := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 99))
	if body.Pos == "" {
		t.Fatal("an initial sync returned no pos")
	}
	if body.Lists["all"].Count != 2 {
		t.Fatalf("list count = %d, want 2", body.Lists["all"].Count)
	}
	for _, roomID := range []string{first, second} {
		room, ok := body.Rooms[roomID]
		if !ok {
			t.Fatalf("%s is missing from an initial sync", roomID)
		}
		if !room.Initial {
			t.Fatalf("%s was not marked initial on the first response", roomID)
		}
		if room.Membership != entity.MembershipJoin {
			t.Fatalf("%s membership = %q, want join", roomID, room.Membership)
		}
	}
	if got := timelineBodies(t, body.Rooms[first].Timeline); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("timeline of the first room = %v, want [hello]", got)
	}
}

func TestASecondSyncCarriesOnlyWhatChanged(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	quiet := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "quiet"})
	busy := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "busy"})

	first := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 99))
	if len(first.Rooms) != 2 {
		t.Fatalf("initial sync carried %d rooms, want 2", len(first.Rooms))
	}

	s.mustSend(t, tenant.ServerName, alice.AccessToken, busy, "1", text("later"))

	second := s.syncOnce(t, tenant.ServerName, alice.AccessToken, first.Pos, window(10, 0, 99))
	if _, ok := second.Rooms[quiet]; ok {
		t.Fatal("a room with no new events came back on an incremental sync")
	}
	room, ok := second.Rooms[busy]
	if !ok {
		t.Fatal("the room with a new event is missing from the incremental sync")
	}
	if room.Initial {
		t.Fatal("a room already sent on this connection came back marked initial")
	}
	if got := timelineBodies(t, room.Timeline); len(got) != 1 || got[0] != "later" {
		t.Fatalf("incremental timeline = %v, want [later]", got)
	}
	if room.NumLive != 1 {
		t.Fatalf("num_live = %d, want 1", room.NumLive)
	}
	if room.Name != nil {
		t.Fatal("an unchanged name was re-sent on an incremental response")
	}
}

func TestReplayingThePreviousPosResendsWhatTheLostResponseCarried(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	first := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 99))

	s.mustSend(t, tenant.ServerName, alice.AccessToken, room, "1", text("landed"))

	lost := s.syncOnce(t, tenant.ServerName, alice.AccessToken, first.Pos, window(10, 0, 99))
	if got := timelineBodies(t, lost.Rooms[room].Timeline); len(got) != 1 || got[0] != "landed" {
		t.Fatalf("first delivery = %v, want [landed]", got)
	}

	again := s.syncOnce(t, tenant.ServerName, alice.AccessToken, first.Pos, window(10, 0, 99))
	if got := timelineBodies(t, again.Rooms[room].Timeline); len(got) != 1 || got[0] != "landed" {
		t.Fatalf("replay after a lost response = %v, want [landed]", got)
	}

	acknowledged := s.syncOnce(t, tenant.ServerName, alice.AccessToken, again.Pos, window(10, 0, 99))
	if _, ok := acknowledged.Rooms[room]; ok {
		t.Fatal("an acknowledged event was delivered a second time")
	}
}

func TestAnAcknowledgedPositionRetiresTheOneBeforeIt(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	first := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 99))
	s.mustSend(t, tenant.ServerName, alice.AccessToken, room, "1", text("one"))
	second := s.syncOnce(t, tenant.ServerName, alice.AccessToken, first.Pos, window(10, 0, 99))
	s.syncOnce(t, tenant.ServerName, alice.AccessToken, second.Pos, window(10, 0, 99))

	rec := s.syncRaw(t, tenant.ServerName, alice.AccessToken, first.Pos, window(10, 0, 99))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a retired pos returned %d: %s", rec.Code, rec.Body)
	}
	if got := errcode(t, rec); got != "M_UNKNOWN_POS" {
		t.Fatalf("a retired pos returned %s, want M_UNKNOWN_POS", got)
	}
}

func TestAPosFromAnotherSessionIsNotAccepted(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	laptop := s.loginAlice(t, "LAPTOP")

	s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	phone := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 99))

	rec := s.syncRaw(t, tenant.ServerName, laptop.AccessToken, phone.Pos, window(10, 0, 99))
	if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_UNKNOWN_POS" {
		t.Fatalf("another device reused a pos and got %d: %s", rec.Code, rec.Body)
	}

	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	rec = s.syncRaw(t, tenant.ServerName, bob.AccessToken, phone.Pos, window(10, 0, 99))
	if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_UNKNOWN_POS" {
		t.Fatalf("another user reused a pos and got %d: %s", rec.Code, rec.Body)
	}
}

func TestAFilterThisServerCannotHonourIsRefused(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	for name, filter := range map[string]map[string]any{
		"is_dm":    {"is_dm": true},
		"tags":     {"tags": []string{"m.favourite"}},
		"not_tags": {"not_tags": []string{"m.lowpriority"}},
		"spaces":   {"spaces": []string{"!space:alpha.test"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := s.syncRaw(t, tenant.ServerName, alice.AccessToken, "", map[string]any{
				"lists": map[string]any{
					"all": map[string]any{
						"range": []int{0, 9}, "timeline_limit": 1, "filters": filter,
					},
				},
			})
			if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_INVALID_PARAM" {
				t.Fatalf("%s was accepted: %d %s", name, rec.Code, rec.Body)
			}
		})
	}
}

func TestSyncIsAdvertisedAsAnUnstableFeature(t *testing.T) {
	s := newServer(t)
	rec := s.get(t, "", "/_matrix/client/versions", "")
	body := decode[struct {
		UnstableFeatures map[string]bool `json:"unstable_features"`
	}](t, rec)
	if !body.UnstableFeatures["org.matrix.simplified_msc3575"] {
		t.Fatalf("unstable_features = %v, want simplified sliding sync advertised", body.UnstableFeatures)
	}
}

func TestRangesAreInclusiveAndCountIgnoresThem(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	var rooms []string
	for _, name := range []string{"one", "two", "three", "four"} {
		rooms = append(rooms, s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": name}))
	}

	body := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(1, 0, 1))
	if body.Lists["all"].Count != 4 {
		t.Fatalf("count = %d, want every matching room regardless of the range", body.Lists["all"].Count)
	}
	if len(body.Rooms) != 2 {
		t.Fatalf("range [0,1] returned %d rooms, want 2", len(body.Rooms))
	}
	for _, roomID := range rooms[:2] {
		if _, ok := body.Rooms[roomID]; ok {
			t.Fatalf("%s is the quietest of four rooms but landed inside range [0,1]", roomID)
		}
	}
	for _, roomID := range rooms[2:] {
		if _, ok := body.Rooms[roomID]; !ok {
			t.Fatalf("%s is among the two most recent rooms but was left out of range [0,1]", roomID)
		}
	}
}

func TestRoomsAreOrderedByTheLastEventTheServerReceived(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	first := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "first"})
	second := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "second"})
	s.mustSend(t, tenant.ServerName, alice.AccessToken, first, "1", text("brings it forward"))

	body := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(1, 0, 0))
	if len(body.Rooms) != 1 {
		t.Fatalf("range [0,0] returned %d rooms, want 1", len(body.Rooms))
	}
	if _, ok := body.Rooms[first]; !ok {
		t.Fatalf("the most recently active room is %s, but the head of the list was %v", first, body.Rooms)
	}
	if _, ok := body.Rooms[second]; ok {
		t.Fatal("the quieter room was ranked ahead of the active one")
	}
}

func TestARoomThatLeavesAndRejoinsTheRangeComesBackLimited(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	watched := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "watched"})
	other := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "other"})

	first := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(2, 0, 1))
	if _, ok := first.Rooms[watched]; !ok {
		t.Fatal("the watched room was not in the initial sync")
	}

	for i := range 5 {
		s.mustSend(t, tenant.ServerName, alice.AccessToken, watched, "w"+string(rune('a'+i)), text("missed"))
	}
	s.mustSend(t, tenant.ServerName, alice.AccessToken, other, "o1", text("other"))

	narrowed := s.syncOnce(t, tenant.ServerName, alice.AccessToken, first.Pos, window(2, 0, 0))
	if _, ok := narrowed.Rooms[watched]; ok {
		t.Fatal("a room outside the range was still delivered")
	}

	widened := s.syncOnce(t, tenant.ServerName, alice.AccessToken, narrowed.Pos, window(2, 0, 1))
	room, ok := widened.Rooms[watched]
	if !ok {
		t.Fatal("the room did not come back when the range widened")
	}
	if !room.Limited {
		t.Fatal("a room that missed events came back without limited set")
	}
	if room.PrevBatch == "" {
		t.Fatal("a limited room came back with no prev_batch to paginate from")
	}
	if room.Initial {
		t.Fatal("a room already sent on this connection came back marked initial")
	}
}

func TestPrevBatchMeetsTheTimelineExactly(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	for i := range 6 {
		s.mustSend(t, tenant.ServerName, alice.AccessToken, room, "m"+string(rune('a'+i)), text(string(rune('a'+i))))
	}

	body := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(3, 0, 9))
	delivered := timelineBodies(t, body.Rooms[room].Timeline)
	if len(delivered) != 3 || delivered[2] != "f" {
		t.Fatalf("timeline = %v, want the last three messages", delivered)
	}

	earlier := timelineBodies(t, s.mustPage(t, tenant.ServerName, alice.AccessToken, room,
		"dir=b&limit=3&from="+url.QueryEscape(body.Rooms[room].PrevBatch)).Chunk)
	if len(earlier) != 3 {
		t.Fatalf("paginating from prev_batch gave %v, want three earlier messages", earlier)
	}
	if earlier[0] != "c" {
		t.Fatalf("prev_batch overlapped or skipped: %v then %v", earlier, delivered)
	}
}

func TestRaisingTheTimelineLimitReplaysHistory(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	for i := range 5 {
		s.mustSend(t, tenant.ServerName, alice.AccessToken, room, "m"+string(rune('a'+i)), text(string(rune('a'+i))))
	}

	first := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(1, 0, 9))
	if got := timelineBodies(t, first.Rooms[room].Timeline); len(got) != 1 {
		t.Fatalf("timeline_limit 1 returned %v", got)
	}

	widened := s.syncOnce(t, tenant.ServerName, alice.AccessToken, first.Pos, window(4, 0, 9))
	wider, ok := widened.Rooms[room]
	if !ok {
		t.Fatal("raising timeline_limit did not re-send the room")
	}
	if !wider.ExpandedTimeline {
		t.Fatal("historic events were sent without expanded_timeline set")
	}
	if got := timelineBodies(t, wider.Timeline); len(got) != 4 {
		t.Fatalf("widened timeline = %v, want four messages", got)
	}
}

func TestAnInviteCarriesStrippedStateAndNoTimeline(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "invited"})
	s.mustSend(t, tenant.ServerName, alice.AccessToken, room, "1", text("private"))
	s.mustAct(t, tenant.ServerName, "/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/invite", alice.AccessToken,
		map[string]any{"user_id": bob.UserID})

	body := s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", window(10, 0, 9))
	invited, ok := body.Rooms[room]
	if !ok {
		t.Fatal("an invited room is missing from sync")
	}
	if invited.Membership != entity.MembershipInvite {
		t.Fatalf("membership = %q, want invite", invited.Membership)
	}
	if len(invited.Timeline) != 0 {
		t.Fatalf("an invite carried %d timeline events", len(invited.Timeline))
	}
	if len(invited.StrippedState) == 0 {
		t.Fatal("an invite carried no stripped_state")
	}
	for _, raw := range invited.StrippedState {
		if strings.Contains(string(raw), "private") {
			t.Fatal("stripped state leaked room content to an invitee")
		}
	}
}

func TestALeftRoomAppearsOnlyIfTheConnectionAlreadyKnewIt(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "shared"})
	s.mustAct(t, tenant.ServerName, "/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/invite", alice.AccessToken,
		map[string]any{"user_id": bob.UserID})
	s.joinAs(t, tenant, room, bob.UserID)
	s.mustAct(t, tenant.ServerName, "/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/leave", bob.AccessToken,
		map[string]any{"user_id": bob.UserID})

	fresh := s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", window(10, 0, 9))
	if _, ok := fresh.Rooms[room]; ok {
		t.Fatal("a room left before the connection existed was sent on a cold bootstrap")
	}

	carol := s.register(t, tenant.ServerName, "carol", goodPassword)
	second := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "second"})
	s.mustAct(t, tenant.ServerName, "/_matrix/client/v3/rooms/"+url.PathEscape(second)+"/invite", alice.AccessToken,
		map[string]any{"user_id": carol.UserID})
	s.joinAs(t, tenant, second, carol.UserID)

	known := s.syncOnce(t, tenant.ServerName, carol.AccessToken, "", window(10, 0, 9))
	if _, ok := known.Rooms[second]; !ok {
		t.Fatal("carol did not receive the room she joined")
	}
	s.mustAct(t, tenant.ServerName, "/_matrix/client/v3/rooms/"+url.PathEscape(second)+"/leave", carol.AccessToken,
		map[string]any{"user_id": carol.UserID})

	after := s.syncOnce(t, tenant.ServerName, carol.AccessToken, known.Pos, window(10, 0, 9))
	room2, ok := after.Rooms[second]
	if !ok {
		t.Fatal("leaving a known room was not reported on the connection that knew it")
	}
	if room2.Membership != entity.MembershipLeave {
		t.Fatalf("membership after leaving = %q, want leave", room2.Membership)
	}
}

func TestAConnectionSurvivesAServerRestart(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	first := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 9))
	s.mustSend(t, tenant.ServerName, alice.AccessToken, room, "1", text("across the restart"))

	restarted := reopen(t, s)
	body := restarted.syncOnce(t, tenant.ServerName, alice.AccessToken, first.Pos, window(10, 0, 9))
	if got := timelineBodies(t, body.Rooms[room].Timeline); len(got) != 1 || got[0] != "across the restart" {
		t.Fatalf("after a restart the connection delivered %v", got)
	}
	if body.Rooms[room].Initial {
		t.Fatal("a restart made the server forget what it had already sent")
	}
}

func TestSyncNeverCarriesHistoryTheCallerMayNotRead(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, tenant.ServerName, alice, room, entity.HistoryVisibilityJoined)

	hidden := s.mustSend(t, tenant.ServerName, alice, room, "hidden", text("before bob arrived"))

	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	s.joinAs(t, tenant, room, bob.UserID)
	visible := s.mustSend(t, tenant.ServerName, alice, room, "visible", text("after bob arrived"))

	body := s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", map[string]any{
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 9}, "timeline_limit": 50,
			"required_state": map[string]any{"include": []map[string]any{{}}},
		}},
	})
	delivered := eventIDs(t, body.Rooms[room].Timeline)
	if contains(delivered, hidden) {
		t.Fatalf("sync delivered %s, which predates bob's join in a joined-visibility room", hidden)
	}
	if !contains(delivered, visible) {
		t.Fatalf("sync withheld %s, which bob is entitled to read", visible)
	}
	for _, id := range eventIDs(t, body.Rooms[room].RequiredState) {
		if id == hidden {
			t.Fatal("required_state delivered an event the timeline correctly withheld")
		}
	}
}

func TestARoomTheCallerWasNeverEntitledToSeeNeverAppears(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	private := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPrivateChat})
	s.mustSend(t, tenant.ServerName, alice, private, "secret", text("not for bob"))

	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	body := s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", window(10, 0, 99))
	if _, ok := body.Rooms[private]; ok {
		t.Fatal("sync returned a room the caller has no membership in")
	}
	if body.Lists["all"].Count != 0 {
		t.Fatalf("list count = %d for a user in no rooms", body.Lists["all"].Count)
	}
}

func TestRequiredStateSelectsByTypeAndKeyIncludingWildcards(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})

	types := func(body syncBody) []string {
		out := make([]string, 0)
		for _, raw := range body.Rooms[room].RequiredState {
			var parsed struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatalf("decode state: %v", err)
			}
			out = append(out, parsed.Type)
		}
		return out
	}

	named := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", map[string]any{
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 9}, "timeline_limit": 1,
			"required_state": map[string]any{"include": []map[string]any{
				{"type": entity.EventTypeName, "state_key": ""},
			}},
		}},
	})
	if got := types(named); len(got) != 1 || got[0] != entity.EventTypeName {
		t.Fatalf("an explicit selector returned %v", got)
	}

	everything := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", map[string]any{
		"conn_id": "wildcard",
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 9}, "timeline_limit": 1,
			"required_state": map[string]any{"include": []map[string]any{{}}},
		}},
	})
	if len(types(everything)) < 5 {
		t.Fatalf("an empty selector returned only %v", types(everything))
	}

	excluded := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", map[string]any{
		"conn_id": "excluded",
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 9}, "timeline_limit": 1,
			"required_state": map[string]any{
				"include": []map[string]any{{}},
				"exclude": []map[string]any{{"type": entity.EventTypeMember}},
			},
		}},
	})
	if slicesContains(types(excluded), entity.EventTypeMember) {
		t.Fatalf("exclude did not remove what include selected: %v", types(excluded))
	}
}

func TestLazyMembersReturnTheSendersOfTheTimeline(t *testing.T) {
	s := newServer(t)
	tenant, alice, aliceID := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})

	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	carol := s.register(t, tenant.ServerName, "carol", goodPassword)
	s.joinAs(t, tenant, room, bob.UserID)
	s.joinAs(t, tenant, room, carol.UserID)
	s.mustSend(t, tenant.ServerName, bob.AccessToken, room, "1", text("from bob"))

	body := s.syncOnce(t, tenant.ServerName, alice, "", map[string]any{
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 9}, "timeline_limit": 1,
			"required_state": map[string]any{
				"include":      []map[string]any{{"type": entity.EventTypeName, "state_key": ""}},
				"exclude":      []map[string]any{{"type": entity.EventTypeMember}},
				"lazy_members": true,
			},
		}},
	})

	var members []string
	for _, raw := range body.Rooms[room].RequiredState {
		var parsed struct {
			Type     string `json:"type"`
			StateKey string `json:"state_key"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("decode state: %v", err)
		}
		if parsed.Type == entity.EventTypeMember {
			members = append(members, parsed.StateKey)
		}
	}
	if !slicesContains(members, bob.UserID) {
		t.Fatalf("lazy members = %v, want the timeline sender %s", members, bob.UserID)
	}
	if !slicesContains(members, aliceID) {
		t.Fatalf("lazy members = %v, want the caller %s", members, aliceID)
	}
	if slicesContains(members, carol.UserID) {
		t.Fatalf("lazy members = %v, want nothing for a silent member", members)
	}
}

func TestARoomSubscriptionReachesOutsideEveryRange(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	quiet := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "quiet"})
	s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "loud"})

	body := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", map[string]any{
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 0}, "timeline_limit": 1,
			"required_state": map[string]any{"include": []map[string]any{}},
		}},
		"room_subscriptions": map[string]any{
			quiet: map[string]any{
				"timeline_limit": 1,
				"required_state": map[string]any{"include": []map[string]any{}},
			},
		},
	})
	room, ok := body.Rooms[quiet]
	if !ok {
		t.Fatal("a subscribed room outside every range was not delivered")
	}
	if len(room.Lists) != 0 {
		t.Fatalf("a room delivered only by subscription named lists %v", room.Lists)
	}
}

func TestTheRoomSummaryCarriesCountsAndHeroes(t *testing.T) {
	s := newServer(t)
	tenant, alice, aliceID := s.resident(t, "alpha.test", "alice")
	named := s.createRoom(t, tenant.ServerName, alice, map[string]any{
		"preset": entity.PresetPublicChat, "name": "has a name",
	})
	anonymous := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})

	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	for _, room := range []string{named, anonymous} {
		s.joinAs(t, tenant, room, bob.UserID)
	}

	body := s.syncOnce(t, tenant.ServerName, alice, "", window(1, 0, 9))

	withName := body.Rooms[named]
	if withName.Name == nil || *withName.Name != "has a name" {
		t.Fatalf("name = %v, want the room name", withName.Name)
	}
	if len(withName.Heroes) != 0 {
		t.Fatalf("a named room carried heroes: %v", withName.Heroes)
	}
	if withName.JoinedCount == nil || *withName.JoinedCount != 2 {
		t.Fatalf("joined_count = %v, want 2", withName.JoinedCount)
	}

	withoutName := body.Rooms[anonymous]
	if withoutName.Name != nil {
		t.Fatalf("an unnamed room reported a name: %v", *withoutName.Name)
	}
	if len(withoutName.Heroes) != 1 || withoutName.Heroes[0].UserID != bob.UserID {
		t.Fatalf("heroes = %v, want only %s", withoutName.Heroes, bob.UserID)
	}
	for _, hero := range withoutName.Heroes {
		if hero.UserID == aliceID {
			t.Fatal("the caller was listed as a hero of their own room")
		}
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, value := range haystack {
		if value == needle {
			return true
		}
	}
	return false
}

func TestALongPollWaitsAndIsWokenByAMessage(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	s.joinAs(t, tenant, room, bob.UserID)
	first := s.syncOnce(t, tenant.ServerName, alice, "", window(10, 0, 9))

	request := window(10, 0, 9)
	request["timeout"] = 5000

	done := make(chan syncBody, 1)
	go func() { done <- s.syncOnce(t, tenant.ServerName, alice, first.Pos, request) }()

	for range 40 {
		time.Sleep(25 * time.Millisecond)
		if s.notifier.Watching() > 0 {
			break
		}
	}
	s.mustSend(t, tenant.ServerName, bob.AccessToken, room, "1", text("woke you"))

	select {
	case body := <-done:
		if got := timelineBodies(t, body.Rooms[room].Timeline); !slicesContains(got, "woke you") {
			t.Fatalf("the woken poll returned %v", got)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("a long poll was not woken by a message in a watched room")
	}
}

func TestATimedOutPollReturnsThePositionItWasGiven(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	first := s.syncOnce(t, tenant.ServerName, alice, "", window(10, 0, 9))

	request := window(10, 0, 9)
	request["timeout"] = 200

	started := time.Now()
	body := s.syncOnce(t, tenant.ServerName, alice, first.Pos, request)
	if time.Since(started) < 150*time.Millisecond {
		t.Fatalf("a poll with a 200ms timeout returned after %s", time.Since(started))
	}
	if len(body.Rooms) != 0 {
		t.Fatalf("a timed out poll carried %d rooms", len(body.Rooms))
	}
	if body.Pos != first.Pos {
		t.Fatalf("a timed out poll moved the position from %s to %s", first.Pos, body.Pos)
	}
}

func TestAParkedConnectionHoldsNoDatabaseConnection(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	first := s.syncOnce(t, tenant.ServerName, alice, "", window(10, 0, 9))

	request := window(10, 0, 9)
	request["timeout"] = 3000

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.syncOnce(t, tenant.ServerName, alice, first.Pos, request)
	}()

	parked := false
	for range 60 {
		time.Sleep(25 * time.Millisecond)
		if s.notifier.Watching() > 0 {
			parked = true
			break
		}
	}
	if !parked {
		t.Fatal("the poll never parked on the notifier")
	}
	if inUse := s.db.Stats().InUse; inUse != 0 {
		t.Fatalf("a parked sync held %d database connections", inUse)
	}
	<-done
}

func TestAWakeUpCrossesTwoInstances(t *testing.T) {
	bus := valkeytest.Connect(t, config.Limits{SendPerUser: 1000, SendWindow: time.Second})
	pg := pgtest.Connect(t, "tenants", "stream_positions")
	reader := newSharedServer(t, "reader", pg, bus)
	writer := newSharedServer(t, "writer", pg, bus)

	tenant, alice, _ := reader.resident(t, "alpha.test", "alice")
	room := reader.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	first := reader.syncOnce(t, tenant.ServerName, alice, "", window(10, 0, 9))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = reader.notifier.Run(ctx) }()
	for range 40 {
		time.Sleep(25 * time.Millisecond)
	}

	request := window(10, 0, 9)
	request["timeout"] = 5000
	done := make(chan syncBody, 1)
	go func() { done <- reader.syncOnce(t, tenant.ServerName, alice, first.Pos, request) }()

	for range 40 {
		time.Sleep(25 * time.Millisecond)
		if reader.notifier.Watching() > 0 {
			break
		}
	}
	writer.mustSend(t, tenant.ServerName, alice, room, "1", text("from the other instance"))

	select {
	case body := <-done:
		if got := timelineBodies(t, body.Rooms[room].Timeline); !slicesContains(got, "from the other instance") {
			t.Fatalf("the cross-instance poll returned %v", got)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("a write on one instance did not wake a poll on the other")
	}
}
