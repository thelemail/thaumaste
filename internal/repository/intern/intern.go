package intern

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/thelemail/thaumaste/internal/pkg/postgres"
)

const (
	selectTypeSQL = `SELECT event_type_nid FROM event_types WHERE event_type = $1`
	insertTypeSQL = `
		INSERT INTO event_types (event_type) VALUES ($1)
		ON CONFLICT (event_type) DO NOTHING
		RETURNING event_type_nid`
	selectStateKeySQL = `SELECT event_state_key_nid FROM event_state_keys WHERE event_state_key = $1`
	insertStateKeySQL = `
		INSERT INTO event_state_keys (event_state_key) VALUES ($1)
		ON CONFLICT (event_state_key) DO NOTHING
		RETURNING event_state_key_nid`
)

func EventType(ctx context.Context, exec postgres.Querier, eventType string) (int64, error) {
	nid, err := lookupOrInsert(ctx, exec, selectTypeSQL, insertTypeSQL, eventType)
	if err != nil {
		return 0, fmt.Errorf("repository: intern event type: %w", err)
	}
	return nid, nil
}

func StateKey(ctx context.Context, exec postgres.Querier, stateKey string) (int64, error) {
	nid, err := lookupOrInsert(ctx, exec, selectStateKeySQL, insertStateKeySQL, stateKey)
	if err != nil {
		return 0, fmt.Errorf("repository: intern state key: %w", err)
	}
	return nid, nil
}

func lookupOrInsert(ctx context.Context, exec postgres.Querier, lookup, insert, value string) (int64, error) {
	var nid int64
	err := exec.QueryRowContext(ctx, lookup, value).Scan(&nid)
	if err == nil {
		return nid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	err = exec.QueryRowContext(ctx, insert, value).Scan(&nid)
	if err == nil {
		return nid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return nid, exec.QueryRowContext(ctx, lookup, value).Scan(&nid)
}
