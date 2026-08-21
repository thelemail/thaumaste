package entity_test

import (
	"crypto/ed25519"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

func message(t *testing.T, sender, body string, ts int64, content map[string]any) entity.Event {
	t.Helper()
	fields := map[string]any{
		"type":             entity.EventTypeMessage,
		"room_id":          "!room:alpha.test",
		"sender":           sender,
		"origin_server_ts": ts,
		"depth":            int64(1),
		"prev_events":      []any{},
		"auth_events":      []any{},
		"content":          map[string]any{"msgtype": "m.text", "body": body},
	}
	if content != nil {
		fields["content"] = content
	}
	raw, err := entity.CanonicalJSON(fields)
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	built, err := entity.NewEventFromJSON(raw, version(t, entity.DefaultRoomVersion))
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	return built
}

func replacement(t *testing.T, sender, body string, ts int64, parentID string) entity.Event {
	t.Helper()
	return message(t, sender, body, ts, map[string]any{
		"msgtype":       "m.text",
		"body":          "* " + body,
		"m.new_content": map[string]any{"msgtype": "m.text", "body": body},
		"m.relates_to":  map[string]any{"rel_type": entity.RelReplace, "event_id": parentID},
	})
}

func stored(e entity.Event, nid int64) entity.StoredEvent {
	return entity.StoredEvent{
		NID:                 nid,
		Event:               e,
		TopologicalOrdering: nid,
		StreamOrdering:      nid,
		Disposition:         entity.DispositionAccepted,
	}
}

func TestAReplacementIsOnlyValidWhenEveryRuleHolds(t *testing.T) {
	original := message(t, "@alice:alpha.test", "cake", 1000, nil)
	valid := replacement(t, "@alice:alpha.test", "carrot cake", 2000, original.ID())

	if !entity.ValidReplacement(original, valid) {
		t.Fatal("a well-formed replacement was rejected")
	}

	stateKey := ""
	stateFields := original.Fields()
	stateFields["state_key"] = stateKey
	stateRaw, err := entity.CanonicalJSON(stateFields)
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	asState, err := entity.NewEventFromJSON(stateRaw, version(t, entity.DefaultRoomVersion))
	if err != nil {
		t.Fatalf("build state event: %v", err)
	}

	noNewContent := message(t, "@alice:alpha.test", "carrot cake", 2000, map[string]any{
		"msgtype":      "m.text",
		"body":         "* carrot cake",
		"m.relates_to": map[string]any{"rel_type": entity.RelReplace, "event_id": original.ID()},
	})

	for name, pair := range map[string][2]entity.Event{
		"a different sender":      {original, replacement(t, "@bob:alpha.test", "carrot cake", 2000, original.ID())},
		"a state event as parent": {asState, valid},
		"a state event as child":  {original, asState},
		"an edit of an edit":      {valid, replacement(t, "@alice:alpha.test", "again", 3000, valid.ID())},
		"a missing m.new_content": {original, noNewContent},
	} {
		if entity.ValidReplacement(pair[0], pair[1]) {
			t.Fatalf("%s was accepted as a replacement", name)
		}
	}
}

func TestTheLatestReplacementWinsOnTimestampThenOnEventID(t *testing.T) {
	original := message(t, "@alice:alpha.test", "cake", 1000, nil)
	older := replacement(t, "@alice:alpha.test", "carrot cake", 2000, original.ID())
	newer := replacement(t, "@alice:alpha.test", "chocolate cake", 3000, original.ID())

	best, ok := entity.ChooseReplacement(original, []entity.StoredEvent{stored(older, 1), stored(newer, 2)})
	if !ok || best.Event.ID() != newer.ID() {
		t.Fatalf("the newer timestamp did not win: %v", best.Event.ID())
	}

	tieA := replacement(t, "@alice:alpha.test", "one", 4000, original.ID())
	tieB := replacement(t, "@alice:alpha.test", "two", 4000, original.ID())
	larger := tieA
	if tieB.ID() > tieA.ID() {
		larger = tieB
	}
	best, ok = entity.ChooseReplacement(original, []entity.StoredEvent{stored(tieA, 1), stored(tieB, 2)})
	if !ok || best.Event.ID() != larger.ID() {
		t.Fatalf("a timestamp tie did not break on the larger event id")
	}
}

func TestAnInvalidReplacementIsSkippedInFavourOfAValidOlderOne(t *testing.T) {
	original := message(t, "@alice:alpha.test", "cake", 1000, nil)
	good := replacement(t, "@alice:alpha.test", "carrot cake", 2000, original.ID())
	broken := message(t, "@alice:alpha.test", "chocolate cake", 3000, map[string]any{
		"msgtype":      "m.text",
		"body":         "* chocolate cake",
		"m.relates_to": map[string]any{"rel_type": entity.RelReplace, "event_id": original.ID()},
	})

	best, ok := entity.ChooseReplacement(original, []entity.StoredEvent{stored(good, 1), stored(broken, 2)})
	if !ok || best.Event.ID() != good.ID() {
		t.Fatalf("a newer invalid edit shadowed a valid one")
	}
}

func TestPlanningABundleSortsCountsAndAttributes(t *testing.T) {
	parent := stored(message(t, "@alice:alpha.test", "root", 1000, nil), 1)
	parentID := parent.Event.ID()

	refs := []entity.RelationRef{
		{ChildNID: 2, EventID: "$edit-old", ParentID: parentID, RelType: entity.RelReplace,
			Sender: "@alice:alpha.test", OriginServerTS: 2000, Position: entity.Position{Topological: 2, Stream: 2}},
		{ChildNID: 3, EventID: "$edit-new", ParentID: parentID, RelType: entity.RelReplace,
			Sender: "@alice:alpha.test", OriginServerTS: 3000, Position: entity.Position{Topological: 3, Stream: 3}},
		{ChildNID: 4, EventID: "$edit-forged", ParentID: parentID, RelType: entity.RelReplace,
			Sender: "@mallory:alpha.test", OriginServerTS: 9000, Position: entity.Position{Topological: 4, Stream: 4}},
		{ChildNID: 5, EventID: "$reply-one", ParentID: parentID, RelType: entity.RelThread,
			Sender: "@bob:alpha.test", OriginServerTS: 4000, Position: entity.Position{Topological: 5, Stream: 5}},
		{ChildNID: 6, EventID: "$reply-two", ParentID: parentID, RelType: entity.RelThread,
			Sender: "@bob:alpha.test", OriginServerTS: 5000, Position: entity.Position{Topological: 6, Stream: 6}},
		{ChildNID: 7, EventID: "$ref", ParentID: parentID, RelType: entity.RelReference,
			Sender: "@bob:alpha.test", OriginServerTS: 6000, Position: entity.Position{Topological: 7, Stream: 7}},
		{ChildNID: 8, EventID: "$react", ParentID: parentID, RelType: entity.RelAnnotation,
			Sender: "@bob:alpha.test", OriginServerTS: 7000, Position: entity.Position{Topological: 8, Stream: 8}},
	}

	plan := entity.PlanBundle(parent, "@carol:alpha.test", refs)
	if len(plan.Replacements) != 2 || plan.Replacements[0] != 3 {
		t.Fatalf("replacements = %v, want the sender's own edits newest first", plan.Replacements)
	}
	if plan.ThreadCount != 2 || plan.ThreadLatest != 6 {
		t.Fatalf("thread summary = %d replies, latest %d", plan.ThreadCount, plan.ThreadLatest)
	}
	if plan.ThreadParticipated {
		t.Fatal("a user who neither wrote the root nor replied counts as a participant")
	}
	if len(plan.Reference) != 1 || plan.Reference[0] != "$ref" {
		t.Fatalf("reference chunk = %v", plan.Reference)
	}

	if !entity.PlanBundle(parent, "@alice:alpha.test", refs).ThreadParticipated {
		t.Fatal("the root's own sender does not count as a participant")
	}
	if !entity.PlanBundle(parent, "@bob:alpha.test", refs).ThreadParticipated {
		t.Fatal("a user who replied does not count as a participant")
	}
}

func TestARedactedParentPlansNoReplacement(t *testing.T) {
	parent := stored(message(t, "@alice:alpha.test", "root", 1000, nil), 1)
	parent.Disposition = entity.DispositionRedacted
	refs := []entity.RelationRef{{
		ChildNID: 2, EventID: "$edit", ParentID: parent.Event.ID(), RelType: entity.RelReplace,
		Sender: "@alice:alpha.test", OriginServerTS: 2000, Position: entity.Position{Topological: 2, Stream: 2},
	}}

	if plan := entity.PlanBundle(parent, "@alice:alpha.test", refs); len(plan.Replacements) != 0 {
		t.Fatalf("a redacted parent still plans a replacement: %v", plan.Replacements)
	}
}

func TestOnlyRelationsWithARelationTypeAreParsed(t *testing.T) {
	for name, content := range map[string]map[string]any{
		"a rich reply": {"m.relates_to": map[string]any{
			"m.in_reply_to": map[string]any{"event_id": "$parent"}}},
		"no relation":      {"body": "plain"},
		"a missing parent": {"m.relates_to": map[string]any{"rel_type": entity.RelThread}},
		"a missing type":   {"m.relates_to": map[string]any{"event_id": "$parent"}},
	} {
		if _, ok := entity.ParseRelation(content); ok {
			t.Fatalf("%s parsed as a relation", name)
		}
	}

	relation, ok := entity.ParseRelation(map[string]any{"m.relates_to": map[string]any{
		"rel_type": entity.RelAnnotation, "event_id": "$parent", "key": "👍",
	}})
	if !ok || relation.RelType != entity.RelAnnotation || relation.ParentID != "$parent" || relation.Key != "👍" {
		t.Fatalf("a well-formed annotation parsed as %+v", relation)
	}
}

func TestRedactionKeepsTheEventIdentityAndItsSignature(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	const serverName = "alpha.test"
	const keyID = entity.KeyID("ed25519:one")

	room := version(t, entity.DefaultRoomVersion)
	built, err := entity.EventBuilder{
		Version:        room,
		RoomID:         "!room:alpha.test",
		Type:           entity.EventTypeMessage,
		Sender:         "@alice:alpha.test",
		Content:        map[string]any{"msgtype": "m.text", "body": "something regrettable"},
		PrevEvents:     []string{"$parent"},
		PrevDepth:      1,
		AuthEvents:     []string{"$create"},
		OriginServerTS: 1000,
	}.Build(entity.KeySigner(serverName, keyID, private))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := built.VerifyContentHash(); err != nil {
		t.Fatalf("the fixture does not verify before redaction: %v", err)
	}

	raw, err := entity.RedactedJSON(built, room)
	if err != nil {
		t.Fatalf("RedactedJSON: %v", err)
	}
	redacted, err := entity.NewEventFromJSON(raw, room)
	if err != nil {
		t.Fatalf("re-read the redacted event: %v", err)
	}

	if redacted.ID() != built.ID() {
		t.Fatalf("redaction changed the event id: %s became %s", built.ID(), redacted.ID())
	}
	if err := redacted.VerifySignature(serverName, keyID, public, room); err != nil {
		t.Fatalf("a redacted event no longer verifies: %v", err)
	}
	if err := redacted.VerifyContentHash(); err == nil {
		t.Fatal("the content hash still matches, so nothing was actually removed")
	}
	if len(redacted.Content()) != 0 {
		t.Fatalf("the redacted content is not empty: %v", redacted.Content())
	}
}
