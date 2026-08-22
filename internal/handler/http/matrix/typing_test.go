package matrix_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) setTyping(t *testing.T, host string, session sessionBody, roomID string,
	body map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/typing/"+url.PathEscape(session.UserID),
		session.AccessToken, body)
}

func (s *server) typingIn(t *testing.T, of entity.Tenant, session sessionBody, roomID string) []string {
	t.Helper()
	found, err := s.typing.ForRoom(t.Context(), of.Scope(), session.UserID, roomID)
	if err != nil {
		t.Fatalf("ForRoom: %v", err)
	}
	slices.Sort(found)
	return found
}

func TestTypingSelfHealsWhenAClientDisappears(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)

	room := s.seedRoom(t, of, alice)
	if rec := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/join", bob.AccessToken,
		map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", rec.Code, rec.Body)
	}

	if rec := s.setTyping(t, of.ServerName, alice, room.RoomID,
		map[string]any{"typing": true, "timeout": 10000}); rec.Code != http.StatusOK {
		t.Fatalf("start typing = %d: %s", rec.Code, rec.Body)
	}
	if got := s.typingIn(t, of, bob, room.RoomID); len(got) != 1 || got[0] != alice.UserID {
		t.Fatalf("typing = %v, want just alice", got)
	}

	restarted := reopen(t, s)
	if got := restarted.typingIn(t, of, bob, room.RoomID); len(got) != 1 {
		t.Fatalf("typing did not survive a restart: %v", got)
	}

	s.clock.Add(11 * time.Second)
	if got := s.typingIn(t, of, bob, room.RoomID); len(got) != 0 {
		t.Fatalf("a client that stopped talking is still typing after the timeout: %v", got)
	}
}

func TestStoppingTypingIsImmediate(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	room := s.seedRoom(t, of, alice)

	if rec := s.setTyping(t, of.ServerName, alice, room.RoomID,
		map[string]any{"typing": true, "timeout": 30000}); rec.Code != http.StatusOK {
		t.Fatalf("start typing = %d: %s", rec.Code, rec.Body)
	}
	if got := s.typingIn(t, of, alice, room.RoomID); len(got) != 1 {
		t.Fatalf("typing = %v, want one", got)
	}

	if rec := s.setTyping(t, of.ServerName, alice, room.RoomID,
		map[string]any{"typing": false}); rec.Code != http.StatusOK {
		t.Fatalf("stop typing = %d: %s", rec.Code, rec.Body)
	}
	got := s.typingIn(t, of, alice, room.RoomID)
	if got == nil {
		got = []string{}
	}
	if len(got) != 0 {
		t.Fatalf("typing = %v after stopping, want an empty set", got)
	}
}

func TestTypingIsScopedToItsRoomAndItsMembers(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	stranger := s.register(t, of.ServerName, "eve", goodPassword)

	room := s.seedRoom(t, of, alice)
	other := s.createRoom(t, of.ServerName, alice.AccessToken, map[string]any{})

	if rec := s.setTyping(t, of.ServerName, alice, room.RoomID,
		map[string]any{"typing": true, "timeout": 30000}); rec.Code != http.StatusOK {
		t.Fatalf("start typing = %d: %s", rec.Code, rec.Body)
	}
	if got := s.typingIn(t, of, alice, other); len(got) != 0 {
		t.Fatalf("typing leaked into another room: %v", got)
	}

	if _, err := s.typing.ForRoom(t.Context(), of.Scope(), stranger.UserID, room.RoomID); err == nil {
		t.Fatal("a non-member read the typing set")
	}

	if rec := s.do(t, http.MethodPut, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/typing/"+url.PathEscape(alice.UserID),
		stranger.AccessToken, map[string]any{"typing": true, "timeout": 1000}); rec.Code != http.StatusForbidden {
		t.Fatalf("setting another user's typing state = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestATypingTimeoutOutsideTheAllowedRangeIsRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	room := s.seedRoom(t, of, alice)

	if rec := s.setTyping(t, of.ServerName, alice, room.RoomID,
		map[string]any{"typing": true, "timeout": 999999999}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an absurd typing timeout = %d, want 400: %s", rec.Code, rec.Body)
	}
	if rec := s.setTyping(t, of.ServerName, alice, room.RoomID,
		map[string]any{"typing": true}); rec.Code != http.StatusOK {
		t.Fatalf("an omitted timeout = %d, want the default to apply: %s", rec.Code, rec.Body)
	}
}
