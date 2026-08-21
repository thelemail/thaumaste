package matrix_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
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

func TestTwoConnectionIdsOnOneDeviceAreIndependent(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	room := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})

	main := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 9))
	side := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", map[string]any{
		"conn_id": "side",
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 9}, "timeline_limit": 10,
			"required_state": map[string]any{"include": []map[string]any{}},
		}},
	})
	if main.Pos == side.Pos {
		t.Fatal("two connection ids shared one position")
	}
	s.mustSend(t, tenant.ServerName, alice.AccessToken, room, "1", text("for both"))

	onMain := s.syncOnce(t, tenant.ServerName, alice.AccessToken, main.Pos, window(10, 0, 9))
	if got := timelineBodies(t, onMain.Rooms[room].Timeline); !slicesContains(got, "for both") {
		t.Fatalf("the main connection missed the message: %v", got)
	}
	onSide := s.syncOnce(t, tenant.ServerName, alice.AccessToken, side.Pos, map[string]any{
		"conn_id": "side",
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 9}, "timeline_limit": 10,
			"required_state": map[string]any{"include": []map[string]any{}},
		}},
	})
	if got := timelineBodies(t, onSide.Rooms[room].Timeline); !slicesContains(got, "for both") {
		t.Fatalf("the side connection missed the message: %v", got)
	}
}

func TestAnAbandonedConnectionExpiresAndTheClientStartsOver(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})

	body := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 9))
	swept, err := s.sync.SweepConnections(t.Context(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("SweepConnections: %v", err)
	}
	if swept == 0 {
		t.Fatal("the sweep found no abandoned connection to remove")
	}

	rec := s.syncRaw(t, tenant.ServerName, alice.AccessToken, body.Pos, window(10, 0, 9))
	if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_UNKNOWN_POS" {
		t.Fatalf("an expired connection answered %d: %s", rec.Code, rec.Body)
	}
	fresh := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 9))
	if len(fresh.Rooms) != 1 {
		t.Fatalf("bootstrapping after expiry returned %d rooms", len(fresh.Rooms))
	}
}

func TestLoggingOutADeviceDestroysItsConnection(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	body := s.syncOnce(t, tenant.ServerName, alice.AccessToken, "", window(10, 0, 9))

	rec := s.do(t, http.MethodPost, tenant.ServerName, "/_matrix/client/v3/logout", alice.AccessToken, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("logout = %d: %s", rec.Code, rec.Body)
	}

	again := s.loginAlice(t, "PHONE")
	failed := s.syncRaw(t, tenant.ServerName, again.AccessToken, body.Pos, window(10, 0, 9))
	if failed.Code != http.StatusBadRequest || errcode(t, failed) != "M_UNKNOWN_POS" {
		t.Fatalf("a position survived the device logout: %d %s", failed.Code, failed.Body)
	}
}

func TestBumpStampMovesOnlyForBumpingEventTypes(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})

	first := s.syncOnce(t, tenant.ServerName, alice, "", window(10, 0, 9))
	created := first.Rooms[room].BumpStamp
	if created == 0 {
		t.Fatal("a created room carried no bump_stamp")
	}

	s.do(t, http.MethodPut, tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/state/m.room.topic",
		alice, map[string]any{"topic": "quiet change"})
	afterTopic := s.syncOnce(t, tenant.ServerName, alice, first.Pos, window(10, 0, 9))
	if got := afterTopic.Rooms[room].BumpStamp; got != created {
		t.Fatalf("a topic change moved bump_stamp from %d to %d", created, got)
	}

	s.mustSend(t, tenant.ServerName, alice, room, "1", text("real activity"))
	afterMessage := s.syncOnce(t, tenant.ServerName, alice, afterTopic.Pos, window(10, 0, 9))
	if got := afterMessage.Rooms[room].BumpStamp; got <= created {
		t.Fatalf("a message left bump_stamp at %d, want more than %d", got, created)
	}
}

func TestFiltersThisServerCanAnswer(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	joined := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	invited := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPrivateChat})
	s.mustAct(t, tenant.ServerName, "/_matrix/client/v3/rooms/"+url.PathEscape(invited)+"/invite", alice,
		map[string]any{"user_id": bob.UserID})
	s.joinAs(t, tenant, joined, bob.UserID)

	filtered := func(filter map[string]any) map[string]syncRoomBody {
		body := s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", map[string]any{
			"conn_id": "f",
			"lists": map[string]any{"all": map[string]any{
				"range": []int{0, 9}, "timeline_limit": 1, "filters": filter,
				"required_state": map[string]any{"include": []map[string]any{}},
			}},
		})
		return body.Rooms
	}

	onlyInvites := filtered(map[string]any{"is_invited": true})
	if _, ok := onlyInvites[invited]; !ok {
		t.Fatal("is_invited did not return the invited room")
	}
	if _, ok := onlyInvites[joined]; ok {
		t.Fatal("is_invited returned a joined room")
	}

	encrypted := filtered(map[string]any{"is_encrypted": true})
	if len(encrypted) == 0 {
		t.Fatal("is_encrypted returned nothing in a server that mandates encryption")
	}
	if len(filtered(map[string]any{"is_encrypted": false})) != 0 {
		t.Fatal("is_encrypted false matched a room in a server that mandates encryption")
	}
	if len(filtered(map[string]any{"not_room_types": []any{nil}})) != 0 {
		t.Fatal("not_room_types [null] failed to exclude ordinary rooms")
	}
	if len(filtered(map[string]any{"room_types": []any{nil}})) == 0 {
		t.Fatal("room_types [null] failed to match ordinary rooms")
	}
}

