package matrix_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) setVisibility(t *testing.T, host, token, roomID, value string) {
	t.Helper()
	rec := s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/state/m.room.history_visibility",
		token, map[string]any{"history_visibility": value})
	if rec.Code != http.StatusOK {
		t.Fatalf("set history visibility %s = %d: %s", value, rec.Code, rec.Body)
	}
}

func (s *server) readable(t *testing.T, host, token, roomID string) []string {
	t.Helper()
	return eventIDs(t, s.mustPage(t, host, token, roomID, "dir=b&limit=1000").Chunk)
}

func TestSharedHistoryIsOpenToALateJoiner(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityShared)

	before := s.mustSend(t, "alpha.test", alice, roomID, "before", text("before bob"))

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)
	after := s.mustSend(t, "alpha.test", alice, roomID, "after", text("after bob"))

	seen := s.readable(t, "alpha.test", bob.AccessToken, roomID)
	if !contains(seen, before) {
		t.Fatal("shared history hid an event sent before the join")
	}
	if !contains(seen, after) {
		t.Fatal("shared history hid an event sent after the join")
	}
}

func TestJoinedHistoryHidesWhatCameBeforeTheJoin(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityJoined)

	before := s.mustSend(t, "alpha.test", alice, roomID, "before", text("before bob"))

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)
	after := s.mustSend(t, "alpha.test", alice, roomID, "after", text("after bob"))

	seen := s.readable(t, "alpha.test", bob.AccessToken, roomID)
	if contains(seen, before) {
		t.Fatal("joined history leaked an event sent before the join")
	}
	if !contains(seen, after) {
		t.Fatal("joined history hid an event sent after the join")
	}

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/event/"+url.PathEscape(before), bob.AccessToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reading a hidden event directly = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestADepartedMemberSeesNothingAfterTheirLeave(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)
	during := s.mustSend(t, "alpha.test", alice, roomID, "during", text("while bob was here"))
	s.mustAct(t, "alpha.test", leavePath(roomID), bob.AccessToken, map[string]any{})
	after := s.mustSend(t, "alpha.test", alice, roomID, "after", text("after bob left"))

	seen := s.readable(t, "alpha.test", bob.AccessToken, roomID)
	if !contains(seen, during) {
		t.Fatal("a departed member lost history they could see before leaving")
	}
	if contains(seen, after) {
		t.Fatal("a departed member can see an event sent after they left")
	}

	forwards := s.mustPage(t, "alpha.test", bob.AccessToken, roomID,
		"dir=f&limit=1000&from="+url.QueryEscape(s.mustPage(t, "alpha.test", bob.AccessToken, roomID, "dir=b&limit=1").Start))
	for _, id := range eventIDs(t, forwards.Chunk) {
		if id == after {
			t.Fatal("forward pagination handed a departed member a later event")
		}
	}
}

func TestForgettingARoomStopsReadingIt(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)
	s.mustSend(t, "alpha.test", alice, roomID, "during", text("while bob was here"))
	s.mustAct(t, "alpha.test", leavePath(roomID), bob.AccessToken, map[string]any{})

	if got := s.readable(t, "alpha.test", bob.AccessToken, roomID); len(got) == 0 {
		t.Fatal("a departed member could read nothing before forgetting")
	}

	s.mustAct(t, "alpha.test", "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/forget",
		bob.AccessToken, map[string]any{})
	if rec := s.messages(t, "alpha.test", bob.AccessToken, roomID, "dir=b"); rec.Code != http.StatusForbidden {
		t.Fatalf("a forgotten room = %d, want 403: %s", rec.Code, rec.Body)
	}

	s.joinAs(t, tenant, roomID, bob.UserID)
	if got := s.readable(t, "alpha.test", bob.AccessToken, roomID); len(got) == 0 {
		t.Fatal("rejoining a forgotten room did not make it readable again")
	}
}

func TestWorldReadableHistoryIsOpenToANonMember(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityWorldReadable)
	open := s.mustSend(t, "alpha.test", alice, roomID, "open", text("anyone may read this"))

	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityJoined)
	closed := s.mustSend(t, "alpha.test", alice, roomID, "closed", text("nobody may read this"))

	outsider := s.register(t, "alpha.test", "outsider", goodPassword)
	seen := s.readable(t, "alpha.test", outsider.AccessToken, roomID)
	if !contains(seen, open) {
		t.Fatal("closing the room retracted history that was world readable when it was sent")
	}
	if contains(seen, closed) {
		t.Fatal("closing the room left later events readable to a non-member")
	}
}

