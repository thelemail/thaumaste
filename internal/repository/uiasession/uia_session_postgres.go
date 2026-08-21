package uiasession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/google/uuid"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const defaultTTL = 15 * time.Minute

type repo struct {
	db    *postgres.Client
	clock func() time.Time
}

func New(db *postgres.Client, clock func() time.Time) repository.UIASession {
	if clock == nil {
		clock = time.Now
	}
	return &repo{db: db, clock: clock}
}

func (r *repo) Create(ctx context.Context, in entity.NewUIASession) (entity.UIASession, error) {
	ttl := in.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	row := dbpg.UiaSession{
		TenantID:  in.TenantID.String(),
		Kind:      string(in.Kind),
		UserID:    in.UserID,
		Completed: types.StringArray{},
		ExpiresAt: r.clock().UTC().Add(ttl),
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		return entity.UIASession{}, fmt.Errorf("repository: create auth session: %w", err)
	}
	return toSession(&row)
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, id uuid.UUID) (entity.UIASession, error) {
	row, err := dbpg.UiaSessions(
		dbpg.UiaSessionWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UiaSessionWhere.ID.EQ(id.String()),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.UIASession{}, repository.ErrUIASessionNotFound
		}
		return entity.UIASession{}, fmt.Errorf("repository: get auth session: %w", err)
	}
	return toSession(row)
}

func (r *repo) Complete(ctx context.Context, scope entity.TenantScope, id uuid.UUID, stage string) (entity.UIASession, error) {
	exec := r.db.Querier(ctx)
	row, err := dbpg.UiaSessions(
		dbpg.UiaSessionWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UiaSessionWhere.ID.EQ(id.String()),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.UIASession{}, repository.ErrUIASessionNotFound
		}
		return entity.UIASession{}, fmt.Errorf("repository: get auth session: %w", err)
	}
	if !slices.Contains(row.Completed, stage) {
		row.Completed = append(row.Completed, stage)
		if _, err := row.Update(ctx, exec, boil.Whitelist(dbpg.UiaSessionColumns.Completed)); err != nil {
			return entity.UIASession{}, fmt.Errorf("repository: complete auth stage: %w", err)
		}
	}
	return toSession(row)
}

func (r *repo) Delete(ctx context.Context, scope entity.TenantScope, id uuid.UUID) error {
	_, err := dbpg.UiaSessions(
		dbpg.UiaSessionWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UiaSessionWhere.ID.EQ(id.String()),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return fmt.Errorf("repository: delete auth session: %w", err)
	}
	return nil
}

func toSession(row *dbpg.UiaSession) (entity.UIASession, error) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		return entity.UIASession{}, fmt.Errorf("parse auth session id: %w", err)
	}
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.UIASession{}, fmt.Errorf("parse auth session tenant id: %w", err)
	}
	return entity.UIASession{
		ID:        id,
		TenantID:  tenantID,
		Kind:      entity.UIAKind(row.Kind),
		UserID:    row.UserID,
		Completed: row.Completed,
		CreatedAt: row.CreatedAt,
		ExpiresAt: row.ExpiresAt,
	}, nil
}