func TestTooManyListsIsRefused(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")

	lists := make(map[string]any, entity.MaxSyncLists+1)
	for i := range entity.MaxSyncLists + 1 {
		lists[fmt.Sprintf("list-%d", i)] = map[string]any{
			"range": []int{0, 1}, "timeline_limit": 1,
			"required_state": map[string]any{"include": []map[string]any{}},
		}
	}
	rec := s.syncRaw(t, tenant.ServerName, alice, "", map[string]any{"lists": lists})
	if rec.Code != http.StatusBadRequest || errcode(t, rec) != "M_INVALID_PARAM" {
		t.Fatalf("%d lists were accepted: %d %s", len(lists), rec.Code, rec.Body)
	}
}

func TestExtensionsAreAcceptedAndAnsweredEmpty(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})

	request := window(1, 0, 9)
	request["extensions"] = map[string]any{
		"to_device": map[string]any{"enabled": true},
		"e2ee":      map[string]any{"enabled": true},
	}
	body := s.syncOnce(t, tenant.ServerName, alice, "", request)
	if body.Extensions == nil {
		t.Fatal("extensions was omitted from the response")
	}
	if len(body.Extensions) != 0 {
		t.Fatalf("extensions = %v, want an empty object until THE-19", body.Extensions)
	}
}

func TestAnIdleConnectionCostsABoundedAmount(t *testing.T) {
	const connections = 150

	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	for range 5 {
		s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	}

	sessions := make([]sessionBody, 0, connections)
	positions := make([]string, 0, connections)
	for i := range connections {
		session := s.loginAlice(t, fmt.Sprintf("DEVICE%03d", i))
		sessions = append(sessions, session)
		positions = append(positions, s.syncOnce(t, tenant.ServerName, session.AccessToken, "", window(5, 0, 9)).Pos)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baseline := runtime.NumGoroutine()

	request := window(5, 0, 9)
	request["timeout"] = 4000

	done := make(chan int, connections)
	for i := range connections {
		go func() {
			done <- s.syncRaw(t, tenant.ServerName, sessions[i].AccessToken, positions[i], request).Code
		}()
	}

	parked := false
	for range 200 {
		time.Sleep(20 * time.Millisecond)
		if s.notifier.Waiters() >= connections {
			parked = true
			break
		}
	}
	if !parked {
		t.Fatalf("only %d of %d connections parked", s.notifier.Waiters(), connections)
	}

	runtime.GC()
	var during runtime.MemStats
	runtime.ReadMemStats(&during)
	goroutines := runtime.NumGoroutine() - baseline
	held := s.db.Stats().InUse
	bytes := int64(during.HeapAlloc) - int64(before.HeapAlloc)

	t.Logf("%d parked connections: %d goroutines, %d database connections, %d heap bytes each",
		connections, goroutines, held, bytes/connections)

	if held != 0 {
		t.Fatalf("%d parked connections held %d database connections", connections, held)
	}
	if perConnection := goroutines / connections; perConnection > 2 {
		t.Fatalf("each parked connection costs %d goroutines", perConnection)
	}
	if bytes > 0 && bytes/connections > 64*1024 {
		t.Fatalf("each parked connection holds %d heap bytes", bytes/connections)
	}

	for range connections {
		if code := <-done; code != http.StatusOK {
			t.Fatalf("a parked connection ended with %d", code)
		}
	}
	if s.notifier.Watching() != 0 {
		t.Fatalf("%d wake-up registrations survived their connections", s.notifier.Watching())
	}
}

func TestSyncNeverNamesARoomOfAnotherDomain(t *testing.T) {
	s := newServer(t)
	alpha, alice, _ := s.resident(t, "alpha.test", "alice")
	beta, bob, bobID := s.resident(t, "beta.test", "bob")

	alphaRoom := s.createRoom(t, alpha.ServerName, alice, map[string]any{
		"preset": entity.PresetPublicChat, "name": "alpha only",
	})
	s.mustSend(t, alpha.ServerName, alice, alphaRoom, "1", text("alpha only"))
	betaRoom := s.createRoom(t, beta.ServerName, bob, map[string]any{
		"preset": entity.PresetPublicChat, "name": "beta only",
	})
	s.mustSend(t, beta.ServerName, bob, betaRoom, "1", text("beta only"))

	body := s.syncOnce(t, beta.ServerName, bob, "", map[string]any{
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, 99}, "timeline_limit": 50,
			"required_state": map[string]any{"include": []map[string]any{{}}, "lazy_members": true},
		}},
		"room_subscriptions": map[string]any{
			alphaRoom: map[string]any{
				"timeline_limit": 50,
				"required_state": map[string]any{"include": []map[string]any{{}}},
			},
		},
	})

	if _, ok := body.Rooms[alphaRoom]; ok {
		t.Fatal("a subscription reached a room of another domain")
	}
	if body.Lists["all"].Count != 1 {
		t.Fatalf("count = %d, want only the caller's own domain", body.Lists["all"].Count)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal sync response: %v", err)
	}
	for _, forbidden := range []string{alphaRoom, "alpha only", "@alice:alpha.test", "alpha.test"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("a sync response for %s named %q", beta.ServerName, forbidden)
		}
	}
	room := body.Rooms[betaRoom]
	for _, hero := range room.Heroes {
		if !strings.HasSuffix(hero.UserID, ":"+beta.ServerName) {
			t.Fatalf("a hero of a beta room is %s", hero.UserID)
		}
	}
	if room.Membership != entity.MembershipJoin || bobID == "" {
		t.Fatalf("the caller's own room came back as %q", room.Membership)
	}
}