func TestTheContextAroundAnEventReadsBothWays(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	sent := s.chatter(t, "alpha.test", alice, roomID, 7)
	middle := sent[3]

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/context/"+url.PathEscape(middle)+"?limit=4", alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("context = %d: %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Event  json.RawMessage   `json:"event"`
		Before []json.RawMessage `json:"events_before"`
		After  []json.RawMessage `json:"events_after"`
		State  []json.RawMessage `json:"state"`
		Start  string            `json:"start"`
		End    string            `json:"end"`
	}](t, rec)

	if got := eventIDs(t, []json.RawMessage{body.Event}); got[0] != middle {
		t.Fatalf("context returned %s, want %s", got[0], middle)
	}
	before := eventIDs(t, body.Before)
	after := eventIDs(t, body.After)
	if len(before) == 0 || len(after) == 0 {
		t.Fatalf("context returned %d before and %d after", len(before), len(after))
	}
	if before[0] != sent[2] {
		t.Fatalf("events_before starts with %s, want the event just before at %s", before[0], sent[2])
	}
	if after[0] != sent[4] {
		t.Fatalf("events_after starts with %s, want the event just after at %s", after[0], sent[4])
	}
	if len(body.State) == 0 {
		t.Fatal("context returned no state")
	}
	if body.Start == "" || body.End == "" {
		t.Fatalf("context tokens are start=%q end=%q", body.Start, body.End)
	}
}

func TestTheRequestedEventComesBackEvenAtLimitZero(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	sent := s.chatter(t, "alpha.test", alice, roomID, 3)

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/context/"+url.PathEscape(sent[1])+"?limit=0", alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("context at limit 0 = %d: %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Event  json.RawMessage   `json:"event"`
		Before []json.RawMessage `json:"events_before"`
		After  []json.RawMessage `json:"events_after"`
	}](t, rec)

	if got := eventIDs(t, []json.RawMessage{body.Event}); got[0] != sent[1] {
		t.Fatalf("context at limit 0 returned %s, want %s", got[0], sent[1])
	}
	if len(body.Before) != 0 || len(body.After) != 0 {
		t.Fatalf("limit 0 returned %d before and %d after", len(body.Before), len(body.After))
	}
}

func TestContextRefusesAnEventTheCallerMayNotSee(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})
	s.setVisibility(t, "alpha.test", alice, roomID, entity.HistoryVisibilityJoined)
	hidden := s.mustSend(t, "alpha.test", alice, roomID, "hidden", text("before bob"))

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/context/"+url.PathEscape(hidden), bob.AccessToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("context on a hidden event = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestAFilterSelectsWithinThePage(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	sent := s.chatter(t, "alpha.test", alice, roomID, 3)

	only := s.mustPage(t, "alpha.test", alice, roomID, `dir=b&limit=1000&filter={"types":["m.room.message"]}`)
	got := eventIDs(t, only.Chunk)
	if len(got) != len(sent) {
		t.Fatalf("a types filter returned %d events, want %d", len(got), len(sent))
	}

	none := s.mustPage(t, "alpha.test", alice, roomID, `dir=b&limit=1000&filter={"not_types":["m.room.*"]}`)
	if len(none.Chunk) != 0 {
		t.Fatalf("a wildcard not_types filter returned %d events", len(none.Chunk))
	}

	nobody := s.mustPage(t, "alpha.test", alice, roomID,
		`dir=b&limit=1000&filter={"not_senders":["`+"@alice:alpha.test"+`"]}`)
	if len(nobody.Chunk) != 0 {
		t.Fatalf("a not_senders filter returned %d events", len(nobody.Chunk))
	}
}

func TestAFilterDoesNotHideTheEventContextWasAskedFor(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	sent := s.chatter(t, "alpha.test", alice, roomID, 3)

	rec := s.get(t, "alpha.test",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/context/"+url.PathEscape(sent[1])+
			`?filter={"types":["m.room.member"]}`, alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("context = %d: %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Event json.RawMessage `json:"event"`
	}](t, rec)
	if got := eventIDs(t, []json.RawMessage{body.Event}); got[0] != sent[1] {
		t.Fatalf("a filter hid the event context was asked for: got %s", got[0])
	}
}

func TestTheMemberListCanBeReadAtAPointInTime(t *testing.T) {
	s := newServer(t)
	tenant, alice, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"preset": entity.PresetPublicChat})

	bob := s.register(t, "alpha.test", "bob", goodPassword)
	s.joinAs(t, tenant, roomID, bob.UserID)
	early := s.mustPage(t, "alpha.test", alice, roomID, "dir=b&limit=1")

	carol := s.register(t, "alpha.test", "carol", goodPassword)
	s.joinAs(t, tenant, roomID, carol.UserID)

	now := s.memberIDs(t, "alpha.test", alice, roomID, "")
	if len(now) != 3 {
		t.Fatalf("the current member list is %v", now)
	}
	then := s.memberIDs(t, "alpha.test", alice, roomID, "at="+url.QueryEscape(early.Start))
	if len(then) != 2 {
		t.Fatalf("the member list at an earlier point is %v, want two members", then)
	}
	for _, id := range then {
		if id == carol.UserID {
			t.Fatal("a point-in-time member list named someone who had not joined yet")
		}
	}
}
