package entity_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/thelemail/thaumaste/internal/entity"
)

const serverName = "alpha.test"

func version(t *testing.T, id entity.RoomVersionID) entity.RoomVersion {
	t.Helper()
	v, err := entity.LookupRoomVersion(id)
	if err != nil {
		t.Fatalf("LookupRoomVersion(%s): %v", id, err)
	}
	return v
}

func signingKey(t *testing.T, seed byte) (entity.KeyID, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	raw := make([]byte, ed25519.SeedSize)
	for i := range raw {
		raw[i] = seed
	}
	priv := ed25519.NewKeyFromSeed(raw)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("not an ed25519 key")
	}
	id, err := entity.NewKeyID("test")
	if err != nil {
		t.Fatalf("NewKeyID: %v", err)
	}
	return id, pub, priv
}

func buildCreate(t *testing.T, v entity.RoomVersion) (entity.Event, ed25519.PublicKey, entity.KeyID) {
	t.Helper()
	keyID, pub, priv := signingKey(t, 1)

	b := entity.EventBuilder{
		Version:        v,
		Type:           entity.EventTypeCreate,
		StateKey:       ptr(""),
		Sender:         "@creator:" + serverName,
		Content:        map[string]any{"room_version": string(v.ID)},
		OriginServerTS: 1000,
	}
	if v.CreateCarriesRoomID {
		b.RoomID = "!opaque:" + serverName
	}
	created, err := b.Build(entity.KeySigner(serverName, keyID, priv))
	if err != nil {
		t.Fatalf("Build create: %v", err)
	}
	return created, pub, keyID
}

func ptr[T any](v T) *T { return &v }

func TestEventIDIsTheReferenceHashInUrlSafeBase64(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	created, _, _ := buildCreate(t, v)

	sum, err := entity.ReferenceHash(created.Fields(), v)
	if err != nil {
		t.Fatalf("ReferenceHash: %v", err)
	}
	want := "$" + base64.RawURLEncoding.EncodeToString(sum[:])

	if created.ID() != want {
		t.Fatalf("event id = %s, want %s", created.ID(), want)
	}
	if !strings.HasPrefix(created.ID(), "$") {
		t.Fatalf("event id lacks the sigil: %s", created.ID())
	}
	if strings.ContainsAny(created.ID(), "+/=") {
		t.Fatalf("event id is not url-safe unpadded: %s", created.ID())
	}
}

func TestTheContentHashAndTheEventIDUseDifferentAlphabets(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	created, _, _ := buildCreate(t, v)

	var body struct {
		Hashes map[string]string `json:"hashes"`
	}
	if err := json.Unmarshal(created.JSON(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	claimed := body.Hashes["sha256"]
	if claimed == "" {
		t.Fatal("no content hash recorded")
	}
	if strings.ContainsAny(claimed, "=") {
		t.Fatalf("content hash is padded: %s", claimed)
	}
	if _, err := base64.RawStdEncoding.DecodeString(claimed); err != nil {
		t.Fatalf("content hash is not standard-alphabet base64: %v", err)
	}

	expected, err := entity.ContentHash(created.Fields())
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if claimed != base64.RawStdEncoding.EncodeToString(expected[:]) {
		t.Fatalf("content hash = %s", claimed)
	}
	if claimed == base64.RawURLEncoding.EncodeToString(expected[:]) {
		return
	}
	if strings.ContainsAny(claimed, "-_") {
		t.Fatalf("content hash uses the url-safe alphabet: %s", claimed)
	}
}

func TestTheContentHashCoversEverythingExceptThreeKeys(t *testing.T) {
	fields := map[string]any{
		"type":       entity.EventTypeMessage,
		"sender":     "@a:" + serverName,
		"content":    map[string]any{"body": "hello"},
		"unsigned":   map[string]any{"age": 12},
		"signatures": map[string]any{"x": "y"},
		"hashes":     map[string]any{"sha256": "stale"},
	}

	with, err := entity.ContentHash(fields)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}

	bare := map[string]any{
		"type":    entity.EventTypeMessage,
		"sender":  "@a:" + serverName,
		"content": map[string]any{"body": "hello"},
	}
	without, err := entity.ContentHash(bare)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if with != without {
		t.Fatal("unsigned, signatures or hashes leaked into the content hash")
	}

	bare["content"] = map[string]any{"body": "changed"}
	changed, err := entity.ContentHash(bare)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if changed == without {
		t.Fatal("the content hash does not cover the content")
	}
}

func TestTheContentHashIsSetBeforeSigning(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	created, pub, keyID := buildCreate(t, v)

	if err := created.VerifyContentHash(); err != nil {
		t.Fatalf("VerifyContentHash: %v", err)
	}
	if err := created.VerifySignature(serverName, keyID, pub, v); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}

	fields := created.Fields()
	delete(fields, "hashes")
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stripped, err := entity.NewEventFromJSON(raw, v)
	if err != nil {
		t.Fatalf("NewEventFromJSON: %v", err)
	}
	if err := stripped.VerifySignature(serverName, keyID, pub, v); err == nil {
		t.Fatal("the signature does not cover the content hash")
	}
}

