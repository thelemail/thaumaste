package entity_test

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

type room struct {
	t       *testing.T
	version entity.RoomVersion
	keyID   entity.KeyID
	key     ed25519.PrivateKey
	roomID  string
	state   entity.StateMap
	last    entity.Event
}

func newRoom(t *testing.T, id entity.RoomVersionID, creator string, additional ...string) *room {
	t.Helper()
	v := version(t, id)
	keyID, _, priv := signingKey(t, 9)

	r := &room{t: t, version: v, keyID: keyID, key: priv, state: entity.StateMap{}}

	content := map[string]any{"room_version": string(v.ID)}
	if len(additional) > 0 {
		extra := make([]any, len(additional))
		for i, id := range additional {
			extra[i] = id
		}
		content["additional_creators"] = extra
	}

	b := entity.EventBuilder{
		Version:        v,
		Type:           entity.EventTypeCreate,
		StateKey:       ptr(""),
		Sender:         creator,
		Content:        content,
		OriginServerTS: 1,
	}
	if v.CreateCarriesRoomID {
		b.RoomID = "!seed:" + serverName
	}
	create, err := b.Build(entity.KeySigner(serverName, keyID, priv))
	if err != nil {
		t.Fatalf("build create: %v", err)
	}
	roomID, err := entity.RoomIDFor(create, v, serverName, "seed")
	if err != nil {
		t.Fatalf("RoomIDFor: %v", err)
	}
	r.roomID = roomID
	r.last = create
	r.state = r.state.Apply(create)

	if err := entity.Authorise(create, entity.StateMap{}, v); err != nil {
		t.Fatalf("create is not authorised: %v", err)
	}
	return r
}

func (r *room) build(eventType string, stateKey *string, sender string, content map[string]any) entity.Event {
	r.t.Helper()
	b := entity.EventBuilder{
		Version:        r.version,
		RoomID:         r.roomID,
		Type:           eventType,
		StateKey:       stateKey,
		Sender:         sender,
		Content:        content,
		PrevEvents:     []string{r.last.ID()},
		PrevDepth:      r.last.Depth(),
		OriginServerTS: r.last.OriginServerTS() + 1,
	}
	b.AuthEvents = entity.SelectAuthEvents(b, r.state)
	e, err := b.Build(entity.KeySigner(serverName, r.keyID, r.key))
	if err != nil {
		r.t.Fatalf("build %s: %v", eventType, err)
	}
	return e
}

func (r *room) authorise(e entity.Event) error {
	return entity.Authorise(e, r.state, r.version)
}

func (r *room) commit(e entity.Event) {
	r.t.Helper()
	if err := r.authorise(e); err != nil {
		r.t.Fatalf("commit %s: %v", e.Type(), err)
	}
	r.state = r.state.Apply(e)
	r.last = e
}

// usersWith names the creator only where the version allows it. Under v11 the creator is an
// ordinary user who happens to hold 100, and dropping them from users demotes them; under v12
// naming them at all is refused.
func (r *room) usersWith(rest map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range rest {
		out[k] = v
	}
	if !r.version.CreatorsOutrankPowerLevels {
		out["@creator:"+serverName] = 100
	}
	return out
}

func (r *room) join(user string) entity.Event {
	r.t.Helper()
	return r.build(entity.EventTypeMember, ptr(user), user, map[string]any{"membership": entity.MembershipJoin})
}

func eachVersion(t *testing.T, fn func(t *testing.T, id entity.RoomVersionID)) {
	t.Helper()
	for _, id := range entity.SupportedRoomVersions() {
		t.Run(string(id), func(t *testing.T) { fn(t, id) })
	}
}

func TestTheCreatorCanJoinTheirOwnRoom(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))

		if got := r.state.Membership("@creator:" + serverName); got != entity.MembershipJoin {
			t.Fatalf("membership = %q", got)
		}
	})
}

func TestAStrangerCannotJoinAnInviteOnlyRoom(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))

		if err := r.authorise(r.join("@stranger:" + serverName)); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}
	})
}

