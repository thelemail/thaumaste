package connection

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const openConnectionSQL = `
	INSERT INTO sync_connections (tenant_id, user_id, device_id, conn_id)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (tenant_id, user_id, device_id, conn_id)
	DO UPDATE SET last_seen_at = now()
	RETURNING connection_nid, tenant_id, user_id, device_id, conn_id,
	          confirmed, confirmed_stream, pending, pending_stream, last_seen_at`

const getConnectionSQL = `
	SELECT connection_nid, tenant_id, user_id, device_id, conn_id,
	       confirmed, confirmed_stream, pending, pending_stream, last_seen_at
	  FROM sync_connections WHERE connection_nid = $1`

const acknowledgeDeleteSQL = `
	DELETE FROM sync_connection_rooms confirmed
	 USING sync_connection_rooms staged
	 WHERE confirmed.connection_nid = $1
	   AND staged.connection_nid = $1
	   AND staged.room_nid = confirmed.room_nid
	   AND staged.pending
	   AND NOT confirmed.pending`

const acknowledgePromoteSQL = `
	UPDATE sync_connection_rooms SET pending = false
	 WHERE connection_nid = $1 AND pending`

const acknowledgeConnectionSQL = `
	UPDATE sync_connections
	   SET confirmed = $2, confirmed_stream = $3, pending = NULL, pending_stream = NULL,
	       last_seen_at = now()
	 WHERE connection_nid = $1 AND pending = $2`

const discardSQL = `
	UPDATE sync_connections
	   SET pending = NULL, pending_stream = NULL, last_seen_at = now()
	 WHERE connection_nid = $1`

const stageConnectionSQL = `
	UPDATE sync_connections
	   SET pending = $2, pending_stream = $3, last_seen_at = now()
	 WHERE connection_nid = $1 AND pending IS NULL AND confirmed < $2`

const selectConfigSQL = `SELECT config_nid FROM sync_state_configs WHERE config_hash = $1`

const insertConfigSQL = `
	INSERT INTO sync_state_configs (config_hash, config) VALUES ($1, $2)
	ON CONFLICT (config_hash) DO NOTHING
	RETURNING config_nid`

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Connection {
	return &repo{db: db}
}

func (r *repo) Open(ctx context.Context, in entity.NewConnection) (entity.Connection, error) {
	if err := in.Validate(); err != nil {
		return entity.Connection{}, err
	}
	row := r.db.Querier(ctx).QueryRowContext(ctx, openConnectionSQL,
		in.TenantID.String(), in.UserID, in.DeviceID, in.ConnID)
	connection, err := scanConnection(row)
	if err != nil {
		return entity.Connection{}, fmt.Errorf("repository: open sync connection: %w", err)
	}
	return connection, nil
}

func (r *repo) Get(ctx context.Context, connectionNID int64) (entity.Connection, error) {
	row := r.db.Querier(ctx).QueryRowContext(ctx, getConnectionSQL, connectionNID)
	connection, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return entity.Connection{}, repository.ErrConnectionNotFound
	}
	if err != nil {
		return entity.Connection{}, fmt.Errorf("repository: get sync connection: %w", err)
	}
	return connection, nil
}