func TestARedactedEventStillVerifiesUnderTheOriginalSignature(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	keyID, pub, priv := signingKey(t, 2)

	created, _, _ := buildCreate(t, v)
	roomID, err := entity.RoomIDFor(created, v, serverName, "")
	if err != nil {
		t.Fatalf("RoomIDFor: %v", err)
	}

	built, err := entity.EventBuilder{
		Version:        v,
		RoomID:         roomID,
		Type:           entity.EventTypeMessage,
		Sender:         "@creator:" + serverName,
		Content:        map[string]any{"body": "secret", "msgtype": "m.text"},
		PrevEvents:     []string{created.ID()},
		PrevDepth:      created.Depth(),
		AuthEvents:     []string{},
		OriginServerTS: 2000,
	}.Build(entity.KeySigner(serverName, keyID, priv))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	redacted := entity.Redact(built.Fields(), v)
	raw, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stripped, err := entity.NewEventFromJSON(raw, v)
	if err != nil {
		t.Fatalf("NewEventFromJSON: %v", err)
	}

	if len(stripped.Content()) != 0 {
		t.Fatalf("redaction kept content: %v", stripped.Content())
	}
	if err := stripped.VerifySignature(serverName, keyID, pub, v); err != nil {
		t.Fatalf("a redacted event no longer verifies: %v", err)
	}
	if stripped.ID() != built.ID() {
		t.Fatalf("redaction changed the event id: %s then %s", built.ID(), stripped.ID())
	}
	if err := stripped.VerifyContentHash(); !errors.Is(err, entity.ErrContentHash) {
		t.Fatalf("a redacted event should fail its content hash, got %v", err)
	}
}

func TestDepthFollowsTheParentAndSaturates(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	keyID, _, priv := signingKey(t, 3)
	created, _, _ := buildCreate(t, v)

	if created.Depth() != 1 {
		t.Fatalf("create depth = %d, want 1", created.Depth())
	}

	next := func(prevDepth int64) entity.Event {
		t.Helper()
		e, err := entity.EventBuilder{
			Version:    v,
			RoomID:     "!room:" + serverName,
			Type:       entity.EventTypeMessage,
			Sender:     "@a:" + serverName,
			Content:    map[string]any{},
			PrevEvents: []string{created.ID()},
			PrevDepth:  prevDepth,
		}.Build(entity.KeySigner(serverName, keyID, priv))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return e
	}

	if got := next(41).Depth(); got != 42 {
		t.Fatalf("depth = %d, want 42", got)
	}
	if got := next(entity.MaxDepth).Depth(); got != entity.MaxDepth {
		t.Fatalf("depth = %d, want it to hold at %d", got, entity.MaxDepth)
	}
	if got := next(entity.MaxDepth - 1).Depth(); got != entity.MaxDepth {
		t.Fatalf("depth = %d, want %d", got, entity.MaxDepth)
	}
}

