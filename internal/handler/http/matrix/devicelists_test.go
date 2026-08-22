package matrix_test

import (
	"net/http"
	"net/url"
	"sort"
	"testing"
	"time"
)

var publicRoom = map[string]any{"preset": "public_chat"}

type keyChangesBody struct {
	Changed []string `json:"changed"`
	Left    []string `json:"left"`
}

func (s *server) joinRoom(t *testing.T, host, roomID, token string) {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/rooms/"+roomID+"/join",
		token, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", rec.Code, rec.Body)
	}
}

func (s *server) leaveRoom(t *testing.T, host, roomID, token string) {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/rooms/"+roomID+"/leave",
		token, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("leave = %d: %s", rec.Code, rec.Body)
	}
}

func (s *server) uploadIdentity(t *testing.T, host string, session sessionBody, seed uint64) {
	t.Helper()
	id := newIdentity(t, seed, session.UserID, session.DeviceID, 0)
	s.mustUpload(t, host, session.AccessToken, map[string]any{"device_keys": id.device})
}

func (s *server) keyChanges(t *testing.T, host, token, from, to string) keyChangesBody {
	t.Helper()
	rec := s.get(t, host, "/_matrix/client/v3/keys/changes?"+
		url.Values{"from": {from}, "to": {to}}.Encode(), token)
	if rec.Code != http.StatusOK {
		t.Fatalf("keys/changes = %d: %s", rec.Code, rec.Body)
	}
	return decode[keyChangesBody](t, rec)
}

func deviceLists(t *testing.T, body legacySyncBody) legacyDeviceBody {
	t.Helper()
	if body.DeviceLists == nil {
		return legacyDeviceBody{}
	}
	return *body.DeviceLists
}

func sameSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAJoiningUserIsAnnouncedOnceToEveryoneAlreadyInTheRoom(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	s.uploadIdentity(t, tenant.ServerName, bob, 41)

	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, publicRoom)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)

	announced := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(settled.NextBatch))
	if !has(deviceLists(t, announced).Changed, bob.UserID) {
		t.Fatalf("a joining user was not announced: %v", deviceLists(t, announced))
	}

	again := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(announced.NextBatch))
	if has(deviceLists(t, again).Changed, bob.UserID) {
		t.Fatalf("a joining user was announced twice: %v", deviceLists(t, again))
	}
}

func TestJoiningARoomAnnouncesEveryoneAlreadyInIt(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	s.uploadIdentity(t, tenant.ServerName, bob, 42)

	roomID := s.createRoom(t, tenant.ServerName, bob.AccessToken, publicRoom)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	s.joinRoom(t, tenant.ServerName, roomID, alice.AccessToken)

	announced := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(settled.NextBatch))
	if !has(deviceLists(t, announced).Changed, bob.UserID) {
		t.Fatalf("a room I joined did not announce its members: %v", deviceLists(t, announced))
	}
}

func TestALeavingUserIsAnnouncedAsLeftAndTheirLaterKeysAreNotChanged(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	s.uploadIdentity(t, tenant.ServerName, bob, 43)

	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, publicRoom)
	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	s.leaveRoom(t, tenant.ServerName, roomID, bob.AccessToken)

	departed := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(settled.NextBatch))
	if !has(deviceLists(t, departed).Left, bob.UserID) {
		t.Fatalf("a departing user was not announced as left: %v", deviceLists(t, departed))
	}

	s.uploadIdentity(t, tenant.ServerName, bob, 44)
	after := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(departed.NextBatch))
	if has(deviceLists(t, after).Changed, bob.UserID) {
		t.Fatalf("a departed user's keys were announced as changed: %v", deviceLists(t, after))
	}
}

func TestLeavingARoomAnnouncesEveryoneNoLongerShared(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	s.uploadIdentity(t, tenant.ServerName, bob, 45)

	roomID := s.createRoom(t, tenant.ServerName, bob.AccessToken, publicRoom)
	s.joinRoom(t, tenant.ServerName, roomID, alice.AccessToken)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	s.leaveRoom(t, tenant.ServerName, roomID, alice.AccessToken)

	departed := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(settled.NextBatch))
	if !has(deviceLists(t, departed).Left, bob.UserID) {
		t.Fatalf("leaving a room did not announce who I stopped sharing with: %v",
			deviceLists(t, departed))
	}
}

