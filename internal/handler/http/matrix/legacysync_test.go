package matrix_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

const legacySyncPath = "/_matrix/client/v3/sync"

type legacySyncBody struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join   map[string]legacyJoinBody   `json:"join"`
		Invite map[string]legacyInviteBody `json:"invite"`
		Knock  map[string]legacyKnockBody  `json:"knock"`
		Leave  map[string]legacyJoinBody   `json:"leave"`
	} `json:"rooms"`
	Presence      legacyEventsBody  `json:"presence"`
	AccountData   legacyEventsBody  `json:"account_data"`
	ToDevice      legacyEventsBody  `json:"to_device"`
	DeviceLists   *legacyDeviceBody `json:"device_lists"`
	OneTimeKeys   map[string]int    `json:"device_one_time_keys_count"`
	FallbackTypes []string          `json:"device_unused_fallback_key_types"`
}

type legacyDeviceBody struct {
	Changed []string `json:"changed"`
	Left    []string `json:"left"`
}

type legacyEventsBody struct {
	Events []json.RawMessage `json:"events"`
}

type legacyJoinBody struct {
	Timeline struct {
		Events    []json.RawMessage `json:"events"`
		PrevBatch string            `json:"prev_batch"`
		Limited   bool              `json:"limited"`
	} `json:"timeline"`
	State       legacyEventsBody `json:"state"`
	Ephemeral   legacyEventsBody `json:"ephemeral"`
	AccountData legacyEventsBody `json:"account_data"`
	Summary     *struct {
		Heroes       []string `json:"m.heroes"`
		JoinedCount  int      `json:"m.joined_member_count"`
		InvitedCount int      `json:"m.invited_member_count"`
	} `json:"summary"`
}

type legacyInviteBody struct {
	InviteState legacyEventsBody `json:"invite_state"`
}

type legacyKnockBody struct {
	KnockState legacyEventsBody `json:"knock_state"`
}

func (s *server) legacySyncRaw(t *testing.T, host, token string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	path := legacySyncPath
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return s.get(t, host, path, token)
}

func (s *server) legacySync(t *testing.T, host, token string, query url.Values) legacySyncBody {
	t.Helper()
	rec := s.legacySyncRaw(t, host, token, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy sync = %d: %s", rec.Code, rec.Body)
	}
	return decode[legacySyncBody](t, rec)
}

func since(token string) url.Values { return url.Values{"since": {token}} }

func typesOf(t *testing.T, raw []json.RawMessage) []string {
	t.Helper()
	out := make([]string, 0, len(raw))
	for _, event := range raw {
		var parsed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event, &parsed); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		out = append(out, parsed.Type)
	}
	return out
}

func has(list []string, wanted string) bool {
	for _, value := range list {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestALegacySyncCarriesTimelineStateAndAReusableToken(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{"name": "one"})
	s.mustSend(t, tenant.ServerName, alice.AccessToken, roomID, "1", text("hello"))

	first := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)
	if first.NextBatch == "" {
		t.Fatal("an initial legacy sync returned no next_batch")
	}
	room, ok := first.Rooms.Join[roomID]
	if !ok {
		t.Fatalf("%s is missing from an initial legacy sync", roomID)
	}
	if got := timelineBodies(t, room.Timeline.Events); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("timeline = %v, want [hello]", got)
	}
	if !has(typesOf(t, room.State.Events), entity.EventTypeCreate) {
		t.Fatalf("state is missing m.room.create: %v", typesOf(t, room.State.Events))
	}
	if room.Summary == nil || room.Summary.JoinedCount != 1 {
		t.Fatalf("summary = %+v, want one joined member", room.Summary)
	}

	quiet := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(first.NextBatch))
	if _, present := quiet.Rooms.Join[roomID]; present {
		t.Fatal("an unchanged room came back in an incremental legacy sync")
	}

	s.mustSend(t, tenant.ServerName, alice.AccessToken, roomID, "2", text("again"))
	second := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(quiet.NextBatch))
	if got := timelineBodies(t, second.Rooms.Join[roomID].Timeline.Events); len(got) != 1 || got[0] != "again" {
		t.Fatalf("incremental timeline = %v, want [again]", got)
	}
}

