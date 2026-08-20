package tenant

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

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

const selectFields = `
id, server_name, state, registration_mode, encryption_required, created_at, updated_at
`

const insertSQL = `
INSERT INTO tenants (server_name, registration_mode, encryption_required)
VALUES ($1, $2, $3)
RETURNING ` + selectFields

func (r *repo) Create(ctx context.Context, in entity.NewTenant) (entity.Tenant, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, insertSQL,
		in.ServerName, string(in.RegistrationMode), in.EncryptionRequired)
	t, err := scanTenant(row)
	if err != nil {
		if isUniqueViolation(err) {
			return entity.Tenant{}, repository.ErrTenantAlreadyExists
		}
		return entity.Tenant{}, fmt.Errorf("repository: create tenant: %w", err)
	}
	return t, nil
}

const getByIDSQL = `SELECT ` + selectFields + ` FROM tenants WHERE id = $1`

func (r *repo) GetByID(ctx context.Context, id uuid.UUID) (entity.Tenant, error) {
	return r.get(ctx, getByIDSQL, id.String())
}

const getByServerNameSQL = `SELECT ` + selectFields + ` FROM tenants WHERE server_name = $1`

func (r *repo) GetByServerName(ctx context.Context, serverName string) (entity.Tenant, error) {
	return r.get(ctx, getByServerNameSQL, serverName)
}

const getByHostSQL = `
SELECT ` + selectFields + `
FROM tenants
JOIN tenant_hosts ON tenant_hosts.tenant_id = tenants.id
WHERE tenant_hosts.host = $1
`

func (r *repo) GetByHost(ctx context.Context, host string) (entity.Tenant, error) {
	return r.get(ctx, getByHostSQL, host)
}

func (r *repo) get(ctx context.Context, query string, arg any) (entity.Tenant, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, query, arg)
	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Tenant{}, repository.ErrTenantNotFound
		}
		return entity.Tenant{}, fmt.Errorf("repository: get tenant: %w", err)
	}
	return t, nil
}

const listSQL = `SELECT ` + selectFields + ` FROM tenants ORDER BY server_name`

func (r *repo) List(ctx context.Context) ([]entity.Tenant, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, listSQL)
	if err != nil {
		return nil, fmt.Errorf("repository: list tenants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, fmt.Errorf("repository: scan tenant: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const setStateSQL = `
UPDATE tenants SET state = $2, updated_at = now() WHERE id = $1
RETURNING ` + selectFields

func (r *repo) SetState(ctx context.Context, id uuid.UUID, state entity.TenantState) (entity.Tenant, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, setStateSQL, id.String(), string(state))
	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Tenant{}, repository.ErrTenantNotFound
		}
		return entity.Tenant{}, fmt.Errorf("repository: set tenant state: %w", err)
	}
	return t, nil
}

const deleteSQL = `DELETE FROM tenants WHERE id = $1`

func (r *repo) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.Querier(ctx).ExecContext(ctx, deleteSQL, id.String())
	if err != nil {
		return fmt.Errorf("repository: delete tenant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: delete tenant: %w", err)
	}
	if n == 0 {
		return repository.ErrTenantNotFound
	}
	return nil
}

const addHostSQL = `INSERT INTO tenant_hosts (host, tenant_id) VALUES ($1, $2)`

func (r *repo) AddHost(ctx context.Context, scope entity.TenantScope, host string) error {
	_, err := r.db.Querier(ctx).ExecContext(ctx, addHostSQL, host, scope.ID().String())
	if err != nil {
		if isUniqueViolation(err) {
			return repository.ErrHostAlreadyClaimed
		}
		return fmt.Errorf("repository: add tenant host: %w", err)
	}
	return nil
}

const listHostsSQL = `SELECT host FROM tenant_hosts WHERE tenant_id = $1 ORDER BY host`

func (r *repo) ListHosts(ctx context.Context, scope entity.TenantScope) ([]string, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, listHostsSQL, scope.ID().String())
	if err != nil {
		return nil, fmt.Errorf("repository: list tenant hosts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, fmt.Errorf("repository: scan tenant host: %w", err)
		}
		out = append(out, host)
	}
	return out, rows.Err()
}

type scannableRow interface {
	Scan(dest ...any) error
}

func scanTenant(row scannableRow) (entity.Tenant, error) {
	var (
		idStr     string
		name      string
		state     string
		mode      string
		encrypted bool
		createdAt time.Time
		updatedAt time.Time
	)
	if err := row.Scan(&idStr, &name, &state, &mode, &encrypted, &createdAt, &updatedAt); err != nil {
		return entity.Tenant{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return entity.Tenant{}, fmt.Errorf("parse tenant id: %w", err)
	}
	return entity.Tenant{
		ID:                 id,
		ServerName:         name,
		State:              entity.TenantState(state),
		RegistrationMode:   entity.RegistrationMode(mode),
		EncryptionRequired: encrypted,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	}, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
