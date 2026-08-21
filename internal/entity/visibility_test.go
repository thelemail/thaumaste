package entity_test

import (
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

type timeline struct {
	t     *testing.T
	room  *room
	all   []entity.StoredEvent
	depth int64
}

func newTimeline(t *testing.T, creator string) *timeline {
	t.Helper()
	return &timeline{t: t, room: newRoom(t, entity.RoomVersion11, creator)}
}

func (tl *timeline) add(e entity.Event) entity.StoredEvent {
	tl.t.Helper()
	tl.depth++
	stored := entity.StoredEvent{
		Event:               e,
		TopologicalOrdering: tl.depth,
		StreamOrdering:      tl.depth * 10,
		Disposition:         entity.DispositionAccepted,
	}
	tl.all = append(tl.all, stored)
	return stored
}

func (tl *timeline) message(sender, body string) entity.StoredEvent {
	tl.t.Helper()
	return tl.add(tl.room.build(entity.EventTypeMessage, nil, sender,
		map[string]any{"msgtype": "m.text", "body": body}))
}

func (tl *timeline) member(target, membership string) entity.StoredEvent {
	tl.t.Helper()
	key := target
	e := tl.room.build(entity.EventTypeMember, &key, target, map[string]any{"membership": membership})
	tl.room.state = tl.room.state.Apply(e)
	tl.room.last = e
	return tl.add(e)
}

func (tl *timeline) visibility(sender, value string) entity.StoredEvent {
	tl.t.Helper()
	key := ""
	e := tl.room.build(entity.EventTypeHistoryVisibility, &key, sender,
		map[string]any{"history_visibility": value})
	tl.room.state = tl.room.state.Apply(e)
	tl.room.last = e
	return tl.add(e)
}

func (tl *timeline) filter(caller string) entity.HistoryFilter {
	tl.t.Helper()
	var visibility, memberships []entity.StoredEvent
	for _, e := range tl.all {
		key, isState := e.Event.StateKey()
		switch {
		case isState && e.Event.Type() == entity.EventTypeHistoryVisibility && key == "":
			visibility = append(visibility, e)
		case isState && e.Event.Type() == entity.EventTypeMember && key == caller:
			memberships = append(memberships, e)
		}
	}
	return entity.NewHistoryFilter(caller, visibility, memberships)
}

const (
	alice = "@creator:" + serverName
	bob   = "@bob:" + serverName
)

func TestSharedHistoryLetsALateJoinerReadWhatCameBefore(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityShared)
	before := tl.message(alice, "said before bob arrived")
	tl.member(bob, entity.MembershipJoin)
	after := tl.message(alice, "said after bob arrived")

	f := tl.filter(bob)
	if !f.Visible(before) {
		t.Fatal("shared history hid an event sent before the join")
	}
	if !f.Visible(after) {
		t.Fatal("shared history hid an event sent after the join")
	}
}

func TestJoinedHistoryHidesEverythingBeforeTheJoin(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityJoined)
	before := tl.message(alice, "said before bob arrived")
	tl.member(bob, entity.MembershipJoin)
	after := tl.message(alice, "said after bob arrived")

	f := tl.filter(bob)
	if f.Visible(before) {
		t.Fatal("joined history leaked an event sent before the join")
	}
	if !f.Visible(after) {
		t.Fatal("joined history hid an event sent after the join")
	}
}

func TestInvitedHistoryStartsAtTheInvite(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityInvited)
	beforeInvite := tl.message(alice, "before the invite")
	tl.member(bob, entity.MembershipInvite)
	betweenInviteAndJoin := tl.message(alice, "after the invite")
	tl.member(bob, entity.MembershipJoin)
	afterJoin := tl.message(alice, "after the join")

	f := tl.filter(bob)
	if f.Visible(beforeInvite) {
		t.Fatal("invited history leaked an event sent before the invite")
	}
	if !f.Visible(betweenInviteAndJoin) {
		t.Fatal("invited history hid an event sent between the invite and the join")
	}
	if !f.Visible(afterJoin) {
		t.Fatal("invited history hid an event sent after the join")
	}
}

func TestWorldReadableHistoryIsOpenToSomeoneWhoNeverJoined(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityWorldReadable)
	open := tl.message(alice, "anyone may read this")

	if !tl.filter(bob).Visible(open) {
		t.Fatal("world readable history refused a non-member")
	}
}