func TestALeaveAndRejoinInOneWindowIsAnnouncedAsChanged(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	s.uploadIdentity(t, tenant.ServerName, bob, 46)

	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, publicRoom)
	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	s.leaveRoom(t, tenant.ServerName, roomID, bob.AccessToken)
	s.uploadIdentity(t, tenant.ServerName, bob, 47)
	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)

	rejoined := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(settled.NextBatch))
	lists := deviceLists(t, rejoined)
	if !has(lists.Changed, bob.UserID) {
		t.Fatalf("a leave and rejoin was not announced as changed: %v", lists)
	}
	if has(lists.Left, bob.UserID) {
		t.Fatalf("a rejoined user was also announced as left: %v", lists)
	}

	answered := s.queryKeys(t, tenant.ServerName, alice.AccessToken,
		map[string][]string{bob.UserID: {}})
	if _, ok := answered.DeviceKeys[bob.UserID][bob.DeviceID]; !ok {
		t.Fatalf("a rejoined user's keys are missing: %v", answered.DeviceKeys)
	}
}

func TestADeviceAddedWhileItsOwnerIsJoiningIsAnnouncedOnce(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, publicRoom)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)
	s.uploadIdentity(t, tenant.ServerName, bob, 48)

	announced := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(settled.NextBatch))
	lists := deviceLists(t, announced)
	seen := 0
	for _, userID := range lists.Changed {
		if userID == bob.UserID {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("a device added while joining appeared %d times in %v", seen, lists.Changed)
	}
	if has(lists.Left, bob.UserID) {
		t.Fatalf("a joining user was also announced as left: %v", lists)
	}
}

func TestKeyChangesAgreesWithSyncAtEveryPosition(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	carol := s.register(t, tenant.ServerName, "carol", goodPassword)
	s.uploadIdentity(t, tenant.ServerName, bob, 51)
	s.uploadIdentity(t, tenant.ServerName, carol, 52)

	mine := s.createRoom(t, tenant.ServerName, alice.AccessToken, publicRoom)
	theirs := s.createRoom(t, tenant.ServerName, carol.AccessToken, publicRoom)

	steps := []struct {
		name string
		run  func()
	}{
		{"bob joins my room", func() { s.joinRoom(t, tenant.ServerName, mine, bob.AccessToken) }},
		{"bob uploads keys", func() { s.uploadIdentity(t, tenant.ServerName, bob, 53) }},
		{"i join carol's room", func() { s.joinRoom(t, tenant.ServerName, theirs, alice.AccessToken) }},
		{"bob leaves my room", func() { s.leaveRoom(t, tenant.ServerName, mine, bob.AccessToken) }},
		{"bob uploads keys again", func() { s.uploadIdentity(t, tenant.ServerName, bob, 54) }},
		{"i leave carol's room", func() { s.leaveRoom(t, tenant.ServerName, theirs, alice.AccessToken) }},
		{"bob rejoins my room", func() { s.joinRoom(t, tenant.ServerName, mine, bob.AccessToken) }},
	}

	from := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil).NextBatch
	for _, step := range steps {
		step.run()
		body := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(from))
		synced := deviceLists(t, body)
		standalone := s.keyChanges(t, tenant.ServerName, alice.AccessToken, from, body.NextBatch)
		if !sameSet(synced.Changed, standalone.Changed) {
			t.Fatalf("after %s: sync changed %v, keys/changes %v",
				step.name, synced.Changed, standalone.Changed)
		}
		if !sameSet(synced.Left, standalone.Left) {
			t.Fatalf("after %s: sync left %v, keys/changes %v",
				step.name, synced.Left, standalone.Left)
		}
		from = body.NextBatch
	}
}

