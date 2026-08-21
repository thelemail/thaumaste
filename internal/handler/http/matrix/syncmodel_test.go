package matrix_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

const (
	convergenceSeed = 0x5eed1e55
	convergenceOps  = 140
)

type roomModel struct {
	membership   string
	name         string
	nameSet      bool
	avatar       string
	avatarSet    bool
	joinedCount  int
	invitedCount int
	countsSet    bool
	state        map[string]string
	timeline     []string
}

type clientModel struct {
	rooms map[string]*roomModel
}

func newClientModel() *clientModel {
	return &clientModel{rooms: make(map[string]*roomModel)}
}

func (m *clientModel) apply(t *testing.T, sc *script, body syncBody) {
	t.Helper()
	for roomID, incoming := range body.Rooms {
		room, ok := m.rooms[roomID]
		if !ok || incoming.Initial {
			room = &roomModel{state: make(map[string]string)}
			m.rooms[roomID] = room
		}
		room.membership = incoming.Membership
		if incoming.Name != nil {
			room.name, room.nameSet = *incoming.Name, true
		}
		if incoming.Avatar != nil {
			room.avatar, room.avatarSet = *incoming.Avatar, true
		}
		if incoming.JoinedCount != nil {
			room.joinedCount, room.countsSet = *incoming.JoinedCount, true
		}
		if incoming.InvitedCount != nil {
			room.invitedCount = *incoming.InvitedCount
		}
		for _, raw := range incoming.RequiredState {
			key, id := stateKeyOf(t, raw)
			room.state[key] = id
		}
		for _, raw := range incoming.Timeline {
			key, id := stateKeyOf(t, raw)
			if key != "" {
				room.state[key] = id
			}
			if !slices.Contains(room.timeline, id) {
				room.timeline = append(room.timeline, id)
			}
		}
		if incoming.Limited && incoming.PrevBatch != "" {
			room.backfill(t, sc, roomID, incoming.PrevBatch)
		}
	}
}

func (r *roomModel) backfill(t *testing.T, sc *script, roomID, from string) {
	t.Helper()
	for range 20 {
		page := sc.server.mustPage(t, sc.tenant.ServerName, sc.tokens[sc.users[0]], roomID,
			"dir=b&limit=20&from="+url.QueryEscape(from))
		if len(page.Chunk) == 0 {
			return
		}
		fresh := false
		for _, raw := range page.Chunk {
			key, id := stateKeyOf(t, raw)
			if key != "" {
				if _, known := r.state[key]; !known {
					r.state[key] = id
				}
			}
			if !slices.Contains(r.timeline, id) {
				r.timeline = append(r.timeline, id)
				fresh = true
			}
		}
		if !fresh || page.End == nil {
			return
		}
		from = *page.End
	}
}

