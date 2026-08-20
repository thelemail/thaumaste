package signingkey

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.SigningKey {
	return &repo{db: db}
}

const selectFields = `tenant_id, key_id, public_key, created_at, expired_at`

const insertSQL = `
INSERT INTO tenant_signing_keys (tenant_id, key_id, public_key, private_key)
VALUES ($1, $2, $3, $4)
RETURNING ` + selectFields

func (r *repo) Insert(ctx context.Context, in entity.NewSigningKey) (entity.SigningKey, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, insertSQL,
		in.TenantID.String(), in.KeyID, []byte(in.PublicKey), in.PrivateKey)
	k, err := scanSigningKey(row)
	if err != nil {
		return entity.SigningKey{}, fmt.Errorf("repository: insert signing key: %w", err)
	}
	return k, nil
}

const activeSQL = `
SELECT ` + selectFields + `
FROM tenant_signing_keys
WHERE tenant_id = $1 AND expired_at IS NULL
`

func (r *repo) Active(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, activeSQL, scope.ID().String())
	k, err := scanSigningKey(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.SigningKey{}, repository.ErrSigningKeyNotFound
		}
		return entity.SigningKey{}, fmt.Errorf("repository: get active signing key: %w", err)
	}
	return k, nil
}

const activePrivateSQL = `
SELECT ` + selectFields + `, private_key
FROM tenant_signing_keys
WHERE tenant_id = $1 AND expired_at IS NULL
`

func (r *repo) ActivePrivate(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, []byte, error) {
	var (
		tenantStr string
		keyID     string
		public    []byte
		createdAt time.Time
		expiredAt sql.NullTime
		private   []byte
	)
	err := r.db.Querier(ctx).QueryRowContext(ctx, activePrivateSQL, scope.ID().String()).
		Scan(&tenantStr, &keyID, &public, &createdAt, &expiredAt, &private)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.SigningKey{}, nil, repository.ErrSigningKeyNotFound
		}
		return entity.SigningKey{}, nil, fmt.Errorf("repository: get active signing key: %w", err)
	}
	k, err := buildSigningKey(tenantStr, keyID, public, createdAt, expiredAt)
	if err != nil {
		return entity.SigningKey{}, nil, err
	}
	return k, private, nil
}

const listSQL = `
SELECT ` + selectFields + `
FROM tenant_signing_keys
WHERE tenant_id = $1
ORDER BY created_at
`

func (r *repo) List(ctx context.Context, scope entity.TenantScope) ([]entity.SigningKey, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, listSQL, scope.ID().String())
	if err != nil {
		return nil, fmt.Errorf("repository: list signing keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.SigningKey
	for rows.Next() {
		k, err := scanSigningKey(rows)
		if err != nil {
			return nil, fmt.Errorf("repository: scan signing key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

const expireSQL = `
UPDATE tenant_signing_keys SET expired_at = $3
WHERE tenant_id = $1 AND key_id = $2 AND expired_at IS NULL
`

func (r *repo) Expire(ctx context.Context, scope entity.TenantScope, keyID string, at time.Time) error {
	res, err := r.db.Querier(ctx).ExecContext(ctx, expireSQL, scope.ID().String(), keyID, at)
	if err != nil {
		return fmt.Errorf("repository: expire signing key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: expire signing key: %w", err)
	}
	if n == 0 {
		return repository.ErrSigningKeyNotFound
	}
	return nil
}

type scannableRow interface {
	Scan(dest ...any) error
}

func scanSigningKey(row scannableRow) (entity.SigningKey, error) {
	var (
		tenantStr string
		keyID     string
		public    []byte
		createdAt time.Time
		expiredAt sql.NullTime
	)
	if err := row.Scan(&tenantStr, &keyID, &public, &createdAt, &expiredAt); err != nil {
		return entity.SigningKey{}, err
	}
	return buildSigningKey(tenantStr, keyID, public, createdAt, expiredAt)
}

func buildSigningKey(tenantStr, keyID string, public []byte, createdAt time.Time, expiredAt sql.NullTime) (entity.SigningKey, error) {
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return entity.SigningKey{}, fmt.Errorf("parse signing key tenant id: %w", err)
	}
	k := entity.SigningKey{
		TenantID:  tenantID,
		KeyID:     keyID,
		PublicKey: ed25519.PublicKey(public),
		CreatedAt: createdAt,
	}
	if expiredAt.Valid {
		at := expiredAt.Time
		k.ExpiredAt = &at
	}
	return k, nil
}
