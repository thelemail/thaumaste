package filter

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
	lookupFilterSQL = `
		SELECT filter_id FROM user_filters
		 WHERE tenant_id = $1 AND user_id = $2 AND filter_hash = $3`

	insertFilterSQL = `
		INSERT INTO user_filters (tenant_id, user_id, filter_id, filter_hash, filter)
		VALUES ($1, $2,
		        (SELECT coalesce(max(filter_id::bigint), -1) + 1 FROM user_filters
		          WHERE tenant_id = $1 AND user_id = $2)::text,
		        $3, $4)
		ON CONFLICT (tenant_id, user_id, filter_hash) DO NOTHING
		RETURNING filter_id`

	getFilterSQL = `
		SELECT filter FROM user_filters
		 WHERE tenant_id = $1 AND user_id = $2 AND filter_id = $3`
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Filter {
	return &repo{db: db}
}

func (r *repo) Store(ctx context.Context, in entity.NewFilter) (string, error) {
	hash, err := in.Filter.Hash()
	if err != nil {
		return "", err
	}
	exec := r.db.Querier(ctx)

	var filterID string
	err = exec.QueryRowContext(ctx, lookupFilterSQL, in.TenantID.String(), in.UserID, hash).Scan(&filterID)
	if err == nil {
		return filterID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("repository: read filter: %w", err)
	}

	err = exec.QueryRowContext(ctx, insertFilterSQL,
		in.TenantID.String(), in.UserID, hash, []byte(in.Filter.Document)).Scan(&filterID)
	if err == nil {
		return filterID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("repository: store filter: %w", err)
	}

	if err := exec.QueryRowContext(ctx, lookupFilterSQL,
		in.TenantID.String(), in.UserID, hash).Scan(&filterID); err != nil {
		return "", fmt.Errorf("repository: read filter: %w", err)
	}
	return filterID, nil
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, userID, filterID string) (entity.Filter, error) {
	var document []byte
	err := r.db.Querier(ctx).QueryRowContext(ctx, getFilterSQL,
		scope.ID().String(), userID, filterID).Scan(&document)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Filter{}, entity.ErrFilterNotFound
		}
		return entity.Filter{}, fmt.Errorf("repository: get filter: %w", err)
	}
	filter, err := entity.ParseFilter(document)
	if err != nil {
		return entity.Filter{}, err
	}
	filter.ID = filterID
	return filter, nil
}
