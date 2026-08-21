package authattempt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.AuthAttempt {
	return &repo{db: db}
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) (entity.AuthAttempt, error) {
	row, err := dbpg.AuthAttempts(
		dbpg.AuthAttemptWhere.TenantID.EQ(scope.ID().String()),
		dbpg.AuthAttemptWhere.Subject.EQ(subject),
		dbpg.AuthAttemptWhere.Kind.EQ(string(kind)),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.AuthAttempt{TenantID: scope.ID(), Subject: subject, Kind: kind}, nil
		}
		return entity.AuthAttempt{}, fmt.Errorf("repository: get auth attempt: %w", err)
	}
	out := entity.AuthAttempt{
		TenantID:    scope.ID(),
		Subject:     row.Subject,
		Kind:        entity.AttemptKind(row.Kind),
		Failures:    row.Failures,
		WindowStart: row.WindowStartedAt,
	}
	if row.LockedUntil.Valid {
		at := row.LockedUntil.Time
		out.LockedUntil = &at
	}
	return out, nil
}

func (r *repo) Save(ctx context.Context, in entity.AuthAttempt) error {
	row := dbpg.AuthAttempt{
		TenantID:        in.TenantID.String(),
		Subject:         in.Subject,
		Kind:            string(in.Kind),
		Failures:        in.Failures,
		WindowStartedAt: in.WindowStart,
	}
	if in.LockedUntil != nil {
		row.LockedUntil = null.TimeFrom(*in.LockedUntil)
	}
	key := []string{
		dbpg.AuthAttemptColumns.TenantID,
		dbpg.AuthAttemptColumns.Subject,
		dbpg.AuthAttemptColumns.Kind,
	}
	update := boil.Whitelist(
		dbpg.AuthAttemptColumns.Failures,
		dbpg.AuthAttemptColumns.WindowStartedAt,
		dbpg.AuthAttemptColumns.LockedUntil,
	)
	if err := row.Upsert(ctx, r.db.Querier(ctx), true, key, update, boil.Infer()); err != nil {
		return fmt.Errorf("repository: save auth attempt: %w", err)
	}
	return nil
}

func (r *repo) Clear(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error {
	_, err := dbpg.AuthAttempts(
		dbpg.AuthAttemptWhere.TenantID.EQ(scope.ID().String()),
		dbpg.AuthAttemptWhere.Subject.EQ(subject),
		dbpg.AuthAttemptWhere.Kind.EQ(string(kind)),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return fmt.Errorf("repository: clear auth attempt: %w", err)
	}
	return nil
}
