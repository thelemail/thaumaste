package keyseal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/thelemail/thaumaste/internal/config"
)

const MasterKeySize = 32

var (
	ErrMasterKeySize = errors.New("keyseal: master key must be 32 bytes")
	ErrCiphertext    = errors.New("keyseal: ciphertext is truncated or corrupt")
)

type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}

type aesSealer struct {
	aead cipher.AEAD
}

func New(cfg config.Signing) (Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(cfg.MasterKey)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(cfg.MasterKey)
		if err != nil {
			return nil, fmt.Errorf("keyseal: decode master key: %w", err)
		}
	}
	return NewWithKey(key)
}

func NewWithKey(key []byte) (Sealer, error) {
	if len(key) != MasterKeySize {
		return nil, fmt.Errorf("%w, got %d", ErrMasterKeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("keyseal: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keyseal: gcm: %w", err)
	}
	return &aesSealer{aead: aead}, nil
}

func (s *aesSealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("keyseal: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *aesSealer) Open(ciphertext []byte) ([]byte, error) {
	size := s.aead.NonceSize()
	if len(ciphertext) < size+s.aead.Overhead() {
		return nil, ErrCiphertext
	}
	plaintext, err := s.aead.Open(nil, ciphertext[:size], ciphertext[size:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCiphertext, err)
	}
	return plaintext, nil
}
