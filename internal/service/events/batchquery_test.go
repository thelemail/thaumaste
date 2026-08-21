package events_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository/event"
)

func TestTheLatestStateEventOfAKeyIsTheCurrentStateOfThatKey(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	created, creator, _ := h.room(t, alpha, entity.RoomVersion12)
	empty := ""

	for _, name := range []string{"first", "second", "third"} {
		h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypeName, StateKey: &empty, Sender: creator,
			Content: map[string]any{"name": name},
		})
	}
	h.send(t, alpha, entity.NewEvent{
		RoomID: created.RoomID, Type: entity.EventTypeTopic, StateKey: &empty, Sender: creator,
		Content: map[string]any{"topic": "on the record"},
	})

	current, err := h.events.CurrentState(t.Context(), created.RoomID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}

	repo := event.New(h.db)
	latest, err := repo.LatestState(t.Context(), []int64{created.NID}, slices.Collect(maps.Keys(current)))
	if err != nil {
		t.Fatalf("LatestState: %v", err)
	}
	if len(latest[created.NID]) != len(current) {
		t.Fatalf("LatestState returned %d keys, current state has %d", len(latest[created.NID]), len(current))
	}
	for _, stored := range latest[created.NID] {
		stateKey, _ := stored.Event.StateKey()
		key := entity.StateKey{Type: stored.Event.Type(), StateKey: stateKey}
		want, ok := current[key]
		if !ok {
			t.Fatalf("LatestState returned %v, which is not current state", key)
		}
		if want.ID() != stored.Event.ID() {
			t.Fatalf("%v: LatestState says %s, current state says %s", key, stored.Event.ID(), want.ID())
		}
	}
}

func TestSinceReturnsEachRoomsOwnTailInOrder(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	one, creator, _ := h.room(t, alpha, entity.RoomVersion12)
	two, _, _ := h.room(t, alpha, entity.RoomVersion12)

	var sent []entity.StoredEvent
	for _, room := range []entity.Room{one, two, one, two, one} {
		sent = append(sent, h.send(t, alpha, entity.NewEvent{
			RoomID: room.RoomID, Type: entity.EventTypeMessage, Sender: creator,
			Content: map[string]any{"msgtype": "m.text", "body": room.RoomID},
		}))
	}
	before := sent[0].StreamOrdering - 1
	upTo := sent[len(sent)-1].StreamOrdering

	repo := event.New(h.db)
	grouped, err := repo.Since(t.Context(), []entity.RoomWindow{{RoomNID: one.NID, After: before}, {RoomNID: two.NID, After: before}}, upTo, 2)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	for _, roomNID := range []int64{one.NID, two.NID} {
		events := grouped[roomNID]
		if len(events) != 2 {
			t.Fatalf("room %d got %d events, want the 2 most recent", roomNID, len(events))
		}
		if events[0].StreamOrdering >= events[1].StreamOrdering {
			t.Fatalf("room %d came back newest first: %d then %d",
				roomNID, events[0].StreamOrdering, events[1].StreamOrdering)
		}
		if events[0].RoomNID != roomNID || events[1].RoomNID != roomNID {
			t.Fatalf("room %d got an event from another room", roomNID)
		}
	}
	if grouped[one.NID][1].StreamOrdering != sent[4].StreamOrdering {
		t.Fatal("Since did not return the newest event of the room")
	}
}

func TestStateHistoryKeepsEveryValueAKeyEverHeld(t *testing.T) {
	h := newHarness(t)
	alpha := h.tenant(t, "alpha.test")
	created, creator, _ := h.room(t, alpha, entity.RoomVersion12)
	empty := ""

	for _, visibility := range []string{"invited", "joined", "shared"} {
		h.send(t, alpha, entity.NewEvent{
			RoomID: created.RoomID, Type: entity.EventTypeHistoryVisibility, StateKey: &empty, Sender: creator,
			Content: map[string]any{"history_visibility": visibility},
		})
	}

	repo := event.New(h.db)
	grouped, err := repo.StateHistory(t.Context(), []int64{created.NID}, entity.EventTypeHistoryVisibility, "")
	if err != nil {
		t.Fatalf("StateHistory: %v", err)
	}
	single, err := repo.ListStateOfType(t.Context(), created.NID, entity.EventTypeHistoryVisibility, "")
	if err != nil {
		t.Fatalf("ListStateOfType: %v", err)
	}
	if len(grouped[created.NID]) != len(single) {
		t.Fatalf("StateHistory returned %d events, ListStateOfType returned %d",
			len(grouped[created.NID]), len(single))
	}
	for i, stored := range grouped[created.NID] {
		if stored.Event.ID() != single[i].Event.ID() {
			t.Fatalf("position %d: StateHistory says %s, ListStateOfType says %s",
				i, stored.Event.ID(), single[i].Event.ID())
		}
	}
}
