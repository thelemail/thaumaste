package entity

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

var (
	ErrBadCredentials    = errors.New("entity: credentials are not valid")
	ErrCredentialFormat  = errors.New("entity: stored credential is malformed")
	ErrNoLocalCredential = errors.New("entity: user has no local password")
	ErrWeakPassword      = errors.New("entity: password is too weak")
)

const (
	CredentialAlgorithmArgon2id = "argon2id"

	MinPasswordLength = 8
	MaxPasswordLength = 512

	credentialSaltBytes = 16
	credentialKeyBytes  = 32
)

// Argon2Params are stored beside every hash rather than assumed, so raising the cost later leaves
// existing rows verifiable and lets each one be re-hashed on the owner's next successful login.
type Argon2Params struct {
	Time    uint32
	Memory  uint32
	Threads uint8
}

func DefaultArgon2Params() Argon2Params {
	return Argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4}
}

func (p Argon2Params) String() string {
	return fmt.Sprintf("t=%d,m=%d,p=%d", p.Time, p.Memory, p.Threads)
}

func ParseArgon2Params(encoded string) (Argon2Params, error) {
	var p Argon2Params
	for _, part := range strings.Split(encoded, ",") {
		key, value, found := strings.Cut(part, "=")
		if !found {
			return Argon2Params{}, ErrCredentialFormat
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return Argon2Params{}, ErrCredentialFormat
		}
		switch key {
		case "t":
			p.Time = uint32(n)
		case "m":
			p.Memory = uint32(n)
		case "p":
			p.Threads = uint8(n)
		default:
			return Argon2Params{}, ErrCredentialFormat
		}
	}
	if p.Time == 0 || p.Memory == 0 || p.Threads == 0 {
		return Argon2Params{}, ErrCredentialFormat
	}
	return p, nil
}

func (p Argon2Params) AtLeast(target Argon2Params) bool {
	return p.Time >= target.Time && p.Memory >= target.Memory && p.Threads >= target.Threads
}

type Credential struct {
	UserID    string
	Algorithm string
	Params    string
	Salt      []byte
	Hash      []byte
}

func (Credential) Validate() error { return nil }

func NewCredential(userID, password string, params Argon2Params, rnd io.Reader) (Credential, error) {
	if err := CheckPasswordStrength(password); err != nil {
		return Credential{}, err
	}
	if rnd == nil {
		rnd = rand.Reader
	}
	salt := make([]byte, credentialSaltBytes)
	if _, err := io.ReadFull(rnd, salt); err != nil {
		return Credential{}, fmt.Errorf("entity: credential salt: %w", err)
	}
	return Credential{
		UserID:    userID,
		Algorithm: CredentialAlgorithmArgon2id,
		Params:    params.String(),
		Salt:      salt,
		Hash:      HashPassword(password, salt, params),
	}, nil
}

// Verify compares in constant time. A mismatch and a malformed row both answer ErrBadCredentials so
// a caller cannot tell one from the other by timing or by message.
func (c Credential) Verify(password string) error {
	if c.Algorithm != CredentialAlgorithmArgon2id {
		return ErrBadCredentials
	}
	params, err := ParseArgon2Params(c.Params)
	if err != nil {
		return ErrBadCredentials
	}
	computed := argon2.IDKey([]byte(password), c.Salt, params.Time, params.Memory, params.Threads, uint32(len(c.Hash)))
	if subtle.ConstantTimeCompare(computed, c.Hash) != 1 {
		return ErrBadCredentials
	}
	return nil
}

func (c Credential) NeedsRehash(target Argon2Params) bool {
	params, err := ParseArgon2Params(c.Params)
	if err != nil {
		return true
	}
	return !params.AtLeast(target)
}

func HashPassword(password string, salt []byte, params Argon2Params) []byte {
	return argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, credentialKeyBytes)
}

func CheckPasswordStrength(password string) error {
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return ErrWeakPassword
	}
	return nil
}
