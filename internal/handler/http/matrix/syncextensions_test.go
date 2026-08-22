package matrix_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

type toDeviceExtension struct {
	NextBatch string            `json:"next_batch"`
	Events    []json.RawMessage `json:"events"`
}

type e2eeExtension struct {
	DeviceLists *struct {
		Changed []string `json:"changed"`
		Left    []string `json:"left"`
	} `json:"device_lists"`
	OneTimeKeys   map[string]int `json:"device_one_time_keys_count"`
	FallbackTypes []string       `json:"device_unused_fallback_key_types"`
}

type accountDataExtension struct {
	Global []json.RawMessage            `json:"global"`
	Rooms  map[string][]json.RawMessage `json:"rooms"`
}

type ephemeralExtension struct {
	Rooms map[string]json.RawMessage `json:"rooms"`
}

func extension[T any](t *testing.T, body syncBody, name string) T {
	t.Helper()
	raw, ok := body.Extensions[name]
	if !ok {
		t.Fatalf("the %s extension is missing: %v", name, body.Extensions)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode the %s extension: %v", name, err)
	}
	return out
}

func withExtensions(base map[string]any, extensions map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	out["extensions"] = extensions
	return out
}

func (s *server) sendToDevice(t *testing.T, host string, sender sessionBody, eventType, txnID string,
	messages map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/sendToDevice/"+url.PathEscape(eventType)+"/"+url.PathEscape(txnID),
		sender.AccessToken, map[string]any{"messages": messages})
}

func (s *server) mustSendToDevice(t *testing.T, host string, sender sessionBody, eventType, txnID string,
	messages map[string]any,
) {
	t.Helper()
	if rec := s.sendToDevice(t, host, sender, eventType, txnID, messages); rec.Code != http.StatusOK {
		t.Fatalf("sendToDevice %s = %d: %s", txnID, rec.Code, rec.Body)
	}
}

func toDeviceEnabled(since string) map[string]any {
	body := map[string]any{"enabled": true}
	if since != "" {
		body["since"] = since
	}
	return map[string]any{"to_device": body}
}

