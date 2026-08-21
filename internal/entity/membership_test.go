package entity_test

import (
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func (r *room) member(target, sender, membership string, rest map[string]any) entity.Event {
	r.t.Helper()
	content := map[string]any{"membership": membership}
	for k, v := range rest {
		content[k] = v
	}
	return r.build(entity.EventTypeMember, ptr(target), sender, content)
}

func (r *room) setJoinRule(rule string, rest map[string]any) {
	r.t.Helper()
	content := map[string]any{"join_rule": rule}
	for k, v := range rest {
		content[k] = v
	}
	r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName, content))
}

func (r *room) setLevels(content map[string]any) {
	r.t.Helper()
	r.commit(r.build(entity.EventTypePowerLevels, ptr(""), "@creator:"+serverName, content))
}

func openRoom(t *testing.T, id entity.RoomVersionID) *room {
	t.Helper()
	r := newRoom(t, id, "@creator:"+serverName)
	r.commit(r.join("@creator:" + serverName))
	r.setJoinRule(entity.JoinRulePublic, nil)
	return r
}

func TestBanningNeedsPowerAndOutranksTheTarget(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := openRoom(t, id)
		creator := "@creator:" + serverName
		r.commit(r.join("@bob:" + serverName))
		r.commit(r.join("@carol:" + serverName))

		peer := r.member("@carol:"+serverName, "@bob:"+serverName, entity.MembershipBan, nil)
		if err := r.authorise(peer); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a powerless user banned a peer: %v", err)
		}

		r.commit(r.member("@bob:"+serverName, creator, entity.MembershipBan, nil))
		if got := r.state.Membership("@bob:" + serverName); got != entity.MembershipBan {
			t.Fatalf("membership after a ban = %q", got)
		}

		rejoin := r.join("@bob:" + serverName)
		if err := r.authorise(rejoin); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a banned user rejoined a public room: %v", err)
		}
	})
}

func TestUnbanningNeedsTheBanLevel(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := openRoom(t, id)
		creator := "@creator:" + serverName
		bob := "@bob:" + serverName
		kicker := "@kicker:" + serverName

		r.commit(r.join(bob))
		r.commit(r.join(kicker))
		r.setLevels(map[string]any{
			"ban":   100,
			"kick":  50,
			"users": r.usersWith(map[string]any{kicker: 50}),
		})
		r.commit(r.member(bob, creator, entity.MembershipBan, nil))

		byKicker := r.member(bob, kicker, entity.MembershipLeave, nil)
		if err := r.authorise(byKicker); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("someone who may only kick unbanned a user: %v", err)
		}

		r.commit(r.member(bob, creator, entity.MembershipLeave, nil))
		if got := r.state.Membership(bob); got != entity.MembershipLeave {
			t.Fatalf("membership after an unban = %q", got)
		}
	})
}

func TestInvitingNeedsPowerAndAnAbsentTarget(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := openRoom(t, id)
		creator := "@creator:" + serverName
		bob := "@bob:" + serverName
		carol := "@carol:" + serverName

		r.commit(r.join(bob))
		r.setLevels(map[string]any{"invite": 50, "users": r.usersWith(nil)})

		byBob := r.member(carol, bob, entity.MembershipInvite, nil)
		if err := r.authorise(byBob); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a powerless user issued an invite: %v", err)
		}

		alreadyJoined := r.member(bob, creator, entity.MembershipInvite, nil)
		if err := r.authorise(alreadyJoined); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a joined user was invited again: %v", err)
		}

		r.commit(r.member(carol, creator, entity.MembershipBan, nil))
		banned := r.member(carol, creator, entity.MembershipInvite, nil)
		if err := r.authorise(banned); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a banned user was invited: %v", err)
		}
	})
}

func TestAnOutsiderCannotSendAMembershipForSomebodyElse(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := openRoom(t, id)
		stranger := "@stranger:" + serverName
		bob := "@bob:" + serverName
		r.commit(r.join(bob))

		for name, e := range map[string]entity.Event{
			"a join for someone else":   r.member(bob, stranger, entity.MembershipJoin, nil),
			"a kick by a non-member":    r.member(bob, stranger, entity.MembershipLeave, nil),
			"a ban by a non-member":     r.member(bob, stranger, entity.MembershipBan, nil),
			"an invite by a non-member": r.member(stranger, stranger, entity.MembershipInvite, nil),
		} {
			if err := r.authorise(e); !errors.Is(err, entity.ErrAuthFailed) {
				t.Fatalf("%s was authorised: %v", name, err)
			}
		}
	})
}

