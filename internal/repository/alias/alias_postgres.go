package alias

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const uniqueViolation = "23505"

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Alias {
	return &repo{db: db}
}

func (r *repo) Create(ctx context.Context, in entity.NewRoomAlias) (entity.RoomAlias, error) {
	row := dbpg.RoomAlias{
		TenantID: in.TenantID.String(),
		Alias:    in.Alias,
		RoomNid:  in.RoomNID,
		Creator:  in.Creator,
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return entity.RoomAlias{}, repository.ErrAliasInUse
		}
		return entity.RoomAlias{}, fmt.Errorf("repository: create alias: %w", err)
	}
	return r.Get(ctx, entity.NewTenantScope(in.TenantID, ""), in.Alias)
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, alias string) (entity.RoomAlias, error) {
	row, err := dbpg.RoomAliases(
		dbpg.RoomAliasWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RoomAliasWhere.Alias.EQ(alias),
		qm.Load(dbpg.RoomAliasRels.RoomNidRoom),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.RoomAlias{}, repository.ErrAliasNotFound
		}
		return entity.RoomAlias{}, fmt.Errorf("repository: get alias: %w", err)
	}
	return toAlias(row)
}

func (r *repo) Delete(ctx context.Context, scope entity.TenantScope, alias string) error {
	n, err := dbpg.RoomAliases(
		dbpg.RoomAliasWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RoomAliasWhere.Alias.EQ(alias),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return fmt.Errorf("repository: delete alias: %w", err)
	}
	if n == 0 {
		return repository.ErrAliasNotFound
	}
	return nil
}

func (r *repo) ListForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64) ([]entity.RoomAlias, error) {
	rows, err := dbpg.RoomAliases(
		dbpg.RoomAliasWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RoomAliasWhere.RoomNid.EQ(roomNID),
		qm.Load(dbpg.RoomAliasRels.RoomNidRoom),
		qm.OrderBy(dbpg.RoomAliasColumns.Alias),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list aliases: %w", err)
	}

	out := make([]entity.RoomAlias, 0, len(rows))
	for _, row := range rows {
		converted, err := toAlias(row)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func toAlias(row *dbpg.RoomAlias) (entity.RoomAlias, error) {
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.RoomAlias{}, fmt.Errorf("parse alias tenant id: %w", err)
	}
	out := entity.RoomAlias{
		TenantID:  tenantID,
		Alias:     row.Alias,
		RoomNID:   row.RoomNid,
		Creator:   row.Creator,
		CreatedAt: row.CreatedAt,
	}
	if row.R != nil && row.R.RoomNidRoom != nil {
		out.RoomID = row.R.RoomNidRoom.RoomID
	}
	return out, nil
}
