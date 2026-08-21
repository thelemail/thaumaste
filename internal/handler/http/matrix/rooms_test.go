package matrix_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

type roomBody struct {
	RoomID  string `json:"room_id"`
	ErrCode string `json:"errcode"`
}

func (s *server) createRoom(t *testing.T, host, token string, body map[string]any) string {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, "/_matrix/client/v3/createRoom", token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("createRoom = %d: %s", rec.Code, rec.Body)
	}
	return decode[roomBody](t, rec).RoomID
}

func (s *server) stateContent(t *testing.T, host, token, roomID, eventType, stateKey string) map[string]any {
	t.Helper()
	path := "/_matrix/client/v3/rooms/" + roomID + "/state/" + eventType + "/" + stateKey
	rec := s.get(t, host, path, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("state %s = %d: %s", eventType, rec.Code, rec.Body)
	}
	return decode[map[string]any](t, rec)
}

func (s *server) resident(t *testing.T, serverName, username string) (entity.Tenant, string, string) {
	t.Helper()
	tenant := s.open(t, serverName)
	session := s.register(t, serverName, username, goodPassword)
	return tenant, session.AccessToken, session.UserID
}

func (s *server) joinAs(t *testing.T, of entity.Tenant, roomID, userID string) {
	t.Helper()
	key := userID
	_, err := s.events.Send(t.Context(), of.Scope(), entity.NewEvent{
		RoomID: roomID, Type: entity.EventTypeMember, StateKey: &key, Sender: userID,
		Content: map[string]any{"membership": entity.MembershipJoin},
	})
	if err != nil {
		t.Fatalf("join %s: %v", userID, err)
	}
}

func TestACreatedRoomIsEncryptedWithTheMandatedAlgorithm(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})

	content := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeEncryption, "")
	if got := content["algorithm"]; got != entity.EncryptionAlgorithm {
		t.Fatalf("algorithm = %v, want %s", got, entity.EncryptionAlgorithm)
	}
}

func TestInitialStateCannotWeakenTheEncryption(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/createRoom", token, map[string]any{
		"initial_state": []map[string]any{
			{"type": entity.EventTypeEncryption, "state_key": "", "content": map[string]any{"algorithm": "none"}},
		},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}

	var rooms int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM rooms").Scan(&rooms); err != nil {
		t.Fatalf("count rooms: %v", err)
	}
	if rooms != 0 {
		t.Fatalf("a refused creation left %d rooms behind", rooms)
	}
}

func TestInitialStateMayRestateTheMandatedEncryption(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{
		"initial_state": []map[string]any{{
			"type":      entity.EventTypeEncryption,
			"state_key": "",
			"content": map[string]any{
				"algorithm":            entity.EncryptionAlgorithm,
				"rotation_period_ms":   604800000,
				"rotation_period_msgs": 100,
			},
		}},
	})

	content := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeEncryption, "")
	if content["algorithm"] != entity.EncryptionAlgorithm {
		t.Fatalf("algorithm = %v", content["algorithm"])
	}
	if content["rotation_period_msgs"] != float64(100) {
		t.Fatalf("rotation_period_msgs = %v, want the client's value", content["rotation_period_msgs"])
	}
}

func TestEncryptionCannotBeRemovedOrDowngraded(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	path := "/_matrix/client/v3/rooms/" + roomID + "/state/" + entity.EventTypeEncryption + "/"

	for name, content := range map[string]map[string]any{
		"an empty content":     {},
		"a missing algorithm":  {"rotation_period_msgs": 10},
		"another algorithm":    {"algorithm": "m.olm.v1.curve25519-aes-sha2"},
		"an algorithm of none": {"algorithm": "none"},
	} {
		rec := s.do(t, http.MethodPut, "alpha.test", path, token, content)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s = %d, want 403: %s", name, rec.Code, rec.Body)
		}
	}

	same := s.do(t, http.MethodPut, "alpha.test", path, token,
		map[string]any{"algorithm": entity.EncryptionAlgorithm, "rotation_period_msgs": 50})
	if same.Code != http.StatusOK {
		t.Fatalf("restating the mandated algorithm = %d: %s", same.Code, same.Body)
	}

	content := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeEncryption, "")
	if content["algorithm"] != entity.EncryptionAlgorithm {
		t.Fatalf("the room ended up with algorithm %v", content["algorithm"])
	}
}

