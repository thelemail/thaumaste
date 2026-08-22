package matrix_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

type directoryBody struct {
	Results []struct {
		UserID      string `json:"user_id"`
		DisplayName string `json:"display_name"`
		AvatarURL   string `json:"avatar_url"`
	} `json:"results"`
	Limited bool `json:"limited"`
}

func (s *server) searchDirectory(t *testing.T, host string, session sessionBody, term string) directoryBody {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/user_directory/search",
		session.AccessToken, map[string]any{"search_term": term})
	if rec.Code != http.StatusOK {
		t.Fatalf("directory search = %d: %s", rec.Code, rec.Body)
	}
	return decode[directoryBody](t, rec)
}

func (s *server) named(t *testing.T, host string, session sessionBody, name string) {
	t.Helper()
	rec := s.do(t, http.MethodPut, host,
		"/_matrix/client/v3/profile/"+url.PathEscape(session.UserID)+"/displayname",
		session.AccessToken, map[string]any{"displayname": name})
	if rec.Code != http.StatusOK {
		t.Fatalf("set display name = %d: %s", rec.Code, rec.Body)
	}
}

func TestTheDirectoryFindsWhoTheCallerMaySeeAndNobodyElse(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")

	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)
	eve := s.register(t, of.ServerName, "eve", goodPassword)
	s.named(t, of.ServerName, alice, "Alice Cooper")
	s.named(t, of.ServerName, bob, "Bob Marley")

	public := s.seedRoom(t, of, alice)
	private := s.createRoom(t, of.ServerName, bob.AccessToken, map[string]any{"preset": "private_chat"})
	if private == "" {
		t.Fatal("the private room was not created")
	}

	found := s.searchDirectory(t, of.ServerName, eve, "Alice Cooper")
	if len(found.Results) != 1 {
		t.Fatalf("a stranger searching a public-room member found %v", found.Results)
	}
	if found.Results[0].UserID != alice.UserID || found.Results[0].DisplayName != "Alice Cooper" {
		t.Fatalf("the directory returned %+v", found.Results[0])
	}

	if hidden := s.searchDirectory(t, of.ServerName, eve, "Bob Marley"); len(hidden.Results) != 0 {
		t.Fatalf("a user in only a private room was found: %v", hidden.Results)
	}

	joined := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(public.RoomID)+"/join", bob.AccessToken, map[string]any{})
	if joined.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", joined.Code, joined.Body)
	}
	if now := s.searchDirectory(t, of.ServerName, eve, "Bob Marley"); len(now.Results) != 1 {
		t.Fatalf("after joining a public room the user is still hidden: %v", now.Results)
	}
}

func TestTheDirectoryNeverReturnsTheCaller(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")

	alice := s.register(t, of.ServerName, "alice", goodPassword)
	s.named(t, of.ServerName, alice, "Alice Cooper")
	s.seedRoom(t, of, alice)

	if found := s.searchDirectory(t, of.ServerName, alice, "Alice Cooper"); len(found.Results) != 0 {
		t.Fatalf("the caller found themselves: %v", found.Results)
	}
}

func TestAPerRoomDisplayNameIsNeverSearchable(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")

	alice := s.register(t, of.ServerName, "alice", goodPassword)
	eve := s.register(t, of.ServerName, "eve", goodPassword)
	s.named(t, of.ServerName, alice, "Alice Cooper")
	room := s.seedRoom(t, of, alice)

	overridden := s.do(t, http.MethodPut, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/state/m.room.member/"+url.PathEscape(alice.UserID),
		alice.AccessToken, map[string]any{"membership": "join", "displayname": "Freddy"})
	if overridden.Code != http.StatusOK {
		t.Fatalf("override the room display name = %d: %s", overridden.Code, overridden.Body)
	}

	if found := s.searchDirectory(t, of.ServerName, eve, "Freddy"); len(found.Results) != 0 {
		t.Fatalf("a per-room display name was searchable: %v", found.Results)
	}
	found := s.searchDirectory(t, of.ServerName, eve, "Alice Cooper")
	if len(found.Results) != 1 || found.Results[0].DisplayName != "Alice Cooper" {
		t.Fatalf("the global display name is not what the directory returns: %v", found.Results)
	}
}

