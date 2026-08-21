package roommember

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

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

func New(db *postgres.Client) repository.RoomMember {
	return &repo{db: db}
}

func (r *repo) Upsert(ctx context.Context, in entity.NewRoomMembership) error {
	row := dbpg.RoomMembership{
		TenantID:   in.TenantID.String(),
		RoomNid:    in.RoomNID,
		UserID:     in.UserID,
		Membership: in.Membership,
		EventNid:   in.EventNID,
		Forgotten:  in.Forgotten,
	}
	err := row.Upsert(ctx, r.db.Querier(ctx), true,
		[]string{
			dbpg.RoomMembershipColumns.TenantID,
			dbpg.RoomMembershipColumns.RoomNid,
			dbpg.RoomMembershipColumns.UserID,
		},
		boil.Whitelist(
			dbpg.RoomMembershipColumns.Membership,
			dbpg.RoomMembershipColumns.EventNid,
			dbpg.RoomMembershipColumns.Forgotten,
		),
		boil.Infer())
	if err != nil {
		return fmt.Errorf("repository: upsert room membership: %w", err)
	}
	return nil
}

func (r *repo) Get(ctx context.Context, roomNID int64, userID string) (entity.RoomMembership, error) {
	row, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.RoomNid.EQ(roomNID),
		dbpg.RoomMembershipWhere.UserID.EQ(userID),
		qm.Load(dbpg.RoomMembershipRels.RoomNidRoom),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.RoomMembership{}, repository.ErrMembershipNotFound
		}
		return entity.RoomMembership{}, fmt.Errorf("repository: get membership: %w", err)
	}
	out, err := toMemberships(dbpg.RoomMembershipSlice{row})
	if err != nil {
		return entity.RoomMembership{}, err
	}
	return out[0], nil
}

func (r *repo) SetForgotten(ctx context.Context, roomNID int64, userID string, forgotten bool) error {
	n, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.RoomNid.EQ(roomNID),
		dbpg.RoomMembershipWhere.UserID.EQ(userID),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.RoomMembershipColumns.Forgotten: forgotten})
	if err != nil {
		return fmt.Errorf("repository: set forgotten: %w", err)
	}
	if n == 0 {
		return repository.ErrMembershipNotFound
	}
	return nil
}

func (r *repo) ListForRoom(ctx context.Context, roomNID int64, membership string) ([]entity.RoomMembership, error) {
	rows, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.RoomNid.EQ(roomNID),
		dbpg.RoomMembershipWhere.Membership.EQ(membership),
		qm.OrderBy(dbpg.RoomMembershipColumns.UserID),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list room members: %w", err)
	}
	return toMemberships(rows)
}

func (r *repo) ListForUser(ctx context.Context, scope entity.TenantScope, userID, membership string) ([]entity.RoomMembership, error) {
	rows, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.TenantID.EQ(scope.ID().String()),
		dbpg.RoomMembershipWhere.UserID.EQ(userID),
		dbpg.RoomMembershipWhere.Membership.EQ(membership),
		qm.Load(dbpg.RoomMembershipRels.RoomNidRoom),
		qm.OrderBy(dbpg.RoomMembershipColumns.RoomNid),
	).All(ctx, r.db.Querier(ctx))
	if err != nil {
		return nil, fmt.Errorf("repository: list rooms for user: %w", err)
	}
	return toMemberships(rows)
}

func (r *repo) CountForRoom(ctx context.Context, roomNID int64, membership string) (int64, error) {
	n, err := dbpg.RoomMemberships(
		dbpg.RoomMembershipWhere.RoomNid.EQ(roomNID),
		dbpg.RoomMembershipWhere.Membership.EQ(membership),
	).Count(ctx, r.db.Querier(ctx))
	if err != nil {
		return 0, fmt.Errorf("repository: count room members: %w", err)
	}
	return n, nil
}

func toMemberships(rows dbpg.RoomMembershipSlice) ([]entity.RoomMembership, error) {
	out := make([]entity.RoomMembership, 0, len(rows))
	for _, row := range rows {
		tenantID, err := uuid.Parse(row.TenantID)
		if err != nil {
			return nil, fmt.Errorf("parse membership tenant id: %w", err)
		}
		converted := entity.RoomMembership{
			TenantID:   tenantID,
			RoomNID:    row.RoomNid,
			UserID:     row.UserID,
			Membership: row.Membership,
			EventNID:   row.EventNid,
			Forgotten:  row.Forgotten,
		}
		if row.R != nil && row.R.RoomNidRoom != nil {
			converted.RoomID = row.R.RoomNidRoom.RoomID
		}
		out = append(out, converted)
	}
	return out, nil
}

