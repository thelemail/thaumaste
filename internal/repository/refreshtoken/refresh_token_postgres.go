package refreshtoken

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/google/uuid"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.RefreshToken {
	return &repo{db: db}
}

func (r *repo) Insert(ctx context.Context, in entity.NewRefreshToken) (entity.RefreshToken, error) {
	row := dbpg.RefreshToken{
		TenantID:  in.TenantID.String(),
		UserID:    in.UserID,
		DeviceID:  in.DeviceID,
		TokenHash: in.TokenHash,
	}
	if in.ExpiresAt != nil {
		row.ExpiresAt = null.TimeFrom(*in.ExpiresAt)
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		return entity.RefreshToken{}, fmt.Errorf("repository: insert refresh token: %w", err)
	}
	return toRefreshToken(&row)
}

func (r *repo) GetByHash(ctx context.Context, tokenHash []byte) (entity.RefreshToken, error) {
	row, err := dbpg.RefreshTokens(dbpg.RefreshTokenWhere.TokenHash.EQ(tokenHash)).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.RefreshToken{}, repository.ErrRefreshTokenNotFound
		}
		return entity.RefreshToken{}, fmt.Errorf("repository: get refresh token: %w", err)
	}
	return toRefreshToken(row)
}

func (r *repo) MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	n, err := dbpg.RefreshTokens(
		dbpg.RefreshTokenWhere.ID.EQ(id.String()),
		dbpg.RefreshTokenWhere.UsedAt.IsNull(),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.RefreshTokenColumns.UsedAt: at})
	if err != nil {
		return fmt.Errorf("repository: mark refresh token used: %w", err)
	}
	if n == 0 {
		return repository.ErrRefreshTokenNotFound
	}
	return nil
}

func (r *repo) RevokeForDevice(ctx context.Context, scope entity.TenantScope, userID, deviceID string, at time.Time) (int64, error) {
	n, err := dbpg.RefreshTokens(
		dbpg.RefreshTokenWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RefreshTokenWhere.UserID.EQ(userID),
		dbpg.RefreshTokenWhere.DeviceID.EQ(deviceID),
		dbpg.RefreshTokenWhere.RevokedAt.IsNull(),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.RefreshTokenColumns.RevokedAt: at})
	if err != nil {
		return 0, fmt.Errorf("repository: revoke refresh tokens: %w", err)
	}
	return n, nil
}

func (r *repo) RevokeForUser(ctx context.Context, scope entity.TenantScope, userID string, at time.Time) (int64, error) {
	n, err := dbpg.RefreshTokens(
		dbpg.RefreshTokenWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RefreshTokenWhere.UserID.EQ(userID),
		dbpg.RefreshTokenWhere.RevokedAt.IsNull(),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.RefreshTokenColumns.RevokedAt: at})
	if err != nil {
		return 0, fmt.Errorf("repository: revoke refresh tokens: %w", err)
	}
	return n, nil
}

func toRefreshToken(row *dbpg.RefreshToken) (entity.RefreshToken, error) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		return entity.RefreshToken{}, fmt.Errorf("parse refresh token id: %w", err)
	}
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.RefreshToken{}, fmt.Errorf("parse refresh token tenant id: %w", err)
	}
	out := entity.RefreshToken{
		ID:        id,
		TenantID:  tenantID,
		UserID:    row.UserID,
		DeviceID:  row.DeviceID,
		CreatedAt: row.CreatedAt,
	}
	for _, pair := range []struct {
		src null.Time
		dst **time.Time
	}{
		{row.ExpiresAt, &out.ExpiresAt},
		{row.RevokedAt, &out.RevokedAt},
		{row.UsedAt, &out.UsedAt},
	} {
		if pair.src.Valid {
			at := pair.src.Time
			*pair.dst = &at
		}
	}
	return out, nil
}
