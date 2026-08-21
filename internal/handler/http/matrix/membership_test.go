package matrix_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (s *server) act(t *testing.T, host, path, token string, body map[string]any) int {
	t.Helper()
	return s.do(t, http.MethodPost, host, path, token, body).Code
}

func (s *server) mustAct(t *testing.T, host, path, token string, body map[string]any) {
	t.Helper()
	rec := s.do(t, http.MethodPost, host, path, token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s = %d: %s", path, rec.Code, rec.Body)
	}
}

func (s *server) membership(t *testing.T, host, token, roomID, userID string) string {
	t.Helper()
	path := "/_matrix/client/v3/rooms/" + roomID + "/state/m.room.member/" + userID
	rec := s.get(t, host, path, token)
	if rec.Code != http.StatusOK {
		return ""
	}
	value, _ := decode[map[string]any](t, rec)["membership"].(string)
	return value
}

func joinPath(roomID string) string   { return "/_matrix/client/v3/rooms/" + roomID + "/join" }
func leavePath(roomID string) string  { return "/_matrix/client/v3/rooms/" + roomID + "/leave" }
func forgetPath(roomID string) string { return "/_matrix/client/v3/rooms/" + roomID + "/forget" }
func knockPath(roomID string) string  { return "/_matrix/client/v3/rooms/" + roomID + "/knock" }

func targetPath(roomID, verb string) string {
	return "/_matrix/client/v3/rooms/" + roomID + "/" + verb
}

func TestAnyoneMayJoinAPublicRoomAndNobodyMayJoinAnInviteOnlyOne(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	public := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	private := s.createRoom(t, "alpha.test", alice, map[string]any{})

	s.mustAct(t, "alpha.test", joinPath(public), bob.AccessToken, map[string]any{})
	if got := s.membership(t, "alpha.test", bob.AccessToken, public, bob.UserID); got != entity.MembershipJoin {
		t.Fatalf("membership after joining a public room = %q", got)
	}

	if code := s.act(t, "alpha.test", joinPath(private), bob.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("joining an invite-only room = %d, want 403", code)
	}
}

func TestAnInviteeMayJoinAndAnInviteNeedsPower(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	carol := s.register(t, "alpha.test", "carol", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	s.mustAct(t, "alpha.test", targetPath(roomID, "invite"), alice, map[string]any{"user_id": bob.UserID})

	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipInvite {
		t.Fatalf("membership after an invite = %q", got)
	}
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})

	if code := s.act(t, "alpha.test", targetPath(roomID, "invite"), alice,
		map[string]any{"user_id": bob.UserID}); code != http.StatusForbidden {
		t.Fatalf("inviting a joined user = %d, want 403", code)
	}

	levels := "/_matrix/client/v3/rooms/" + roomID + "/state/m.room.power_levels/"
	rec := s.do(t, http.MethodPut, "alpha.test", levels, alice, map[string]any{"invite": 50})
	if rec.Code != http.StatusOK {
		t.Fatalf("raising the invite level = %d: %s", rec.Code, rec.Body)
	}
	if code := s.act(t, "alpha.test", targetPath(roomID, "invite"), bob.AccessToken,
		map[string]any{"user_id": carol.UserID}); code != http.StatusForbidden {
		t.Fatalf("a powerless user invited someone = %d, want 403", code)
	}
}

func TestABannedUserCannotJoinAndUnbanRestoresThem(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", targetPath(roomID, "ban"), alice, map[string]any{"user_id": bob.UserID})

	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipBan {
		t.Fatalf("membership after a ban = %q", got)
	}
	if code := s.act(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("a banned user joined a public room = %d", code)
	}

	s.mustAct(t, "alpha.test", targetPath(roomID, "unban"), alice, map[string]any{"user_id": bob.UserID})
	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipLeave {
		t.Fatalf("membership after an unban = %q", got)
	}
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
}

func TestUnbanningSomeoneWhoIsNotBannedIsRefused(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})

	rec := s.do(t, http.MethodPost, "alpha.test", targetPath(roomID, "unban"), alice,
		map[string]any{"user_id": bob.UserID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unbanning a joined user = %d, want 400: %s", rec.Code, rec.Body)
	}
	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipJoin {
		t.Fatalf("the refused unban changed the membership to %q", got)
	}
}