func TestAnInvitedUserCanJoin(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const guest = "@guest:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeMember, ptr(guest), "@creator:"+serverName,
			map[string]any{"membership": entity.MembershipInvite}))
		r.commit(r.join(guest))

		if got := r.state.Membership(guest); got != entity.MembershipJoin {
			t.Fatalf("membership = %q", got)
		}
	})
}

func TestAnyoneCanJoinAPublicRoom(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRulePublic}))

		r.commit(r.join("@stranger:" + serverName))
	})
}

func TestABannedUserCannotJoinEvenAPublicRoom(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const outcast = "@outcast:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRulePublic}))
		r.commit(r.build(entity.EventTypeMember, ptr(outcast), "@creator:"+serverName,
			map[string]any{"membership": entity.MembershipBan}))

		if err := r.authorise(r.join(outcast)); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}
	})
}

func TestAUserWhoIsNotJoinedCannotSendMessages(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))

		outsider := r.build(entity.EventTypeMessage, nil, "@stranger:"+serverName,
			map[string]any{"body": "hello"})
		if err := r.authorise(outsider); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}

		r.commit(r.build(entity.EventTypeMessage, nil, "@creator:"+serverName,
			map[string]any{"body": "hello"}))
	})
}

func TestAStateKeyThatNamesAnotherUserIsRefused(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))

		e := r.build("m.custom.state", ptr("@someone:"+serverName), "@creator:"+serverName, map[string]any{})
		if err := r.authorise(e); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}
	})
}

func TestAUserCannotGrantThemselvesMorePowerThanTheyHold(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const member = "@member:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRulePublic}))
		r.commit(r.join(member))
		r.commit(r.build(entity.EventTypePowerLevels, ptr(""), "@creator:"+serverName, map[string]any{
			"users":          r.usersWith(map[string]any{member: 0}),
			"users_default":  0,
			"state_default":  50,
			"events_default": 0,
		}))

		grab := r.build(entity.EventTypePowerLevels, ptr(""), member, map[string]any{
			"users": r.usersWith(map[string]any{member: 100}),
		})
		if err := r.authorise(grab); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}
	})
}

func TestAKickNeedsPowerOverTheTarget(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const member = "@member:" + serverName
		const other = "@other:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRulePublic}))
		r.commit(r.join(member))
		r.commit(r.join(other))

		kick := r.build(entity.EventTypeMember, ptr(other), member,
			map[string]any{"membership": entity.MembershipLeave})
		if err := r.authorise(kick); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a peer kicked a peer: %v", err)
		}

		r.commit(r.build(entity.EventTypeMember, ptr(other), "@creator:"+serverName,
			map[string]any{"membership": entity.MembershipLeave}))
	})
}

func TestAnyoneMayLeaveOnTheirOwn(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const member = "@member:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRulePublic}))
		r.commit(r.join(member))

		r.commit(r.build(entity.EventTypeMember, ptr(member), member,
			map[string]any{"membership": entity.MembershipLeave}))
	})
}

func TestKnockingNeedsAKnockRoom(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const guest = "@guest:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))

		knock := r.build(entity.EventTypeMember, ptr(guest), guest,
			map[string]any{"membership": entity.MembershipKnock})
		if err := r.authorise(knock); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("knocked on an invite-only room: %v", err)
		}

		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRuleKnock}))
		r.commit(r.build(entity.EventTypeMember, ptr(guest), guest,
			map[string]any{"membership": entity.MembershipKnock}))
	})
}

func TestARestrictedJoinNeedsAnAuthorisingUserWhoCanInvite(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const guest = "@guest:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRuleRestricted}))

		bare := r.build(entity.EventTypeMember, ptr(guest), guest,
			map[string]any{"membership": entity.MembershipJoin})
		if err := r.authorise(bare); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a restricted join succeeded without an authorising user: %v", err)
		}

		absent := r.build(entity.EventTypeMember, ptr(guest), guest, map[string]any{
			"membership":                       entity.MembershipJoin,
			"join_authorised_via_users_server": "@nobody:" + serverName,
		})
		if err := r.authorise(absent); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("an absent authorising user was accepted: %v", err)
		}

		r.commit(r.build(entity.EventTypeMember, ptr(guest), guest, map[string]any{
			"membership":                       entity.MembershipJoin,
			"join_authorised_via_users_server": "@creator:" + serverName,
		}))
	})
}