func TestTheCreationChainIsLinearAndLeavesOneExtremity(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"name": "n", "topic": "t"})

	timeline, err := s.events.Page(t.Context(), roomID, entity.PageRequest{Limit: entity.MaxPageLimit})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline) < 7 {
		t.Fatalf("the creation chain wrote only %d events", len(timeline))
	}
	for i := 1; i < len(timeline); i++ {
		prev := timeline[i].Event.PrevEvents()
		if len(prev) != 1 || prev[0] != timeline[i-1].Event.ID() {
			t.Fatalf("event %d names %v as its parent, want %s", i, prev, timeline[i-1].Event.ID())
		}
	}
	if got := timeline[0].Event.Type(); got != entity.EventTypeCreate {
		t.Fatalf("the chain starts with %s", got)
	}

	room, err := s.events.Room(t.Context(), roomID)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if room.TenantID != tenant.ID {
		t.Fatal("the room belongs to another tenant")
	}
}

func TestAFailedLinkRollsTheWholeRoomBack(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/createRoom", token, map[string]any{
		"initial_state": []map[string]any{{
			"type":      entity.EventTypeName,
			"state_key": "",
			"content":   map[string]any{"name": strings.Repeat("x", entity.MaxEventBytes)},
		}},
	})
	if rec.Code != http.StatusRequestEntityTooLarge || errcode(t, rec) != "M_TOO_LARGE" {
		t.Fatalf("an oversized state event = %d %s", rec.Code, rec.Body)
	}

	for _, table := range []string{"rooms", "events", "room_memberships", "state_snapshots"} {
		var rows int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&rows); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if rows != 0 {
			t.Fatalf("%s holds %d rows after a failed creation", table, rows)
		}
	}

	s.createRoom(t, "alpha.test", token, map[string]any{})
	var rooms int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM rooms").Scan(&rooms); err != nil {
		t.Fatalf("count rooms: %v", err)
	}
	if rooms != 1 {
		t.Fatalf("rooms = %d after one good creation, so the failed one leaked", rooms)
	}
}

func TestPresetsDecideTheJoinRuleAndHistoryVisibility(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	for _, c := range []struct {
		body     map[string]any
		joinRule string
	}{
		{map[string]any{}, entity.JoinRuleInvite},
		{map[string]any{"visibility": entity.VisibilityPublic}, entity.JoinRulePublic},
		{map[string]any{"preset": entity.PresetPublicChat}, entity.JoinRulePublic},
		{map[string]any{"preset": entity.PresetPrivateChat}, entity.JoinRuleInvite},
		{map[string]any{"preset": entity.PresetTrustedPrivateChat}, entity.JoinRuleInvite},
	} {
		roomID := s.createRoom(t, "alpha.test", token, c.body)

		rules := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeJoinRules, "")
		if rules["join_rule"] != c.joinRule {
			t.Fatalf("%v produced join_rule %v, want %s", c.body, rules["join_rule"], c.joinRule)
		}
		history := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeHistoryVisibility, "")
		if history["history_visibility"] != entity.HistoryVisibilityShared {
			t.Fatalf("%v produced history_visibility %v", c.body, history["history_visibility"])
		}
		guest := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeGuestAccess, "")
		if guest["guest_access"] != entity.GuestAccessForbidden {
			t.Fatalf("%v allows guests: %v", c.body, guest["guest_access"])
		}
	}
}

func TestNameAndTopicOverrideTheSameTypesInInitialState(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{
		"name":  "the winner",
		"topic": "the winning topic",
		"initial_state": []map[string]any{
			{"type": entity.EventTypeName, "state_key": "", "content": map[string]any{"name": "the loser"}},
			{"type": entity.EventTypeTopic, "state_key": "", "content": map[string]any{"topic": "the losing topic"}},
		},
	})

	if got := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeName, "")["name"]; got != "the winner" {
		t.Fatalf("name = %v", got)
	}
	topic := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeTopic, "")
	if topic["topic"] != "the winning topic" {
		t.Fatalf("topic = %v", topic["topic"])
	}
	if _, ok := topic["m.topic"]; !ok {
		t.Fatal("a topic set through the topic field carries no rich representation")
	}
}

