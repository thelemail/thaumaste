package matrix_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) memberContent(t *testing.T, host string, session sessionBody, roomID, target string) map[string]any {
	t.Helper()
	rec := s.get(t, host, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+
		"/state/m.room.member/"+url.PathEscape(target), session.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("read member state = %d: %s", rec.Code, rec.Body)
	}
	return decode[map[string]any](t, rec)
}

func TestAProfileChangeReachesEveryJoinedRoom(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")

	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)
	s.named(t, of.ServerName, bob, "Bob")

	first := s.seedRoom(t, of, alice)
	second := s.createRoom(t, of.ServerName, alice.AccessToken, map[string]any{"visibility": entity.VisibilityPublic})
	left := s.createRoom(t, of.ServerName, alice.AccessToken, map[string]any{"visibility": entity.VisibilityPublic})

	for _, roomID := range []string{first.RoomID, second, left} {
		joined := s.do(t, http.MethodPost, of.ServerName,
			"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/join", bob.AccessToken, map[string]any{})
		if joined.Code != http.StatusOK {
			t.Fatalf("join = %d: %s", joined.Code, joined.Body)
		}
	}

	overridden := s.do(t, http.MethodPut, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(second)+"/state/m.room.member/"+url.PathEscape(bob.UserID),
		bob.AccessToken, map[string]any{"membership": "join", "displayname": "Bobby the second"})
	if overridden.Code != http.StatusOK {
		t.Fatalf("override the room display name = %d: %s", overridden.Code, overridden.Body)
	}

	departed := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(left)+"/leave", bob.AccessToken, map[string]any{})
	if departed.Code != http.StatusOK {
		t.Fatalf("leave = %d: %s", departed.Code, departed.Body)
	}

	before, err := s.events.Page(t.Context(), first.RoomID, entity.PageRequest{Limit: entity.MaxPageLimit})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	s.named(t, of.ServerName, bob, "Robert")

	for _, roomID := range []string{first.RoomID, second} {
		if got := s.memberContent(t, of.ServerName, alice, roomID, bob.UserID)["displayname"]; got != "Robert" {
			t.Fatalf("room %s carries displayname %v, want Robert", roomID, got)
		}
	}
	if got := s.memberContent(t, of.ServerName, alice, left, bob.UserID)["membership"]; got != entity.MembershipLeave {
		t.Fatalf("the left room's membership changed to %v", got)
	}

	after, err := s.events.Page(t.Context(), first.RoomID, entity.PageRequest{Limit: entity.MaxPageLimit})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("the room went from %d to %d events, want exactly one more", len(before), len(after))
	}
	for i, stored := range before {
		if after[i].Event.ID() != stored.Event.ID() {
			t.Fatalf("event %d changed from %s to %s, so history was rewritten",
				i, stored.Event.ID(), after[i].Event.ID())
		}
	}
}

func TestAnUnauthenticatedProfileReadKeepsWorking(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	s.named(t, of.ServerName, alice, "Alice Cooper")

	rec := s.get(t, of.ServerName,
		"/_matrix/client/v3/profile/"+url.PathEscape(alice.UserID)+"/displayname", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("an unauthenticated profile read = %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := decode[map[string]any](t, rec)["displayname"]; got != "Alice Cooper" {
		t.Fatalf("displayname = %v", got)
	}
}

func TestTheWholeProfileCanBeSetAtOnce(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	rec := s.do(t, http.MethodPut, of.ServerName,
		"/_matrix/client/v3/profile/"+url.PathEscape(alice.UserID), alice.AccessToken,
		map[string]any{"displayname": "Alice Cooper", "avatar_url": "mxc://alpha.test/abc"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set the whole profile = %d: %s", rec.Code, rec.Body)
	}

	profile := decode[map[string]any](t, s.get(t, of.ServerName,
		"/_matrix/client/v3/profile/"+url.PathEscape(alice.UserID), ""))
	if profile["displayname"] != "Alice Cooper" || profile["avatar_url"] != "mxc://alpha.test/abc" {
		t.Fatalf("the profile reads back as %v", profile)
	}
}