func TestAThirdPartyInviteNeedsPowerToInvite(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const member = "@member:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeJoinRules, ptr(""), "@creator:"+serverName,
			map[string]any{"join_rule": entity.JoinRulePublic}))
		r.commit(r.join(member))
		r.commit(r.build(entity.EventTypePowerLevels, ptr(""), "@creator:"+serverName, map[string]any{
			"users":  r.usersWith(map[string]any{member: 0}),
			"invite": 50,
		}))

		refused := r.build(entity.EventTypeThirdPartyInvite, ptr("token"), member, map[string]any{})
		if err := r.authorise(refused); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("a member without invite power issued one: %v", err)
		}

		r.commit(r.build(entity.EventTypeThirdPartyInvite, ptr("token"), "@creator:"+serverName, map[string]any{}))
	})
}

func TestAnInviteCarryingAThirdPartyTokenSelectsThatInvite(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		const guest = "@guest:" + serverName
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))
		r.commit(r.build(entity.EventTypeThirdPartyInvite, ptr("tok"), "@creator:"+serverName, map[string]any{}))

		b := entity.EventBuilder{
			Version:    r.version,
			RoomID:     r.roomID,
			Type:       entity.EventTypeMember,
			StateKey:   ptr(guest),
			Sender:     "@creator:" + serverName,
			PrevEvents: []string{r.last.ID()},
			PrevDepth:  r.last.Depth(),
			Content: map[string]any{
				"membership": entity.MembershipInvite,
				"third_party_invite": map[string]any{
					"signed": map[string]any{"token": "tok", "mxid": guest},
				},
			},
		}
		selected := entity.SelectAuthEvents(b, r.state)

		invite, _ := r.state.Get(entity.EventTypeThirdPartyInvite, "tok")
		var found bool
		for _, id := range selected {
			if id == invite.ID() {
				found = true
			}
		}
		if !found {
			t.Fatalf("the third party invite was not selected: %v", selected)
		}
	})
}

func TestCreateIsSelectedForAuthEventsOnlyBeforeVersionTwelve(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		join := r.join("@creator:" + serverName)

		create, _ := r.state.Create()
		var namesCreate bool
		for _, authID := range join.AuthEvents() {
			if authID == create.ID() {
				namesCreate = true
			}
		}

		if namesCreate != r.version.CreateInAuthEvents {
			t.Fatalf("version %s names create in auth_events = %v, want %v",
				id, namesCreate, r.version.CreateInAuthEvents)
		}
	})
}

func TestVersionTwelveRefusesAnAuthEventsListThatNamesCreate(t *testing.T) {
	r := newRoom(t, entity.RoomVersion12, "@creator:"+serverName)
	create, _ := r.state.Create()
	join := r.join("@creator:" + serverName)

	err := entity.CheckAuthEvents(join, []entity.Event{create}, r.version)
	if !errors.Is(err, entity.ErrAuthEventsInvalid) {
		t.Fatalf("error = %v, want ErrAuthEventsInvalid", err)
	}
}

func TestVersionElevenRequiresAuthEventsToNameCreate(t *testing.T) {
	r := newRoom(t, entity.RoomVersion11, "@creator:"+serverName)
	join := r.join("@creator:" + serverName)

	if err := entity.CheckAuthEvents(join, []entity.Event{}, r.version); !errors.Is(err, entity.ErrAuthEventsInvalid) {
		t.Fatalf("error = %v, want ErrAuthEventsInvalid", err)
	}
	create, _ := r.state.Create()
	if err := entity.CheckAuthEvents(join, []entity.Event{create}, r.version); err != nil {
		t.Fatalf("CheckAuthEvents: %v", err)
	}
}

