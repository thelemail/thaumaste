package entity_test

import (
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func filtered(t *testing.T, raw string) entity.RoomEventFilter {
	t.Helper()
	filter, err := entity.ParseRoomEventFilter(raw)
	if err != nil {
		t.Fatalf("ParseRoomEventFilter(%s): %v", raw, err)
	}
	return filter
}

func TestAnEmptyFilterKeepsEverything(t *testing.T) {
	tl := newTimeline(t, alice)
	message := tl.message(alice, "hello")

	if !filtered(t, "").Keeps(message.Event) {
		t.Fatal("an absent filter dropped an event")
	}
	if !filtered(t, "{}").Keeps(message.Event) {
		t.Fatal("an empty filter dropped an event")
	}
}

func TestTypesSelectAndNotTypesExclude(t *testing.T) {
	tl := newTimeline(t, alice)
	message := tl.message(alice, "hello").Event
	membership := tl.member(bob, entity.MembershipJoin).Event

	if !filtered(t, `{"types":["m.room.message"]}`).Keeps(message) {
		t.Fatal("types dropped the type it names")
	}
	if filtered(t, `{"types":["m.room.message"]}`).Keeps(membership) {
		t.Fatal("types kept a type it does not name")
	}
	if filtered(t, `{"not_types":["m.room.message"]}`).Keeps(message) {
		t.Fatal("not_types kept the type it names")
	}
	if !filtered(t, `{"not_types":["m.room.message"]}`).Keeps(membership) {
		t.Fatal("not_types dropped a type it does not name")
	}
	if filtered(t, `{"types":["m.room.message"],"not_types":["m.room.message"]}`).Keeps(message) {
		t.Fatal("not_types did not override types")
	}
}

func TestATypeWildcardMatchesASequence(t *testing.T) {
	tl := newTimeline(t, alice)
	message := tl.message(alice, "hello").Event

	for _, pattern := range []string{"m.room.*", "*", "*.message", "m.*.message", "m.room.message"} {
		if !filtered(t, `{"types":["`+pattern+`"]}`).Keeps(message) {
			t.Fatalf("pattern %q did not match m.room.message", pattern)
		}
	}
	for _, pattern := range []string{"m.call.*", "*.state", "m.room.messag", "x*"} {
		if filtered(t, `{"types":["`+pattern+`"]}`).Keeps(message) {
			t.Fatalf("pattern %q matched m.room.message", pattern)
		}
	}
}

func TestSendersSelectAndNotSendersExclude(t *testing.T) {
	tl := newTimeline(t, alice)
	fromAlice := tl.message(alice, "hello").Event

	if !filtered(t, `{"senders":["`+alice+`"]}`).Keeps(fromAlice) {
		t.Fatal("senders dropped the sender it names")
	}
	if filtered(t, `{"senders":["`+bob+`"]}`).Keeps(fromAlice) {
		t.Fatal("senders kept a sender it does not name")
	}
	if filtered(t, `{"not_senders":["`+alice+`"]}`).Keeps(fromAlice) {
		t.Fatal("not_senders kept the sender it names")
	}
}

func TestContainsURLSelectsBothWays(t *testing.T) {
	tl := newTimeline(t, alice)
	plain := tl.message(alice, "hello").Event
	withURL := tl.add(tl.room.build(entity.EventTypeMessage, nil, alice, map[string]any{
		"msgtype": "m.image", "body": "picture.png", "url": "mxc://alpha.test/abc",
	})).Event

	if filtered(t, `{"contains_url":true}`).Keeps(plain) {
		t.Fatal("contains_url true kept an event with no url")
	}
	if !filtered(t, `{"contains_url":true}`).Keeps(withURL) {
		t.Fatal("contains_url true dropped an event with a url")
	}
	if !filtered(t, `{"contains_url":false}`).Keeps(plain) {
		t.Fatal("contains_url false dropped an event with no url")
	}
	if filtered(t, `{"contains_url":false}`).Keeps(withURL) {
		t.Fatal("contains_url false kept an event with a url")
	}
}

func TestAMalformedFilterIsRefused(t *testing.T) {
	for _, raw := range []string{
		`not json`,
		`{"types":"not_a_list"}`,
		`{"senders":["not a user id"]}`,
		`{"not_senders":["alice"]}`,
		`{"limit":-1}`,
	} {
		if _, err := entity.ParseRoomEventFilter(raw); err == nil {
			t.Fatalf("ParseRoomEventFilter(%s) was accepted", raw)
		} else if !errors.Is(err, entity.ErrBadFilter) {
			t.Fatalf("ParseRoomEventFilter(%s) error = %v, want %v", raw, err, entity.ErrBadFilter)
		}
	}
}

func TestAFilterCarriesItsOwnLimit(t *testing.T) {
	if got := filtered(t, `{"limit":25}`).Limit; got != 25 {
		t.Fatalf("limit = %d, want 25", got)
	}
	if got := filtered(t, `{}`).Limit; got != 0 {
		t.Fatalf("absent limit = %d, want 0", got)
	}
}