func TestKeyChangesHonoursItsUpperBound(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, publicRoom)
	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)

	from := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)
	to := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(from.NextBatch))

	s.uploadIdentity(t, tenant.ServerName, bob, 55)

	bounded := s.keyChanges(t, tenant.ServerName, alice.AccessToken, from.NextBatch, to.NextBatch)
	if has(bounded.Changed, bob.UserID) || has(bounded.Left, bob.UserID) {
		t.Fatalf("a change after the upper bound was reported: %v", bounded)
	}

	open := s.legacySync(t, tenant.ServerName, alice.AccessToken, since(to.NextBatch))
	widened := s.keyChanges(t, tenant.ServerName, alice.AccessToken, from.NextBatch, open.NextBatch)
	if !has(widened.Changed, bob.UserID) {
		t.Fatalf("widening the bound did not reveal the change: %v", widened)
	}
}

func TestKeyChangesRequiresBothBounds(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	for name, query := range map[string]url.Values{
		"neither":    {},
		"only from":  {"from": {settled.NextBatch}},
		"only to":    {"to": {settled.NextBatch}},
		"a bad from": {"from": {"not-a-token"}, "to": {settled.NextBatch}},
		"a bad to":   {"from": {settled.NextBatch}, "to": {"not-a-token"}},
	} {
		rec := s.get(t, tenant.ServerName, "/_matrix/client/v3/keys/changes?"+query.Encode(),
			alice.AccessToken)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("keys/changes with %s = %d, want 400: %s", name, rec.Code, rec.Body)
		}
	}
}

func TestADeviceChangeEndsACoMembersParkedSync(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	alice := s.register(t, tenant.ServerName, "alice", goodPassword)
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, alice.AccessToken, publicRoom)
	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)
	settled := s.legacySync(t, tenant.ServerName, alice.AccessToken, nil)

	type outcome struct {
		body  legacySyncBody
		spent time.Duration
	}
	done := make(chan outcome, 1)
	go func() {
		started := time.Now()
		body := s.legacySync(t, tenant.ServerName, alice.AccessToken, url.Values{
			"since":   {settled.NextBatch},
			"timeout": {"2000"},
		})
		done <- outcome{body: body, spent: time.Since(started)}
	}()

	time.Sleep(150 * time.Millisecond)
	s.uploadIdentity(t, tenant.ServerName, bob, 56)

	select {
	case got := <-done:
		if got.spent > 1500*time.Millisecond {
			t.Fatalf("a device change did not wake the parked sync; it ran %s", got.spent)
		}
		if !has(deviceLists(t, got.body).Changed, bob.UserID) {
			t.Fatalf("the woken sync carried %v", deviceLists(t, got.body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the parked sync never returned")
	}
}

func TestTwoDevicesOfOneUserTrackDeviceListsIndependently(t *testing.T) {
	s := newServer(t)
	tenant := s.open(t, "alpha.test")
	_ = s.register(t, tenant.ServerName, "alice", goodPassword)
	phone := s.loginAlice(t, "PHONE")
	laptop := s.loginAlice(t, "LAPTOP")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	roomID := s.createRoom(t, tenant.ServerName, phone.AccessToken, publicRoom)
	s.joinRoom(t, tenant.ServerName, roomID, bob.AccessToken)

	early := s.legacySync(t, tenant.ServerName, phone.AccessToken, nil)
	s.uploadIdentity(t, tenant.ServerName, bob, 57)
	late := s.legacySync(t, tenant.ServerName, laptop.AccessToken, nil)

	onPhone := s.legacySync(t, tenant.ServerName, phone.AccessToken, since(early.NextBatch))
	if !has(deviceLists(t, onPhone).Changed, bob.UserID) {
		t.Fatalf("the device parked earlier missed the change: %v", deviceLists(t, onPhone))
	}

	onLaptop := s.legacySync(t, tenant.ServerName, laptop.AccessToken, since(late.NextBatch))
	if has(deviceLists(t, onLaptop).Changed, bob.UserID) {
		t.Fatalf("the device that synced after the change saw it again: %v",
			deviceLists(t, onLaptop))
	}
}