func TestDirectorySearchCannotReachAnotherDomain(t *testing.T) {
	s := newServer(t)
	alpha := s.open(t, "alpha.test")
	beta := s.open(t, "beta.test")

	here := s.register(t, alpha.ServerName, "alice", goodPassword)
	there := s.register(t, beta.ServerName, "alice", goodPassword)
	s.named(t, alpha.ServerName, here, "Alice Cooper")
	s.named(t, beta.ServerName, there, "Alice Cooper")

	s.seedRoom(t, alpha, here)
	s.seedRoom(t, beta, there)

	seeker := s.register(t, alpha.ServerName, "eve", goodPassword)

	terms := []string{
		"Alice Cooper", "alice", "Alice", "a", "@", ":", "beta.test",
		there.UserID, "@alice:beta.test", "cooper", "COOPER", "%", "_",
	}
	for _, term := range terms {
		found := s.searchDirectory(t, alpha.ServerName, seeker, term)
		for _, result := range found.Results {
			if result.UserID == there.UserID {
				t.Fatalf("searching %q from alpha.test returned the beta.test user", term)
			}
			if !hostedBy(result.UserID, "alpha.test") {
				t.Fatalf("searching %q returned %s, which is not an alpha.test user", term, result.UserID)
			}
		}
	}

	if own := s.searchDirectory(t, alpha.ServerName, seeker, "Alice Cooper"); len(own.Results) != 1 {
		t.Fatalf("the search returned %d of its own domain's users, want 1", len(own.Results))
	}
}

func hostedBy(userID, serverName string) bool {
	return len(userID) > len(serverName) && userID[len(userID)-len(serverName)-1:] == ":"+serverName
}

func (s *server) seedColliding(t *testing.T, of entity.Tenant, roomID, userID, localpart, displayName string) {
	t.Helper()

	var roomNID, eventNID int64
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT r.room_nid, min(e.event_nid) FROM rooms r JOIN events e ON e.room_nid = r.room_nid
		  WHERE r.room_id = $1 GROUP BY r.room_nid`, roomID).Scan(&roomNID, &eventNID); err != nil {
		t.Fatalf("read the seeded room: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO users (tenant_id, user_id, localpart, display_name) VALUES ($1, $2, $3, $4)`,
		of.ID.String(), userID, localpart, displayName); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO room_memberships (tenant_id, room_nid, user_id, membership, event_nid)
		 VALUES ($1, $2, $3, 'join', $4)`,
		of.ID.String(), roomNID, userID, eventNID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

func TestTheDirectoryScopesEveryReadToOneDomain(t *testing.T) {
	s := newServer(t)
	alpha := s.open(t, "alpha.test")
	beta := s.open(t, "beta.test")

	resident := s.register(t, alpha.ServerName, "resident", goodPassword)
	elsewhere := s.register(t, beta.ServerName, "resident", goodPassword)
	here := s.seedRoom(t, alpha, resident)
	there := s.seedRoom(t, beta, elsewhere)

	const seeker = "@shared:example.test"
	const target = "@target:example.test"

	s.seedColliding(t, alpha, here.RoomID, seeker, "shared", "Shared Seeker")
	s.seedColliding(t, beta, there.RoomID, seeker, "shared", "Shared Seeker")
	s.seedColliding(t, alpha, here.RoomID, target, "target", "Target Alpha")
	s.seedColliding(t, beta, there.RoomID, target, "target", "Target Beta")

	found, limited, err := s.directory.Search(t.Context(), alpha.Scope(), seeker,
		entity.DirectorySearch{Term: "Target"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if limited {
		t.Fatal("the search reported truncation it did not do")
	}
	if len(found) != 1 {
		t.Fatalf("searching under alpha.test returned %d results, want only its own: %+v", len(found), found)
	}
	if found[0].DisplayName != "Target Alpha" {
		t.Fatalf("searching under alpha.test returned %+v", found[0])
	}
}
