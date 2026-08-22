package presence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const (
	setPresenceSQL = `
		INSERT INTO user_presence (tenant_id, user_id, presence, status_msg, last_active_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (tenant_id, user_id) DO UPDATE
		   SET presence = EXCLUDED.presence, status_msg = EXCLUDED.status_msg,
		       last_active_at = EXCLUDED.last_active_at, updated_at = EXCLUDED.updated_at`

	getPresenceSQL = `
		SELECT user_id, presence, status_msg, last_active_at
		  FROM user_presence WHERE tenant_id = $1 AND user_id = $2`
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Presence {
	return &repo{db: db}
}

func (r *repo) Set(ctx context.Context, in entity.NewPresence, at time.Time) error {
	_, err := r.db.Querier(ctx).ExecContext(ctx, setPresenceSQL,
		in.TenantID.String(), in.UserID, in.State, in.StatusMsg, at)
	if err != nil {
		return fmt.Errorf("repository: set presence: %w", err)
	}
	return nil
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.Presence, error) {
	var found entity.Presence
	err := r.db.Querier(ctx).QueryRowContext(ctx, getPresenceSQL, scope.ID().String(), userID).
		Scan(&found.UserID, &found.State, &found.StatusMsg, &found.LastActiveAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Presence{}, repository.ErrPresenceNotFound
		}
		return entity.Presence{}, fmt.Errorf("repository: get presence: %w", err)
	}
	return found, nil
}
