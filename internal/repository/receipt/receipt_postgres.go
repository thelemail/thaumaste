package receipt

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const (
	setReceiptSQL = `
		INSERT INTO receipts (tenant_id, room_nid, user_id, receipt_type, thread_id, event_nid, ts, stream_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, room_nid, user_id, receipt_type, thread_id) DO UPDATE
		   SET event_nid = EXCLUDED.event_nid, ts = EXCLUDED.ts, stream_id = EXCLUDED.stream_id`

	selectReceiptSQL = `
		SELECT r.user_id, r.receipt_type, r.thread_id, e.event_id, r.event_nid,
		       e.stream_ordering, r.ts, r.stream_id, room.room_id
		  FROM receipts r
		  JOIN events e ON e.event_nid = r.event_nid
		  JOIN rooms room ON room.room_nid = r.room_nid`

	forUserSQL = selectReceiptSQL + `
		 WHERE r.tenant_id = $1 AND r.room_nid = $2 AND r.user_id = $3`

	forRoomSQL = selectReceiptSQL + `
		 WHERE r.tenant_id = $1 AND r.room_nid = $2
		   AND (r.receipt_type = 'm.read' OR r.user_id = $3)
		 ORDER BY r.stream_id`

	unreadSinceSQL = `
		SELECT count(*) FROM events
		 WHERE room_nid = $1 AND stream_ordering > $2 AND sender <> $3
		   AND event_state_key_nid IS NULL
		   AND disposition IN ('accepted', 'redacted')`
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Receipt {
	return &repo{db: db}
}

func (r *repo) Set(ctx context.Context, in entity.NewReceipt, stream int64) error {
	_, err := r.db.Querier(ctx).ExecContext(ctx, setReceiptSQL,
		in.TenantID.String(), in.RoomNID, in.UserID, in.Type, in.ThreadID, in.EventNID, in.Timestamp, stream)
	if err != nil {
		return fmt.Errorf("repository: set receipt: %w", err)
	}
	return nil
}

func (r *repo) ForUser(ctx context.Context, scope entity.TenantScope, roomNID int64,
	userID string,
) ([]entity.Receipt, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, forUserSQL, scope.ID().String(), roomNID, userID)
	if err != nil {
		return nil, fmt.Errorf("repository: read receipts: %w", err)
	}
	return scanReceipts(rows)
}

func (r *repo) ForRoom(ctx context.Context, scope entity.TenantScope, roomNID int64,
	caller string,
) ([]entity.Receipt, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, forRoomSQL, scope.ID().String(), roomNID, caller)
	if err != nil {
		return nil, fmt.Errorf("repository: read receipts: %w", err)
	}
	return scanReceipts(rows)
}

func (r *repo) Since(ctx context.Context, scope entity.TenantScope, roomNIDs []int64, caller string,
	after int64,
) ([]entity.Receipt, error) {
	if len(roomNIDs) == 0 {
		return nil, nil
	}
	args := []any{scope.ID().String(), caller, after}
	slots := make([]string, 0, len(roomNIDs))
	for _, roomNID := range roomNIDs {
		args = append(args, roomNID)
		slots = append(slots, "$"+strconv.Itoa(len(args)))
	}
	rows, err := r.db.Querier(ctx).QueryContext(ctx, selectReceiptSQL+`
		 WHERE r.tenant_id = $1
		   AND (r.receipt_type = 'm.read' OR r.user_id = $2)
		   AND r.stream_id > $3
		   AND r.room_nid IN (`+strings.Join(slots, ", ")+`)
		 ORDER BY r.stream_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: read receipts: %w", err)
	}
	return scanReceipts(rows)
}

func (r *repo) UnreadSince(ctx context.Context, roomNID, position int64, exclude string) (int, error) {
	var count int
	if err := r.db.Querier(ctx).QueryRowContext(ctx, unreadSinceSQL,
		roomNID, position, exclude).Scan(&count); err != nil {
		return 0, fmt.Errorf("repository: count unread: %w", err)
	}
	return count, nil
}

func scanReceipts(rows *sql.Rows) ([]entity.Receipt, error) {
	defer func() { _ = rows.Close() }()

	var out []entity.Receipt
	for rows.Next() {
		var found entity.Receipt
		if err := rows.Scan(&found.UserID, &found.Type, &found.ThreadID, &found.EventID,
			&found.EventNID, &found.Position, &found.Timestamp, &found.StreamID, &found.RoomID); err != nil {
			return nil, fmt.Errorf("repository: scan receipt: %w", err)
		}
		out = append(out, found)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: read receipts: %w", err)
	}
	return out, nil
}
