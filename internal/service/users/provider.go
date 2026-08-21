package users

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
)

// provider turns a login request into a user id, or refuses. The local store and the external
// assertion are both providers, neither wrapping the other, so a deployment can offer one, the
// other, or both without either being a special case.
type provider interface {
	loginType() string
	authenticate(ctx context.Context, scope entity.TenantScope, in providerInput) (providerResult, error)
}

type providerInput struct {
	Identifier string
	Password   string
	Token      string
	ServerName string
}

type providerResult struct {
	UserID      string
	Provision   bool
	DisplayName string
}

type localProvider struct {
	users       repository.User
	credentials repository.Credential
	params      entity.Argon2Params
}

func (p *localProvider) loginType() string { return entity.LoginTypePassword }

func (p *localProvider) authenticate(ctx context.Context, scope entity.TenantScope, in providerInput) (providerResult, error) {
	userID, err := entity.ResolveUserID(in.Identifier, in.ServerName)
	if err != nil {
		return providerResult{}, entity.ErrBadCredentials
	}

	credential, err := p.credentials.Get(ctx, scope, userID)
	if err != nil {
		if errors.Is(err, repository.ErrCredentialNotFound) {
			burnHash(in.Password, p.params)
			return providerResult{}, entity.ErrBadCredentials
		}
		return providerResult{}, err
	}
	if err := credential.Verify(in.Password); err != nil {
		return providerResult{}, entity.ErrBadCredentials
	}
	return providerResult{UserID: userID}, nil
}

var decoySalt = make([]byte, 16)

// burnHash spends the same work a real verification would. Answering an unknown user faster than a
// wrong password is how an attacker enumerates accounts without ever guessing one.
func burnHash(password string, params entity.Argon2Params) {
	entity.HashPassword(password, decoySalt, params)
}

type assertion struct {
	Subject     string `json:"sub"`
	ServerName  string `json:"server_name"`
	DisplayName string `json:"name,omitempty"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
}

type assertionProvider struct {
	key   ed25519.PublicKey
	ttl   time.Duration
	clock func() time.Time
}

func (p *assertionProvider) loginType() string { return entity.LoginTypeToken }

func (p *assertionProvider) authenticate(_ context.Context, _ entity.TenantScope, in providerInput) (providerResult, error) {
	if len(p.key) == 0 {
		return providerResult{}, entity.ErrBadCredentials
	}
	body, signature, found := strings.Cut(in.Token, ".")
	if !found {
		return providerResult{}, entity.ErrBadCredentials
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return providerResult{}, entity.ErrBadCredentials
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return providerResult{}, entity.ErrBadCredentials
	}
	if !ed25519.Verify(p.key, raw, sig) {
		return providerResult{}, entity.ErrBadCredentials
	}

	var claims assertion
	if err := json.Unmarshal(raw, &claims); err != nil {
		return providerResult{}, entity.ErrBadCredentials
	}
	now := p.clock().UTC()
	if claims.ExpiresAt == 0 || now.After(time.UnixMilli(claims.ExpiresAt)) {
		return providerResult{}, entity.ErrBadCredentials
	}
	if claims.ServerName != in.ServerName {
		return providerResult{}, entity.ErrBadCredentials
	}

	userID, err := entity.ResolveUserID(claims.Subject, in.ServerName)
	if err != nil {
		return providerResult{}, entity.ErrBadCredentials
	}
	return providerResult{UserID: userID, Provision: true, DisplayName: claims.DisplayName}, nil
}

func SignAssertion(key ed25519.PrivateKey, subject, serverName, displayName string, issued time.Time, ttl time.Duration) (string, error) {
	body, err := json.Marshal(assertion{
		Subject:     subject,
		ServerName:  serverName,
		DisplayName: displayName,
		IssuedAt:    issued.UTC().UnixMilli(),
		ExpiresAt:   issued.UTC().Add(ttl).UnixMilli(),
	})
	if err != nil {
		return "", fmt.Errorf("users: encode assertion: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, body)), nil
}