func TestAKnockerMustBeInvitedBeforeJoining(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		creator := "@creator:" + serverName
		bob := "@bob:" + serverName
		r.commit(r.join(creator))
		r.setJoinRule(entity.JoinRuleKnock, nil)

		r.commit(r.member(bob, bob, entity.MembershipKnock, nil))
		if got := r.state.Membership(bob); got != entity.MembershipKnock {
			t.Fatalf("membership after a knock = %q", got)
		}

		straightIn := r.join(bob)
		if err := r.authorise(straightIn); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a knocker joined without an invite: %v", err)
		}

		withdraw := r.member(bob, bob, entity.MembershipLeave, nil)
		if err := r.authorise(withdraw); err != nil {
			t.Fatalf("a knocker could not withdraw: %v", err)
		}

		r.commit(r.member(bob, creator, entity.MembershipInvite, nil))
		r.commit(r.join(bob))
	})
}

func TestAKnockNeedsAKnockableRoomAndAnAbsentSender(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := openRoom(t, id)
		bob := "@bob:" + serverName

		onPublic := r.member(bob, bob, entity.MembershipKnock, nil)
		if err := r.authorise(onPublic); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a knock landed on a public room: %v", err)
		}

		r.setJoinRule(entity.JoinRuleKnock, nil)
		r.commit(r.member(bob, bob, entity.MembershipKnock, nil))
		r.commit(r.member(bob, "@creator:"+serverName, entity.MembershipInvite, nil))
		r.commit(r.join(bob))

		again := r.member(bob, bob, entity.MembershipKnock, nil)
		if err := r.authorise(again); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a joined user knocked: %v", err)
		}
	})
}

func TestARestrictedJoinIsRefusedWithoutAWorkingAuthorisingUser(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		creator := "@creator:" + serverName
		bob := "@bob:" + serverName
		absent := "@absent:" + serverName

		r.commit(r.join(creator))
		r.setJoinRule(entity.JoinRuleRestricted, map[string]any{
			"allow": []any{map[string]any{
				"type": entity.MembershipTypeRoom, "room_id": "!gate:" + serverName,
			}},
		})

		bare := r.join(bob)
		if err := r.authorise(bare); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a restricted join with no authorising user was allowed: %v", err)
		}

		notJoined := r.member(bob, bob, entity.MembershipJoin, map[string]any{
			"join_authorised_via_users_server": absent,
		})
		if err := r.authorise(notJoined); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("an authorising user who is not in the room was accepted: %v", err)
		}

		good := r.member(bob, bob, entity.MembershipJoin, map[string]any{
			"join_authorised_via_users_server": creator,
		})
		if err := r.authorise(good); err != nil {
			t.Fatalf("a properly authorised restricted join was refused: %v", err)
		}
	})
}

func TestARestrictedRoomReadsItsAllowList(t *testing.T) {
	r := newRoom(t, entity.DefaultRoomVersion, "@creator:"+serverName)
	r.commit(r.join("@creator:" + serverName))
	r.setJoinRule(entity.JoinRuleRestricted, map[string]any{
		"allow": []any{
			map[string]any{"type": entity.MembershipTypeRoom, "room_id": "!gate:" + serverName},
			map[string]any{"type": "m.something_else", "room_id": "!other:" + serverName},
			map[string]any{"type": entity.MembershipTypeRoom},
			"not an object",
		},
	})

	allowed := r.state.AllowedRooms()
	if len(allowed) != 1 || allowed[0] != "!gate:"+serverName {
		t.Fatalf("allowed rooms = %v, want only the well formed entry", allowed)
	}
	if !r.state.Restricted() {
		t.Fatal("a restricted room does not report itself as restricted")
	}
	if r.state.AcceptsKnocks() {
		t.Fatal("a restricted room claims to accept knocks")
	}
}

