package accesstoken

import (
	"context"
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

func New(db *postgres.Client) repository.AccessToken {
	return &repo{db: db}
}

const selectFields = `id, tenant_id, user_id, created_at, expires_at, revoked_at`

const insertSQL = `
INSERT INTO access_tokens (tenant_id, token_hash, user_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING ` + selectFields

func (r *repo) Insert(ctx context.Context, in entity.NewAccessToken) (entity.AccessToken, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, insertSQL,
		in.TenantID.String(), in.TokenHash, in.UserID, nullTime(in.ExpiresAt))
	t, err := scanAccessToken(row)
	if err != nil {
		return entity.AccessToken{}, fmt.Errorf("repository: insert access token: %w", err)
	}
	return t, nil
}

const getByHashSQL = `SELECT ` + selectFields + ` FROM access_tokens WHERE token_hash = $1`

func (r *repo) GetByHash(ctx context.Context, tokenHash []byte) (entity.AccessToken, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, getByHashSQL, tokenHash)
	t, err := scanAccessToken(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.AccessToken{}, repository.ErrAccessTokenNotFound
		}
		return entity.AccessToken{}, fmt.Errorf("repository: get access token: %w", err)
	}
	return t, nil
}

const listForUserSQL = `
SELECT ` + selectFields + `
FROM access_tokens
WHERE tenant_id = $1 AND user_id = $2
ORDER BY created_at
`

func (r *repo) ListForUser(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.AccessToken, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, listForUserSQL, scope.ID().String(), userID)
	if err != nil {
		return nil, fmt.Errorf("repository: list access tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.AccessToken
	for rows.Next() {
		t, err := scanAccessToken(rows)
		if err != nil {
			return nil, fmt.Errorf("repository: scan access token: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const revokeSQL = `UPDATE access_tokens SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`

func (r *repo) Revoke(ctx context.Context, id uuid.UUID, at time.Time) error {
	res, err := r.db.Querier(ctx).ExecContext(ctx, revokeSQL, id.String(), at)
	if err != nil {
		return fmt.Errorf("repository: revoke access token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: revoke access token: %w", err)
	}
	if n == 0 {
		return repository.ErrAccessTokenNotFound
	}
	return nil
}

const revokeForUserSQL = `
UPDATE access_tokens SET revoked_at = $3
WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL
`

func (r *repo) RevokeForUser(ctx context.Context, scope entity.TenantScope, userID string, at time.Time) (int64, error) {
	res, err := r.db.Querier(ctx).ExecContext(ctx, revokeForUserSQL, scope.ID().String(), userID, at)
	if err != nil {
		return 0, fmt.Errorf("repository: revoke access tokens: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repository: revoke access tokens: %w", err)
	}
	return n, nil
}

type scannableRow interface {
	Scan(dest ...any) error
}

func scanAccessToken(row scannableRow) (entity.AccessToken, error) {
	var (
		idStr     string
		tenantStr string
		userID    string
		createdAt time.Time
		expiresAt sql.NullTime
		revokedAt sql.NullTime
	)
	if err := row.Scan(&idStr, &tenantStr, &userID, &createdAt, &expiresAt, &revokedAt); err != nil {
		return entity.AccessToken{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return entity.AccessToken{}, fmt.Errorf("parse access token id: %w", err)
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return entity.AccessToken{}, fmt.Errorf("parse access token tenant id: %w", err)
	}
	t := entity.AccessToken{ID: id, TenantID: tenantID, UserID: userID, CreatedAt: createdAt}
	if expiresAt.Valid {
		at := expiresAt.Time
		t.ExpiresAt = &at
	}
	if revokedAt.Valid {
		at := revokedAt.Time
		t.RevokedAt = &at
	}
	return t, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
