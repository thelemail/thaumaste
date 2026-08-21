package signing_test

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/pkg/signing"
)

func keypair(t *testing.T, seed byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	raw := make([]byte, ed25519.SeedSize)
	for i := range raw {
		raw[i] = seed
	}
	priv := ed25519.NewKeyFromSeed(raw)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("public key is not ed25519")
	}
	return pub, priv
}

func keyID(t *testing.T, version string) signing.KeyID {
	t.Helper()
	id, err := signing.NewKeyID(version)
	if err != nil {
		t.Fatalf("NewKeyID(%q): %v", version, err)
	}
	return id
}

func sign(t *testing.T, raw string, priv ed25519.PrivateKey, id signing.KeyID) []byte {
	t.Helper()
	out, err := signing.Sign([]byte(raw), "domain", id, priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return out
}

func TestSignedObjectVerifiesUnderItsOwnKey(t *testing.T) {
	pub, priv := keypair(t, 1)
	id := keyID(t, "abc123")

	signed := sign(t, `{"b":2,"a":1}`, priv, id)

	if err := signing.Verify(signed, "domain", id, pub); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSignedObjectDoesNotVerifyUnderAnotherKey(t *testing.T) {
	_, priv := keypair(t, 1)
	other, _ := keypair(t, 2)
	id := keyID(t, "abc123")

	signed := sign(t, `{"a":1}`, priv, id)

	if err := signing.Verify(signed, "domain", id, other); !errors.Is(err, signing.ErrBadSignature) {
		t.Fatalf("Verify error = %v, want ErrBadSignature", err)
	}
}

func TestSignatureCoversNeitherSignaturesNorUnsigned(t *testing.T) {
	pub, priv := keypair(t, 3)
	id := keyID(t, "v1")

	signed := sign(t, `{"a":1,"unsigned":{"age":100}}`, priv, id)

	var object map[string]any
	if err := json.Unmarshal(signed, &object); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	object["unsigned"] = map[string]any{"age": 999}
	tampered, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := signing.Verify(tampered, "domain", id, pub); err != nil {
		t.Fatalf("Verify after changing unsigned: %v", err)
	}
}

func TestChangingAnySignedFieldBreaksTheSignature(t *testing.T) {
	pub, priv := keypair(t, 4)
	id := keyID(t, "v1")

	signed := sign(t, `{"a":1,"unsigned":{"age":1}}`, priv, id)

	var object map[string]any
	if err := json.Unmarshal(signed, &object); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	object["a"] = 2
	tampered, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := signing.Verify(tampered, "domain", id, pub); !errors.Is(err, signing.ErrBadSignature) {
		t.Fatalf("Verify error = %v, want ErrBadSignature", err)
	}
}

func TestUnsignedSurvivesSigningUntouched(t *testing.T) {
	_, priv := keypair(t, 5)

	signed := sign(t, `{"a":1,"unsigned":{"age":100}}`, priv, keyID(t, "v1"))

	var object map[string]any
	if err := json.Unmarshal(signed, &object); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	unsigned, ok := object["unsigned"].(map[string]any)
	if !ok || unsigned["age"] != float64(100) {
		t.Fatalf("unsigned = %v", object["unsigned"])
	}
}

func TestSigningTwiceKeepsBothSignatures(t *testing.T) {
	pubOne, privOne := keypair(t, 6)
	pubTwo, privTwo := keypair(t, 7)
	one, two := keyID(t, "one"), keyID(t, "two")

	signed := sign(t, `{"a":1}`, privOne, one)
	signed, err := signing.Sign(signed, "domain", two, privTwo)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := signing.Verify(signed, "domain", one, pubOne); err != nil {
		t.Fatalf("Verify first key: %v", err)
	}
	if err := signing.Verify(signed, "domain", two, pubTwo); err != nil {
		t.Fatalf("Verify second key: %v", err)
	}
}

func TestVerifyReportsAMissingSignatureSeparately(t *testing.T) {
	pub, priv := keypair(t, 8)

	signed := sign(t, `{"a":1}`, priv, keyID(t, "present"))

	err := signing.Verify(signed, "domain", keyID(t, "absent"), pub)
	if !errors.Is(err, signing.ErrNoSignature) {
		t.Fatalf("Verify error = %v, want ErrNoSignature", err)
	}
	if err := signing.Verify(signed, "elsewhere", keyID(t, "present"), pub); !errors.Is(err, signing.ErrNoSignature) {
		t.Fatalf("Verify for another server error = %v, want ErrNoSignature", err)
	}
}

func TestSignaturesAreUnpaddedBase64(t *testing.T) {
	_, priv := keypair(t, 9)
	id := keyID(t, "v1")

	signed := sign(t, `{"a":1}`, priv, id)

	var object struct {
		Signatures map[string]map[string]string `json:"signatures"`
	}
	if err := json.Unmarshal(signed, &object); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	encoded := object.Signatures["domain"][string(id)]
	if encoded == "" {
		t.Fatal("no signature recorded")
	}
	for _, r := range encoded {
		if r == '=' {
			t.Fatalf("signature carries padding: %s", encoded)
		}
	}
}

func TestKeyIDsRejectCharactersTheSpecDoesNotAllow(t *testing.T) {
	for _, version := range []string{"", "a-b", "a:b", "a.b", "a b"} {
		if _, err := signing.NewKeyID(version); !errors.Is(err, signing.ErrKeyVersionFormat) {
			t.Fatalf("NewKeyID(%q) error = %v, want ErrKeyVersionFormat", version, err)
		}
	}
	if _, err := signing.NewKeyID("aZ0_"); err != nil {
		t.Fatalf("NewKeyID(aZ0_): %v", err)
	}
}

func TestKeyIDRoundTripsThroughItsVersion(t *testing.T) {
	id := keyID(t, "abc_123")
	if string(id) != "ed25519:abc_123" {
		t.Fatalf("KeyID = %s", id)
	}
	version, err := id.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "abc_123" {
		t.Fatalf("Version = %s", version)
	}
	if _, err := signing.KeyID("rsa:abc").Version(); !errors.Is(err, signing.ErrMalformedKeyID) {
		t.Fatalf("Version of a non-ed25519 key id error = %v, want ErrMalformedKeyID", err)
	}
}

func TestKeysDecodeWithOrWithoutPadding(t *testing.T) {
	pub, _ := keypair(t, 10)
	encoded := signing.EncodeKey(pub)

	unpadded, err := signing.DecodeKey(encoded)
	if err != nil {
		t.Fatalf("DecodeKey unpadded: %v", err)
	}
	padded, err := signing.DecodeKey(encoded + "=")
	if err != nil {
		t.Fatalf("DecodeKey padded: %v", err)
	}
	if string(unpadded) != string(pub) || string(padded) != string(pub) {
		t.Fatal("decoded key does not match")
	}
}
