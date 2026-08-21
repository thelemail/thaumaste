package tenant

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

func New(db *postgres.Client) repository.Tenant {
	return &repo{db: db}
}

func (r *repo) Create(ctx context.Context, in entity.NewTenant) (entity.Tenant, error) {
	row := dbpg.Tenant{
		ServerName:       in.ServerName,
		RegistrationMode: string(in.RegistrationMode),
	}
	err := row.Insert(ctx, r.db.Querier(ctx),
		boil.Whitelist(dbpg.TenantColumns.ServerName, dbpg.TenantColumns.RegistrationMode))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return entity.Tenant{}, repository.ErrTenantAlreadyExists
		}
		return entity.Tenant{}, fmt.Errorf("repository: create tenant: %w", err)
	}
	return toTenant(&row)
}

func (r *repo) GetByID(ctx context.Context, id uuid.UUID) (entity.Tenant, error) {
	return r.one(ctx, dbpg.TenantWhere.ID.EQ(id.String()))
}

func (r *repo) GetByServerName(ctx context.Context, serverName string) (entity.Tenant, error) {
	return r.one(ctx, dbpg.TenantWhere.ServerName.EQ(serverName))
}

func (r *repo) GetByHost(ctx context.Context, host string) (entity.Tenant, error) {
	return r.one(ctx,
		qm.InnerJoin("tenant_hosts th on th.tenant_id = tenants.id"),
		qm.Where("th.host = ?", host),
	)
}

func (r *repo) one(ctx context.Context, mods ...qm.QueryMod) (entity.Tenant, error) {
	row, err := dbpg.Tenants(mods...).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Tenant{}, repository.ErrTenantNotFound
		}
		return entity.Tenant{}, fmt.Errorf("repository: get tenant: %w", err)
	}
	return toTenant(row)
}

func (r *repo) List(ctx context.Context) ([]entity.Tenant, error) {
	rows, err := dbpg.Tenants(qm.OrderBy(dbpg.TenantColumns.ServerName)).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list tenants: %w", err)
	}
	out := make([]entity.Tenant, 0, len(rows))
	for _, row := range rows {
		converted, err := toTenant(row)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func (r *repo) SetState(ctx context.Context, id uuid.UUID, state entity.TenantState) (entity.Tenant, error) {
	exec := r.db.Querier(ctx)
	row, err := dbpg.Tenants(dbpg.TenantWhere.ID.EQ(id.String())).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Tenant{}, repository.ErrTenantNotFound
		}
		return entity.Tenant{}, fmt.Errorf("repository: get tenant: %w", err)
	}
	row.State = string(state)
	_, err = row.Update(ctx, exec, boil.Whitelist(dbpg.TenantColumns.State, dbpg.TenantColumns.UpdatedAt))
	if err != nil {
		return entity.Tenant{}, fmt.Errorf("repository: set tenant state: %w", err)
	}
	return toTenant(row)
}

func (r *repo) SetRegistration(ctx context.Context, id uuid.UUID, mode entity.RegistrationMode) (entity.Tenant, error) {
	exec := r.db.Querier(ctx)
	row, err := dbpg.Tenants(dbpg.TenantWhere.ID.EQ(id.String())).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Tenant{}, repository.ErrTenantNotFound
		}
		return entity.Tenant{}, fmt.Errorf("repository: get tenant: %w", err)
	}
	row.RegistrationMode = string(mode)
	_, err = row.Update(ctx, exec, boil.Whitelist(dbpg.TenantColumns.RegistrationMode, dbpg.TenantColumns.UpdatedAt))
	if err != nil {
		return entity.Tenant{}, fmt.Errorf("repository: set registration mode: %w", err)
	}
	return toTenant(row)
}

func (r *repo) Delete(ctx context.Context, id uuid.UUID) error {
	n, err := dbpg.Tenants(dbpg.TenantWhere.ID.EQ(id.String())).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return fmt.Errorf("repository: delete tenant: %w", err)
	}
	if n == 0 {
		return repository.ErrTenantNotFound
	}
	return nil
}

func (r *repo) AddHost(ctx context.Context, scope entity.TenantScope, host string) error {
	row := dbpg.TenantHost{Host: host, TenantID: scope.ID().String()}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return repository.ErrHostAlreadyClaimed
		}
		return fmt.Errorf("repository: add tenant host: %w", err)
	}
	return nil
}

func (r *repo) ListHosts(ctx context.Context, scope entity.TenantScope) ([]string, error) {
	rows, err := dbpg.TenantHosts(
		dbpg.TenantHostWhere.TenantID.EQ(scope.ID().String()),
		qm.OrderBy(dbpg.TenantHostColumns.Host),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list tenant hosts: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Host)
	}
	return out, nil
}

func toTenant(row *dbpg.Tenant) (entity.Tenant, error) {
	id, err := uuid.Parse(row.ID)
	if err != nil {
		return entity.Tenant{}, fmt.Errorf("parse tenant id: %w", err)
	}
	return entity.Tenant{
		ID:                 id,
		ServerName:         row.ServerName,
		State:              entity.TenantState(row.State),
		RegistrationMode:   entity.RegistrationMode(row.RegistrationMode),
		EncryptionRequired: row.EncryptionRequired,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}
