package tokens_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/repository/accesstoken"
	"github.com/thelemail/thaumaste/internal/repository/signingkey"
	"github.com/thelemail/thaumaste/internal/repository/tenant"
	"github.com/thelemail/thaumaste/internal/service"
	"github.com/thelemail/thaumaste/internal/service/tenants"
	"github.com/thelemail/thaumaste/internal/service/tokens"
	"github.com/thelemail/thaumaste/internal/testutil/pgtest"
)

type fixture struct {
	tokens  service.Tokens
	tenants service.Tenants
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pg := pgtest.Connect(t, "tenants")
	sealer, err := keyseal.NewWithKey(make([]byte, keyseal.MasterKeySize))
	if err != nil {
		t.Fatalf("keyseal: %v", err)
	}
	f := &fixture{now: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)}
	f.tenants = tenants.New(tenant.New(pg), signingkey.New(pg), sealer, pg, nil, nil)
	f.tokens = tokens.New(accesstoken.New(pg), func() time.Time { return f.now }, nil)
	return f
}

func (f *fixture) tenant(t *testing.T, serverName string) entity.Tenant {
	t.Helper()
	created, err := f.tenants.Create(t.Context(), entity.NewTenant{ServerName: serverName})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return created
}

func TestAMintedTokenResolvesBackToItsOwnerAndDomain(t *testing.T) {
	f := newFixture(t)
	alpha := f.tenant(t, "alpha.test")

	secret, minted, err := f.tokens.Mint(t.Context(), alpha.Scope(), "@someone:alpha.test", time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	resolved, err := f.tokens.Resolve(t.Context(), secret)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ID != minted.ID {
		t.Fatalf("resolved a different token: %s vs %s", resolved.ID, minted.ID)
	}
	if resolved.TenantID != alpha.ID {
		t.Fatal("the token does not name the domain it was minted for")
	}
	if resolved.UserID != "@someone:alpha.test" {
		t.Fatalf("user = %q", resolved.UserID)
	}
}

func TestTheSecretIsNotStored(t *testing.T) {
	f := newFixture(t)
	alpha := f.tenant(t, "alpha.test")

	secret, _, err := f.tokens.Mint(t.Context(), alpha.Scope(), "@someone:alpha.test", time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	pg := pgtest.Connect(t)
	var found int
	err = pg.QueryRowContext(t.Context(),
		`SELECT count(*) FROM access_tokens WHERE encode(token_hash, 'escape') = $1`, secret).Scan(&found)
	if err != nil {
		t.Fatalf("search for the secret: %v", err)
	}
	if found != 0 {
		t.Fatal("the token secret is stored as written")
	}
}

func TestTwoMintsNeverProduceTheSameSecret(t *testing.T) {
	f := newFixture(t)
	alpha := f.tenant(t, "alpha.test")

	seen := make(map[string]struct{}, 50)
	for range 50 {
		secret, _, err := f.tokens.Mint(t.Context(), alpha.Scope(), "@someone:alpha.test", time.Hour)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if _, repeated := seen[secret]; repeated {
			t.Fatal("a secret was issued twice")
		}
		seen[secret] = struct{}{}
	}
}

func TestATokenStopsResolvingOnceItExpires(t *testing.T) {
	f := newFixture(t)
	alpha := f.tenant(t, "alpha.test")

	secret, _, err := f.tokens.Mint(t.Context(), alpha.Scope(), "@someone:alpha.test", time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	f.now = f.now.Add(59 * time.Minute)
	if _, err := f.tokens.Resolve(t.Context(), secret); err != nil {
		t.Fatalf("Resolve before expiry: %v", err)
	}

	f.now = f.now.Add(2 * time.Minute)
	if _, err := f.tokens.Resolve(t.Context(), secret); !errors.Is(err, entity.ErrTokenExpired) {
		t.Fatalf("Resolve after expiry = %v, want ErrTokenExpired", err)
	}
}

func TestATokenWithoutATtlDoesNotExpire(t *testing.T) {
	f := newFixture(t)
	alpha := f.tenant(t, "alpha.test")

	secret, _, err := f.tokens.Mint(t.Context(), alpha.Scope(), "@someone:alpha.test", 0)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	f.now = f.now.AddDate(1, 0, 0)
	if _, err := f.tokens.Resolve(t.Context(), secret); err != nil {
		t.Fatalf("Resolve a year later: %v", err)
	}
}

func TestARevokedTokenIsRefusedForever(t *testing.T) {
	f := newFixture(t)
	alpha := f.tenant(t, "alpha.test")

	secret, minted, err := f.tokens.Mint(t.Context(), alpha.Scope(), "@someone:alpha.test", time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := f.tokens.Revoke(t.Context(), minted.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, err := f.tokens.Resolve(t.Context(), secret); !errors.Is(err, entity.ErrTokenRevoked) {
		t.Fatalf("Resolve = %v, want ErrTokenRevoked", err)
	}
	if err := f.tokens.Revoke(t.Context(), minted.ID); !errors.Is(err, entity.ErrTokenNotFound) {
		t.Fatalf("revoking twice = %v", err)
	}
}

func TestRevokingOneUsersTokensLeavesTheOtherDomainAlone(t *testing.T) {
	f := newFixture(t)
	alpha := f.tenant(t, "alpha.test")
	beta := f.tenant(t, "beta.test")

	const user = "@someone:shared"
	alphaSecret, _, err := f.tokens.Mint(t.Context(), alpha.Scope(), user, time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	betaSecret, _, err := f.tokens.Mint(t.Context(), beta.Scope(), user, time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	revoked, err := f.tokens.RevokeForUser(t.Context(), alpha.Scope(), user)
	if err != nil {
		t.Fatalf("RevokeForUser: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked = %d, want 1", revoked)
	}

	if _, err := f.tokens.Resolve(t.Context(), alphaSecret); !errors.Is(err, entity.ErrTokenRevoked) {
		t.Fatalf("alpha's token = %v, want revoked", err)
	}
	if _, err := f.tokens.Resolve(t.Context(), betaSecret); err != nil {
		t.Fatalf("beta's token was revoked too: %v", err)
	}
}

func TestAnUnknownOrEmptySecretResolvesToNothing(t *testing.T) {
	f := newFixture(t)

	for _, secret := range []string{"", "not-a-token"} {
		if _, err := f.tokens.Resolve(t.Context(), secret); !errors.Is(err, entity.ErrTokenNotFound) {
			t.Fatalf("Resolve(%q) = %v, want ErrTokenNotFound", secret, err)
		}
	}
	if err := f.tokens.Revoke(t.Context(), uuid.New()); !errors.Is(err, entity.ErrTokenNotFound) {
		t.Fatalf("Revoke of an unknown id = %v", err)
	}
}