func TestALongPollSurvivesTheServerWriteTimeoutOverARealSocket(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})

	origin := &http.Server{
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 300 * time.Millisecond,
	}
	listener := httptest.NewUnstartedServer(s.router)
	listener.Config = origin
	listener.Start()
	defer listener.Close()

	first := s.syncOnce(t, tenant.ServerName, alice, "", window(10, 0, 9))

	request := window(10, 0, 9)
	request["timeout"] = 1500
	request["pos"] = first.Pos
	request["conn_id"] = "test"
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal sync request: %v", err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, listener.URL+syncPath, strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = tenant.ServerName
	req.Header.Set("Authorization", "Bearer "+alice)
	req.Header.Set("Content-Type", "application/json")

	go func() {
		time.Sleep(700 * time.Millisecond)
		s.mustSend(t, tenant.ServerName, alice, room, "1", text("past the write timeout"))
	}()

	response, err := listener.Client().Do(req)
	if err != nil {
		t.Fatalf("a long poll outliving the write timeout failed: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("sync over a real socket = %d", response.StatusCode)
	}

	var body syncBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if got := timelineBodies(t, body.Rooms[room].Timeline); !slicesContains(got, "past the write timeout") {
		t.Fatalf("the poll returned %v", got)
	}
}

func TestNumLiveCountsOnlyWhatArrivedSinceTheLastResponse(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})
	for i := range 6 {
		s.mustSend(t, tenant.ServerName, alice, room, "m"+string(rune('a'+i)), text(string(rune('a'+i))))
	}

	first := s.syncOnce(t, tenant.ServerName, alice, "", window(2, 0, 9))
	if got := first.Rooms[room].NumLive; got != 0 {
		t.Fatalf("an initial sync reported num_live %d", got)
	}

	s.mustSend(t, tenant.ServerName, alice, room, "new", text("new"))
	second := s.syncOnce(t, tenant.ServerName, alice, first.Pos, window(2, 0, 9))
	if got := second.Rooms[room].NumLive; got != 1 {
		t.Fatalf("num_live = %d after one new message, want 1", got)
	}

	widened := s.syncOnce(t, tenant.ServerName, alice, second.Pos, window(6, 0, 9))
	room6 := widened.Rooms[room]
	if !room6.ExpandedTimeline {
		t.Fatal("raising the limit did not set expanded_timeline")
	}
	if len(room6.Timeline) < 5 {
		t.Fatalf("an expanded timeline carried %d events", len(room6.Timeline))
	}
	if room6.NumLive != 0 {
		t.Fatalf("num_live = %d on a timeline made of history, want 0", room6.NumLive)
	}
}