func TestCreationContentSurvivesExceptTheRoomVersion(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{
		"creation_content": map[string]any{"m.federate": false, "custom": "kept", "room_version": "nonsense"},
	})

	create := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeCreate, "")
	if create["custom"] != "kept" {
		t.Fatalf("a creation_content key was dropped: %v", create)
	}
	if create["m.federate"] != false {
		t.Fatalf("m.federate = %v", create["m.federate"])
	}
	if create["room_version"] != string(entity.DefaultRoomVersion) {
		t.Fatalf("room_version = %v, want %s", create["room_version"], entity.DefaultRoomVersion)
	}
}

func TestRoomVersionIsRefusedByShapeAndBySupport(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	numeric := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/createRoom", token,
		map[string]any{"room_version": 1})
	if numeric.Code != http.StatusBadRequest || errcode(t, numeric) != "M_BAD_JSON" {
		t.Fatalf("a numeric room_version = %d %s", numeric.Code, numeric.Body)
	}

	unknown := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/createRoom", token,
		map[string]any{"room_version": "not-a-version"})
	if unknown.Code != http.StatusBadRequest || errcode(t, unknown) != "M_UNSUPPORTED_ROOM_VERSION" {
		t.Fatalf("an unknown room_version = %d %s", unknown.Code, unknown.Body)
	}

	for _, supported := range entity.SupportedRoomVersions() {
		roomID := s.createRoom(t, "alpha.test", token, map[string]any{"room_version": string(supported)})
		create := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeCreate, "")
		if create["room_version"] != string(supported) {
			t.Fatalf("version %s produced %v", supported, create["room_version"])
		}
	}
}

func TestAStateKeyHoldsOneEventNoMatterHowOftenItIsWritten(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	roomID := s.createRoom(t, "alpha.test", token, map[string]any{})
	path := "/_matrix/client/v3/rooms/" + roomID + "/state/" + entity.EventTypeName + "/"

	for _, name := range []string{"first", "second", "third"} {
		rec := s.do(t, http.MethodPut, "alpha.test", path, token, map[string]any{"name": name})
		if rec.Code != http.StatusOK {
			t.Fatalf("set name %s = %d: %s", name, rec.Code, rec.Body)
		}
	}

	if got := s.stateContent(t, "alpha.test", token, roomID, entity.EventTypeName, "")["name"]; got != "third" {
		t.Fatalf("name = %v, want the newest write", got)
	}

	full := s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+roomID+"/state", token)
	if full.Code != http.StatusOK {
		t.Fatalf("state = %d: %s", full.Code, full.Body)
	}
	var names int
	for _, e := range decode[[]map[string]any](t, full) {
		if e["type"] == entity.EventTypeName {
			names++
		}
	}
	if names != 1 {
		t.Fatalf("the resolved state holds %d name events, want 1", names)
	}
}

func TestAnAliasResolvesOnlyWithinItsOwnDomain(t *testing.T) {
	s := newServer(t)
	_, alphaToken, _ := s.resident(t, "alpha.test", "alice")
	_, betaToken, _ := s.resident(t, "beta.test", "alice")

	alphaRoom := s.createRoom(t, "alpha.test", alphaToken, map[string]any{"room_alias_name": "shared"})
	betaRoom := s.createRoom(t, "beta.test", betaToken, map[string]any{"room_alias_name": "shared"})
	if alphaRoom == betaRoom {
		t.Fatal("two domains produced the same room")
	}

	for _, c := range []struct{ host, token, alias, want string }{
		{"alpha.test", alphaToken, "#shared:alpha.test", alphaRoom},
		{"beta.test", betaToken, "#shared:beta.test", betaRoom},
	} {
		rec := s.get(t, c.host, "/_matrix/client/v3/directory/room/"+c.alias, c.token)
		if rec.Code != http.StatusOK {
			t.Fatalf("resolve %s = %d: %s", c.alias, rec.Code, rec.Body)
		}
		if got := decode[roomBody](t, rec).RoomID; got != c.want {
			t.Fatalf("%s resolved to %s, want %s", c.alias, got, c.want)
		}
	}

	foreign := s.get(t, "alpha.test", "/_matrix/client/v3/directory/room/%23shared:beta.test", alphaToken)
	if foreign.Code != http.StatusBadRequest {
		t.Fatalf("alpha resolved beta's alias: %d %s", foreign.Code, foreign.Body)
	}
}

