package entity

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrForkedDAG      = errors.New("entity: an event may name exactly one prev_event")
	ErrCreateHasPrev  = errors.New("entity: the create event cannot name a prev_event")
	ErrMissingRoomID  = errors.New("entity: event needs a room id")
	ErrBuilderInvalid = errors.New("entity: event builder is incomplete")
)

type EventBuilder struct {
	Version        RoomVersion
	RoomID         string
	Type           string
	StateKey       *string
	Sender         string
	Content        map[string]any
	PrevEvents     []string
	PrevDepth      int64
	AuthEvents     []string
	OriginServerTS int64
}

type Signer func(document []byte) ([]byte, error)

func KeySigner(serverName string, keyID KeyID, key ed25519.PrivateKey) Signer {
	return func(document []byte) ([]byte, error) {
		return SignJSON(document, serverName, keyID, key)
	}
}

func (b EventBuilder) Build(sign Signer) (Event, error) {
	if err := b.check(); err != nil {
		return Event{}, err
	}

	fields := map[string]any{
		"type":             b.Type,
		"sender":           b.Sender,
		"content":          orEmpty(b.Content),
		"depth":            b.depth(),
		"prev_events":      orEmptyList(b.PrevEvents),
		"auth_events":      orEmptyList(b.AuthEvents),
		"origin_server_ts": b.OriginServerTS,
	}
	if b.StateKey != nil {
		fields["state_key"] = *b.StateKey
	}
	if b.Type != EventTypeCreate || b.Version.CreateCarriesRoomID {
		fields["room_id"] = b.RoomID
	}

	contentHash, err := ContentHash(fields)
	if err != nil {
		return Event{}, err
	}
	fields["hashes"] = map[string]any{"sha256": signingEncoding.EncodeToString(contentHash[:])}

	signed, err := b.attachSignature(fields, sign)
	if err != nil {
		return Event{}, err
	}
	fields["signatures"] = signed

	raw, err := CanonicalJSON(fields)
	if err != nil {
		return Event{}, err
	}
	return NewEventFromJSON(raw, b.Version)
}

func (b EventBuilder) attachSignature(fields map[string]any, sign Signer) (any, error) {
	redacted := Redact(fields, b.Version)
	raw, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("entity: marshal event for signing: %w", err)
	}
	out, err := sign(raw)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(out, &object); err != nil {
		return nil, fmt.Errorf("entity: read signed event: %w", err)
	}
	return object["signatures"], nil
}

func (b EventBuilder) check() error {
	if b.Type == "" || b.Sender == "" {
		return ErrBuilderInvalid
	}
	if b.Type == EventTypeCreate {
		if len(b.PrevEvents) != 0 {
			return ErrCreateHasPrev
		}
		return nil
	}
	if b.RoomID == "" {
		return ErrMissingRoomID
	}
	if len(b.PrevEvents) != 1 {
		return fmt.Errorf("%w: got %d", ErrForkedDAG, len(b.PrevEvents))
	}
	return nil
}

func (b EventBuilder) depth() int64 {
	if b.Type == EventTypeCreate {
		return 1
	}
	if b.PrevDepth >= MaxDepth {
		return MaxDepth
	}
	return b.PrevDepth + 1
}

func RoomIDFor(create Event, version RoomVersion, serverName string, opaque string) (string, error) {
	switch version.RoomIDFormat {
	case RoomIDFormatCreateEventHash:
		return "!" + create.ID()[1:], nil
	case RoomIDFormatOpaqueDomain:
		if opaque == "" {
			return "", ErrBuilderInvalid
		}
		return "!" + opaque + ":" + serverName, nil
	default:
		return "", fmt.Errorf("%w: room id format", ErrUnsupportedRoomVersion)
	}
}

func orEmpty(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return in
}

func orEmptyList(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