func TestKickingNeedsPowerAndAPresentTarget(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	carol := s.register(t, "alpha.test", "carol", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", joinPath(roomID), carol.AccessToken, map[string]any{})

	if code := s.act(t, "alpha.test", targetPath(roomID, "kick"), bob.AccessToken,
		map[string]any{"user_id": carol.UserID}); code != http.StatusForbidden {
		t.Fatalf("a peer kicked a peer = %d, want 403", code)
	}

	s.mustAct(t, "alpha.test", targetPath(roomID, "kick"), alice, map[string]any{"user_id": bob.UserID})
	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipLeave {
		t.Fatalf("membership after a kick = %q", got)
	}

	if code := s.act(t, "alpha.test", targetPath(roomID, "kick"), alice,
		map[string]any{"user_id": bob.UserID}); code != http.StatusForbidden {
		t.Fatalf("kicking someone who already left = %d, want 403", code)
	}

	stranger := s.register(t, "alpha.test", "stranger", goodPassword)
	if code := s.act(t, "alpha.test", targetPath(roomID, "kick"), alice,
		map[string]any{"user_id": stranger.UserID}); code != http.StatusForbidden {
		t.Fatalf("kicking someone never present = %d, want 403", code)
	}
}

func TestLeavingCoversRejectingAnInviteAndIsRefusedToStrangers(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	stranger := s.register(t, "alpha.test", "stranger", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	s.mustAct(t, "alpha.test", targetPath(roomID, "invite"), alice, map[string]any{"user_id": bob.UserID})

	s.mustAct(t, "alpha.test", leavePath(roomID), bob.AccessToken, map[string]any{"reason": "no thanks"})
	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipLeave {
		t.Fatalf("membership after rejecting an invite = %q", got)
	}

	if code := s.act(t, "alpha.test", leavePath(roomID), stranger.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("a stranger left a room they were never in = %d, want 403", code)
	}
}

func TestKnockingNeedsAKnockableRoom(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	if code := s.act(t, "alpha.test", knockPath(roomID), bob.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("knocking on an invite-only room = %d, want 403", code)
	}

	rules := "/_matrix/client/v3/rooms/" + roomID + "/state/m.room.join_rules/"
	if rec := s.do(t, http.MethodPut, "alpha.test", rules, alice,
		map[string]any{"join_rule": entity.JoinRuleKnock}); rec.Code != http.StatusOK {
		t.Fatalf("setting the knock join rule = %d: %s", rec.Code, rec.Body)
	}

	s.mustAct(t, "alpha.test", knockPath(roomID), bob.AccessToken, map[string]any{"reason": "let me in"})
	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipKnock {
		t.Fatalf("membership after knocking = %q", got)
	}

	if code := s.act(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("a knocker joined without an invite = %d, want 403", code)
	}

	s.mustAct(t, "alpha.test", targetPath(roomID, "invite"), alice, map[string]any{"user_id": bob.UserID})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
}

func TestKnockingIsRefusedWhileJoinedOrBanned(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	rules := "/_matrix/client/v3/rooms/" + roomID + "/state/m.room.join_rules/"
	if rec := s.do(t, http.MethodPut, "alpha.test", rules, alice,
		map[string]any{"join_rule": entity.JoinRuleKnock}); rec.Code != http.StatusOK {
		t.Fatalf("setting the knock join rule = %d: %s", rec.Code, rec.Body)
	}

	s.mustAct(t, "alpha.test", targetPath(roomID, "invite"), alice, map[string]any{"user_id": bob.UserID})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	if code := s.act(t, "alpha.test", knockPath(roomID), bob.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("a joined user knocked = %d, want 403", code)
	}

	s.mustAct(t, "alpha.test", targetPath(roomID, "ban"), alice, map[string]any{"user_id": bob.UserID})
	if code := s.act(t, "alpha.test", knockPath(roomID), bob.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("a banned user knocked = %d, want 403", code)
	}
}

func TestNobodyMayGrantMorePowerThanTheyHold(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	carol := s.register(t, "alpha.test", "carol", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", joinPath(roomID), carol.AccessToken, map[string]any{})

	levels := "/_matrix/client/v3/rooms/" + roomID + "/state/m.room.power_levels/"
	base := map[string]any{"users": map[string]any{bob.UserID: 50}}
	if rec := s.do(t, http.MethodPut, "alpha.test", levels, alice, base); rec.Code != http.StatusOK {
		t.Fatalf("the creator could not set power levels = %d: %s", rec.Code, rec.Body)
	}

	tooHigh := map[string]any{"users": map[string]any{bob.UserID: 50, carol.UserID: 100}}
	if rec := s.do(t, http.MethodPut, "alpha.test", levels, bob.AccessToken, tooHigh); rec.Code != http.StatusForbidden {
		t.Fatalf("a user granted more power than they hold = %d: %s", rec.Code, rec.Body)
	}

	demotePeer := map[string]any{"users": map[string]any{bob.UserID: 50, carol.UserID: 50}}
	if rec := s.do(t, http.MethodPut, "alpha.test", levels, alice, demotePeer); rec.Code != http.StatusOK {
		t.Fatalf("the creator could not lift carol = %d: %s", rec.Code, rec.Body)
	}
	peerDemote := map[string]any{"users": map[string]any{bob.UserID: 50, carol.UserID: 0}}
	if rec := s.do(t, http.MethodPut, "alpha.test", levels, bob.AccessToken, peerDemote); rec.Code != http.StatusForbidden {
		t.Fatalf("a peer demoted a peer = %d: %s", rec.Code, rec.Body)
	}

	selfDemote := map[string]any{"users": map[string]any{bob.UserID: 0, carol.UserID: 50}}
	if rec := s.do(t, http.MethodPut, "alpha.test", levels, bob.AccessToken, selfDemote); rec.Code != http.StatusOK {
		t.Fatalf("a user could not demote themselves = %d: %s", rec.Code, rec.Body)
	}
}

func TestARestrictedRoomAdmitsMembersOfTheAllowedRoom(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	outsider := s.register(t, "alpha.test", "outsider", goodPassword)

	gate := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(gate), bob.AccessToken, map[string]any{})

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	rules := "/_matrix/client/v3/rooms/" + roomID + "/state/m.room.join_rules/"
	restricted := map[string]any{
		"join_rule": entity.JoinRuleRestricted,
		"allow": []map[string]any{
			{"type": entity.MembershipTypeRoom, "room_id": gate},
		},
	}
	if rec := s.do(t, http.MethodPut, "alpha.test", rules, alice, restricted); rec.Code != http.StatusOK {
		t.Fatalf("setting a restricted join rule = %d: %s", rec.Code, rec.Body)
	}

	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipJoin {
		t.Fatalf("a member of the allowed room could not join: %q", got)
	}

	member := s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+roomID+"/state/m.room.member/"+bob.UserID, alice)
	via, _ := decode[map[string]any](t, member)["join_authorised_via_users_server"].(string)
	if via == "" {
		t.Fatal("a restricted join carries no authorising user")
	}

	if code := s.act(t, "alpha.test", joinPath(roomID), outsider.AccessToken, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("a non-member of the allowed room joined = %d, want 403", code)
	}
}

func TestForgettingNeedsTheRoomToHaveBeenLeft(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})

	rec := s.do(t, http.MethodPost, "alpha.test", forgetPath(roomID), bob.AccessToken, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forgetting a joined room = %d, want 400: %s", rec.Code, rec.Body)
	}

	s.mustAct(t, "alpha.test", leavePath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", forgetPath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", forgetPath(roomID), bob.AccessToken, map[string]any{})

	if !s.forgotten(t, roomID, bob.UserID) {
		t.Fatal("the room was not recorded as forgotten")
	}

	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	if s.forgotten(t, roomID, bob.UserID) {
		t.Fatal("rejoining did not clear the forgotten flag")
	}
}