func TestOnlyMembersCanListTheAliasesOfARoom(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")
	strangerSession := s.register(t, "alpha.test", "stranger", goodPassword)

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"room_alias_name": "listed"})

	mine := s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+roomID+"/aliases", token)
	if mine.Code != http.StatusOK {
		t.Fatalf("the creator cannot list aliases: %d %s", mine.Code, mine.Body)
	}
	listed := decode[struct {
		Aliases []string `json:"aliases"`
	}](t, mine).Aliases
	if len(listed) != 1 || listed[0] != "#listed:alpha.test" {
		t.Fatalf("aliases = %v", listed)
	}

	theirs := s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+roomID+"/aliases", strangerSession.AccessToken)
	if theirs.Code != http.StatusForbidden {
		t.Fatalf("a stranger listed the aliases: %d %s", theirs.Code, theirs.Body)
	}
}

func TestOnlyTheAliasCreatorOrAnOperatorMayDeleteAnAlias(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"visibility": entity.VisibilityPublic})
	s.joinAs(t, tenant, roomID, bob.UserID)

	made := s.do(t, http.MethodPut, "alpha.test", "/_matrix/client/v3/directory/room/%23bobs:alpha.test",
		bob.AccessToken, map[string]any{"room_id": roomID})
	if made.Code != http.StatusOK {
		t.Fatalf("bob could not create an alias: %d %s", made.Code, made.Body)
	}

	carol := s.register(t, "alpha.test", "carol", goodPassword)
	s.joinAs(t, tenant, roomID, carol.UserID)
	theirs := s.do(t, http.MethodDelete, "alpha.test", "/_matrix/client/v3/directory/room/%23bobs:alpha.test",
		carol.AccessToken, nil)
	if theirs.Code != http.StatusForbidden {
		t.Fatalf("carol deleted bob's alias: %d %s", theirs.Code, theirs.Body)
	}

	own := s.do(t, http.MethodDelete, "alpha.test", "/_matrix/client/v3/directory/room/%23bobs:alpha.test",
		bob.AccessToken, nil)
	if own.Code != http.StatusOK {
		t.Fatalf("bob could not delete his own alias: %d %s", own.Code, own.Body)
	}

	operatorAlias := "/_matrix/client/v3/directory/room/%23bobs2:alpha.test"
	if rec := s.do(t, http.MethodPut, "alpha.test", operatorAlias, bob.AccessToken,
		map[string]any{"room_id": roomID}); rec.Code != http.StatusOK {
		t.Fatalf("second alias = %d: %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, http.MethodDelete, "alpha.test", operatorAlias, token, nil); rec.Code != http.StatusOK {
		t.Fatalf("the room creator could not delete a member's alias: %d %s", rec.Code, rec.Body)
	}
}

