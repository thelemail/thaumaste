package entity

import (
	"encoding/json"
	"errors"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

const (
	MaxKeyAlgorithmBytes = 64
	MaxKeyIDBytes        = 255
	MaxKeyJSONBytes      = 65536

	AlgorithmSignedCurve25519 = "signed_curve25519"

	keyIDParts     = 2
	keyIDSeparator = ":"
	signaturesKey  = "signatures"
)

var (
	ErrKeyIdentityMismatch = errors.New("entity: uploaded device keys name another user or device")
	ErrDeviceKeyMalformed  = errors.New("entity: device keys must carry algorithms, keys and signatures")
	ErrKeyIDMalformed      = errors.New("entity: key identifiers must be <algorithm>:<key id>")
	ErrOneTimeKeyConflict  = errors.New("entity: a one-time key with that identifier already exists")
	ErrFallbackKeyConflict = errors.New("entity: more than one fallback key for an algorithm")
	ErrTooManyOneTimeKeys  = errors.New("entity: too many one-time keys for the device")
	ErrTooManyKeyTargets   = errors.New("entity: too many users or devices in the request")
)

type KeyIdentifier struct {
	Algorithm string
	KeyID     string
}

func ParseKeyIdentifier(raw string) (KeyIdentifier, error) {
	parts := strings.SplitN(raw, keyIDSeparator, keyIDParts)
	if len(parts) != keyIDParts || parts[0] == "" || parts[1] == "" {
		return KeyIdentifier{}, ErrKeyIDMalformed
	}
	if len(parts[0]) > MaxKeyAlgorithmBytes || len(parts[1]) > MaxKeyIDBytes {
		return KeyIdentifier{}, ErrKeyIDMalformed
	}
	return KeyIdentifier{Algorithm: parts[0], KeyID: parts[1]}, nil
}

func (k KeyIdentifier) String() string { return k.Algorithm + keyIDSeparator + k.KeyID }

type DeviceKey struct {
	UserID      string
	DeviceID    string
	DisplayName string
	KeyJSON     json.RawMessage
}

func (DeviceKey) Validate() error { return nil }

type ClaimedKey struct {
	UserID   string
	DeviceID string
	KeyID    KeyIdentifier
	KeyJSON  json.RawMessage
}

func (ClaimedKey) Validate() error { return nil }

type NewDeviceKey struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	KeyJSON  []byte
}

func (n NewDeviceKey) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.KeyJSON, validation.Required, validation.Length(1, MaxKeyJSONBytes)),
	)
}

type NewOneTimeKey struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	KeyID    KeyIdentifier
	KeyJSON  []byte
}

func (n NewOneTimeKey) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.KeyJSON, validation.Required, validation.Length(1, MaxKeyJSONBytes)),
	)
}

type NewFallbackKey struct {
	TenantID uuid.UUID
	UserID   string
	DeviceID string
	KeyID    KeyIdentifier
	KeyJSON  []byte
}

func (n NewFallbackKey) Validate() error {
	return validation.ValidateStruct(&n,
		validation.Field(&n.TenantID, validation.By(notNilUUID)),
		validation.Field(&n.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&n.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
		validation.Field(&n.KeyJSON, validation.Required, validation.Length(1, MaxKeyJSONBytes)),
	)
}

type KeyUpload struct {
	TenantID     uuid.UUID
	UserID       string
	DeviceID     string
	DeviceKeys   json.RawMessage
	OneTimeKeys  map[string]json.RawMessage
	FallbackKeys map[string]json.RawMessage
}

func (u KeyUpload) Validate() error {
	if err := validation.ValidateStruct(&u,
		validation.Field(&u.TenantID, validation.By(notNilUUID)),
		validation.Field(&u.UserID, validation.Required, validation.Length(1, MaxUserIDBytes)),
		validation.Field(&u.DeviceID, validation.Required, validation.Length(1, MaxDeviceIDBytes)),
	); err != nil {
		return err
	}
	if len(u.DeviceKeys) > 0 {
		if err := u.checkDeviceKeys(); err != nil {
			return err
		}
	}
	if _, err := u.identify(u.OneTimeKeys); err != nil {
		return err
	}
	fallback, err := u.identify(u.FallbackKeys)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(fallback))
	for id := range fallback {
		if seen[id.Algorithm] {
			return ErrFallbackKeyConflict
		}
		seen[id.Algorithm] = true
	}
	return nil
}

type deviceKeyFields struct {
	UserID     string          `json:"user_id"`
	DeviceID   string          `json:"device_id"`
	Algorithms json.RawMessage `json:"algorithms"`
	Keys       json.RawMessage `json:"keys"`
	Signatures json.RawMessage `json:"signatures"`
}

func (u KeyUpload) checkDeviceKeys() error {
	var fields deviceKeyFields
	if err := json.Unmarshal(u.DeviceKeys, &fields); err != nil {
		return ErrDeviceKeyMalformed
	}
	if fields.UserID != u.UserID || fields.DeviceID != u.DeviceID {
		return ErrKeyIdentityMismatch
	}
	if len(fields.Algorithms) == 0 || len(fields.Keys) == 0 || len(fields.Signatures) == 0 {
		return ErrDeviceKeyMalformed
	}
	return nil
}

func (u KeyUpload) identify(keys map[string]json.RawMessage) (map[KeyIdentifier]json.RawMessage, error) {
	out := make(map[KeyIdentifier]json.RawMessage, len(keys))
	for raw, key := range keys {
		id, err := ParseKeyIdentifier(raw)
		if err != nil {
			return nil, err
		}
		out[id] = key
	}
	return out, nil
}

func (u KeyUpload) OneTime() (map[KeyIdentifier]json.RawMessage, error) {
	return u.identify(u.OneTimeKeys)
}

func (u KeyUpload) Fallback() (map[KeyIdentifier]json.RawMessage, error) {
	return u.identify(u.FallbackKeys)
}

func KeyMaterial(raw []byte) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return CanonicalJSONFrom(raw)
	}
	delete(object, signaturesKey)
	return CanonicalJSON(object)
}

type KeyQuery struct {
	Devices map[string][]string
}

func (q KeyQuery) Validate() error {
	return validation.ValidateStruct(&q,
		validation.Field(&q.Devices, validation.NotNil),
	)
}

type KeyClaim struct {
	Devices map[string]map[string]string
}

func (c KeyClaim) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.Devices, validation.NotNil),
	)
}
