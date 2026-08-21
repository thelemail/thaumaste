package signingkey

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"time"

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

func New(db *postgres.Client) repository.SigningKey {
	return &repo{db: db}
}

func (r *repo) Insert(ctx context.Context, in entity.NewSigningKey) (entity.SigningKey, error) {
	row := dbpg.TenantSigningKey{
		TenantID:   in.TenantID.String(),
		KeyID:      in.KeyID,
		PublicKey:  in.PublicKey,
		PrivateKey: in.PrivateKey,
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		return entity.SigningKey{}, fmt.Errorf("repository: insert signing key: %w", err)
	}
	return toSigningKey(&row)
}

func (r *repo) Active(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, error) {
	row, err := r.activeRow(ctx, scope)
	if err != nil {
		return entity.SigningKey{}, err
	}
	return toSigningKey(row)
}

func (r *repo) ActivePrivate(ctx context.Context, scope entity.TenantScope) (entity.SigningKey, []byte, error) {
	row, err := r.activeRow(ctx, scope)
	if err != nil {
		return entity.SigningKey{}, nil, err
	}
	key, err := toSigningKey(row)
	if err != nil {
		return entity.SigningKey{}, nil, err
	}
	return key, row.PrivateKey, nil
}

func (r *repo) activeRow(ctx context.Context, scope entity.TenantScope) (*dbpg.TenantSigningKey, error) {
	row, err := dbpg.TenantSigningKeys(
		dbpg.TenantSigningKeyWhere.TenantID.EQ(scope.ID().String()),
		dbpg.TenantSigningKeyWhere.ExpiredAt.IsNull(),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repository.ErrSigningKeyNotFound
		}
		return nil, fmt.Errorf("repository: get active signing key: %w", err)
	}
	return row, nil
}

func (r *repo) List(ctx context.Context, scope entity.TenantScope) ([]entity.SigningKey, error) {
	rows, err := dbpg.TenantSigningKeys(
		dbpg.TenantSigningKeyWhere.TenantID.EQ(scope.ID().String()),
		qm.OrderBy(dbpg.TenantSigningKeyColumns.CreatedAt),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list signing keys: %w", err)
	}
	out := make([]entity.SigningKey, 0, len(rows))
	for _, row := range rows {
		converted, err := toSigningKey(row)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func (r *repo) Expire(ctx context.Context, scope entity.TenantScope, keyID string, at time.Time) error {
	n, err := dbpg.TenantSigningKeys(
		dbpg.TenantSigningKeyWhere.TenantID.EQ(scope.ID().String()),
		dbpg.TenantSigningKeyWhere.KeyID.EQ(keyID),
		dbpg.TenantSigningKeyWhere.ExpiredAt.IsNull(),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.TenantSigningKeyColumns.ExpiredAt: at})
	if err != nil {
		return fmt.Errorf("repository: expire signing key: %w", err)
	}
	if n == 0 {
		return repository.ErrSigningKeyNotFound
	}
	return nil
}

func (r *repo) AllSealed(ctx context.Context) ([]entity.SealedSigningKey, error) {
	rows, err := dbpg.TenantSigningKeys(
		qm.OrderBy(dbpg.TenantSigningKeyColumns.TenantID+", "+dbpg.TenantSigningKeyColumns.KeyID),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list sealed signing keys: %w", err)
	}
	out := make([]entity.SealedSigningKey, 0, len(rows))
	for _, row := range rows {
		tenantID, err := uuid.Parse(row.TenantID)
		if err != nil {
			return nil, fmt.Errorf("parse signing key tenant id: %w", err)
		}
		out = append(out, entity.SealedSigningKey{
			TenantID:   tenantID,
			KeyID:      row.KeyID,
			PrivateKey: row.PrivateKey,
		})
	}
	return out, nil
}

func (r *repo) Reseal(ctx context.Context, key entity.SealedSigningKey) error {
	n, err := dbpg.TenantSigningKeys(
		dbpg.TenantSigningKeyWhere.TenantID.EQ(key.TenantID.String()),
		dbpg.TenantSigningKeyWhere.KeyID.EQ(key.KeyID),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.TenantSigningKeyColumns.PrivateKey: key.PrivateKey})
	if err != nil {
		return fmt.Errorf("repository: reseal signing key: %w", err)
	}
	if n == 0 {
		return repository.ErrSigningKeyNotFound
	}
	return nil
}

func toSigningKey(row *dbpg.TenantSigningKey) (entity.SigningKey, error) {
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.SigningKey{}, fmt.Errorf("parse signing key tenant id: %w", err)
	}
	key := entity.SigningKey{
		TenantID:  tenantID,
		KeyID:     row.KeyID,
		PublicKey: ed25519.PublicKey(row.PublicKey),
		CreatedAt: row.CreatedAt,
	}
	if row.ExpiredAt.Valid {
		at := row.ExpiredAt.Time
		key.ExpiredAt = &at
	}
	return key, nil
}
