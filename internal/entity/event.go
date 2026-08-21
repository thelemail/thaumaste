package entity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
)

const (
	EventTypeCreate            = "m.room.create"
	EventTypeMember            = "m.room.member"
	EventTypePowerLevels       = "m.room.power_levels"
	EventTypeJoinRules         = "m.room.join_rules"
	EventTypeHistoryVisibility = "m.room.history_visibility"
	EventTypeThirdPartyInvite  = "m.room.third_party_invite"
	EventTypeRedaction         = "m.room.redaction"
	EventTypeMessage           = "m.room.message"
	EventTypeEncryption        = "m.room.encryption"
	EventTypeName              = "m.room.name"
	EventTypeTopic             = "m.room.topic"
	EventTypeAvatar            = "m.room.avatar"
	EventTypeCanonicalAlias    = "m.room.canonical_alias"
	EventTypeGuestAccess       = "m.room.guest_access"
	EventTypeTombstone         = "m.room.tombstone"
	EventTypeServerACL         = "m.room.server_acl"
	EventTypeEncrypted         = "m.room.encrypted"
	EventTypeSticker           = "m.sticker"
	EventTypeCallInvite        = "m.call.invite"
	EventTypePollStart         = "m.poll.start"
	EventTypeBeaconInfo        = "m.beacon_info"
)

const (
	MaxEventBytes    = 65536
	MaxUserIDBytes   = 255
	MaxRoomIDBytes   = 255
	MaxEventIDBytes  = 255
	MaxStateKeyBytes = 255
	MaxEventTypeSize = 255

	MaxPrevEvents = 20
	MaxAuthEvents = 10

	MaxDepth = int64(1)<<53 - 1
)

var (
	ErrEventTooLarge     = errors.New("entity: event exceeds the maximum size")
	ErrEventFieldTooLong = errors.New("entity: event field exceeds its maximum length")
	ErrEventMalformed    = errors.New("entity: event is malformed")
	ErrContentHash       = errors.New("entity: content hash does not match")
	ErrTooManyPrevEvents = errors.New("entity: too many prev_events")
	ErrTooManyAuthEvents = errors.New("entity: too many auth_events")
)

var eventIDEncoding = base64.RawURLEncoding

type Event struct {
	fields map[string]any
	json   []byte
	id     string
	roomID string
}

func NewEventFromJSON(raw []byte, version RoomVersion) (Event, error) {
	canonical, err := CanonicalJSONFrom(raw)
	if err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrEventMalformed, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrEventMalformed, err)
	}
	if fields == nil {
		return Event{}, ErrEventMalformed
	}

	e := Event{fields: fields, json: canonical}
	if err := e.checkLimits(version); err != nil {
		return Event{}, err
	}
	id, err := e.deriveID(version)
	if err != nil {
		return Event{}, err
	}
	e.id = id
	e.roomID, _ = fields["room_id"].(string)
	return e, nil
}

func (e Event) JSON() []byte          { return e.json }
func (e Event) ID() string            { return e.id }
func (e Event) RoomID() string        { return e.roomID }
func (e Event) Type() string          { return e.stringField("type") }
func (e Event) Sender() string        { return e.stringField("sender") }
func (e Event) Depth() int64          { return e.intField("depth") }
func (e Event) OriginServerTS() int64 { return e.intField("origin_server_ts") }

func (e Event) StateKey() (string, bool) {
	value, ok := e.fields["state_key"].(string)
	return value, ok
}

func (e Event) IsState() bool {
	_, ok := e.fields["state_key"]
	return ok
}

func (e Event) Content() map[string]any {
	content, _ := e.fields["content"].(map[string]any)
	if content == nil {
		return map[string]any{}
	}
	return content
}

func (e Event) PrevEvents() []string { return e.idList("prev_events") }
func (e Event) AuthEvents() []string { return e.idList("auth_events") }

func (e Event) Fields() map[string]any { return cloneMap(e.fields) }

func (e Event) stringField(key string) string {
	value, _ := e.fields[key].(string)
	return value
}

func (e Event) intField(key string) int64 {
	switch value := e.fields[key].(type) {
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0
		}
		return n
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func (e Event) idList(key string) []string {
	raw, _ := e.fields[key].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if id, ok := item.(string); ok {
			out = append(out, id)
		}
	}
	return out
}

func (e Event) checkLimits(version RoomVersion) error {
	if len(e.json) > MaxEventBytes {
		return fmt.Errorf("%w: %d bytes", ErrEventTooLarge, len(e.json))
	}
	limits := []struct {
		key   string
		limit int
	}{
		{"sender", MaxUserIDBytes},
		{"room_id", MaxRoomIDBytes},
		{"state_key", MaxStateKeyBytes},
		{"type", MaxEventTypeSize},
		{"event_id", MaxEventIDBytes},
	}
	for _, l := range limits {
		if value, ok := e.fields[l.key].(string); ok && len(value) > l.limit {
			return fmt.Errorf("%w: %s is %d bytes", ErrEventFieldTooLong, l.key, len(value))
		}
	}
	if len(e.idList("prev_events")) > MaxPrevEvents {
		return ErrTooManyPrevEvents
	}
	if len(e.idList("auth_events")) > MaxAuthEvents {
		return ErrTooManyAuthEvents
	}
	if e.Type() == EventTypeCreate {
		_, present := e.fields["room_id"]
		if present != version.CreateCarriesRoomID {
			return fmt.Errorf("%w: create event room_id present=%v under version %s",
				ErrRoomVersionMismatch, present, version.ID)
		}
	}
	return nil
}

func ContentHash(fields map[string]any) ([32]byte, error) {
	stripped := cloneMap(fields)
	delete(stripped, "unsigned")
	delete(stripped, "signatures")
	delete(stripped, "hashes")

	canonical, err := CanonicalJSON(stripped)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func ReferenceHash(fields map[string]any, version RoomVersion) ([32]byte, error) {
	redacted := Redact(fields, version)
	delete(redacted, "signatures")
	delete(redacted, "unsigned")

	canonical, err := CanonicalJSON(redacted)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func (e Event) deriveID(version RoomVersion) (string, error) {
	sum, err := ReferenceHash(e.fields, version)
	if err != nil {
		return "", err
	}
	return "$" + eventIDEncoding.EncodeToString(sum[:]), nil
}

func (e Event) VerifyContentHash() error {
	hashes, _ := e.fields["hashes"].(map[string]any)
	claimed, _ := hashes["sha256"].(string)
	if claimed == "" {
		return fmt.Errorf("%w: no sha256 hash", ErrContentHash)
	}
	expected, err := ContentHash(e.fields)
	if err != nil {
		return err
	}
	if claimed != signingEncoding.EncodeToString(expected[:]) {
		return ErrContentHash
	}
	return nil
}

func (e Event) VerifySignature(serverName string, keyID KeyID, key ed25519.PublicKey, version RoomVersion) error {
	redacted := Redact(e.fields, version)
	redacted["signatures"] = e.fields["signatures"]

	raw, err := json.Marshal(redacted)
	if err != nil {
		return fmt.Errorf("entity: marshal redacted event: %w", err)
	}
	return VerifyJSON(raw, serverName, keyID, key)
}

func EventIDEncoding() *base64.Encoding { return eventIDEncoding }

func SenderDomain(userID string) (string, error) {
	_, domain, found := strings.Cut(userID, ":")
	if !found || domain == "" || !strings.HasPrefix(userID, "@") {
		return "", fmt.Errorf("%w: sender %q", ErrEventMalformed, userID)
	}
	return domain, nil
}

func cloneMap(in map[string]any) map[string]any {
	return maps.Clone(in)
}