func TestWideningRequiredStateSendsTheNewlyMatchedStateWithoutAResync(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{
		"preset": entity.PresetPublicChat, "name": "named", "topic": "a topic",
	})

	ask := func(include []map[string]any) map[string]any {
		return map[string]any{
			"lists": map[string]any{"all": map[string]any{
				"range": []int{0, 9}, "timeline_limit": 1,
				"required_state": map[string]any{"include": include},
			}},
		}
	}
	narrow := ask([]map[string]any{{"type": entity.EventTypeName, "state_key": ""}})
	wide := ask([]map[string]any{
		{"type": entity.EventTypeName, "state_key": ""},
		{"type": entity.EventTypeTopic, "state_key": ""},
	})

	first := s.syncOnce(t, tenant.ServerName, alice, "", narrow)
	if got := len(first.Rooms[room].RequiredState); got != 1 {
		t.Fatalf("the narrow request returned %d state events", got)
	}

	widened := s.syncOnce(t, tenant.ServerName, alice, first.Pos, wide)
	after, ok := widened.Rooms[room]
	if !ok {
		t.Fatal("widening required_state did not re-send the room")
	}
	if after.Initial {
		t.Fatal("widening required_state forced a full resync of the room")
	}
	var types []string
	for _, raw := range after.RequiredState {
		var parsed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("decode state: %v", err)
		}
		types = append(types, parsed.Type)
	}
	if !slicesContains(types, entity.EventTypeTopic) {
		t.Fatalf("required_state = %v, want the newly matched topic", types)
	}
}

func TestASyncTimelineCarriesTheSameBundleAsPagination(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"preset": entity.PresetPublicChat})

	original := s.mustSend(t, tenant.ServerName, alice, room, "1", text("first draft"))
	s.mustSendType(t, tenant.ServerName, alice, room, entity.EventTypeMessage, "2", map[string]any{
		"msgtype": "m.text", "body": "* second draft",
		"m.new_content": map[string]any{"msgtype": "m.text", "body": "second draft"},
		"m.relates_to":  map[string]any{"rel_type": entity.RelReplace, "event_id": original},
	})

	body := s.syncOnce(t, tenant.ServerName, alice, "", window(10, 0, 9))
	fromSync := bundleOf(t, body.Rooms[room].Timeline, original)
	if fromSync == "" {
		t.Fatal("a sync timeline carried no m.relations bundle for an edited message")
	}

	page := s.mustPage(t, tenant.ServerName, alice, room, "dir=b&limit=20")
	fromMessages := bundleOf(t, page.Chunk, original)
	if fromSync != fromMessages {
		t.Fatalf("sync bundled %s, /messages bundled %s", fromSync, fromMessages)
	}
}

func bundleOf(t *testing.T, events []json.RawMessage, eventID string) string {
	t.Helper()
	for _, raw := range events {
		var parsed struct {
			EventID  string `json:"event_id"`
			Unsigned struct {
				Relations struct {
					Replace struct {
						EventID string `json:"event_id"`
					} `json:"m.replace"`
				} `json:"m.relations"`
			} `json:"unsigned"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if parsed.EventID == eventID {
			return parsed.Unsigned.Relations.Replace.EventID
		}
	}
	return ""
}

func TestRemovingARoomNameIsSentAsNullRatherThanOmitted(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{
		"preset": entity.PresetPublicChat, "name": "named for now",
	})

	first := s.syncOnce(t, tenant.ServerName, alice, "", window(1, 0, 9))
	if first.Rooms[room].Name == nil || *first.Rooms[room].Name != "named for now" {
		t.Fatalf("name = %v, want the room name", first.Rooms[room].Name)
	}

	s.do(t, http.MethodPut, tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/state/m.room.name",
		alice, map[string]any{"name": ""})

	rec := s.syncRaw(t, tenant.ServerName, alice, first.Pos, window(1, 0, 9))
	if rec.Code != http.StatusOK {
		t.Fatalf("sync = %d: %s", rec.Code, rec.Body)
	}
	var wire struct {
		Rooms map[string]map[string]json.RawMessage `json:"rooms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	fields, ok := wire.Rooms[room]
	if !ok {
		t.Fatal("removing the room name did not re-send the room")
	}
	value, present := fields["name"]
	if !present {
		t.Fatal("a removed room name was omitted, which a client reads as unchanged")
	}
	if string(value) != "null" {
		t.Fatalf("a removed room name came back as %s, want null", value)
	}
}

func TestStrippedStateCarriesOnlyWhatAnInviteeNeeds(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	room := s.createRoom(t, tenant.ServerName, alice, map[string]any{"name": "invited"})
	s.mustAct(t, tenant.ServerName, "/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/invite", alice,
		map[string]any{"user_id": bob.UserID})

	body := s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", window(10, 0, 9))
	for _, raw := range body.Rooms[room].StrippedState {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("decode stripped state: %v", err)
		}
		for key := range fields {
			switch key {
			case "type", "state_key", "sender", "content":
			default:
				t.Fatalf("a stripped state event carried %q", key)
			}
		}
		for _, required := range []string{"type", "state_key", "sender", "content"} {
			if _, ok := fields[required]; !ok {
				t.Fatalf("a stripped state event is missing %q", required)
			}
		}
	}
}