func (s *server) forgotten(t *testing.T, roomID, userID string) bool {
	t.Helper()
	var value bool
	err := s.db.QueryRowContext(t.Context(), `
		SELECT m.forgotten FROM room_memberships m
		JOIN rooms r ON r.room_nid = m.room_nid
		WHERE r.room_id = $1 AND m.user_id = $2`, roomID, userID).Scan(&value)
	if err != nil {
		t.Fatalf("read forgotten: %v", err)
	}
	return value
}

func TestTheMemberListFollowsWhatTheCallerMaySee(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)
	carol := s.register(t, "alpha.test", "carol", goodPassword)
	stranger := s.register(t, "alpha.test", "stranger", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", leavePath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", joinPath(roomID), carol.AccessToken, map[string]any{})

	joined := s.memberIDs(t, "alpha.test", alice, roomID, "")
	if len(joined) != 3 {
		t.Fatalf("the current member list is %v", joined)
	}

	atLeave := s.memberIDs(t, "alpha.test", bob.AccessToken, roomID, "")
	if len(atLeave) != 2 {
		t.Fatalf("bob sees %v, want the list as it was when he left", atLeave)
	}
	for _, id := range atLeave {
		if id == carol.UserID {
			t.Fatal("a user who left can see someone who joined afterwards")
		}
	}

	onlyJoined := s.memberIDs(t, "alpha.test", alice, roomID, "membership=join")
	if len(onlyJoined) != 2 {
		t.Fatalf("the join filter returned %v", onlyJoined)
	}
	notLeft := s.memberIDs(t, "alpha.test", alice, roomID, "not_membership=leave")
	if len(notLeft) != 2 {
		t.Fatalf("the not_membership filter returned %v", notLeft)
	}

	if rec := s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+roomID+"/members", stranger.AccessToken); rec.Code != http.StatusForbidden {
		t.Fatalf("a stranger read the member list = %d: %s", rec.Code, rec.Body)
	}

	if rec := s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+roomID+"/members?at=nonsense", alice); rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed point-in-time token = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func (s *server) memberIDs(t *testing.T, host, token, roomID, query string) []string {
	t.Helper()
	path := "/_matrix/client/v3/rooms/" + roomID + "/members"
	if query != "" {
		path += "?" + query
	}
	rec := s.get(t, host, path, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("members = %d: %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Chunk []struct {
			StateKey string `json:"state_key"`
		} `json:"chunk"`
	}](t, rec)
	out := make([]string, 0, len(body.Chunk))
	for _, e := range body.Chunk {
		out = append(out, e.StateKey)
	}
	return out
}

