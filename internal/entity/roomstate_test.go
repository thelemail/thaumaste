package entity_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func TestOnlyTheMandatedEncryptionAlgorithmIsAccepted(t *testing.T) {
	for name, content := range map[string]map[string]any{
		"nothing at all":     {},
		"a missing key":      {"rotation_period_msgs": 10},
		"an empty algorithm": {"algorithm": ""},
		"another algorithm":  {"algorithm": "m.olm.v1.curve25519-aes-sha2"},
		"a non-string":       {"algorithm": 12},
	} {
		if err := entity.ValidateStateContent(entity.EventTypeEncryption, content); !errors.Is(err, entity.ErrEncryptionRequired) {
			t.Fatalf("%s = %v, want ErrEncryptionRequired", name, err)
		}
	}

	ok := map[string]any{"algorithm": entity.EncryptionAlgorithm, "rotation_period_ms": 1000}
	if err := entity.ValidateStateContent(entity.EventTypeEncryption, ok); err != nil {
		t.Fatalf("the mandated algorithm was refused: %v", err)
	}
}

func TestJoinRulesAreHeldToTheKnownSet(t *testing.T) {
	for _, rule := range []string{
		entity.JoinRulePublic, entity.JoinRuleInvite, entity.JoinRuleKnock,
		entity.JoinRuleRestricted, entity.JoinRuleKnockRestricted, entity.JoinRulePrivate,
	} {
		content := map[string]any{"join_rule": rule}
		if err := entity.ValidateStateContent(entity.EventTypeJoinRules, content); err != nil {
			t.Fatalf("%s was refused: %v", rule, err)
		}
	}
	for name, content := range map[string]map[string]any{
		"an unknown rule": {"join_rule": "sideways"},
		"no rule at all":  {},
		"a non-string":    {"join_rule": 3},
	} {
		if err := entity.ValidateStateContent(entity.EventTypeJoinRules, content); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestARestrictedAllowListIsCheckedForShape(t *testing.T) {
	good := map[string]any{
		"join_rule": entity.JoinRuleRestricted,
		"allow": []any{map[string]any{
			"type": entity.MembershipTypeRoom, "room_id": "!space:alpha.test",
		}},
	}
	if err := entity.ValidateStateContent(entity.EventTypeJoinRules, good); err != nil {
		t.Fatalf("a well formed allow list was refused: %v", err)
	}

	for name, allow := range map[string]any{
		"not a list":       "everyone",
		"not objects":      []any{"!space:alpha.test"},
		"a wrong type":     []any{map[string]any{"type": "m.vibes", "room_id": "!s:alpha.test"}},
		"no room id":       []any{map[string]any{"type": entity.MembershipTypeRoom}},
		"not a room id":    []any{map[string]any{"type": entity.MembershipTypeRoom, "room_id": "@user:alpha.test"}},
		"an oversized one": []any{map[string]any{"type": entity.MembershipTypeRoom, "room_id": "!" + strings.Repeat("x", entity.MaxRoomIDBytes)}},
	} {
		content := map[string]any{"join_rule": entity.JoinRuleRestricted, "allow": allow}
		if err := entity.ValidateStateContent(entity.EventTypeJoinRules, content); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestAliasesRoundTripAndRefuseWhatTheGrammarForbids(t *testing.T) {
	alias, err := entity.MintAlias("room", "alpha.test")
	if err != nil {
		t.Fatalf("MintAlias: %v", err)
	}
	if alias != "#room:alpha.test" {
		t.Fatalf("alias = %q", alias)
	}
	localpart, serverName, err := entity.ParseAlias(alias)
	if err != nil || localpart != "room" || serverName != "alpha.test" {
		t.Fatalf("ParseAlias(%q) = %q, %q, %v", alias, localpart, serverName, err)
	}

	unicode, err := entity.MintAlias("老虎Â£я🤨👉ඞ", "alpha.test")
	if err != nil {
		t.Fatalf("a unicode localpart was refused: %v", err)
	}
	if _, _, err := entity.ParseAlias(unicode); err != nil {
		t.Fatalf("a unicode alias does not parse: %v", err)
	}

	for name, bad := range map[string]string{
		"no sigil":       "room:alpha.test",
		"no domain":      "#room",
		"an empty local": "#:alpha.test",
		"a bare sigil":   "#",
		"a user id":      "@user:alpha.test",
		"an inner colon": "#ro:om:alpha.test",
		"too long":       "#" + strings.Repeat("x", entity.MaxAliasBytes) + ":alpha.test",
	} {
		if _, _, err := entity.ParseAlias(bad); err == nil {
			t.Fatalf("%s parsed as an alias", name)
		}
	}
}

func TestALocalAliasMustCarryThisServersName(t *testing.T) {
	if _, err := entity.ParseLocalAlias("#room:alpha.test", "alpha.test"); err != nil {
		t.Fatalf("a local alias was refused: %v", err)
	}
	_, err := entity.ParseLocalAlias("#room:beta.test", "alpha.test")
	if !errors.Is(err, entity.ErrAliasForeign) {
		t.Fatalf("error = %v, want ErrAliasForeign", err)
	}
}

func TestCanonicalAliasContentNamesEveryAliasItClaims(t *testing.T) {
	content := map[string]any{
		"alias":       "#main:alpha.test",
		"alt_aliases": []any{"#other:alpha.test", "#main:alpha.test"},
	}
	got := entity.CanonicalAliases(content)
	want := []string{"#main:alpha.test", "#other:alpha.test"}
	if len(got) != len(want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("aliases = %v, want %v", got, want)
		}
	}

	bad := map[string]any{"alias": "%nonsense:alpha.test"}
	if err := entity.ValidateStateContent(entity.EventTypeCanonicalAlias, bad); !errors.Is(err, entity.ErrAliasInvalid) {
		t.Fatalf("error = %v, want ErrAliasInvalid", err)
	}
}

func TestCreatorsAreNotNamedInThePowerLevelsFromVersionTwelve(t *testing.T) {
	in := entity.NewRoomRequest{
		Creator:    "@creator:alpha.test",
		ServerName: "alpha.test",
		Visibility: entity.VisibilityPrivate,
		Preset:     entity.PresetPrivateChat,
	}

	twelve, err := entity.LookupRoomVersion(entity.RoomVersion12)
	if err != nil {
		t.Fatalf("LookupRoomVersion: %v", err)
	}
	users, _ := in.PowerLevelContent(twelve)["users"].(map[string]any)
	if _, named := users[in.Creator]; named {
		t.Fatal("v12 power levels name the creator, which the auth rules reject")
	}

	eleven, err := entity.LookupRoomVersion(entity.RoomVersion11)
	if err != nil {
		t.Fatalf("LookupRoomVersion: %v", err)
	}
	users, _ = in.PowerLevelContent(eleven)["users"].(map[string]any)
	if users[in.Creator] != int64(100) {
		t.Fatalf("v11 power levels give the creator %v, want 100", users[in.Creator])
	}
}

func TestTrustedPrivateChatLiftsEveryInvitee(t *testing.T) {
	in := entity.NewRoomRequest{
		Creator:    "@creator:alpha.test",
		ServerName: "alpha.test",
		Visibility: entity.VisibilityPrivate,
		Preset:     entity.PresetTrustedPrivateChat,
		Invite:     []string{"@friend:alpha.test"},
	}
	version, err := entity.LookupRoomVersion(entity.DefaultRoomVersion)
	if err != nil {
		t.Fatalf("LookupRoomVersion: %v", err)
	}
	users, _ := in.PowerLevelContent(version)["users"].(map[string]any)
	if users["@friend:alpha.test"] != int64(100) {
		t.Fatalf("an invitee of a trusted private chat got %v", users["@friend:alpha.test"])
	}

	plain := in
	plain.Preset = entity.PresetPrivateChat
	users, _ = plain.PowerLevelContent(version)["users"].(map[string]any)
	if _, lifted := users["@friend:alpha.test"]; lifted {
		t.Fatal("a plain private chat lifted an invitee")
	}
}

func TestARoomRequestRefusesWhatItCannotBuild(t *testing.T) {
	base := entity.NewRoomRequest{
		Creator:    "@creator:alpha.test",
		ServerName: "alpha.test",
		Version:    entity.DefaultRoomVersion,
		Visibility: entity.VisibilityPrivate,
		Preset:     entity.PresetPrivateChat,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("a plain request was refused: %v", err)
	}

	cases := map[string]func(*entity.NewRoomRequest){
		"an unknown version":    func(r *entity.NewRoomRequest) { r.Version = "99" },
		"an unknown visibility": func(r *entity.NewRoomRequest) { r.Visibility = "translucent" },
		"an unknown preset":     func(r *entity.NewRoomRequest) { r.Preset = "chatty" },
		"a bad creator":         func(r *entity.NewRoomRequest) { r.Creator = "creator" },
		"a bad invitee":         func(r *entity.NewRoomRequest) { r.Invite = []string{"friend"} },
		"a bad alias":           func(r *entity.NewRoomRequest) { r.AliasLocalpart = "no:colons" },
		"weakened encryption": func(r *entity.NewRoomRequest) {
			r.InitialState = []entity.InitialState{{
				Type: entity.EventTypeEncryption, Content: map[string]any{"algorithm": "none"},
			}}
		},
		"a nameless initial state": func(r *entity.NewRoomRequest) {
			r.InitialState = []entity.InitialState{{Content: map[string]any{}}}
		},
	}
	for name, mutate := range cases {
		in := base
		mutate(&in)
		if err := in.Validate(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