func TestDuplicateAuthEventsAreRefused(t *testing.T) {
	r := newRoom(t, entity.RoomVersion11, "@creator:"+serverName)
	create, _ := r.state.Create()
	join := r.join("@creator:" + serverName)

	err := entity.CheckAuthEvents(join, []entity.Event{create, create}, r.version)
	if !errors.Is(err, entity.ErrAuthEventsInvalid) {
		t.Fatalf("error = %v, want ErrAuthEventsInvalid", err)
	}
}

func TestACreatorOutranksEveryPowerLevelUnderVersionTwelve(t *testing.T) {
	const creator = "@creator:" + serverName
	r := newRoom(t, entity.RoomVersion12, creator)
	r.commit(r.join(creator))

	// Nobody may name a creator in users, so the creator cannot be demoted at all.
	demote := r.build(entity.EventTypePowerLevels, ptr(""), creator, map[string]any{
		"users": map[string]any{creator: 0},
	})
	if err := r.authorise(demote); !errors.Is(err, entity.ErrPowerLevelsNameCreator) {
		t.Fatalf("error = %v, want ErrPowerLevelsNameCreator", err)
	}

	r.commit(r.build(entity.EventTypePowerLevels, ptr(""), creator, map[string]any{
		"users":         map[string]any{},
		"users_default": 0,
		"state_default": 100,
		"ban":           100,
	}))

	levels, err := r.state.PowerLevels(r.version)
	if err != nil {
		t.Fatalf("PowerLevels: %v", err)
	}
	if !levels.UserLevel(creator).IsCreator() {
		t.Fatal("the creator lost creator power")
	}
	if !levels.UserLevel(creator).AtLeast(entity.Power(100)) {
		t.Fatal("the creator does not outrank 100")
	}
	if levels.UserLevel(creator).GreaterThan(entity.CreatorPower()) {
		t.Fatal("one creator outranks another")
	}

	// Even with state_default at 100 and no users entry, the creator can still send state.
	r.commit(r.build(entity.EventTypeJoinRules, ptr(""), creator,
		map[string]any{"join_rule": entity.JoinRulePublic}))
}

func TestUnderVersionElevenTheCreatorIsJustAUserWithAHundred(t *testing.T) {
	const creator = "@creator:" + serverName
	r := newRoom(t, entity.RoomVersion11, creator)
	r.commit(r.join(creator))

	levels, err := r.state.PowerLevels(r.version)
	if err != nil {
		t.Fatalf("PowerLevels: %v", err)
	}
	if levels.UserLevel(creator).IsCreator() {
		t.Fatal("v11 gave the creator unbounded power")
	}
	if levels.UserLevel(creator).Value() != 100 {
		t.Fatalf("creator level = %d, want 100", levels.UserLevel(creator).Value())
	}

	// And unlike v12, naming the creator in users is allowed.
	r.commit(r.build(entity.EventTypePowerLevels, ptr(""), creator, map[string]any{
		"users": map[string]any{creator: 100},
	}))
}

func TestAdditionalCreatorsOutrankPowerLevelsToo(t *testing.T) {
	const creator = "@creator:" + serverName
	const second = "@second:" + serverName
	r := newRoom(t, entity.RoomVersion12, creator, second)
	r.commit(r.join(creator))
	r.commit(r.build(entity.EventTypeJoinRules, ptr(""), creator,
		map[string]any{"join_rule": entity.JoinRulePublic}))
	r.commit(r.join(second))

	levels, err := r.state.PowerLevels(r.version)
	if err != nil {
		t.Fatalf("PowerLevels: %v", err)
	}
	if !levels.UserLevel(second).IsCreator() {
		t.Fatal("an additional creator did not get creator power")
	}

	named := r.build(entity.EventTypePowerLevels, ptr(""), creator, map[string]any{
		"users": map[string]any{second: 50},
	})
	if err := r.authorise(named); !errors.Is(err, entity.ErrPowerLevelsNameCreator) {
		t.Fatalf("error = %v, want ErrPowerLevelsNameCreator", err)
	}
}

