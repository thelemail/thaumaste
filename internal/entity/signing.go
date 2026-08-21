package entity

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

const SigningAlgorithm = "ed25519"

var (
	ErrNoSignature      = errors.New("entity: object carries no signature from that key")
	ErrBadSignature     = errors.New("entity: signature does not verify")
	ErrMalformedKeyID   = errors.New("entity: malformed key id")
	ErrNotAnObject      = errors.New("entity: value is not a json object")
	ErrKeyVersionFormat = errors.New("entity: key version must match [a-zA-Z0-9_]+")
)

var keyVersionPattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

var signingEncoding = base64.RawStdEncoding

type KeyID string

func NewKeyID(version string) (KeyID, error) {
	if !keyVersionPattern.MatchString(version) {
		return "", fmt.Errorf("%w: %q", ErrKeyVersionFormat, version)
	}
	return KeyID(SigningAlgorithm + ":" + version), nil
}

func (k KeyID) Version() (string, error) {
	prefix := SigningAlgorithm + ":"
	if len(k) <= len(prefix) || string(k[:len(prefix)]) != prefix {
		return "", fmt.Errorf("%w: %q", ErrMalformedKeyID, string(k))
	}
	version := string(k[len(prefix):])
	if !keyVersionPattern.MatchString(version) {
		return "", fmt.Errorf("%w: %q", ErrKeyVersionFormat, version)
	}
	return version, nil
}

func EncodeBase64(b []byte) string { return signingEncoding.EncodeToString(b) }

func DecodeBase64(s string) ([]byte, error) {
	if b, err := signingEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("entity: decode base64: %w", err)
	}
	return b, nil
}

func SignJSON(raw []byte, serverName string, keyID KeyID, key ed25519.PrivateKey) ([]byte, error) {
	object, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}

	signatures, _ := object["signatures"].(map[string]any)
	if signatures == nil {
		signatures = map[string]any{}
	}
	unsigned, hasUnsigned := object["unsigned"]

	delete(object, "signatures")
	delete(object, "unsigned")

	canonical, err := CanonicalJSON(object)
	if err != nil {
		return nil, err
	}

	byServer, _ := signatures[serverName].(map[string]any)
	if byServer == nil {
		byServer = map[string]any{}
	}
	byServer[string(keyID)] = signingEncoding.EncodeToString(ed25519.Sign(key, canonical))
	signatures[serverName] = byServer

	object["signatures"] = signatures
	if hasUnsigned {
		object["unsigned"] = unsigned
	}

	out, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("entity: marshal signed object: %w", err)
	}
	return out, nil
}

func VerifyJSON(raw []byte, serverName string, keyID KeyID, key ed25519.PublicKey) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}

	signatures, _ := object["signatures"].(map[string]any)
	byServer, _ := signatures[serverName].(map[string]any)
	encoded, _ := byServer[string(keyID)].(string)
	if encoded == "" {
		return fmt.Errorf("%w: %s %s", ErrNoSignature, serverName, keyID)
	}
	signature, err := DecodeBase64(encoded)
	if err != nil {
		return err
	}

	delete(object, "signatures")
	delete(object, "unsigned")

	canonical, err := CanonicalJSON(object)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, canonical, signature) {
		return fmt.Errorf("%w: %s %s", ErrBadSignature, serverName, keyID)
	}
	return nil
}

func decodeObject(raw []byte) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotAnObject, err)
	}
	if object == nil {
		return nil, ErrNotAnObject
	}
	return object, nil
}