func TestAnInventedLegacySinceTokenIsRefused(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	rec := s.legacySyncRaw(t, tenant.ServerName, alice.AccessToken, since("not-a-token"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an invented since token = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestALegacySyncSeparatesInviteFromJoin(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, aliceToken, map[string]any{"name": "invited"})
	invited := s.do(t, http.MethodPost, tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/invite", aliceToken,
		map[string]any{"user_id": bob.UserID})
	if invited.Code != http.StatusOK {
		t.Fatalf("invite = %d: %s", invited.Code, invited.Body)
	}

	body := s.legacySync(t, tenant.ServerName, bob.AccessToken, nil)
	if _, ok := body.Rooms.Join[roomID]; ok {
		t.Fatal("an invited room appeared under join")
	}
	room, ok := body.Rooms.Invite[roomID]
	if !ok {
		t.Fatalf("%s is missing from invite: %s", roomID, mustJSON(t, body.Rooms))
	}
	if !has(typesOf(t, room.InviteState.Events), entity.EventTypeMember) {
		t.Fatalf("invite_state has no membership: %v", typesOf(t, room.InviteState.Events))
	}
}

func TestALegacySyncReportsALeaveOnceAndThenForgetsIt(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, aliceToken,
		map[string]any{"name": "leaving", "preset": "public_chat"})
	s.joinAs(t, tenant, roomID, bob.UserID)

	settled := s.legacySync(t, tenant.ServerName, bob.AccessToken, nil)
	if _, ok := settled.Rooms.Join[roomID]; !ok {
		t.Fatalf("%s is missing from bob's initial sync", roomID)
	}

	left := s.do(t, http.MethodPost, tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/leave", bob.AccessToken, map[string]any{})
	if left.Code != http.StatusOK {
		t.Fatalf("leave = %d: %s", left.Code, left.Body)
	}

	departure := s.legacySync(t, tenant.ServerName, bob.AccessToken, since(settled.NextBatch))
	if _, ok := departure.Rooms.Leave[roomID]; !ok {
		t.Fatalf("the leave is missing: %s", mustJSON(t, departure.Rooms))
	}

	s.mustSend(t, tenant.ServerName, aliceToken, roomID, "after", text("without bob"))

	after := s.legacySync(t, tenant.ServerName, bob.AccessToken, since(departure.NextBatch))
	if _, ok := after.Rooms.Leave[roomID]; ok {
		t.Fatalf("a settled leave came back a second time: %s", mustJSON(t, after.Rooms))
	}
}

func TestALegacySyncCarriesGlobalAndRoomAccountData(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")
	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, map[string]any{})

	base := "/_matrix/client/v3/user/" + url.PathEscape(alice.UserID)
	global := s.do(t, http.MethodPut, tenant.ServerName, base+"/account_data/org.example.global",
		alice.AccessToken, map[string]any{"value": "one"})
	if global.Code != http.StatusOK {
		t.Fatalf("set global account data = %d: %s", global.Code, global.Body)
	}
	roomData := s.do(t, http.MethodPut, tenant.ServerName,
		base+"/rooms/"+url.PathEscape(roomID)+"/account_data/org.example.room",
		alice.AccessToken, map[string]any{"value": "two"})
	if roomData.Code != http.StatusOK {
		t.Fatalf("set room account data = %d: %s", roomData.Code, roomData.Body)
	}

	body := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)
	if !has(typesOf(t, body.AccountData.Events), "org.example.global") {
		t.Fatalf("global account data is missing: %v", typesOf(t, body.AccountData.Events))
	}
	room := body.Rooms.Join[roomID]
	if !has(typesOf(t, room.AccountData.Events), "org.example.room") {
		t.Fatalf("room account data is missing: %v", typesOf(t, room.AccountData.Events))
	}
	if has(typesOf(t, body.AccountData.Events), "org.example.room") {
		t.Fatal("room account data leaked into the global section")
	}
}