func TestAToDeviceMessageReachesTheNamedDeviceAndNobodyElse(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	phone := s.loginAlice(t, "PHONE")
	laptop := s.loginAlice(t, "LAPTOP")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	s.mustSendToDevice(t, tenant.ServerName, bob, "m.room_key", "one", map[string]any{
		phone.UserID: map[string]any{phone.DeviceID: map[string]any{"body": "for the phone"}},
	})

	delivered := extension[toDeviceExtension](t,
		s.syncOnce(t, tenant.ServerName, phone.AccessToken, "", withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
		"to_device")
	if len(delivered.Events) != 1 {
		t.Fatalf("the named device received %d messages, want 1", len(delivered.Events))
	}
	var event struct {
		Sender  string          `json:"sender"`
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(delivered.Events[0], &event); err != nil {
		t.Fatalf("decode the to-device event: %v", err)
	}
	if event.Sender != bob.UserID {
		t.Fatalf("sender = %q, want %q stamped by the server", event.Sender, bob.UserID)
	}
	if event.Type != "m.room_key" {
		t.Fatalf("type = %q, want m.room_key", event.Type)
	}

	other := extension[toDeviceExtension](t,
		s.syncOnce(t, tenant.ServerName, laptop.AccessToken, "", withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
		"to_device")
	if len(other.Events) != 0 {
		t.Fatalf("a message addressed to one device reached another: %s", other.Events)
	}
}

func TestAToDeviceWildcardReachesEveryDeviceOfOneUserOnly(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	phone := s.loginAlice(t, "PHONE")
	laptop := s.loginAlice(t, "LAPTOP")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	s.mustSendToDevice(t, tenant.ServerName, bob, "m.room_key", "wild", map[string]any{
		phone.UserID: map[string]any{"*": map[string]any{"body": "everyone"}},
	})

	for _, session := range []sessionBody{phone, laptop} {
		got := extension[toDeviceExtension](t,
			s.syncOnce(t, tenant.ServerName, session.AccessToken, "", withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
			"to_device")
		if len(got.Events) != 1 {
			t.Fatalf("%s received %d messages, want 1", session.DeviceID, len(got.Events))
		}
	}

	sender := extension[toDeviceExtension](t,
		s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
		"to_device")
	if len(sender.Events) != 0 {
		t.Fatalf("a wildcard for one user reached another user: %s", sender.Events)
	}
}

func TestTheSameToDeviceTransactionSendsOneMessage(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	phone := s.loginAlice(t, "PHONE")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	messages := map[string]any{
		phone.UserID: map[string]any{phone.DeviceID: map[string]any{"body": "once"}},
	}
	s.mustSendToDevice(t, tenant.ServerName, bob, "m.room_key", "same", messages)
	s.mustSendToDevice(t, tenant.ServerName, bob, "m.room_key", "same", messages)

	got := extension[toDeviceExtension](t,
		s.syncOnce(t, tenant.ServerName, phone.AccessToken, "", withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
		"to_device")
	if len(got.Events) != 1 {
		t.Fatalf("a replayed transaction delivered %d messages, want 1", len(got.Events))
	}
}

func TestAToDeviceMessageSurvivesACrashAndIsDeliveredOnce(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	phone := s.loginAlice(t, "PHONE")
	laptop := s.loginAlice(t, "LAPTOP")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	s.mustSendToDevice(t, tenant.ServerName, bob, "m.room_key", "crash", map[string]any{
		phone.UserID: map[string]any{phone.DeviceID: map[string]any{"body": "the room key"}},
	})

	first := extension[toDeviceExtension](t,
		s.syncOnce(t, tenant.ServerName, phone.AccessToken, "", withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
		"to_device")
	if len(first.Events) != 1 {
		t.Fatalf("first delivery carried %d messages, want 1", len(first.Events))
	}

	restarted := reopen(t, s)

	replay := extension[toDeviceExtension](t,
		restarted.syncOnce(t, tenant.ServerName, phone.AccessToken, "",
			withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
		"to_device")
	if len(replay.Events) != 1 {
		t.Fatalf("a client that crashed before acknowledging lost its message: %s", replay.Events)
	}
	if string(replay.Events[0]) != string(first.Events[0]) {
		t.Fatalf("redelivery differed:\n%s\n%s", first.Events[0], replay.Events[0])
	}

	acknowledged := extension[toDeviceExtension](t,
		restarted.syncOnce(t, tenant.ServerName, phone.AccessToken, "",
			withExtensions(window(1, 0, 9), toDeviceEnabled(replay.NextBatch))),
		"to_device")
	if len(acknowledged.Events) != 0 {
		t.Fatalf("an acknowledged message came back: %s", acknowledged.Events)
	}

	stale := extension[toDeviceExtension](t,
		restarted.syncOnce(t, tenant.ServerName, phone.AccessToken, "",
			withExtensions(window(1, 0, 9), toDeviceEnabled(first.NextBatch))),
		"to_device")
	if len(stale.Events) != 0 {
		t.Fatalf("replaying the old since after acknowledgement resurrected the message: %s", stale.Events)
	}

	untouched := extension[toDeviceExtension](t,
		restarted.syncOnce(t, tenant.ServerName, laptop.AccessToken, "", withExtensions(window(1, 0, 9), toDeviceEnabled(""))),
		"to_device")
	if len(untouched.Events) != 0 {
		t.Fatalf("another device of the same user received the message: %s", untouched.Events)
	}
}

func TestOneTimeKeyCountsNeverLetAClientRunOutSilently(t *testing.T) {
	s := newServer(t)
	tenant, _, _ := s.resident(t, "alpha.test", "alice")
	phone := s.loginAlice(t, "PHONE")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	request := withExtensions(window(1, 0, 9), map[string]any{"e2ee": map[string]any{"enabled": true}})

	empty := extension[e2eeExtension](t,
		s.syncOnce(t, tenant.ServerName, phone.AccessToken, "", request), "e2ee")
	if empty.OneTimeKeys == nil {
		t.Fatal("device_one_time_keys_count is absent from an initial response")
	}
	if len(empty.OneTimeKeys) != 0 {
		t.Fatalf("a device with no keys reported %v", empty.OneTimeKeys)
	}
	if empty.FallbackTypes == nil {
		t.Fatal("device_unused_fallback_key_types is absent from an initial response")
	}

	id := newIdentity(t, 0, phone.UserID, phone.DeviceID, 2)
	names := id.oneTimeIDs()
	s.mustUpload(t, tenant.ServerName, phone.AccessToken, map[string]any{
		"device_keys":   id.device,
		"one_time_keys": id.oneTime,
	})

	uploaded := extension[e2eeExtension](t,
		s.syncOnce(t, tenant.ServerName, phone.AccessToken, "", request), "e2ee")
	if uploaded.OneTimeKeys["signed_curve25519"] != 2 {
		t.Fatalf("after uploading two keys the count = %v", uploaded.OneTimeKeys)
	}

	claimed := s.do(t, http.MethodPost, tenant.ServerName, "/_matrix/client/v3/keys/claim",
		bob.AccessToken, map[string]any{"one_time_keys": map[string]any{
			phone.UserID: map[string]any{phone.DeviceID: "signed_curve25519"},
		}})
	if claimed.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", claimed.Code, claimed.Body)
	}

	after := extension[e2eeExtension](t,
		s.syncOnce(t, tenant.ServerName, phone.AccessToken, "", request), "e2ee")
	if after.OneTimeKeys["signed_curve25519"] != 1 {
		t.Fatalf("after a claim the count = %v, want one key left", after.OneTimeKeys)
	}

	drained := s.do(t, http.MethodPost, tenant.ServerName, "/_matrix/client/v3/keys/claim",
		bob.AccessToken, map[string]any{"one_time_keys": map[string]any{
			phone.UserID: map[string]any{phone.DeviceID: "signed_curve25519"},
		}})
	if drained.Code != http.StatusOK {
		t.Fatalf("second claim = %d: %s", drained.Code, drained.Body)
	}

	exhausted := extension[e2eeExtension](t,
		s.syncOnce(t, tenant.ServerName, phone.AccessToken, "", request), "e2ee")
	if exhausted.OneTimeKeys["signed_curve25519"] != 0 {
		t.Fatalf("an exhausted device reported %v rather than nothing left", exhausted.OneTimeKeys)
	}
	_ = names
}

func TestANewDeviceMakesItsOwnerAppearInDeviceLists(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, aliceID := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, aliceToken,
		map[string]any{"preset": entity.PresetPublicChat})
	s.joinAs(t, tenant, roomID, bob.UserID)

	request := withExtensions(window(1, 0, 9), map[string]any{"e2ee": map[string]any{"enabled": true}})
	settled := s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", request)

	s.loginAlice(t, "FRESH")

	woken := s.syncOnce(t, tenant.ServerName, bob.AccessToken, settled.Pos, request)
	lists := extension[e2eeExtension](t, woken, "e2ee")
	if lists.DeviceLists == nil {
		t.Fatalf("device_lists is absent after a co-member logged in a new device: %v", woken.Extensions)
	}
	if !has(lists.DeviceLists.Changed, aliceID) {
		t.Fatalf("changed = %v, want %s", lists.DeviceLists.Changed, aliceID)
	}
	_ = aliceToken
}

func TestAnExtensionOnlyChangeEndsALongPoll(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, aliceID := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, tenant.ServerName, aliceToken, map[string]any{})

	request := withExtensions(window(1, 0, 9),
		map[string]any{"account_data": map[string]any{"enabled": true}})
	settled := s.syncOnce(t, tenant.ServerName, aliceToken, "", request)

	base := "/_matrix/client/v3/user/" + url.PathEscape(aliceID)
	stored := s.do(t, http.MethodPut, tenant.ServerName, base+"/account_data/org.example.late",
		aliceToken, map[string]any{"value": "late"})
	if stored.Code != http.StatusOK {
		t.Fatalf("set account data = %d: %s", stored.Code, stored.Body)
	}

	waited := withExtensions(window(1, 0, 9),
		map[string]any{"account_data": map[string]any{"enabled": true}})
	waited["timeout"] = 2000
	body := s.syncOnce(t, tenant.ServerName, aliceToken, settled.Pos, waited)

	data := extension[accountDataExtension](t, body, "account_data")
	if !has(typesOf(t, data.Global), "org.example.late") {
		t.Fatalf("account data did not end the long poll: %v", typesOf(t, data.Global))
	}
	_ = roomID
}

func TestEnablingAnExtensionMidConnectionBackfills(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, aliceID := s.resident(t, "alpha.test", "alice")
	s.createRoom(t, tenant.ServerName, aliceToken, map[string]any{})

	bare := window(1, 0, 9)
	settled := s.syncOnce(t, tenant.ServerName, aliceToken, "", bare)

	base := "/_matrix/client/v3/user/" + url.PathEscape(aliceID)
	for _, name := range []string{"org.example.one", "org.example.two"} {
		stored := s.do(t, http.MethodPut, tenant.ServerName, base+"/account_data/"+name,
			aliceToken, map[string]any{"value": name})
		if stored.Code != http.StatusOK {
			t.Fatalf("set %s = %d: %s", name, stored.Code, stored.Body)
		}
	}

	quiet := s.syncOnce(t, tenant.ServerName, aliceToken, settled.Pos, bare)
	if _, present := quiet.Extensions["account_data"]; present {
		t.Fatal("a disabled extension answered anyway")
	}

	enabled := withExtensions(window(1, 0, 9),
		map[string]any{"account_data": map[string]any{"enabled": true}})
	body := s.syncOnce(t, tenant.ServerName, aliceToken, quiet.Pos, enabled)

	data := extension[accountDataExtension](t, body, "account_data")
	kinds := typesOf(t, data.Global)
	for _, name := range []string{"org.example.one", "org.example.two"} {
		if !has(kinds, name) {
			t.Fatalf("enabling the extension mid-connection lost %s: %v", name, kinds)
		}
	}
}

func TestAPrivateReceiptReachesItsOwnerAndNobodyElse(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, aliceID := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, aliceToken,
		map[string]any{"preset": entity.PresetPublicChat})
	s.joinAs(t, tenant, roomID, bob.UserID)
	eventID := s.mustSend(t, tenant.ServerName, aliceToken, roomID, "1", text("hello"))

	alice := sessionBody{UserID: aliceID, AccessToken: aliceToken}
	s.mustSendReceipt(t, tenant.ServerName, alice, roomID, entity.ReceiptReadPrivate, eventID, map[string]any{})
	s.mustSendReceipt(t, tenant.ServerName, alice, roomID, entity.ReceiptRead, eventID, map[string]any{})

	request := withExtensions(window(1, 0, 9), map[string]any{"receipts": map[string]any{"enabled": true}})

	own := extension[ephemeralExtension](t,
		s.syncOnce(t, tenant.ServerName, aliceToken, "", request), "receipts")
	if own.Rooms[roomID] == nil {
		t.Fatalf("the owner did not receive their own private receipt: %v", own.Rooms)
	}
	if !carriesReceipt(t, own.Rooms[roomID], entity.ReceiptReadPrivate, aliceID) {
		t.Fatalf("the private receipt is missing from the owner's response: %s", own.Rooms[roomID])
	}

	theirs := extension[ephemeralExtension](t,
		s.syncOnce(t, tenant.ServerName, bob.AccessToken, "", request), "receipts")
	event := theirs.Rooms[roomID]
	if event == nil {
		t.Fatalf("the public receipt did not reach another member: %v", theirs.Rooms)
	}
	if !carriesReceipt(t, event, entity.ReceiptRead, aliceID) {
		t.Fatalf("the public receipt is missing: %s", event)
	}
	if carriesReceipt(t, event, entity.ReceiptReadPrivate, aliceID) {
		t.Fatalf("a private receipt reached another user: %s", event)
	}
}

func TestNoEphemeralExtensionEventCarriesARoomID(t *testing.T) {
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

	request := withExtensions(window(1, 0, 9), map[string]any{
		"receipts": map[string]any{"enabled": true},
		"typing":   map[string]any{"enabled": true},
	})
	body := s.syncOnce(t, tenant.ServerName, aliceToken, "", request)

	for _, name := range []string{"receipts", "typing"} {
		section := extension[ephemeralExtension](t, body, name)
		for id, raw := range section.Rooms {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("decode %s for %s: %v", name, id, err)
			}
			if _, present := fields["room_id"]; present {
				t.Fatalf("a %s event carries room_id: %s", name, raw)
			}
		}
	}
}

func TestTypingStoppingIsDeliveredAsAnEmptySet(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, aliceID := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, tenant.ServerName, aliceToken, map[string]any{})

	path := "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) + "/typing/" + url.PathEscape(aliceID)
	started := s.do(t, http.MethodPut, tenant.ServerName, path, aliceToken,
		map[string]any{"typing": true, "timeout": 30000})
	if started.Code != http.StatusOK {
		t.Fatalf("start typing = %d: %s", started.Code, started.Body)
	}

	request := withExtensions(window(1, 0, 9), map[string]any{"typing": map[string]any{"enabled": true}})
	settled := s.syncOnce(t, tenant.ServerName, aliceToken, "", request)
	if typing := extension[ephemeralExtension](t, settled, "typing"); typing.Rooms[roomID] == nil {
		t.Fatalf("the typing set is missing: %v", typing.Rooms)
	}

	stopped := s.do(t, http.MethodPut, tenant.ServerName, path, aliceToken,
		map[string]any{"typing": false})
	if stopped.Code != http.StatusOK {
		t.Fatalf("stop typing = %d: %s", stopped.Code, stopped.Body)
	}

	after := s.syncOnce(t, tenant.ServerName, aliceToken, settled.Pos, request)
	raw := extension[ephemeralExtension](t, after, "typing").Rooms[roomID]
	if raw == nil {
		t.Fatalf("stopping typing was not delivered: %v", after.Extensions)
	}
	var event struct {
		Content struct {
			UserIDs []string `json:"user_ids"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode the typing event: %v", err)
	}
	if len(event.Content.UserIDs) != 0 {
		t.Fatalf("user_ids = %v, want an empty set", event.Content.UserIDs)
	}
}

func TestRoomAccountDataOnlyAppearsForRoomsInTheResponse(t *testing.T) {
	s := newServer(t)
	tenant, aliceToken, aliceID := s.resident(t, "alpha.test", "alice")
	shown := s.createRoom(t, tenant.ServerName, aliceToken, map[string]any{"name": "shown"})

	base := "/_matrix/client/v3/user/" + url.PathEscape(aliceID)
	stored := s.do(t, http.MethodPut, tenant.ServerName,
		base+"/rooms/"+url.PathEscape(shown)+"/account_data/org.example.room",
		aliceToken, map[string]any{"value": "one"})
	if stored.Code != http.StatusOK {
		t.Fatalf("set room account data = %d: %s", stored.Code, stored.Body)
	}

	request := withExtensions(window(1, 0, 9), map[string]any{
		"account_data": map[string]any{"enabled": true, "rooms": []string{shown}},
	})
	data := extension[accountDataExtension](t,
		s.syncOnce(t, tenant.ServerName, aliceToken, "", request), "account_data")
	if len(data.Rooms[shown]) == 0 {
		t.Fatalf("the scoped room is missing its account data: %v", data.Rooms)
	}
	for roomID := range data.Rooms {
		if roomID != shown {
			t.Fatalf("account data arrived for an unscoped room %s", roomID)
		}
	}
}

func carriesReceipt(t *testing.T, raw json.RawMessage, receiptType, userID string) bool {
	t.Helper()
	var event struct {
		Content map[string]map[string]map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode the receipt event: %v", err)
	}
	for _, types := range event.Content {
		if _, ok := types[receiptType][userID]; ok {
			return true
		}
	}
	return false
}
