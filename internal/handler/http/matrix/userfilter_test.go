package matrix_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func filterPath(session sessionBody, suffix string) string {
	return "/_matrix/client/v3/user/" + url.PathEscape(session.UserID) + "/filter" + suffix
}

func (s *server) storeFilter(t *testing.T, host string, session sessionBody, document any) string {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, filterPath(session, ""), session.AccessToken, document)
	if rec.Code != http.StatusOK {
		t.Fatalf("create filter = %d: %s", rec.Code, rec.Body)
	}
	return decode[struct {
		FilterID string `json:"filter_id"`
	}](t, rec).FilterID
}

func TestAFilterRoundTripsAndDeduplicates(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	document := map[string]any{"room": map[string]any{"timeline": map[string]any{"limit": 10}}}
	filterID := s.storeFilter(t, of.ServerName, alice, document)
	if filterID == "" {
		t.Fatal("the server returned an empty filter id")
	}
	if filterID[0] == '{' {
		t.Fatalf("filter id %q starts with a brace, which the spec forbids", filterID)
	}

	fetched := s.get(t, of.ServerName, filterPath(alice, "/"+filterID), alice.AccessToken)
	if fetched.Code != http.StatusOK {
		t.Fatalf("download filter = %d: %s", fetched.Code, fetched.Body)
	}
	var back struct {
		Room struct {
			Timeline struct {
				Limit int `json:"limit"`
			} `json:"timeline"`
		} `json:"room"`
	}
	if err := json.Unmarshal(fetched.Body.Bytes(), &back); err != nil {
		t.Fatalf("decode %s: %v", fetched.Body, err)
	}
	if back.Room.Timeline.Limit != 10 {
		t.Fatalf("room.timeline.limit = %d, want 10", back.Room.Timeline.Limit)
	}

	if again := s.storeFilter(t, of.ServerName, alice, document); again != filterID {
		t.Fatalf("an identical filter got id %q, want the original %q", again, filterID)
	}
	other := s.storeFilter(t, of.ServerName, alice,
		map[string]any{"room": map[string]any{"timeline": map[string]any{"limit": 11}}})
	if other == filterID {
		t.Fatalf("a different filter reused id %q", filterID)
	}
}

func TestEveryMalformedFilterIsRefusedWithFourHundred(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)

	const nao = "not_an_object"
	const nal = "not_a_list"
	timeline := func(field string, value any) map[string]any {
		return map[string]any{"room": map[string]any{"timeline": map[string]any{field: value}}}
	}

	cases := []map[string]any{
		{"presence": nao},
		{"room": map[string]any{"timeline": nao}},
		{"room": map[string]any{"state": nao}},
		{"room": map[string]any{"ephemeral": nao}},
		{"room": map[string]any{"account_data": nao}},
		timeline("rooms", nal),
		timeline("not_rooms", nal),
		timeline("senders", nal),
		timeline("not_senders", nal),
		timeline("types", nal),
		timeline("not_types", nal),
		timeline("types", []int{1}),
		timeline("rooms", []string{"not_a_room_id"}),
		timeline("senders", []string{"not_a_sender_id"}),
	}

	for _, document := range cases {
		rec := s.do(t, http.MethodPost, of.ServerName, filterPath(alice, ""), alice.AccessToken, document)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("filter %v = %d, want 400: %s", document, rec.Code, rec.Body)
		}
		if code := errcode(t, rec); code != "M_BAD_JSON" {
			t.Fatalf("filter %v errcode = %s, want M_BAD_JSON", document, code)
		}
	}
}

func TestAFilterBelongsToOneUser(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	bob := s.register(t, of.ServerName, "bob", goodPassword)

	filterID := s.storeFilter(t, of.ServerName, alice,
		map[string]any{"room": map[string]any{"timeline": map[string]any{"limit": 10}}})

	if rec := s.get(t, of.ServerName, filterPath(alice, "/"+filterID), bob.AccessToken); rec.Code != http.StatusForbidden {
		t.Fatalf("reading another user's filter = %d, want 403: %s", rec.Code, rec.Body)
	}
	if rec := s.get(t, of.ServerName, filterPath(bob, "/"+filterID), bob.AccessToken); rec.Code != http.StatusNotFound {
		t.Fatalf("an id belonging to someone else = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestAStoredFilterFiltersMessagesLikeInlineJSONDoes(t *testing.T) {
	s := newServer(t)
	of := s.open(t, "alpha.test")
	alice := s.register(t, of.ServerName, "alice", goodPassword)
	room := s.seedRoom(t, of, alice)

	for i := range 3 {
		sent := s.do(t, http.MethodPut, of.ServerName,
			"/_matrix/client/v3/rooms/"+url.PathEscape(room.RoomID)+"/send/m.room.message/filtered-"+string(rune('a'+i)),
			alice.AccessToken, map[string]any{"msgtype": "m.text", "body": "hello"})
		if sent.Code != http.StatusOK {
			t.Fatalf("send = %d: %s", sent.Code, sent.Body)
		}
	}

	document := map[string]any{"room": map[string]any{"timeline": map[string]any{
		"types": []string{"m.room.message"},
	}}}
	filterID := s.storeFilter(t, of.ServerName, alice, document)

	inline := s.messagesWith(t, of.ServerName, alice, room.RoomID,
		`{"types":["m.room.message"]}`)
	stored := s.messagesWith(t, of.ServerName, alice, room.RoomID, filterID)

	if len(inline) == 0 {
		t.Fatal("the inline filter returned nothing, so the comparison proves nothing")
	}
	if len(stored) != len(inline) {
		t.Fatalf("the stored filter returned %d events and the inline one %d", len(stored), len(inline))
	}
	for _, event := range stored {
		if event["type"] != "m.room.message" {
			t.Fatalf("the stored filter let through %v", event["type"])
		}
	}
}

func (s *server) messagesWith(t *testing.T, host string, session sessionBody, roomID, filter string) []map[string]any {
	t.Helper()
	path := "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) +
		"/messages?dir=b&limit=50&filter=" + url.QueryEscape(filter)
	rec := s.get(t, host, path, session.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages = %d: %s", rec.Code, rec.Body)
	}
	return decode[struct {
		Chunk []map[string]any `json:"chunk"`
	}](t, rec).Chunk
}