func (r *repo) CountForRooms(ctx context.Context, roomNIDs []int64) (map[int64]entity.MemberCounts, error) {
	if len(roomNIDs) == 0 {
		return nil, nil
	}
	args, slots := placeholders(nil, roomNIDs)
	rows, err := r.db.Querier(ctx).QueryContext(ctx, `
		SELECT room_nid,
		       count(*) FILTER (WHERE membership = 'join')   AS joined,
		       count(*) FILTER (WHERE membership = 'invite') AS invited
		  FROM room_memberships
		 WHERE room_nid IN (`+strings.Join(slots, ", ")+`)
		 GROUP BY room_nid`, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: count members for rooms: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64]entity.MemberCounts, len(roomNIDs))
	for rows.Next() {
		var roomNID int64
		var counts entity.MemberCounts
		if err := rows.Scan(&roomNID, &counts.Joined, &counts.Invited); err != nil {
			return nil, fmt.Errorf("repository: scan member counts: %w", err)
		}
		out[roomNID] = counts
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: count members for rooms: %w", err)
	}
	return out, nil
}

func (r *repo) ListForSync(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.SyncRoom, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, `
		SELECT m.room_nid, r.room_id, r.room_version, m.membership, m.event_nid, m.forgotten,
		       r.last_stream, r.bump_stream
		  FROM room_memberships m
		  JOIN rooms r ON r.room_nid = m.room_nid
		 WHERE m.tenant_id = $1 AND m.user_id = $2
		 ORDER BY r.last_stream DESC, m.room_nid DESC`, scope.ID().String(), userID)
	if err != nil {
		return nil, fmt.Errorf("repository: list sync rooms: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.SyncRoom
	for rows.Next() {
		var room entity.SyncRoom
		var version string
		if err := rows.Scan(&room.RoomNID, &room.RoomID, &version, &room.Membership,
			&room.EventNID, &room.Forgotten, &room.LastStream, &room.BumpStream); err != nil {
			return nil, fmt.Errorf("repository: scan sync room: %w", err)
		}
		room.Version = entity.RoomVersionID(version)
		out = append(out, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: list sync rooms: %w", err)
	}
	return out, nil
}

func (r *repo) ListForRooms(ctx context.Context, roomNIDs []int64, userIDs []string) ([]entity.RoomMembership, error) {
	if len(roomNIDs) == 0 || len(userIDs) == 0 {
		return nil, nil
	}
	args, roomSlots := placeholders(nil, roomNIDs)
	args, userSlots := placeholders(args, userIDs)
	rows, err := r.db.Querier(ctx).QueryContext(ctx, `
		SELECT room_nid, user_id, membership, event_nid, forgotten
		  FROM room_memberships
		 WHERE room_nid IN (`+strings.Join(roomSlots, ", ")+`)
		   AND user_id IN (`+strings.Join(userSlots, ", ")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: list members for rooms: %w", err)
	}
	return scanMemberships(rows)
}

func (r *repo) Heroes(ctx context.Context, roomNIDs []int64, exclude string, limit int) (map[int64][]entity.RoomMembership, error) {
	if len(roomNIDs) == 0 || limit <= 0 {
		return nil, nil
	}
	args := []any{exclude, limit}
	slots := make([]string, 0, len(roomNIDs))
	for _, roomNID := range roomNIDs {
		args = append(args, roomNID)
		slots = append(slots, "($"+strconv.Itoa(len(args))+"::bigint)")
	}
	rows, err := r.db.Querier(ctx).QueryContext(ctx, `
		WITH wanted (room_nid) AS (VALUES `+strings.Join(slots, ", ")+`)
		SELECT hero.* FROM wanted
		CROSS JOIN LATERAL (
			SELECT m.room_nid, m.user_id, m.membership, m.event_nid, m.forgotten
			  FROM room_memberships m
			 WHERE m.room_nid = wanted.room_nid
			   AND m.user_id <> $1
			   AND m.membership IN ('join', 'invite')
			 ORDER BY m.membership, m.user_id
			 LIMIT $2) hero`, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: list heroes: %w", err)
	}
	members, err := scanMemberships(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]entity.RoomMembership, len(roomNIDs))
	for _, member := range members {
		out[member.RoomNID] = append(out[member.RoomNID], member)
	}
	return out, nil
}

func scanMemberships(rows *sql.Rows) ([]entity.RoomMembership, error) {
	defer func() { _ = rows.Close() }()
	var out []entity.RoomMembership
	for rows.Next() {
		var member entity.RoomMembership
		if err := rows.Scan(&member.RoomNID, &member.UserID, &member.Membership,
			&member.EventNID, &member.Forgotten); err != nil {
			return nil, fmt.Errorf("repository: scan membership: %w", err)
		}
		out = append(out, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: list memberships: %w", err)
	}
	return out, nil
}

func placeholders[T any](args []any, values []T) ([]any, []string) {
	slots := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, value)
		slots = append(slots, "$"+strconv.Itoa(len(args)))
	}
	return args, slots
}