func (r *repo) Rooms(ctx context.Context, connectionNID int64, pending bool) ([]entity.RoomStatus, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, `
		SELECT room_nid, sent_to, timeline_limit, c.config
		  FROM sync_connection_rooms
		  JOIN sync_state_configs c USING (config_nid)
		 WHERE connection_nid = $1 AND pending = $2`, connectionNID, pending)
	if err != nil {
		return nil, fmt.Errorf("repository: list connection rooms: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.RoomStatus
	for rows.Next() {
		var status entity.RoomStatus
		if err := rows.Scan(&status.RoomNID, &status.SentTo, &status.TimelineLimit, &status.RequiredState); err != nil {
			return nil, fmt.Errorf("repository: scan connection room: %w", err)
		}
		out = append(out, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: list connection rooms: %w", err)
	}
	return out, nil
}

func (r *repo) Acknowledge(ctx context.Context, connectionNID, generation, stream int64) error {
	exec := r.db.Querier(ctx)
	if _, err := exec.ExecContext(ctx, acknowledgeDeleteSQL, connectionNID); err != nil {
		return fmt.Errorf("repository: acknowledge sync position: %w", err)
	}
	if _, err := exec.ExecContext(ctx, acknowledgePromoteSQL, connectionNID); err != nil {
		return fmt.Errorf("repository: acknowledge sync position: %w", err)
	}
	result, err := exec.ExecContext(ctx, acknowledgeConnectionSQL, connectionNID, generation, stream)
	if err != nil {
		return fmt.Errorf("repository: acknowledge sync position: %w", err)
	}
	return expectOne(result, "acknowledge sync position")
}

func (r *repo) Discard(ctx context.Context, connectionNID int64) error {
	exec := r.db.Querier(ctx)
	if _, err := exec.ExecContext(ctx,
		`DELETE FROM sync_connection_rooms WHERE connection_nid = $1 AND pending`, connectionNID); err != nil {
		return fmt.Errorf("repository: discard staged sync response: %w", err)
	}
	if _, err := exec.ExecContext(ctx, discardSQL, connectionNID); err != nil {
		return fmt.Errorf("repository: discard staged sync response: %w", err)
	}
	return nil
}

func (r *repo) Reset(ctx context.Context, connectionNID int64) error {
	exec := r.db.Querier(ctx)
	if _, err := exec.ExecContext(ctx,
		`DELETE FROM sync_connection_rooms WHERE connection_nid = $1`, connectionNID); err != nil {
		return fmt.Errorf("repository: reset sync connection: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `
		UPDATE sync_connections
		   SET confirmed = 0, confirmed_stream = 0, pending = NULL, pending_stream = NULL,
		       last_seen_at = now()
		 WHERE connection_nid = $1`, connectionNID); err != nil {
		return fmt.Errorf("repository: reset sync connection: %w", err)
	}
	return nil
}

func (r *repo) Stage(ctx context.Context, connectionNID, generation, stream int64, rooms []entity.NewRoomStatus) error {
	exec := r.db.Querier(ctx)
	result, err := exec.ExecContext(ctx, stageConnectionSQL, connectionNID, generation, stream)
	if err != nil {
		return fmt.Errorf("repository: stage sync response: %w", err)
	}
	if err := expectOne(result, "stage sync response"); err != nil {
		return err
	}
	if len(rooms) == 0 {
		return nil
	}

	args := []any{connectionNID}
	slots := make([]string, 0, len(rooms))
	for _, room := range rooms {
		if err := room.Validate(); err != nil {
			return err
		}
		configNID, err := r.config(ctx, room.RequiredState)
		if err != nil {
			return err
		}
		args = append(args, room.RoomNID, room.SentTo, room.TimelineLimit, configNID)
		n := len(args)
		slots = append(slots, fmt.Sprintf("($1, $%d, true, $%d, $%d, $%d)", n-3, n-2, n-1, n))
	}

	if _, err := exec.ExecContext(ctx, `
		INSERT INTO sync_connection_rooms
			(connection_nid, room_nid, pending, sent_to, timeline_limit, config_nid)
		VALUES `+strings.Join(slots, ", ")+`
		ON CONFLICT (connection_nid, room_nid, pending)
		DO UPDATE SET sent_to = EXCLUDED.sent_to,
		              timeline_limit = EXCLUDED.timeline_limit,
		              config_nid = EXCLUDED.config_nid`, args...); err != nil {
		return fmt.Errorf("repository: stage sync rooms: %w", err)
	}
	return nil
}

func (r *repo) config(ctx context.Context, required []byte) (int64, error) {
	sum := sha256.Sum256(required)
	exec := r.db.Querier(ctx)

	var nid int64
	err := exec.QueryRowContext(ctx, selectConfigSQL, sum[:]).Scan(&nid)
	if err == nil {
		return nid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("repository: intern required state: %w", err)
	}

	err = exec.QueryRowContext(ctx, insertConfigSQL, sum[:], required).Scan(&nid)
	if err == nil {
		return nid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("repository: intern required state: %w", err)
	}
	if err := exec.QueryRowContext(ctx, selectConfigSQL, sum[:]).Scan(&nid); err != nil {
		return 0, fmt.Errorf("repository: intern required state: %w", err)
	}
	return nid, nil
}

func (r *repo) Touch(ctx context.Context, connectionNID int64, at time.Time) error {
	if _, err := r.db.Querier(ctx).ExecContext(ctx,
		`UPDATE sync_connections SET last_seen_at = $2 WHERE connection_nid = $1`,
		connectionNID, at); err != nil {
		return fmt.Errorf("repository: touch sync connection: %w", err)
	}
	return nil
}

func (r *repo) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.Querier(ctx).ExecContext(ctx,
		`DELETE FROM sync_connections WHERE last_seen_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("repository: sweep sync connections: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repository: sweep sync connections: %w", err)
	}
	return deleted, nil
}

func scanConnection(row *sql.Row) (entity.Connection, error) {
	var (
		connection entity.Connection
		tenantID   string
		pending    sql.NullInt64
		stream     sql.NullInt64
	)
	if err := row.Scan(&connection.NID, &tenantID, &connection.UserID, &connection.DeviceID,
		&connection.ConnID, &connection.Confirmed, &connection.ConfirmedStream,
		&pending, &stream, &connection.LastSeenAt); err != nil {
		return entity.Connection{}, err
	}
	parsed, err := uuid.Parse(tenantID)
	if err != nil {
		return entity.Connection{}, err
	}
	connection.TenantID = parsed
	if pending.Valid {
		connection.Pending = &pending.Int64
		connection.PendingStream = &stream.Int64
	}
	return connection, nil
}

func expectOne(result sql.Result, what string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: %s: %w", what, err)
	}
	if affected != 1 {
		return repository.ErrConnectionNotFound
	}
	return nil
}
