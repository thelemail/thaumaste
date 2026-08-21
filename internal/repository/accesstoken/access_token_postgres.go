package accesstoken

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/uuid"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
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

func (r *repo) Insert(ctx context.Context, in entity.NewAccessToken) (entity.AccessToken, error) {
	row := dbpg.AccessToken{
		TenantID:  in.TenantID.String(),
		TokenHash: in.TokenHash,
		UserID:    in.UserID,
	}
	if in.ExpiresAt != nil {
		row.ExpiresAt = null.TimeFrom(*in.ExpiresAt)
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		return entity.AccessToken{}, fmt.Errorf("repository: insert access token: %w", err)
	}
	return toAccessToken(&row)
}

func (r *repo) GetByHash(ctx context.Context, tokenHash []byte) (entity.AccessToken, error) {
	row, err := dbpg.AccessTokens(dbpg.AccessTokenWhere.TokenHash.EQ(tokenHash)).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.AccessToken{}, repository.ErrAccessTokenNotFound
		}
		return entity.AccessToken{}, fmt.Errorf("repository: get access token: %w", err)
	}
	return toAccessToken(row)
}

func (r *repo) ListForUser(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.AccessToken, error) {
	rows, err := dbpg.AccessTokens(
		dbpg.AccessTokenWhere.TenantID.EQ(scope.ID().String()),
		dbpg.AccessTokenWhere.UserID.EQ(userID),
		qm.OrderBy(dbpg.AccessTokenColumns.CreatedAt),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list access tokens: %w", err)
	}
	out := make([]entity.AccessToken, 0, len(rows))
	for _, row := range rows {
		converted, err := toAccessToken(row)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func (r *repo) Revoke(ctx context.Context, id uuid.UUID, at time.Time) error {
	n, err := dbpg.AccessTokens(
		dbpg.AccessTokenWhere.ID.EQ(id.String()),
		dbpg.AccessTokenWhere.RevokedAt.IsNull(),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.AccessTokenColumns.RevokedAt: at})
	if err != nil {
		return fmt.Errorf("repository: revoke access token: %w", err)
	}
	if n == 0 {
		return repository.ErrAccessTokenNotFound
	}
	return nil
}

func (r *repo) RevokeForUser(ctx context.Context, scope entity.TenantScope, userID string, at time.Time) (int64, error) {
	n, err := dbpg.AccessTokens(
		dbpg.AccessTokenWhere.TenantID.EQ(scope.ID().String()),
		dbpg.AccessTokenWhere.UserID.EQ(userID),
		dbpg.AccessTokenWhere.RevokedAt.IsNull(),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.AccessTokenColumns.RevokedAt: at})
	if err != nil {
		return 0, fmt.Errorf("repository: revoke access tokens: %w", err)
	}
	return n, nil
}

func toAccessToken(row *dbpg.AccessToken) (entity.AccessToken, error) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		return entity.AccessToken{}, fmt.Errorf("parse access token id: %w", err)
	}
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.AccessToken{}, fmt.Errorf("parse access token tenant id: %w", err)
	}
	token := entity.AccessToken{
		ID:        id,
		TenantID:  tenantID,
		UserID:    row.UserID,
		CreatedAt: row.CreatedAt,
	}
	if row.ExpiresAt.Valid {
		at := row.ExpiresAt.Time
		token.ExpiresAt = &at
	}
	if row.RevokedAt.Valid {
		at := row.RevokedAt.Time
		token.RevokedAt = &at
	}
	return token, nil
}
