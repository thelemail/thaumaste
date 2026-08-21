package device

import (
	"context"
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

func New(db *postgres.Client) repository.Device {
	return &repo{db: db}
}

func (r *repo) Upsert(ctx context.Context, in entity.NewDevice) (entity.Device, error) {
	exec := r.db.Querier(ctx)
	key := []string{
		dbpg.DeviceColumns.TenantID,
		dbpg.DeviceColumns.UserID,
		dbpg.DeviceColumns.DeviceID,
	}
	row := dbpg.Device{
		TenantID:    in.TenantID.String(),
		UserID:      in.UserID,
		DeviceID:    in.DeviceID,
		DisplayName: in.DisplayName,
	}

	update := boil.None()
	if in.DisplayName != "" {
		update = boil.Whitelist(dbpg.DeviceColumns.DisplayName)
	}
	if err := row.Upsert(ctx, exec, in.DisplayName != "", key, update, boil.Infer()); err != nil {
		return entity.Device{}, fmt.Errorf("repository: upsert device: %w", err)
	}
	return r.Get(ctx, entity.NewTenantScope(in.TenantID, ""), in.UserID, in.DeviceID)
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, userID, deviceID string) (entity.Device, error) {
	row, err := dbpg.Devices(
		dbpg.DeviceWhere.TenantID.EQ(scope.ID().String()),
		dbpg.DeviceWhere.UserID.EQ(userID),
		dbpg.DeviceWhere.DeviceID.EQ(deviceID),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Device{}, repository.ErrDeviceNotFound
		}
		return entity.Device{}, fmt.Errorf("repository: get device: %w", err)
	}
	return toDevice(row)
}

func (r *repo) ListForUser(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.Device, error) {
	rows, err := dbpg.Devices(
		dbpg.DeviceWhere.TenantID.EQ(scope.ID().String()),
		dbpg.DeviceWhere.UserID.EQ(userID),
		qm.OrderBy(dbpg.DeviceColumns.CreatedAt+", "+dbpg.DeviceColumns.DeviceID),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list devices: %w", err)
	}
	out := make([]entity.Device, 0, len(rows))
	for _, row := range rows {
		converted, err := toDevice(row)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func (r *repo) Rename(ctx context.Context, scope entity.TenantScope, userID, deviceID, displayName string) error {
	n, err := dbpg.Devices(
		dbpg.DeviceWhere.TenantID.EQ(scope.ID().String()),
		dbpg.DeviceWhere.UserID.EQ(userID),
		dbpg.DeviceWhere.DeviceID.EQ(deviceID),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.DeviceColumns.DisplayName: displayName})
	if err != nil {
		return fmt.Errorf("repository: rename device: %w", err)
	}
	if n == 0 {
		return repository.ErrDeviceNotFound
	}
	return nil
}

func (r *repo) Delete(ctx context.Context, scope entity.TenantScope, userID, deviceID string) error {
	n, err := dbpg.Devices(
		dbpg.DeviceWhere.TenantID.EQ(scope.ID().String()),
		dbpg.DeviceWhere.UserID.EQ(userID),
		dbpg.DeviceWhere.DeviceID.EQ(deviceID),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return fmt.Errorf("repository: delete device: %w", err)
	}
	if n == 0 {
		return repository.ErrDeviceNotFound
	}
	return nil
}

func (r *repo) DeleteAllForUser(ctx context.Context, scope entity.TenantScope, userID string) (int64, error) {
	n, err := dbpg.Devices(
		dbpg.DeviceWhere.TenantID.EQ(scope.ID().String()),
		dbpg.DeviceWhere.UserID.EQ(userID),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return 0, fmt.Errorf("repository: delete devices: %w", err)
	}
	return n, nil
}

func (r *repo) Touch(ctx context.Context, scope entity.TenantScope, userID, deviceID, ip string, at time.Time) error {
	_, err := dbpg.Devices(
		dbpg.DeviceWhere.TenantID.EQ(scope.ID().String()),
		dbpg.DeviceWhere.UserID.EQ(userID),
		dbpg.DeviceWhere.DeviceID.EQ(deviceID),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{
		dbpg.DeviceColumns.LastSeenIP: ip,
		dbpg.DeviceColumns.LastSeenTS: at,
	})
	if err != nil {
		return fmt.Errorf("repository: touch device: %w", err)
	}
	return nil
}

func toDevice(row *dbpg.Device) (entity.Device, error) {
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.Device{}, fmt.Errorf("parse device tenant id: %w", err)
	}
	out := entity.Device{
		TenantID:    tenantID,
		UserID:      row.UserID,
		DeviceID:    row.DeviceID,
		DisplayName: row.DisplayName,
		LastSeenIP:  row.LastSeenIP,
		CreatedAt:   row.CreatedAt,
	}
	if row.LastSeenTS.Valid {
		at := row.LastSeenTS.Time
		out.LastSeenTS = &at
	}
	return out, nil
}
