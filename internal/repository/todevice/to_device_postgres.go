package todevice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const (
	sinceSQL = `
		SELECT sender, event_type, content, stream_id
		  FROM to_device_messages
		 WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3 AND stream_id > $4
		 ORDER BY stream_id
		 LIMIT $5`

	deleteUpToSQL = `
		DELETE FROM to_device_messages
		 WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3 AND stream_id <= $4`

	recordedSQL = `
		SELECT 1 FROM to_device_transactions
		 WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3 AND txn_id = $4`

	recordSQL = `
		INSERT INTO to_device_transactions (tenant_id, user_id, device_id, txn_id)
		VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`

	sweepSQL = `DELETE FROM to_device_messages WHERE created_at < $1`
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.ToDevice {
	return &repo{db: db}
}

func (r *repo) Add(ctx context.Context, messages []entity.NewToDeviceMessage, positions []int64) error {
	if len(messages) == 0 {
		return nil
	}
	args := make([]any, 0, len(messages)*7)
	slots := make([]string, 0, len(messages))
	for i, message := range messages {
		args = append(args, message.TenantID.String(), message.UserID, message.DeviceID,
			positions[i], message.Sender, message.Type, message.Content)
		base := len(args) - 7
		slots = append(slots, fmt.Sprintf("($%d::uuid, $%d, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7))
	}
	_, err := r.db.Querier(ctx).ExecContext(ctx, `
		INSERT INTO to_device_messages
			(tenant_id, user_id, device_id, stream_id, sender, event_type, content)
		VALUES `+strings.Join(slots, ", "), args...)
	if err != nil {
		return fmt.Errorf("repository: queue to-device messages: %w", err)
	}
	return nil
}

func (r *repo) Since(ctx context.Context, scope entity.TenantScope, userID, deviceID string,
	after int64, limit int,
) ([]entity.ToDeviceMessage, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, sinceSQL,
		scope.ID().String(), userID, deviceID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("repository: read to-device messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.ToDeviceMessage
	for rows.Next() {
		var message entity.ToDeviceMessage
		var content []byte
		if err := rows.Scan(&message.Sender, &message.Type, &content, &message.StreamID); err != nil {
			return nil, fmt.Errorf("repository: scan to-device message: %w", err)
		}
		message.Content = content
		out = append(out, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: read to-device messages: %w", err)
	}
	return out, nil
}

func (r *repo) DeleteUpTo(ctx context.Context, scope entity.TenantScope, userID, deviceID string,
	upTo int64,
) error {
	if upTo <= 0 {
		return nil
	}
	if _, err := r.db.Querier(ctx).ExecContext(ctx, deleteUpToSQL,
		scope.ID().String(), userID, deviceID, upTo); err != nil {
		return fmt.Errorf("repository: acknowledge to-device messages: %w", err)
	}
	return nil
}

func (r *repo) Recorded(ctx context.Context, scope entity.TenantScope, userID, deviceID,
	txnID string,
) (bool, error) {
	var found int
	err := r.db.Querier(ctx).QueryRowContext(ctx, recordedSQL,
		scope.ID().String(), userID, deviceID, txnID).Scan(&found)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("repository: read to-device transaction: %w", err)
	}
}

func (r *repo) Record(ctx context.Context, scope entity.TenantScope, userID, deviceID, txnID string) error {
	if _, err := r.db.Querier(ctx).ExecContext(ctx, recordSQL,
		scope.ID().String(), userID, deviceID, txnID); err != nil {
		return fmt.Errorf("repository: record to-device transaction: %w", err)
	}
	return nil
}

func (r *repo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.Querier(ctx).ExecContext(ctx, sweepSQL, cutoff)
	if err != nil {
		return 0, fmt.Errorf("repository: sweep to-device messages: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repository: sweep to-device messages: %w", err)
	}
	return affected, nil
}
