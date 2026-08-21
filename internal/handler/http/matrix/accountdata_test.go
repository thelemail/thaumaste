package matrix_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func accountDataPath(session sessionBody, roomID, dataType string) string {
	path := "/_matrix/client/v3/user/" + url.PathEscape(session.UserID)
	if roomID != "" {
		path += "/rooms/" + url.PathEscape(roomID)
	}
	return path + "/account_data/" + dataType
}

func (s *server) putAccountData(t *testing.T, host string, session sessionBody, roomID, dataType string,
	content any,
) *httptest.ResponseRecorder {
	t.Helper()
	return s.do(t, http.MethodPut, host, accountDataPath(session, roomID, dataType),
		session.AccessToken, content)
}

func (s *server) fetchAccountData(t *testing.T, host string, session sessionBody, roomID,
	dataType string,
) *httptest.ResponseRecorder {
	t.Helper()
	return s.get(t, host, accountDataPath(session, roomID, dataType), session.AccessToken)
}

func TestAccountDataRoundTripsIncludingLargeValues(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	if rec := s.putAccountData(t, of.ServerName, alice, "", "test.key",
		map[string]any{"value": "first"}); rec.Code != http.StatusOK {
		t.Fatalf("set global account data = %d: %s", rec.Code, rec.Body)
	}
	if got := decode[map[string]any](t, s.fetchAccountData(t, of.ServerName, alice, "", "test.key"))["value"]; got != "first" {
		t.Fatalf("global account data = %v, want first", got)
	}

	if rec := s.putAccountData(t, of.ServerName, alice, "", "test.key",
		map[string]any{"value": "second"}); rec.Code != http.StatusOK {
		t.Fatalf("overwrite global account data = %d: %s", rec.Code, rec.Body)
	}
	replaced := decode[map[string]any](t, s.fetchAccountData(t, of.ServerName, alice, "", "test.key"))
	if replaced["value"] != "second" {
		t.Fatalf("global account data = %v, want second", replaced["value"])
	}
	if len(replaced) != 1 {
		t.Fatalf("a second write merged instead of replacing: %v", replaced)
	}

	room := s.seedRoom(t, of, alice)
	if rec := s.putAccountData(t, of.ServerName, alice, room.RoomID, "test.key",
		map[string]any{"value": "room first"}); rec.Code != http.StatusOK {
		t.Fatalf("set room account data = %d: %s", rec.Code, rec.Body)
	}
	if got := decode[map[string]any](t, s.fetchAccountData(t, of.ServerName, alice, room.RoomID, "test.key"))["value"]; got != "room first" {
		t.Fatalf("room account data = %v, want room first", got)
	}
	if got := decode[map[string]any](t, s.fetchAccountData(t, of.ServerName, alice, "", "test.key"))["value"]; got != "second" {
		t.Fatalf("the room write changed the global value to %v", got)
	}

	restarted := reopen(t, s)
	if got := decode[map[string]any](t, restarted.fetchAccountData(t, of.ServerName, alice, "", "test.key"))["value"]; got != "second" {
		t.Fatalf("account data did not survive a restart: %v", got)
	}

	big := strings.Repeat("x", entity.MaxAccountDataBytes-32)
	if rec := s.putAccountData(t, of.ServerName, alice, "", "large.key",
		map[string]any{"value": big}); rec.Code != http.StatusOK {
		t.Fatalf("a value inside the limit was refused: %d %s", rec.Code, rec.Body)
	}
	stored := decode[map[string]any](t, s.fetchAccountData(t, of.ServerName, alice, "", "large.key"))
	if stored["value"] != big {
		t.Fatalf("a large value did not round-trip byte for byte")
	}

	over := strings.Repeat("y", entity.MaxAccountDataBytes+1)
	rec := s.putAccountData(t, of.ServerName, alice, "", "toolarge.key", map[string]any{"value": over})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a value past the limit = %d, want 400: %s", rec.Code, rec.Body)
	}
	if code := errcode(t, rec); code != "M_TOO_LARGE" {
		t.Fatalf("errcode = %s, want M_TOO_LARGE", code)
	}
}

func TestAnUnsetAccountDataTypeIsNotFound(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	rec := s.fetchAccountData(t, of.ServerName, alice, "", "never.set")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unset type = %d, want 404: %s", rec.Code, rec.Body)
	}

	if put := s.putAccountData(t, of.ServerName, alice, "", "empty.key",
		map[string]any{}); put.Code != http.StatusOK {
		t.Fatalf("setting an empty object = %d: %s", put.Code, put.Body)
	}
	empty := s.fetchAccountData(t, of.ServerName, alice, "", "empty.key")
	if empty.Code != http.StatusOK {
		t.Fatalf("an empty object reads back as %d, want 200", empty.Code)
	}
	if body := strings.TrimSpace(empty.Body.String()); body != "{}" {
		t.Fatalf("an empty object read back as %s", body)
	}
}

func TestAccountDataOfAnotherUserIsRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)

	path := accountDataPath(bob, "", "test.key")
	if rec := s.do(t, http.MethodPut, of.ServerName, path, alice.AccessToken,
		map[string]any{"value": "theirs"}); rec.Code != http.StatusForbidden {
		t.Fatalf("writing another user's account data = %d, want 403: %s", rec.Code, rec.Body)
	}
	if rec := s.get(t, of.ServerName, path, alice.AccessToken); rec.Code != http.StatusForbidden {
		t.Fatalf("reading another user's account data = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestServerControlledAccountDataTypesAreRefused(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	room := s.seedRoom(t, of, alice)

	for _, reserved := range []string{entity.AccountDataFullyRead, entity.AccountDataPushRules} {
		for _, roomID := range []string{"", room.RoomID} {
			rec := s.putAccountData(t, of.ServerName, alice, roomID, reserved,
				map[string]any{"event_id": "$something"})
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("setting %s = %d, want 405: %s", reserved, rec.Code, rec.Body)
			}
			if code := errcode(t, rec); code != "M_BAD_JSON" {
				t.Fatalf("errcode for %s = %s, want M_BAD_JSON", reserved, code)
			}
		}
	}
}

func TestSecretStorageTypesAreStoredUntouched(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	secret := map[string]any{
		"algorithm": "m.secret_storage.v1.aes-hmac-sha2",
		"name":      "Default key",
		"iv":        "bJPtCJnFFsCFTMcBrLGWQg",
		"mac":       "NAAeIiZUqPnIm0e0IEZLPTPT2y6R1TrbRQUvE1jgHFY",
	}
	for _, dataType := range []string{"m.secret_storage.default_key", "m.secret_storage.key.abcdef", "m.cross_signing.master"} {
		if rec := s.putAccountData(t, of.ServerName, alice, "", dataType, secret); rec.Code != http.StatusOK {
			t.Fatalf("storing %s = %d: %s", dataType, rec.Code, rec.Body)
		}
		stored := decode[map[string]any](t, s.fetchAccountData(t, of.ServerName, alice, "", dataType))
		for field, want := range secret {
			if stored[field] != want {
				t.Fatalf("%s.%s = %v, want %v", dataType, field, stored[field], want)
			}
		}
	}
}

func TestTagsAreTheSameRoomAccountData(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	room := s.seedRoom(t, of, alice)

	base := "/_matrix/client/v3/user/" + url.PathEscape(alice.UserID) +
		"/rooms/" + url.PathEscape(room.RoomID) + "/tags"

	if rec := s.do(t, http.MethodPut, of.ServerName, base+"/m.favourite", alice.AccessToken,
		map[string]any{"order": 0.5}); rec.Code != http.StatusOK {
		t.Fatalf("set a tag = %d: %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, http.MethodPut, of.ServerName, base+"/u.work", alice.AccessToken,
		map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("set a second tag = %d: %s", rec.Code, rec.Body)
	}

	listed := decode[struct {
		Tags map[string]json.RawMessage `json:"tags"`
	}](t, s.get(t, of.ServerName, base, alice.AccessToken))
	if len(listed.Tags) != 2 {
		t.Fatalf("listed %d tags, want 2: %v", len(listed.Tags), listed.Tags)
	}

	stored := decode[struct {
		Tags map[string]json.RawMessage `json:"tags"`
	}](t, s.fetchAccountData(t, of.ServerName, alice, room.RoomID, entity.AccountDataTags))
	if len(stored.Tags) != 2 {
		t.Fatalf("the m.tag room account data holds %v", stored.Tags)
	}

	if rec := s.do(t, http.MethodDelete, of.ServerName, base+"/m.favourite", alice.AccessToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete a tag = %d: %s", rec.Code, rec.Body)
	}
	after := decode[struct {
		Tags map[string]json.RawMessage `json:"tags"`
	}](t, s.get(t, of.ServerName, base, alice.AccessToken))
	if len(after.Tags) != 1 {
		t.Fatalf("after deleting one tag %v remain", after.Tags)
	}
	if _, ok := after.Tags["u.work"]; !ok {
		t.Fatalf("the wrong tag was removed: %v", after.Tags)
	}
}

func TestAccountDataOfOneDomainIsInvisibleToAnother(t *testing.T) {
	s := newServer(t)
	alpha := s.open(t, "alpha.test")
	beta := s.open(t, "beta.test")

	one := s.register(t, alpha.ServerName, "alice", goodPassword)
	two := s.register(t, beta.ServerName, "alice", goodPassword)

	if rec := s.putAccountData(t, alpha.ServerName, one, "", "test.key",
		map[string]any{"value": "alpha"}); rec.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", rec.Code, rec.Body)
	}
	if rec := s.putAccountData(t, beta.ServerName, two, "", "test.key",
		map[string]any{"value": "beta"}); rec.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", rec.Code, rec.Body)
	}

	if got := decode[map[string]any](t, s.fetchAccountData(t, alpha.ServerName, one, "", "test.key"))["value"]; got != "alpha" {
		t.Fatalf("alpha.test read %v", got)
	}
	if got := decode[map[string]any](t, s.fetchAccountData(t, beta.ServerName, two, "", "test.key"))["value"]; got != "beta" {
		t.Fatalf("beta.test read %v", got)
	}
}