func TestDemotingAPeerIsRefusedButDemotingYourselfIsNot(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := openRoom(t, id)
		bob := "@bob:" + serverName
		carol := "@carol:" + serverName
		r.commit(r.join(bob))
		r.commit(r.join(carol))
		r.setLevels(map[string]any{"users": r.usersWith(map[string]any{bob: 50, carol: 50})})

		demotePeer := r.build(entity.EventTypePowerLevels, ptr(""), bob,
			map[string]any{"users": r.usersWith(map[string]any{bob: 50, carol: 0})})
		if err := r.authorise(demotePeer); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a peer demoted a peer: %v", err)
		}

		promoteAbove := r.build(entity.EventTypePowerLevels, ptr(""), bob,
			map[string]any{"users": r.usersWith(map[string]any{bob: 50, carol: 100})})
		if err := r.authorise(promoteAbove); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a user granted more power than they hold: %v", err)
		}

		demoteSelf := r.build(entity.EventTypePowerLevels, ptr(""), bob,
			map[string]any{"users": r.usersWith(map[string]any{bob: 0, carol: 50})})
		if err := r.authorise(demoteSelf); err != nil {
			t.Fatalf("a user could not demote themselves: %v", err)
		}
	})
}

func TestAMembershipEventNeedsAKnownMembership(t *testing.T) {
	r := openRoom(t, entity.DefaultRoomVersion)
	bob := "@bob:" + serverName

	for name, content := range map[string]map[string]any{
		"an unknown membership": {"membership": "lurking"},
		"an empty membership":   {"membership": ""},
		"no membership at all":  {},
	} {
		e := r.build(entity.EventTypeMember, ptr(bob), bob, content)
		if err := r.authorise(e); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("%s was authorised: %v", name, err)
		}
	}
}

func TestAMembershipChangeCarriesWhatTheRulesNeed(t *testing.T) {
	base := entity.MembershipChange{
		RoomID:     "!room:" + serverName,
		Sender:     "@alice:" + serverName,
		Target:     "@bob:" + serverName,
		Membership: entity.MembershipInvite,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("a plain invite was refused: %v", err)
	}

	content := base.Content()
	if content["membership"] != entity.MembershipInvite {
		t.Fatalf("content = %v", content)
	}
	if _, ok := content["displayname"]; !ok {
		t.Fatal("an invite carries no displayname key")
	}
	if _, ok := content["reason"]; ok {
		t.Fatal("an empty reason was written into the content")
	}

	withReason := base
	withReason.Reason = "because"
	if withReason.Content()["reason"] != "because" {
		t.Fatal("a reason was dropped")
	}

	leave := base
	leave.Membership = entity.MembershipLeave
	if _, ok := leave.Content()["displayname"]; ok {
		t.Fatal("a leave carries a displayname")
	}

	for name, mutate := range map[string]func(*entity.MembershipChange){
		"an unknown membership": func(m *entity.MembershipChange) { m.Membership = "lurking" },
		"a bad sender":          func(m *entity.MembershipChange) { m.Sender = "alice" },
		"a bad target":          func(m *entity.MembershipChange) { m.Target = "bob" },
		"no room":               func(m *entity.MembershipChange) { m.RoomID = "" },
		"a bad authoriser":      func(m *entity.MembershipChange) { m.AuthorisedBy = "nobody" },
	} {
		in := base
		mutate(&in)
		if err := in.Validate(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestAMembersFilterRefusesWhatItCannotAnswer(t *testing.T) {
	if err := (entity.MembersFilter{}).Validate(); err != nil {
		t.Fatalf("an empty filter was refused: %v", err)
	}
	if err := (entity.MembersFilter{At: "s1"}).Validate(); !errors.Is(err, entity.ErrPointInTime) {
		t.Fatalf("a point in time query = %v, want ErrPointInTime", err)
	}
	if err := (entity.MembersFilter{Membership: "lurking"}).Validate(); !errors.Is(err, entity.ErrUnknownMembership) {
		t.Fatalf("an unknown membership = %v", err)
	}

	filter := entity.MembersFilter{Membership: entity.MembershipJoin}
	if !filter.Keeps(entity.MembershipJoin) || filter.Keeps(entity.MembershipLeave) {
		t.Fatal("the membership filter does not select")
	}
	filter = entity.MembersFilter{NotMembership: entity.MembershipLeave}
	if filter.Keeps(entity.MembershipLeave) || !filter.Keeps(entity.MembershipJoin) {
		t.Fatal("the not_membership filter does not exclude")
	}
}