func TestAForkIsRefusedRatherThanFolded(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	keyID, _, priv := signingKey(t, 4)

	for _, prev := range [][]string{{}, {"$a", "$b"}} {
		_, err := entity.EventBuilder{
			Version:    v,
			RoomID:     "!room:" + serverName,
			Type:       entity.EventTypeMessage,
			Sender:     "@a:" + serverName,
			PrevEvents: prev,
		}.Build(entity.KeySigner(serverName, keyID, priv))
		if !errors.Is(err, entity.ErrForkedDAG) {
			t.Fatalf("Build with %d prev_events error = %v, want ErrForkedDAG", len(prev), err)
		}
	}
}

func TestAnEventOverTheSizeLimitIsRefused(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	keyID, _, priv := signingKey(t, 5)

	_, err := entity.EventBuilder{
		Version:    v,
		RoomID:     "!room:" + serverName,
		Type:       entity.EventTypeMessage,
		Sender:     "@a:" + serverName,
		Content:    map[string]any{"body": strings.Repeat("x", entity.MaxEventBytes)},
		PrevEvents: []string{"$parent"},
	}.Build(entity.KeySigner(serverName, keyID, priv))

	if !errors.Is(err, entity.ErrEventTooLarge) {
		t.Fatalf("error = %v, want ErrEventTooLarge", err)
	}
}

func TestFieldLimitsAreMeasuredInBytesNotRunes(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	keyID, _, priv := signingKey(t, 6)

	long := strings.Repeat("日", 200)

	_, err := entity.EventBuilder{
		Version:    v,
		RoomID:     "!room:" + serverName,
		Type:       entity.EventTypeMessage,
		StateKey:   ptr(long),
		Sender:     "@a:" + serverName,
		PrevEvents: []string{"$parent"},
	}.Build(entity.KeySigner(serverName, keyID, priv))

	if !errors.Is(err, entity.ErrEventFieldTooLong) {
		t.Fatalf("error = %v, want ErrEventFieldTooLong", err)
	}
}

func TestTooManyParentsOrAuthEventsAreRefused(t *testing.T) {
	v := version(t, entity.RoomVersion12)

	fields := map[string]any{
		"type":        entity.EventTypeMessage,
		"sender":      "@a:" + serverName,
		"room_id":     "!room:" + serverName,
		"content":     map[string]any{},
		"depth":       2,
		"prev_events": make([]any, entity.MaxPrevEvents+1),
		"auth_events": []any{},
	}
	for i := range fields["prev_events"].([]any) {
		fields["prev_events"].([]any)[i] = "$e"
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := entity.NewEventFromJSON(raw, v); !errors.Is(err, entity.ErrTooManyPrevEvents) {
		t.Fatalf("error = %v, want ErrTooManyPrevEvents", err)
	}
}

func TestTheStoredBytesAreCanonical(t *testing.T) {
	v := version(t, entity.RoomVersion12)
	created, _, _ := buildCreate(t, v)

	again, err := entity.CanonicalJSONFrom(created.JSON())
	if err != nil {
		t.Fatalf("CanonicalJSONFrom: %v", err)
	}
	if string(again) != string(created.JSON()) {
		t.Fatal("the stored event bytes are not canonical")
	}

	sum := sha256.Sum256(created.JSON())
	reparsed, err := entity.NewEventFromJSON(created.JSON(), v)
	if err != nil {
		t.Fatalf("NewEventFromJSON: %v", err)
	}
	if sha256.Sum256(reparsed.JSON()) != sum {
		t.Fatal("re-reading an event changed its bytes")
	}
	if reparsed.ID() != created.ID() {
		t.Fatalf("re-reading changed the id: %s then %s", created.ID(), reparsed.ID())
	}
}

func TestSenderDomainIsParsedNotAssumed(t *testing.T) {
	domain, err := entity.SenderDomain("@alice:example.com")
	if err != nil || domain != "example.com" {
		t.Fatalf("SenderDomain = %q, %v", domain, err)
	}
	if _, err := entity.SenderDomain("@alice:example.com:8448"); err != nil {
		t.Fatalf("SenderDomain with a port: %v", err)
	}
	for _, bad := range []string{"", "alice:example.com", "@alice", "@alice:"} {
		if _, err := entity.SenderDomain(bad); !errors.Is(err, entity.ErrEventMalformed) {
			t.Fatalf("SenderDomain(%q) error = %v", bad, err)
		}
	}
}