func TestALegacySyncCarriesReceiptsAndTypingAsEphemeral(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, aliceID := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, tenant.ServerName, aliceToken, map[string]any{})
	eventID := s.mustSend(t, tenant.ServerName, aliceToken, roomID, "1", text("hello"))

	alice := sessionBody{UserID: aliceID, AccessToken: aliceToken}
	s.mustSendReceipt(t, tenant.ServerName, alice, roomID, entity.ReceiptRead, eventID, map[string]any{})

	typed := s.do(t, http.MethodPut, tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/typing/"+url.PathEscape(aliceID),
		aliceToken, map[string]any{"typing": true, "timeout": 30000})
	if typed.Code != http.StatusOK {
		t.Fatalf("typing = %d: %s", typed.Code, typed.Body)
	}

	body := s.legacySync(t, tenant.ServerName, aliceToken, nil)
	kinds := typesOf(t, body.Rooms.Join[roomID].Ephemeral.Events)
	if !has(kinds, entity.EventTypeReceipt) || !has(kinds, entity.EventTypeTyping) {
		t.Fatalf("ephemeral = %v, want a receipt and typing", kinds)
	}
	for _, raw := range body.Rooms.Join[roomID].Ephemeral.Events {
		if json.Valid(raw) && hasKey(t, raw, "room_id") {
			t.Fatalf("an ephemeral event carries room_id: %s", raw)
		}
	}
}

func TestALegacySyncFilterNarrowsTheTimelineAndState(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, tenant.ServerName, aliceToken, map[string]any{})
	s.mustSend(t, tenant.ServerName, aliceToken, roomID, "1", text("hello"))

	filter := `{"room":{"timeline":{"types":["m.room.message"],"limit":10},"state":{"types":[]}}}`
	body := s.legacySync(t, tenant.ServerName, aliceToken, url.Values{"filter": {filter}})
	room := body.Rooms.Join[roomID]
	for _, kind := range typesOf(t, room.Timeline.Events) {
		if kind != entity.EventTypeMessage {
			t.Fatalf("the timeline filter let %s through", kind)
		}
	}
	if len(room.State.Events) != 0 {
		t.Fatalf("an empty state type list still returned %v", typesOf(t, room.State.Events))
	}
}

func TestALegacySyncDeliversAToDeviceMessageAndOneTimeKeyCounts(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	sent := s.do(t, http.MethodPut, tenant.ServerName,
		"/_matrix/client/v3/sendToDevice/m.room_key_request/txn-1", alice.AccessToken,
		map[string]any{"messages": map[string]any{
			alice.UserID: map[string]any{alice.DeviceID: map[string]any{"body": "secret"}},
		}})
	if sent.Code != http.StatusOK {
		t.Fatalf("sendToDevice = %d: %s", sent.Code, sent.Body)
	}

	body := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)
	if len(body.ToDevice.Events) != 1 {
		t.Fatalf("to_device carried %d events, want 1: %s", len(body.ToDevice.Events),
			mustJSON(t, body.ToDevice))
	}
	if body.OneTimeKeys == nil {
		t.Fatal("device_one_time_keys_count is absent")
	}
	if body.FallbackTypes == nil {
		t.Fatal("device_unused_fallback_key_types is absent")
	}

	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(body.NextBatch))
	if len(settled.ToDevice.Events) != 0 {
		t.Fatalf("an acknowledged to-device message came back: %s", mustJSON(t, settled.ToDevice))
	}
}

func TestALegacySyncRedeliversAnUnacknowledgedToDeviceMessage(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	alice := s.loginAlice(t, "PHONE")

	sent := s.do(t, http.MethodPut, tenant.ServerName,
		"/_matrix/client/v3/sendToDevice/m.room_key/txn-1", alice.AccessToken,
		map[string]any{"messages": map[string]any{
			alice.UserID: map[string]any{alice.DeviceID: map[string]any{"body": "key"}},
		}})
	if sent.Code != http.StatusOK {
		t.Fatalf("sendToDevice = %d: %s", sent.Code, sent.Body)
	}

	first := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)
	if len(first.ToDevice.Events) != 1 {
		t.Fatalf("first delivery carried %d events, want 1", len(first.ToDevice.Events))
	}

	replay := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)
	if len(replay.ToDevice.Events) != 1 {
		t.Fatalf("a client that never acknowledged lost its message: %s", mustJSON(t, replay.ToDevice))
	}
	if string(replay.ToDevice.Events[0]) != string(first.ToDevice.Events[0]) {
		t.Fatalf("redelivery differed:\n%s\n%s", first.ToDevice.Events[0], replay.ToDevice.Events[0])
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func hasKey(t *testing.T, raw json.RawMessage, key string) bool {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	_, ok := fields[key]
	if ok {
		return true
	}
	content, ok := fields["content"]
	if !ok {
		return false
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(content, &nested); err != nil {
		return false
	}
	_, ok = nested[key]
	return ok
}