func TestAUserOfAnotherDomainCannotBeInvited(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	_, _, betaID := s.resident(t, "beta.test", "alice")

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{})
	if code := s.act(t, "alpha.test", targetPath(roomID, "invite"), alice,
		map[string]any{"user_id": betaID}); code != http.StatusForbidden {
		t.Fatalf("inviting a user of another domain = %d, want 403", code)
	}

	invented := fmt.Sprintf("@ghost:%s", "alpha.test")
	if code := s.act(t, "alpha.test", targetPath(roomID, "invite"), alice,
		map[string]any{"user_id": invented}); code != http.StatusForbidden {
		t.Fatalf("inviting a user who does not exist = %d, want 403", code)
	}
}

func TestJoiningTwiceIsAcceptedAndKeepsOneExtremity(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})
	s.mustAct(t, "alpha.test", joinPath(roomID), bob.AccessToken, map[string]any{})

	timeline, err := s.events.Page(t.Context(), roomID, entity.PageRequest{Limit: entity.MaxPageLimit})
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	for i := 1; i < len(timeline); i++ {
		prev := timeline[i].Event.PrevEvents()
		if len(prev) != 1 || prev[0] != timeline[i-1].Event.ID() {
			t.Fatalf("event %d forked the room: %v", i, prev)
		}
	}
	if got := s.membership(t, "alpha.test", alice, roomID, bob.UserID); got != entity.MembershipJoin {
		t.Fatalf("membership after joining twice = %q", got)
	}
}

func TestAJoinCarriesTheProfileUnlessTheClientOverridesIt(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	name := "/_matrix/client/v3/profile/" + bob.UserID + "/displayname"
	if rec := s.do(t, http.MethodPut, "alpha.test", name, bob.AccessToken,
		map[string]any{"displayname": "Bobby"}); rec.Code != http.StatusOK {
		t.Fatalf("setting a display name = %d: %s", rec.Code, rec.Body)
	}

	shared := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(shared), bob.AccessToken, map[string]any{})
	member := s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+shared+"/state/m.room.member/"+bob.UserID, alice)
	if got := decode[map[string]any](t, member)["displayname"]; got != "Bobby" {
		t.Fatalf("the join carries displayname %v, want the profile", got)
	}

	other := s.createRoom(t, "alpha.test", alice, map[string]any{"visibility": entity.VisibilityPublic})
	s.mustAct(t, "alpha.test", joinPath(other), bob.AccessToken, map[string]any{"displayname": "Robert"})
	member = s.get(t, "alpha.test", "/_matrix/client/v3/rooms/"+other+"/state/m.room.member/"+bob.UserID, alice)
	if got := decode[map[string]any](t, member)["displayname"]; got != "Robert" {
		t.Fatalf("a room-specific display name was ignored: %v", got)
	}
}

func TestJoiningByAliasReachesTheSameRoom(t *testing.T) {
	s := newServer(t)
	_, alice, _ := s.resident(t, "alpha.test", "alice")
	bob := s.register(t, "alpha.test", "bob", goodPassword)

	roomID := s.createRoom(t, "alpha.test", alice, map[string]any{
		"visibility": entity.VisibilityPublic, "room_alias_name": "gathering",
	})

	rec := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/join/%23gathering:alpha.test",
		bob.AccessToken, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("joining by alias = %d: %s", rec.Code, rec.Body)
	}
	if got := decode[roomBody](t, rec).RoomID; got != roomID {
		t.Fatalf("joining by alias reached %s, want %s", got, roomID)
	}

	foreign := s.do(t, http.MethodPost, "alpha.test", "/_matrix/client/v3/join/%23gathering:beta.test",
		bob.AccessToken, map[string]any{})
	if foreign.Code == http.StatusOK {
		t.Fatal("an alias of another domain resolved")
	}
}