func TestCanonicalAliasMustNameAnAliasOfThisRoom(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"room_alias_name": "canon"})
	s.createRoom(t, "alpha.test", token, map[string]any{"room_alias_name": "elsewhere"})

	path := "/_matrix/client/v3/rooms/" + roomID + "/state/" + entity.EventTypeCanonicalAlias + "/"

	for name, c := range map[string]struct {
		content map[string]any
		code    string
	}{
		"a malformed alias":     {map[string]any{"alias": "%nonsense:alpha.test"}, "M_INVALID_PARAM"},
		"an alias that is gone": {map[string]any{"alias": "#missing:alpha.test"}, "M_BAD_ALIAS"},
		"another room's alias":  {map[string]any{"alias": "#elsewhere:alpha.test"}, "M_BAD_ALIAS"},
		"a bad alt alias":       {map[string]any{"alias": "#canon:alpha.test", "alt_aliases": []string{"#missing:alpha.test"}}, "M_BAD_ALIAS"},
	} {
		rec := s.do(t, http.MethodPut, "alpha.test", path, token, c.content)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400: %s", name, rec.Code, rec.Body)
		}
		if got := errcode(t, rec); got != c.code {
			t.Fatalf("%s errcode = %s, want %s", name, got, c.code)
		}
	}

	good := s.do(t, http.MethodPut, "alpha.test", path, token, map[string]any{"alias": "#canon:alpha.test"})
	if good.Code != http.StatusOK {
		t.Fatalf("the room's own alias was refused: %d %s", good.Code, good.Body)
	}
}

func TestPublishingToTheDirectoryNeedsAliasAuthority(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", token, map[string]any{"visibility": entity.VisibilityPublic})
	s.joinAs(t, tenant, roomID, bob.UserID)

	path := "/_matrix/client/v3/directory/list/room/" + roomID

	theirs := s.do(t, http.MethodPut, "alpha.test", path, bob.AccessToken,
		map[string]any{"visibility": entity.VisibilityPrivate})
	if theirs.Code != http.StatusForbidden {
		t.Fatalf("a plain member changed the visibility: %d %s", theirs.Code, theirs.Body)
	}

	mine := s.do(t, http.MethodPut, "alpha.test", path, token,
		map[string]any{"visibility": entity.VisibilityPrivate})
	if mine.Code != http.StatusOK {
		t.Fatalf("the creator could not change the visibility: %d %s", mine.Code, mine.Body)
	}

	rec := s.get(t, "alpha.test", path, token)
	if decode[struct {
		Visibility string `json:"visibility"`
	}](t, rec).Visibility != entity.VisibilityPrivate {
		t.Fatalf("visibility did not change: %s", rec.Body)
	}
}

func TestThePublicRoomListNeverCrossesADomain(t *testing.T) {
	s := newServer(t)
	_, alphaToken, _ := s.resident(t, "alpha.test", "alice")
	_, betaToken, _ := s.resident(t, "beta.test", "alice")

	alphaRoom := s.createRoom(t, "alpha.test", alphaToken, map[string]any{
		"visibility": entity.VisibilityPublic, "name": "alpha room",
	})
	betaRoom := s.createRoom(t, "beta.test", betaToken, map[string]any{
		"visibility": entity.VisibilityPublic, "name": "beta room",
	})
	s.createRoom(t, "alpha.test", alphaToken, map[string]any{"name": "kept private"})

	for _, c := range []struct{ host, token, want, absent string }{
		{"alpha.test", alphaToken, alphaRoom, betaRoom},
		{"beta.test", betaToken, betaRoom, alphaRoom},
	} {
		rec := s.get(t, c.host, "/_matrix/client/v3/publicRooms", c.token)
		if rec.Code != http.StatusOK {
			t.Fatalf("publicRooms on %s = %d: %s", c.host, rec.Code, rec.Body)
		}
		body := decode[struct {
			Chunk []struct {
				RoomID           string `json:"room_id"`
				Name             string `json:"name"`
				NumJoinedMembers int    `json:"num_joined_members"`
			} `json:"chunk"`
		}](t, rec)

		if len(body.Chunk) != 1 {
			t.Fatalf("%s lists %d public rooms, want 1: %s", c.host, len(body.Chunk), rec.Body)
		}
		if body.Chunk[0].RoomID != c.want {
			t.Fatalf("%s lists %s, want %s", c.host, body.Chunk[0].RoomID, c.want)
		}
		if body.Chunk[0].NumJoinedMembers != 1 {
			t.Fatalf("%s reports %d members", c.host, body.Chunk[0].NumJoinedMembers)
		}
	}
}

