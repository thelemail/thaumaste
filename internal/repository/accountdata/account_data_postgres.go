package accountdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const (
	lockAccountDataSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`

	setGlobalSQL = `
		INSERT INTO account_data (tenant_id, user_id, type, content, stream_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, user_id, type) DO UPDATE
		   SET content = EXCLUDED.content, stream_id = EXCLUDED.stream_id, updated_at = now()`

	setRoomSQL = `
		INSERT INTO room_account_data (tenant_id, user_id, room_nid, type, content, stream_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, user_id, room_nid, type) DO UPDATE
		   SET content = EXCLUDED.content, stream_id = EXCLUDED.stream_id, updated_at = now()`

	getGlobalSQL = `
		SELECT content, stream_id FROM account_data
		 WHERE tenant_id = $1 AND user_id = $2 AND type = $3`

	getRoomSQL = `
		SELECT d.content, d.stream_id FROM room_account_data d
		 WHERE d.tenant_id = $1 AND d.user_id = $2 AND d.room_nid = $3 AND d.type = $4`
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.AccountData {
	return &repo{db: db}
}

func (r *repo) Lock(ctx context.Context, scope entity.TenantScope, userID string) error {
	name := scope.ID().String() + "\x1e" + userID
	if _, err := r.db.Querier(ctx).ExecContext(ctx, lockAccountDataSQL, name); err != nil {
		return fmt.Errorf("repository: lock account data: %w", err)
	}
	return nil
}

func (r *repo) Set(ctx context.Context, in entity.NewAccountData, stream int64) error {
	var err error
	if in.RoomNID == 0 {
		_, err = r.db.Querier(ctx).ExecContext(ctx, setGlobalSQL,
			in.TenantID.String(), in.UserID, in.Type, in.Content, stream)
	} else {
		_, err = r.db.Querier(ctx).ExecContext(ctx, setRoomSQL,
			in.TenantID.String(), in.UserID, in.RoomNID, in.Type, in.Content, stream)
	}
	if err != nil {
		return fmt.Errorf("repository: set account data: %w", err)
	}
	return nil
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, userID string, roomNID int64,
	dataType string,
) (entity.AccountData, error) {
	var (
		content []byte
		stream  int64
		err     error
	)
	if roomNID == 0 {
		err = r.db.Querier(ctx).QueryRowContext(ctx, getGlobalSQL,
			scope.ID().String(), userID, dataType).Scan(&content, &stream)
	} else {
		err = r.db.Querier(ctx).QueryRowContext(ctx, getRoomSQL,
			scope.ID().String(), userID, roomNID, dataType).Scan(&content, &stream)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.AccountData{}, repository.ErrAccountDataNotFound
		}
		return entity.AccountData{}, fmt.Errorf("repository: get account data: %w", err)
	}
	return entity.AccountData{Type: dataType, Content: content, StreamID: stream}, nil
}

const sinceAccountDataSQL = `
	SELECT '' AS room_id, type, content, stream_id
	  FROM account_data
	 WHERE tenant_id = $1 AND user_id = $2 AND stream_id > $3
	UNION ALL
	SELECT r.room_id, d.type, d.content, d.stream_id
	  FROM room_account_data d
	  JOIN rooms r ON r.room_nid = d.room_nid
	 WHERE d.tenant_id = $1 AND d.user_id = $2 AND d.stream_id > $3
	 ORDER BY stream_id`

func (r *repo) Since(ctx context.Context, scope entity.TenantScope, userID string,
	after int64,
) ([]entity.AccountData, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, sinceAccountDataSQL,
		scope.ID().String(), userID, after)
	if err != nil {
		return nil, fmt.Errorf("repository: read account data: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.AccountData
	for rows.Next() {
		var found entity.AccountData
		var content []byte
		if err := rows.Scan(&found.RoomID, &found.Type, &content, &found.StreamID); err != nil {
			return nil, fmt.Errorf("repository: scan account data: %w", err)
		}
		found.Content = content
		out = append(out, found)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: read account data: %w", err)
	}
	return out, nil
}
