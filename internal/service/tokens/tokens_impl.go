package tokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

const secretBytes = 32

type srv struct {
	tokens repository.AccessToken
	clock  func() time.Time
	rnd    io.Reader
}

func New(tokens repository.AccessToken, clock func() time.Time, rnd io.Reader) service.Tokens {
	if clock == nil {
		clock = time.Now
	}
	if rnd == nil {
		rnd = rand.Reader
	}
	return &srv{tokens: tokens, clock: clock, rnd: rnd}
}

func (s *srv) Mint(ctx context.Context, scope entity.TenantScope, userID string, ttl time.Duration) (string, entity.AccessToken, error) {
	raw := make([]byte, secretBytes)
	if _, err := io.ReadFull(s.rnd, raw); err != nil {
		return "", entity.AccessToken{}, fmt.Errorf("tokens: generate secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)

	in := entity.NewAccessToken{
		TenantID:  scope.ID(),
		UserID:    userID,
		TokenHash: hash(secret),
	}
	if ttl > 0 {
		at := s.clock().UTC().Add(ttl)
		in.ExpiresAt = &at
	}
	if err := in.Validate(); err != nil {
		return "", entity.AccessToken{}, err
	}

	t, err := s.tokens.Insert(ctx, in)
	if err != nil {
		return "", entity.AccessToken{}, err
	}
	return secret, t, nil
}

func (s *srv) Resolve(ctx context.Context, token string) (entity.AccessToken, error) {
	if token == "" {
		return entity.AccessToken{}, entity.ErrTokenNotFound
	}
	t, err := s.tokens.GetByHash(ctx, hash(token))
	if err != nil {
		if errors.Is(err, repository.ErrAccessTokenNotFound) {
			return entity.AccessToken{}, entity.ErrTokenNotFound
		}
		return entity.AccessToken{}, err
	}
	if err := t.Usable(s.clock().UTC()); err != nil {
		return entity.AccessToken{}, err
	}
	return t, nil
}

func (s *srv) Revoke(ctx context.Context, id uuid.UUID) error {
	if err := s.tokens.Revoke(ctx, id, s.clock().UTC()); err != nil {
		if errors.Is(err, repository.ErrAccessTokenNotFound) {
			return entity.ErrTokenNotFound
		}
		return err
	}
	return nil
}

func (s *srv) RevokeForUser(ctx context.Context, scope entity.TenantScope, userID string) (int64, error) {
	return s.tokens.RevokeForUser(ctx, scope, userID, s.clock().UTC())
}

func hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
