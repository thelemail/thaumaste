package devicelist

import (
	"context"
	"fmt"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const (
	recordSQL = `
		INSERT INTO device_list_changes (tenant_id, user_id, stream_id)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`

	changedSinceSQL = `
		SELECT DISTINCT user_id FROM device_list_changes
		 WHERE tenant_id = $1 AND stream_id > $2 AND stream_id <= $3
		 ORDER BY user_id`
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.DeviceList {
	return &repo{db: db}
}

func (r *repo) Record(ctx context.Context, in entity.NewDeviceListChange, stream int64) error {
	if _, err := r.db.Querier(ctx).ExecContext(ctx, recordSQL,
		in.TenantID.String(), in.UserID, stream); err != nil {
		return fmt.Errorf("repository: record device list change: %w", err)
	}
	return nil
}

func (r *repo) ChangedSince(ctx context.Context, scope entity.TenantScope, after, upTo int64) ([]string, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, changedSinceSQL, scope.ID().String(), after, upTo)
	if err != nil {
		return nil, fmt.Errorf("repository: read device list changes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("repository: scan device list change: %w", err)
		}
		out = append(out, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: read device list changes: %w", err)
	}
	return out, nil
}