func TestClosingAWorldReadableRoomDoesNotRetractWhatWasOpen(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityWorldReadable)
	open := tl.message(alice, "said while the room was open")
	tl.visibility(alice, entity.HistoryVisibilityJoined)
	closed := tl.message(alice, "said after the room closed")

	f := tl.filter(bob)
	if !f.Visible(open) {
		t.Fatal("closing the room retracted history that was world readable when it was sent")
	}
	if f.Visible(closed) {
		t.Fatal("closing the room left later events readable")
	}
}

func TestAUserWhoLeftSeesNothingAfterTheirLeave(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityShared)
	tl.member(bob, entity.MembershipJoin)
	during := tl.message(alice, "said while bob was here")
	leave := tl.member(bob, entity.MembershipLeave)
	after := tl.message(alice, "said after bob left")

	f := tl.filter(bob)
	if !f.Visible(during) {
		t.Fatal("a departed member lost history they could see before leaving")
	}
	if !f.Visible(leave) {
		t.Fatal("a departed member cannot see their own leave")
	}
	if f.Visible(after) {
		t.Fatal("a departed member can see an event sent after they left")
	}
}

func TestRejoiningRestoresTheHistoryAgain(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityShared)
	tl.member(bob, entity.MembershipJoin)
	tl.member(bob, entity.MembershipLeave)
	away := tl.message(alice, "said while bob was away")
	tl.member(bob, entity.MembershipJoin)
	back := tl.message(alice, "said once bob was back")

	f := tl.filter(bob)
	if !f.Visible(away) {
		t.Fatal("shared history hid an event sent between a leave and a rejoin")
	}
	if !f.Visible(back) {
		t.Fatal("shared history hid an event sent after the rejoin")
	}
}

func TestAUserAlwaysSeesTheirOwnMembershipEvents(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityJoined)
	invite := tl.member(bob, entity.MembershipInvite)
	join := tl.member(bob, entity.MembershipJoin)
	leave := tl.member(bob, entity.MembershipLeave)

	f := tl.filter(bob)
	for name, e := range map[string]entity.StoredEvent{"join": join, "leave": leave} {
		if !f.Visible(e) {
			t.Fatalf("a user cannot see their own %s under joined history", name)
		}
	}
	if f.Visible(invite) {
		t.Fatal("an invite before entitlement is visible under joined history")
	}
}

func TestAVisibilityChangeIsVisibleFromEitherSide(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityJoined)
	opening := tl.visibility(alice, entity.HistoryVisibilityWorldReadable)
	closing := tl.visibility(alice, entity.HistoryVisibilityJoined)

	f := tl.filter(bob)
	if !f.Visible(opening) {
		t.Fatal("the event that opened the room is hidden by the state before it")
	}
	if !f.Visible(closing) {
		t.Fatal("the event that closed the room is hidden by the state after it")
	}
}

func TestAnEventThatWasNotDeliveredIsNeverVisible(t *testing.T) {
	tl := newTimeline(t, alice)
	tl.visibility(alice, entity.HistoryVisibilityWorldReadable)
	open := tl.message(alice, "anyone may read this")

	f := tl.filter(bob)
	for _, disposition := range []entity.Disposition{
		entity.DispositionRejected, entity.DispositionSoftFailed, entity.DispositionOutlier,
	} {
		hidden := open
		hidden.Disposition = disposition
		if f.Visible(hidden) {
			t.Fatalf("a %s event was delivered", disposition)
		}
	}
	redacted := open
	redacted.Disposition = entity.DispositionRedacted
	if !f.Visible(redacted) {
		t.Fatal("a redacted event was withheld entirely rather than delivered stripped")
	}
}

func TestTheDefaultVisibilityIsShared(t *testing.T) {
	tl := newTimeline(t, alice)
	before := tl.message(alice, "said before bob arrived")
	tl.member(bob, entity.MembershipJoin)

	if !tl.filter(bob).Visible(before) {
		t.Fatal("a room with no history visibility event did not default to shared")
	}
}

func TestMembershipAtCountsTheEventItself(t *testing.T) {
	tl := newTimeline(t, alice)
	early := tl.message(alice, "before bob")
	join := tl.member(bob, entity.MembershipJoin)
	later := tl.message(alice, "after bob")

	f := tl.filter(bob)
	if got := f.MembershipAt(entity.PositionOf(early)); got != entity.MembershipLeave {
		t.Fatalf("membership before any event = %q, want %q", got, entity.MembershipLeave)
	}
	if got := f.MembershipAt(entity.PositionOf(join)); got != entity.MembershipJoin {
		t.Fatalf("membership at the join = %q, want %q", got, entity.MembershipJoin)
	}
	if got := f.MembershipAt(entity.PositionOf(later)); got != entity.MembershipJoin {
		t.Fatalf("membership after the join = %q, want %q", got, entity.MembershipJoin)
	}
}