func TestARoomIDMustNameItsCreateEventUnderVersionTwelve(t *testing.T) {
	r := newRoom(t, entity.RoomVersion12, "@creator:"+serverName)
	create, _ := r.state.Create()

	if r.roomID != "!"+create.ID()[1:] {
		t.Fatalf("room id %s does not name the create event %s", r.roomID, create.ID())
	}

	imposter := entity.EventBuilder{
		Version:    r.version,
		RoomID:     "!not-the-create-event:" + serverName,
		Type:       entity.EventTypeMessage,
		Sender:     "@creator:" + serverName,
		PrevEvents: []string{create.ID()},
		PrevDepth:  create.Depth(),
	}
	e, err := imposter.Build(entity.KeySigner(serverName, r.keyID, r.key))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := r.authorise(e); !errors.Is(err, entity.ErrAuthFailed) {
		t.Fatalf("error = %v, want ErrAuthFailed", err)
	}
}

func TestARoomIDCarriesADomainUnderVersionEleven(t *testing.T) {
	r := newRoom(t, entity.RoomVersion11, "@creator:"+serverName)

	if r.roomID != "!seed:"+serverName {
		t.Fatalf("room id = %s", r.roomID)
	}
	create, _ := r.state.Create()
	if create.RoomID() == "" {
		t.Fatal("a v11 create event must carry its room id")
	}
}

func TestACreateEventDeclaringAnotherVersionIsRefused(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	keyID, _, priv := signingKey(t, 11)

	create, err := entity.EventBuilder{
		Version:  v,
		Type:     entity.EventTypeCreate,
		StateKey: ptr(""),
		Sender:   "@creator:" + serverName,
		Content:  map[string]any{"room_version": "11"},
	}.Build(entity.KeySigner(serverName, keyID, priv))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := entity.Authorise(create, entity.StateMap{}, v); !errors.Is(err, entity.ErrUnsupportedRoomVersion) {
		t.Fatalf("error = %v, want ErrUnsupportedRoomVersion", err)
	}
}

func TestARoomThatRefusesFederationRefusesForeignSenders(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		v := version(t, id)
		keyID, _, priv := signingKey(t, 12)

		b := entity.EventBuilder{
			Version:  v,
			Type:     entity.EventTypeCreate,
			StateKey: ptr(""),
			Sender:   "@creator:" + serverName,
			Content: map[string]any{
				"room_version": string(v.ID),
				"m.federate":   false,
			},
		}
		if v.CreateCarriesRoomID {
			b.RoomID = "!seed:" + serverName
		}
		create, err := b.Build(entity.KeySigner(serverName, keyID, priv))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		roomID, err := entity.RoomIDFor(create, v, serverName, "seed")
		if err != nil {
			t.Fatalf("RoomIDFor: %v", err)
		}
		state := entity.StateMap{}.Apply(create)

		foreign, err := entity.EventBuilder{
			Version:    v,
			RoomID:     roomID,
			Type:       entity.EventTypeMember,
			StateKey:   ptr("@guest:elsewhere.test"),
			Sender:     "@guest:elsewhere.test",
			Content:    map[string]any{"membership": entity.MembershipJoin},
			PrevEvents: []string{create.ID()},
			PrevDepth:  create.Depth(),
		}.Build(entity.KeySigner(serverName, keyID, priv))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}

		if err := entity.Authorise(foreign, state, v); !errors.Is(err, entity.ErrAuthFailed) {
			t.Fatalf("error = %v, want ErrAuthFailed", err)
		}
	})
}

func TestPowerLevelsWithNonIntegerValuesAreRefused(t *testing.T) {
	eachVersion(t, func(t *testing.T, id entity.RoomVersionID) {
		r := newRoom(t, id, "@creator:"+serverName)
		r.commit(r.join("@creator:" + serverName))

		for _, content := range []map[string]any{
			{"ban": "fifty"},
			{"users": map[string]any{"@a:" + serverName: "high"}},
			{"users": map[string]any{"not-a-user": 50}},
			{"events": "everything"},
		} {
			e := r.build(entity.EventTypePowerLevels, ptr(""), "@creator:"+serverName, content)
			if err := r.authorise(e); !errors.Is(err, entity.ErrPowerLevelsMalformed) {
				t.Fatalf("content %v error = %v, want ErrPowerLevelsMalformed", content, err)
			}
		}
	})
}