func TestPublicRoomSearchMatchesNameAndTopic(t *testing.T) {
	s := newServer(t)
	_, token, _ := s.resident(t, "alpha.test", "alice")

	wanted := s.createRoom(t, "alpha.test", token, map[string]any{
		"visibility": entity.VisibilityPublic, "name": "Test Name", "topic": "Test Topic Wombles",
	})
	s.createRoom(t, "alpha.test", token, map[string]any{
		"visibility": entity.VisibilityPublic, "name": "Something else",
	})

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/publicRooms", token, map[string]any{
		"filter": map[string]any{"generic_search_term": "wombles"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d: %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Chunk []roomBody `json:"chunk"`
	}](t, rec)
	if len(body.Chunk) != 1 || body.Chunk[0].RoomID != wanted {
		t.Fatalf("search returned %s", rec.Body)
	}
}

func TestARoomOfAnotherDomainIsIndistinguishableFromNoRoomAtAll(t *testing.T) {
	s := newServer(t)
	_, alphaToken, _ := s.resident(t, "alpha.test", "alice")
	_, betaToken, _ := s.resident(t, "beta.test", "alice")

	betaRoom := s.createRoom(t, "beta.test", betaToken, map[string]any{})
	invented := "!" + strings.Repeat("A", 43)

	for _, path := range []string{
		"/_matrix/client/v3/rooms/%s/state",
		"/_matrix/client/v3/rooms/%s/state/m.room.name/",
		"/_matrix/client/v3/rooms/%s/joined_members",
		"/_matrix/client/v3/rooms/%s/members",
		"/_matrix/client/v3/rooms/%s/aliases",
	} {
		foreign := s.get(t, "alpha.test", fmt.Sprintf(path, betaRoom), alphaToken)
		nothing := s.get(t, "alpha.test", fmt.Sprintf(path, invented), alphaToken)

		if foreign.Code != http.StatusForbidden {
			t.Fatalf("%s on another domain's room = %d, want 403: %s", path, foreign.Code, foreign.Body)
		}
		if foreign.Code != nothing.Code || foreign.Body.String() != nothing.Body.String() {
			t.Fatalf("%s tells a real room from an invented one: %d %s vs %d %s",
				path, foreign.Code, foreign.Body, nothing.Code, nothing.Body)
		}
	}

	for _, path := range []string{
		"/_matrix/client/v3/rooms/%s/join",
		"/_matrix/client/v3/rooms/%s/leave",
		"/_matrix/client/v3/rooms/%s/forget",
	} {
		foreign := s.do(t, http.MethodPost, "alpha.test", fmt.Sprintf(path, betaRoom), alphaToken, map[string]any{})
		nothing := s.do(t, http.MethodPost, "alpha.test", fmt.Sprintf(path, invented), alphaToken, map[string]any{})

		if foreign.Code != http.StatusForbidden {
			t.Fatalf("%s on another domain's room = %d, want 403: %s", path, foreign.Code, foreign.Body)
		}
		if foreign.Code != nothing.Code || foreign.Body.String() != nothing.Body.String() {
			t.Fatalf("%s tells a real room from an invented one: %d %s vs %d %s",
				path, foreign.Code, foreign.Body, nothing.Code, nothing.Body)
		}
	}

	if rec := s.get(t, "alpha.test", "/_matrix/client/v3/directory/list/room/"+betaRoom, alphaToken); rec.Code != http.StatusNotFound {
		t.Fatalf("the directory listing of another domain's room = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestJoinedRoomsListsOnlyWhatTheCallerIsIn(t *testing.T) {
	s := newServer(t)
	tenant, token, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	mine := s.createRoom(t, "alpha.test", token, map[string]any{"visibility": entity.VisibilityPublic})
	s.createRoom(t, "alpha.test", token, map[string]any{})
	s.joinAs(t, tenant, mine, bob.UserID)

	rec := s.get(t, "alpha.test", "/_matrix/client/v3/joined_rooms", bob.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("joined_rooms = %d: %s", rec.Code, rec.Body)
	}
	joined := decode[struct {
		JoinedRooms []string `json:"joined_rooms"`
	}](t, rec).JoinedRooms
	if len(joined) != 1 || joined[0] != mine {
		t.Fatalf("bob is in %v, want only %s", joined, mine)
	}
}
