package keyseal_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
)

func master(seed byte) []byte {
	key := make([]byte, keyseal.MasterKeySize)
	for i := range key {
		key[i] = seed
	}
	return key
}

func sealer(t *testing.T, seed byte) keyseal.Sealer {
	t.Helper()
	s, err := keyseal.NewWithKey(master(seed))
	if err != nil {
		t.Fatalf("NewWithKey: %v", err)
	}
	return s
}

func TestSealedKeyMaterialRoundTrips(t *testing.T) {
	s := sealer(t, 1)
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	sealed, err := s.Seal(priv)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, priv) {
		t.Fatal("opened key does not match the sealed one")
	}
}

func TestCiphertextDoesNotContainThePlaintext(t *testing.T) {
	s := sealer(t, 2)
	plaintext := []byte("this must not appear in the database")

	sealed, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("ciphertext leaks the plaintext")
	}
}

func TestSealingTheSameKeyTwiceGivesDifferentCiphertext(t *testing.T) {
	s := sealer(t, 3)
	plaintext := []byte("same input")

	first, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("nonce is being reused")
	}
}

func TestAnotherMasterKeyCannotOpenIt(t *testing.T) {
	sealed, err := sealer(t, 4).Seal([]byte("secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := sealer(t, 5).Open(sealed); !errors.Is(err, keyseal.ErrCiphertext) {
		t.Fatalf("Open error = %v, want ErrCiphertext", err)
	}
}

func TestTamperedCiphertextIsRejected(t *testing.T) {
	s := sealer(t, 6)
	sealed, err := s.Seal([]byte("secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := s.Open(tampered); !errors.Is(err, keyseal.ErrCiphertext) {
		t.Fatalf("Open error = %v, want ErrCiphertext", err)
	}

	if _, err := s.Open(sealed[:4]); !errors.Is(err, keyseal.ErrCiphertext) {
		t.Fatalf("Open of a truncated ciphertext error = %v, want ErrCiphertext", err)
	}
}

func TestAMasterKeyOfTheWrongSizeIsRefused(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33} {
		if _, err := keyseal.NewWithKey(make([]byte, size)); !errors.Is(err, keyseal.ErrMasterKeySize) {
			t.Fatalf("NewWithKey(%d bytes) error = %v, want ErrMasterKeySize", size, err)
		}
	}
}

func TestTheMasterKeyIsReadWithOrWithoutPadding(t *testing.T) {
	key := master(7)
	for _, encoded := range []string{
		base64.StdEncoding.EncodeToString(key),
		base64.RawStdEncoding.EncodeToString(key),
	} {
		if _, err := keyseal.New(config.Signing{MasterKey: encoded}); err != nil {
			t.Fatalf("New(%q): %v", encoded, err)
		}
	}
	if _, err := keyseal.New(config.Signing{MasterKey: "not base64!"}); err == nil {
		t.Fatal("New accepted a master key that is not base64")
	}
}
