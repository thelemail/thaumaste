package tenants

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/pkg/signing"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

var keyVersionEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

const keyVersionBytes = 6

type srv struct {
	tenants repository.Tenant
	keys    repository.SigningKey
	sealer  keyseal.Sealer
	tx      repository.Transactor
	clock   func() time.Time
	rnd     io.Reader
}

func New(
	tenants repository.Tenant,
	keys repository.SigningKey,
	sealer keyseal.Sealer,
	tx repository.Transactor,
	clock func() time.Time,
	rnd io.Reader,
) service.Tenants {
	if clock == nil {
		clock = time.Now
	}
	if rnd == nil {
		rnd = rand.Reader
	}
	return &srv{tenants: tenants, keys: keys, sealer: sealer, tx: tx, clock: clock, rnd: rnd}
}

func (s *srv) Create(ctx context.Context, in entity.NewTenant) (entity.Tenant, error) {
	if in.RegistrationMode == "" {
		in.RegistrationMode = entity.RegistrationClosed
	}
	in.ServerName = entity.NormaliseHost(in.ServerName)
	if len(in.Hosts) == 0 {
		in.Hosts = []string{in.ServerName}
	}
	for i, host := range in.Hosts {
		in.Hosts[i] = entity.NormaliseHost(host)
	}
	if err := in.Validate(); err != nil {
		return entity.Tenant{}, err
	}

	var created entity.Tenant
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		t, err := s.tenants.Create(ctx, in)
		if err != nil {
			if errors.Is(err, repository.ErrTenantAlreadyExists) {
				return entity.ErrTenantAlreadyExists
			}
			return err
		}
		for _, host := range in.Hosts {
			if err := s.tenants.AddHost(ctx, t.Scope(), host); err != nil {
				if errors.Is(err, repository.ErrHostAlreadyClaimed) {
					return entity.ErrHostAlreadyClaimed
				}
				return err
			}
		}
		if _, err := s.mintKey(ctx, t.Scope()); err != nil {
			return err
		}
		created = t
		return nil
	})
	if err != nil {
		return entity.Tenant{}, err
	}
	return created, nil
}

func (s *srv) List(ctx context.Context) ([]entity.Tenant, error) {
	return s.tenants.List(ctx)
}

func (s *srv) ByHost(ctx context.Context, host string) (entity.Tenant, error) {
	t, err := s.tenants.GetByHost(ctx, entity.NormaliseHost(host))
	if err != nil {
		if errors.Is(err, repository.ErrTenantNotFound) {
			return entity.Tenant{}, entity.ErrTenantNotFound
		}
		return entity.Tenant{}, err
	}
	return t, nil
}

func (s *srv) ByServerName(ctx context.Context, serverName string) (entity.Tenant, error) {
	t, err := s.tenants.GetByServerName(ctx, entity.NormaliseHost(serverName))
	if err != nil {
		if errors.Is(err, repository.ErrTenantNotFound) {
			return entity.Tenant{}, entity.ErrTenantNotFound
		}
		return entity.Tenant{}, err
	}
	return t, nil
}

func (s *srv) Hosts(ctx context.Context, scope entity.TenantScope) ([]string, error) {
	return s.tenants.ListHosts(ctx, scope)
}

func (s *srv) AddHost(ctx context.Context, scope entity.TenantScope, host string) error {
	host = entity.NormaliseHost(host)
	if host == "" {
		return entity.ErrInvalidServerName
	}
	if err := s.tenants.AddHost(ctx, scope, host); err != nil {
		if errors.Is(err, repository.ErrHostAlreadyClaimed) {
			return entity.ErrHostAlreadyClaimed
		}
		return err
	}
	return nil
}

func (s *srv) Suspend(ctx context.Context, id uuid.UUID) (entity.Tenant, error) {
	return s.setState(ctx, id, entity.TenantStateSuspended)
}

func (s *srv) Resume(ctx context.Context, id uuid.UUID) (entity.Tenant, error) {
	return s.setState(ctx, id, entity.TenantStateActive)
}

func (s *srv) setState(ctx context.Context, id uuid.UUID, state entity.TenantState) (entity.Tenant, error) {
	t, err := s.tenants.SetState(ctx, id, state)
	if err != nil {
		if errors.Is(err, repository.ErrTenantNotFound) {
			return entity.Tenant{}, entity.ErrTenantNotFound
		}
		return entity.Tenant{}, err
	}
	return t, nil
}

func (s *srv) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.tenants.Delete(ctx, id); err != nil {
		if errors.Is(err, repository.ErrTenantNotFound) {
			return entity.ErrTenantNotFound
		}
		return err
	}
	return nil
}

func (s *srv) Keys(ctx context.Context, scope entity.TenantScope) ([]entity.SigningKey, error) {
	return s.keys.List(ctx, scope)
}

func (s *srv) RotateKey(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, error) {
	var rotated entity.SigningKey
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.keys.Active(ctx, scope)
		switch {
		case errors.Is(err, repository.ErrSigningKeyNotFound):
		case err != nil:
			return err
		default:
			if err := s.keys.Expire(ctx, scope, current.KeyID, s.clock().UTC()); err != nil {
				return err
			}
		}
		rotated, err = s.mintKey(ctx, scope)
		return err
	})
	if err != nil {
		return entity.SigningKey{}, err
	}
	return rotated, nil
}

func (s *srv) SignAs(ctx context.Context, scope entity.TenantScope, document []byte) ([]byte, error) {
	key, sealed, err := s.keys.ActivePrivate(ctx, scope)
	if err != nil {
		if errors.Is(err, repository.ErrSigningKeyNotFound) {
			return nil, entity.ErrNoActiveSigningKey
		}
		return nil, err
	}
	private, err := s.sealer.Open(sealed)
	if err != nil {
		return nil, err
	}
	if len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("tenants: signing key for %s is %d bytes", scope.ServerName(), len(private))
	}
	return signing.Sign(document, scope.ServerName(), signing.KeyID(key.KeyID), ed25519.PrivateKey(private))
}

func (s *srv) mintKey(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, error) {
	public, private, err := ed25519.GenerateKey(s.rnd)
	if err != nil {
		return entity.SigningKey{}, fmt.Errorf("tenants: generate signing key: %w", err)
	}
	version, err := s.keyVersion()
	if err != nil {
		return entity.SigningKey{}, err
	}
	keyID, err := signing.NewKeyID(version)
	if err != nil {
		return entity.SigningKey{}, err
	}
	sealed, err := s.sealer.Seal(private)
	if err != nil {
		return entity.SigningKey{}, err
	}

	in := entity.NewSigningKey{
		TenantID:   scope.ID(),
		KeyID:      string(keyID),
		PublicKey:  public,
		PrivateKey: sealed,
	}
	if err := in.Validate(); err != nil {
		return entity.SigningKey{}, err
	}
	return s.keys.Insert(ctx, in)
}

func (s *srv) keyVersion() (string, error) {
	raw := make([]byte, keyVersionBytes)
	if _, err := io.ReadFull(s.rnd, raw); err != nil {
		return "", fmt.Errorf("tenants: key version: %w", err)
	}
	return strings.ToLower(keyVersionEncoding.EncodeToString(raw)), nil
}
