package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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

func New(db *postgres.Client) repository.User {
	return &repo{db: db}
}

func (r *repo) Create(ctx context.Context, in entity.NewUser) (entity.User, error) {
	userID, err := entity.MintUserID(in.Localpart, in.ServerName)
	if err != nil {
		return entity.User{}, err
	}
	row := dbpg.User{
		TenantID:    in.TenantID.String(),
		UserID:      userID,
		Localpart:   in.Localpart,
		DisplayName: in.DisplayName,
	}
	if err := row.Insert(ctx, r.db.Querier(ctx), boil.Infer()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return entity.User{}, repository.ErrUserInUse
		}
		return entity.User{}, fmt.Errorf("repository: create user: %w", err)
	}
	return toUser(&row)
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.User, error) {
	row, err := dbpg.Users(
		dbpg.UserWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UserWhere.UserID.EQ(userID),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.User{}, repository.ErrUserNotFound
		}
		return entity.User{}, fmt.Errorf("repository: get user: %w", err)
	}
	return toUser(row)
}

func (r *repo) Exists(ctx context.Context, scope entity.TenantScope, localpart string) (bool, error) {
	found, err := dbpg.Users(
		dbpg.UserWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UserWhere.Localpart.EQ(localpart),
	).Exists(ctx, r.db.Querier(ctx))
	if err != nil {
		return false, fmt.Errorf("repository: user exists: %w", err)
	}
	return found, nil
}

func (r *repo) UpdateProfile(ctx context.Context, scope entity.TenantScope, userID string, in entity.UpdateProfile) (entity.User, error) {
	exec := r.db.Querier(ctx)
	row, err := dbpg.Users(
		dbpg.UserWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UserWhere.UserID.EQ(userID),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.User{}, repository.ErrUserNotFound
		}
		return entity.User{}, fmt.Errorf("repository: get user: %w", err)
	}

	columns := make([]string, 0, 2)
	if in.DisplayName != nil {
		row.DisplayName = *in.DisplayName
		columns = append(columns, dbpg.UserColumns.DisplayName)
	}
	if in.AvatarURL != nil {
		row.AvatarURL = *in.AvatarURL
		columns = append(columns, dbpg.UserColumns.AvatarURL)
	}
	if len(columns) == 0 {
		return toUser(row)
	}
	if _, err := row.Update(ctx, exec, boil.Whitelist(columns...)); err != nil {
		return entity.User{}, fmt.Errorf("repository: update profile: %w", err)
	}
	return toUser(row)
}

func (r *repo) Deactivate(ctx context.Context, scope entity.TenantScope, userID string, at time.Time) error {
	n, err := dbpg.Users(
		dbpg.UserWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UserWhere.UserID.EQ(userID),
		qm.Where(dbpg.UserColumns.DeactivatedAt+" IS NULL"),
	).UpdateAll(ctx, r.db.Querier(ctx), dbpg.M{dbpg.UserColumns.DeactivatedAt: at})
	if err != nil {
		return fmt.Errorf("repository: deactivate user: %w", err)
	}
	if n == 0 {
		return repository.ErrUserNotFound
	}
	return nil
}

func toUser(row *dbpg.User) (entity.User, error) {
	tenantID, err := uuid.Parse(row.TenantID)
	if err != nil {
		return entity.User{}, fmt.Errorf("parse user tenant id: %w", err)
	}
	out := entity.User{
		TenantID:    tenantID,
		UserID:      row.UserID,
		Localpart:   row.Localpart,
		DisplayName: row.DisplayName,
		AvatarURL:   row.AvatarURL,
		CreatedAt:   row.CreatedAt,
	}
	if row.DeactivatedAt.Valid {
		at := row.DeactivatedAt.Time
		out.DeactivatedAt = &at
	}
	return out, nil
}

const searchUsersSQL = `
	SELECT u.user_id, u.display_name, u.avatar_url
	  FROM users u
	 WHERE u.tenant_id = $1
	   AND u.user_id <> $2
	   AND u.deactivated_at IS NULL
	   AND (lower(u.localpart) LIKE $3 OR lower(u.user_id) LIKE $3 OR lower(u.display_name) LIKE $4)
	   AND EXISTS (
	       SELECT 1 FROM room_memberships m
	        WHERE m.tenant_id = u.tenant_id
	          AND m.user_id = u.user_id
	          AND m.membership IN ('join', 'invite')
	          AND (m.room_nid = ANY(string_to_array($5, ',')::bigint[])
	               OR EXISTS (SELECT 1 FROM room_memberships mine
	                           WHERE mine.tenant_id = u.tenant_id
	                             AND mine.user_id = $2
	                             AND mine.room_nid = m.room_nid
	                             AND mine.membership IN ('join', 'invite'))))
	 ORDER BY u.display_name, u.user_id
	 LIMIT $6`

func (r *repo) Search(ctx context.Context, scope entity.TenantScope, caller, term string,
	discoverable []int64, limit int,
) ([]entity.DirectoryResult, error) {
	prefix := strings.ToLower(term) + "%"
	anywhere := "%" + strings.ToLower(term) + "%"
	rooms := make([]string, 0, len(discoverable))
	for _, roomNID := range discoverable {
		rooms = append(rooms, strconv.FormatInt(roomNID, 10))
	}
	rows, err := r.db.Querier(ctx).QueryContext(ctx, searchUsersSQL,
		scope.ID().String(), caller, prefix, anywhere, strings.Join(rooms, ","), limit)
	if err != nil {
		return nil, fmt.Errorf("repository: search users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.DirectoryResult
	for rows.Next() {
		var found entity.DirectoryResult
		if err := rows.Scan(&found.UserID, &found.DisplayName, &found.AvatarURL); err != nil {
			return nil, fmt.Errorf("repository: scan user: %w", err)
		}
		out = append(out, found)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: search users: %w", err)
	}
	return out, nil
}