func stateKeyOf(t *testing.T, raw json.RawMessage) (string, string) {
	t.Helper()
	var parsed struct {
		Type     string  `json:"type"`
		StateKey *string `json:"state_key"`
		EventID  string  `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if parsed.StateKey == nil {
		return "", parsed.EventID
	}
	return parsed.Type + "|" + *parsed.StateKey, parsed.EventID
}

type script struct {
	server *server
	tenant entity.Tenant
	tokens map[string]string
	users  []string
	rooms  []string
	sent   map[string][]string
	rng    *rand.Rand
}

func (sc *script) request(limit, high int) map[string]any {
	return map[string]any{
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, high}, "timeline_limit": limit,
			"required_state": map[string]any{"include": []map[string]any{{}}},
		}},
	}
}

func (sc *script) send(t *testing.T, room, user, body string) {
	t.Helper()
	rec := sc.server.do(t, http.MethodPut, sc.tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/send/m.room.message/"+body,
		sc.tokens[user], text(body))
	if rec.Code != http.StatusOK {
		return
	}
	sc.sent[room] = append(sc.sent[room], decode[struct {
		EventID string `json:"event_id"`
	}](t, rec).EventID)
}

func (sc *script) rename(t *testing.T, room, user, name string) {
	t.Helper()
	sc.server.do(t, http.MethodPut, sc.tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/state/m.room.name",
		sc.tokens[user], map[string]any{"name": name})
}

func (sc *script) redact(t *testing.T, room, user string) {
	t.Helper()
	ids := sc.sent[room]
	if len(ids) == 0 {
		return
	}
	target := ids[sc.rng.IntN(len(ids))]
	sc.server.do(t, http.MethodPut, sc.tenant.ServerName,
		"/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/redact/"+url.PathEscape(target)+"/r"+target[1:8],
		sc.tokens[user], map[string]any{})
}

func TestASyncClientConvergesOnAFreshBootstrap(t *testing.T) {
	s := newServer(t)
	tenant, alice, aliceID := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)
	carol := s.register(t, tenant.ServerName, "carol", goodPassword)

	sc := &script{
		server: s,
		tenant: tenant,
		tokens: map[string]string{aliceID: alice, bob.UserID: bob.AccessToken, carol.UserID: carol.AccessToken},
		users:  []string{aliceID, bob.UserID, carol.UserID},
		sent:   make(map[string][]string),
		rng:    rand.New(rand.NewPCG(convergenceSeed, convergenceSeed)),
	}
	for i := range 6 {
		room := s.createRoom(t, tenant.ServerName, alice, map[string]any{
			"preset": entity.PresetPublicChat, "name": fmt.Sprintf("room %d", i),
		})
		sc.rooms = append(sc.rooms, room)
		for _, user := range sc.users[1:] {
			s.joinAs(t, tenant, room, user)
		}
	}

	model := newClientModel()
	limit, high := 5, 5
	body := s.syncOnce(t, tenant.ServerName, alice, "", sc.request(limit, high))
	model.apply(t, sc, body)
	pos := body.Pos

	dropped, restarts := 0, 0
	for step := range convergenceOps {
		room := sc.rooms[sc.rng.IntN(len(sc.rooms))]
		user := sc.users[sc.rng.IntN(len(sc.users))]

		switch sc.rng.IntN(10) {
		case 0, 1, 2, 3, 4:
			sc.send(t, room, user, fmt.Sprintf("m%d", step))
		case 5:
			sc.rename(t, room, aliceID, fmt.Sprintf("renamed %d", step))
		case 6:
			sc.redact(t, room, aliceID)
		case 7:
			limit = 1 + sc.rng.IntN(8)
		case 8:
			high = 2 + sc.rng.IntN(4)
		case 9:
			s.setVisibility(t, tenant.ServerName, alice, room,
				[]string{entity.HistoryVisibilityShared, entity.HistoryVisibilityWorldReadable}[sc.rng.IntN(2)])
		}

		next := s.syncOnce(t, tenant.ServerName, alice, pos, sc.request(limit, high))
		if sc.rng.IntN(8) == 0 {
			dropped++
			continue
		}
		model.apply(t, sc, next)
		pos = next.Pos

		if sc.rng.IntN(40) == 0 {
			restarts++
			s = reopen(t, s)
			sc.server = s
		}
	}
	if dropped == 0 || restarts == 0 {
		t.Fatalf("the script exercised %d dropped responses and %d restarts", dropped, restarts)
	}

	fresh := s.syncOnce(t, tenant.ServerName, alice, "", map[string]any{
		"conn_id": "bootstrap",
		"lists": map[string]any{"all": map[string]any{
			"range": []int{0, len(sc.rooms)}, "timeline_limit": 50,
			"required_state": map[string]any{"include": []map[string]any{{}}},
		}},
	})
	bootstrap := newClientModel()
	bootstrap.apply(t, sc, fresh)

	if len(bootstrap.rooms) != len(sc.rooms) {
		t.Fatalf("a fresh bootstrap saw %d rooms, the script created %d", len(bootstrap.rooms), len(sc.rooms))
	}
	for roomID, want := range bootstrap.rooms {
		got, ok := model.rooms[roomID]
		if !ok {
			t.Fatalf("%s exists on a fresh bootstrap but the incremental client never learned of it", roomID)
		}
		if got.membership != want.membership {
			t.Fatalf("%s membership: incremental %q, bootstrap %q", roomID, got.membership, want.membership)
		}
		if got.name != want.name {
			t.Fatalf("%s name: incremental %q, bootstrap %q", roomID, got.name, want.name)
		}
		if got.joinedCount != want.joinedCount {
			t.Fatalf("%s joined_count: incremental %d, bootstrap %d", roomID, got.joinedCount, want.joinedCount)
		}
		for key, id := range want.state {
			if got.state[key] != id {
				t.Fatalf("%s state %s: incremental %q, bootstrap %q", roomID, key, got.state[key], id)
			}
		}
		if len(got.state) != len(want.state) {
			t.Fatalf("%s carries %d state keys incrementally and %d on a bootstrap",
				roomID, len(got.state), len(want.state))
		}
		for _, id := range want.timeline {
			if !slices.Contains(got.timeline, id) {
				t.Fatalf("%s: the incremental client never received %s", roomID, id)
			}
		}
	}
}

func TestNoEventIsDeliveredTwiceOnAConnectionTheClientKeptUp(t *testing.T) {
	s := newServer(t)
	tenant, alice, aliceID := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, tenant.ServerName, "bob", goodPassword)

	sc := &script{
		server: s, tenant: tenant,
		tokens: map[string]string{aliceID: alice, bob.UserID: bob.AccessToken},
		users:  []string{aliceID, bob.UserID},
		sent:   make(map[string][]string),
		rng:    rand.New(rand.NewPCG(convergenceSeed, 7)),
	}
	for i := range 3 {
		room := s.createRoom(t, tenant.ServerName, alice, map[string]any{
			"preset": entity.PresetPublicChat, "name": fmt.Sprintf("room %d", i),
		})
		sc.rooms = append(sc.rooms, room)
		s.joinAs(t, tenant, room, bob.UserID)
	}

	seen := make(map[string]int)
	body := s.syncOnce(t, tenant.ServerName, alice, "", sc.request(20, 5))
	count(t, seen, body)
	pos := body.Pos

	for step := range 60 {
		room := sc.rooms[sc.rng.IntN(len(sc.rooms))]
		sc.send(t, room, sc.users[sc.rng.IntN(len(sc.users))], fmt.Sprintf("m%d", step))

		next := s.syncOnce(t, tenant.ServerName, alice, pos, sc.request(20, 5))
		count(t, seen, next)
		pos = next.Pos
	}

	for id, times := range seen {
		if times != 1 {
			t.Fatalf("%s was delivered %d times on a connection the client kept up with", id, times)
		}
	}
	var delivered int
	for _, ids := range sc.sent {
		for _, id := range ids {
			if seen[id] == 0 {
				t.Fatalf("%s was sent but never delivered", id)
			}
			delivered++
		}
	}
	if delivered == 0 {
		t.Fatal("the script sent nothing")
	}
}

func count(t *testing.T, seen map[string]int, body syncBody) {
	t.Helper()
	for _, room := range body.Rooms {
		for _, id := range eventIDs(t, room.Timeline) {
			seen[id]++
		}
	}
}
