package matrix_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func presencePath(session sessionBody) string {
	return "/_matrix/client/v3/presence/" + url.PathEscape(session.UserID) + "/status"
}

type presenceBody struct {
	Presence      string `json:"presence"`
	StatusMsg     string `json:"status_msg"`
	LastActiveAgo int64  `json:"last_active_ago"`
}

func (s *server) enablePresence(t *testing.T, of entity.Tenant) entity.Tenant {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE tenants SET presence_enabled = true WHERE id = $1`, of.ID.String()); err != nil {
		t.Fatalf("enable presence: %v", err)
	}
	updated, err := s.tenants.ByServerName(t.Context(), of.ServerName)
	if err != nil {
		t.Fatalf("reload the tenant: %v", err)
	}
	return updated
}

func TestPresenceOffAnswersPredictablyAndStoresNothing(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	set := s.do(t, http.MethodPut, of.ServerName, presencePath(alice), alice.AccessToken,
		map[string]any{"presence": entity.PresenceOnline, "status_msg": "at my desk"})
	if set.Code != http.StatusOK {
		t.Fatalf("setting presence while disabled = %d, want 200: %s", set.Code, set.Body)
	}

	got := decode[presenceBody](t, s.get(t, of.ServerName, presencePath(alice), alice.AccessToken))
	if got.Presence != entity.PresenceOffline {
		t.Fatalf("presence = %q while disabled, want offline", got.Presence)
	}
	if got.StatusMsg != "" || got.LastActiveAgo != 0 {
		t.Fatalf("a disabled domain leaked %+v", got)
	}

	var count int
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM user_presence WHERE user_id = $1`, alice.UserID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a disabled domain stored %d presence rows", count)
	}

	if bad := s.do(t, http.MethodPut, of.ServerName, presencePath(alice), alice.AccessToken,
		map[string]any{"presence": "telepathic"}); bad.Code != http.StatusBadRequest {
		t.Fatalf("an unknown presence state = %d, want 400: %s", bad.Code, bad.Body)
	}
}

func TestPresenceOnStoresAndReturnsTheState(t *testing.T) {
	s := newServer(t)
	of := s.enablePresence(t, s.open(t, "alpha.test"))
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	set := s.do(t, http.MethodPut, of.ServerName, presencePath(alice), alice.AccessToken,
		map[string]any{"presence": entity.PresenceOnline, "status_msg": "at my desk"})
	if set.Code != http.StatusOK {
		t.Fatalf("set presence = %d: %s", set.Code, set.Body)
	}

	got := decode[presenceBody](t, s.get(t, of.ServerName, presencePath(alice), alice.AccessToken))
	if got.Presence != entity.PresenceOnline || got.StatusMsg != "at my desk" {
		t.Fatalf("presence read back as %+v", got)
	}
	if got.LastActiveAgo < 0 {
		t.Fatalf("last_active_ago = %d", got.LastActiveAgo)
	}
}

func TestOneDomainsPresenceSwitchDoesNotAffectAnother(t *testing.T) {
	s := newServer(t)
	alpha := s.enablePresence(t, s.open(t, "alpha.test"))
	beta := s.open(t, "beta.test")

	here := s.register(t, alpha.ServerName, "alice", goodPassword)
	there := s.register(t, beta.ServerName, "alice", goodPassword)

	for _, each := range []struct {
		host    string
		session sessionBody
	}{{alpha.ServerName, here}, {beta.ServerName, there}} {
		rec := s.do(t, http.MethodPut, each.host, presencePath(each.session), each.session.AccessToken,
			map[string]any{"presence": entity.PresenceOnline, "status_msg": "here"})
		if rec.Code != http.StatusOK {
			t.Fatalf("set presence on %s = %d: %s", each.host, rec.Code, rec.Body)
		}
	}

	enabled := decode[presenceBody](t, s.get(t, alpha.ServerName, presencePath(here), here.AccessToken))
	if enabled.Presence != entity.PresenceOnline {
		t.Fatalf("the enabled domain reported %q", enabled.Presence)
	}
	disabled := decode[presenceBody](t, s.get(t, beta.ServerName, presencePath(there), there.AccessToken))
	if disabled.Presence != entity.PresenceOffline {
		t.Fatalf("the disabled domain reported %q", disabled.Presence)
	}

	var count int
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM user_presence WHERE tenant_id = $1`, beta.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("the disabled domain stored %d presence rows", count)
	}
}

func TestPresenceOfAnotherUserNeedsASharedRoom(t *testing.T) {
	s := newServer(t)
	of := s.enablePresence(t, s.open(t, "alpha.test"))
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	stranger := s.register(t, of.ServerName, "eve", goodPassword)

	if rec := s.do(t, http.MethodPut, of.ServerName, presencePath(alice), alice.AccessToken,
		map[string]any{"presence": entity.PresenceOnline}); rec.Code != http.StatusOK {
		t.Fatalf("set presence = %d: %s", rec.Code, rec.Body)
	}

	if rec := s.get(t, of.ServerName, presencePath(alice), stranger.AccessToken); rec.Code != http.StatusForbidden {
		t.Fatalf("a stranger read presence = %d, want 403: %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, http.MethodPut, of.ServerName, presencePath(alice), stranger.AccessToken,
		map[string]any{"presence": entity.PresenceOnline}); rec.Code != http.StatusForbidden {
		t.Fatalf("a stranger set another user's presence = %d, want 403: %s", rec.Code, rec.Body)
	}

	room := s.seedRoom(t, of, alice)
	if rec := s.do(t, http.MethodPost, of.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/join", stranger.AccessToken,
		map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", rec.Code, rec.Body)
	}
	if rec := s.get(t, of.ServerName, presencePath(alice), stranger.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("a co-member read presence = %d, want 200: %s", rec.Code, rec.Body)
	}
}
