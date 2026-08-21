package key

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

const (
	upsertDeviceKeySQL = `
		INSERT INTO device_keys (tenant_id, user_id, device_id, key_json)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, user_id, device_id) DO UPDATE
		   SET key_json = EXCLUDED.key_json, updated_at = now()
		 WHERE device_keys.key_json <> EXCLUDED.key_json`

	listDeviceKeysSQL = `
		SELECT k.user_id, k.device_id, COALESCE(d.display_name, ''), k.key_json
		  FROM device_keys k
		  JOIN devices d
		    ON d.tenant_id = k.tenant_id AND d.user_id = k.user_id AND d.device_id = k.device_id
		 WHERE k.tenant_id = $1 AND k.user_id IN (%s)
		 ORDER BY k.user_id, k.device_id`

	countOneTimeSQL = `
		SELECT algorithm, count(*)
		  FROM device_one_time_keys
		 WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3
		 GROUP BY algorithm`

	claimOneTimeSQL = `
		DELETE FROM device_one_time_keys
		 WHERE (tenant_id, user_id, device_id, algorithm, key_id) IN (
		       SELECT tenant_id, user_id, device_id, algorithm, key_id
		         FROM device_one_time_keys
		        WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3 AND algorithm = $4
		        ORDER BY ordinal
		        LIMIT 1
		        FOR UPDATE SKIP LOCKED)
		RETURNING key_id, key_json`

	setFallbackSQL = `
		INSERT INTO device_fallback_keys (tenant_id, user_id, device_id, algorithm, key_id, key_json, used)
		VALUES ($1, $2, $3, $4, $5, $6, false)
		ON CONFLICT (tenant_id, user_id, device_id, algorithm) DO UPDATE
		   SET key_id = EXCLUDED.key_id, key_json = EXCLUDED.key_json, used = false
		 WHERE device_fallback_keys.key_json <> EXCLUDED.key_json`

	claimFallbackSQL = `
		UPDATE device_fallback_keys
		   SET used = true
		 WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3 AND algorithm = $4
		RETURNING key_id, key_json`

	unusedFallbackSQL = `
		SELECT algorithm
		  FROM device_fallback_keys
		 WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3 AND NOT used
		 ORDER BY algorithm`
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Key {
	return &repo{db: db}
}

func (r *repo) UpsertDevice(ctx context.Context, in entity.NewDeviceKey) error {
	if _, err := r.db.Querier(ctx).ExecContext(ctx, upsertDeviceKeySQL,
		in.TenantID.String(), in.UserID, in.DeviceID, in.KeyJSON); err != nil {
		return fmt.Errorf("repository: upsert device keys: %w", err)
	}
	return nil
}

func (r *repo) ListDevices(ctx context.Context, scope entity.TenantScope, userIDs []string) ([]entity.DeviceKey, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	args := []any{scope.ID().String()}
	slots := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		args = append(args, userID)
		slots = append(slots, "$"+strconv.Itoa(len(args)))
	}
	rows, err := r.db.Querier(ctx).QueryContext(ctx,
		fmt.Sprintf(listDeviceKeysSQL, strings.Join(slots, ", ")), args...)
	if err != nil {
		return nil, fmt.Errorf("repository: list device keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []entity.DeviceKey
	for rows.Next() {
		var key entity.DeviceKey
		var raw []byte
		if err := rows.Scan(&key.UserID, &key.DeviceID, &key.DisplayName, &raw); err != nil {
			return nil, fmt.Errorf("repository: scan device key: %w", err)
		}
		key.KeyJSON = raw
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: list device keys: %w", err)
	}
	return out, nil
}

func (r *repo) AddOneTime(ctx context.Context, keys []entity.NewOneTimeKey) error {
	if len(keys) == 0 {
		return nil
	}
	args := make([]any, 0, len(keys)*6)
	slots := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, key.TenantID.String(), key.UserID, key.DeviceID,
			key.KeyID.Algorithm, key.KeyID.KeyID, key.KeyJSON)
		base := len(args) - 6
		slots = append(slots, fmt.Sprintf("($%d::uuid, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5, base+6))
	}
	_, err := r.db.Querier(ctx).ExecContext(ctx, `
		INSERT INTO device_one_time_keys (tenant_id, user_id, device_id, algorithm, key_id, key_json)
		VALUES `+strings.Join(slots, ", ")+`
		ON CONFLICT (tenant_id, user_id, device_id, algorithm, key_id) DO NOTHING`, args...)
	if err != nil {
		return fmt.Errorf("repository: add one-time keys: %w", err)
	}
	return nil
}

func (r *repo) ExistingOneTime(ctx context.Context, scope entity.TenantScope, userID, deviceID string,
	ids []entity.KeyIdentifier,
) (map[entity.KeyIdentifier][]byte, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{scope.ID().String(), userID, deviceID}
	slots := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id.Algorithm, id.KeyID)
		slots = append(slots, "($"+strconv.Itoa(len(args)-1)+", $"+strconv.Itoa(len(args))+")")
	}
	rows, err := r.db.Querier(ctx).QueryContext(ctx, `
		SELECT algorithm, key_id, key_json
		  FROM device_one_time_keys
		 WHERE tenant_id = $1 AND user_id = $2 AND device_id = $3
		   AND (algorithm, key_id) IN (`+strings.Join(slots, ", ")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: read one-time keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[entity.KeyIdentifier][]byte, len(ids))
	for rows.Next() {
		var id entity.KeyIdentifier
		var raw []byte
		if err := rows.Scan(&id.Algorithm, &id.KeyID, &raw); err != nil {
			return nil, fmt.Errorf("repository: scan one-time key: %w", err)
		}
		out[id] = raw
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: read one-time keys: %w", err)
	}
	return out, nil
}

func (r *repo) CountOneTime(ctx context.Context, scope entity.TenantScope, userID, deviceID string) (map[string]int, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, countOneTimeSQL, scope.ID().String(), userID, deviceID)
	if err != nil {
		return nil, fmt.Errorf("repository: count one-time keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int{}
	for rows.Next() {
		var algorithm string
		var count int
		if err := rows.Scan(&algorithm, &count); err != nil {
			return nil, fmt.Errorf("repository: scan one-time key count: %w", err)
		}
		out[algorithm] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: count one-time keys: %w", err)
	}
	return out, nil
}

func (r *repo) ClaimOneTime(ctx context.Context, scope entity.TenantScope, userID, deviceID, algorithm string) (entity.ClaimedKey, error) {
	return r.claim(ctx, claimOneTimeSQL, scope, userID, deviceID, algorithm)
}

func (r *repo) ClaimFallback(ctx context.Context, scope entity.TenantScope, userID, deviceID, algorithm string) (entity.ClaimedKey, error) {
	return r.claim(ctx, claimFallbackSQL, scope, userID, deviceID, algorithm)
}

func (r *repo) claim(ctx context.Context, query string, scope entity.TenantScope,
	userID, deviceID, algorithm string,
) (entity.ClaimedKey, error) {
	var keyID string
	var raw []byte
	err := r.db.Querier(ctx).QueryRowContext(ctx, query,
		scope.ID().String(), userID, deviceID, algorithm).Scan(&keyID, &raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.ClaimedKey{}, repository.ErrKeyNotFound
		}
		return entity.ClaimedKey{}, fmt.Errorf("repository: claim key: %w", err)
	}
	return entity.ClaimedKey{
		UserID:   userID,
		DeviceID: deviceID,
		KeyID:    entity.KeyIdentifier{Algorithm: algorithm, KeyID: keyID},
		KeyJSON:  raw,
	}, nil
}

func (r *repo) SetFallback(ctx context.Context, in entity.NewFallbackKey) error {
	if _, err := r.db.Querier(ctx).ExecContext(ctx, setFallbackSQL,
		in.TenantID.String(), in.UserID, in.DeviceID, in.KeyID.Algorithm, in.KeyID.KeyID, in.KeyJSON); err != nil {
		return fmt.Errorf("repository: set fallback key: %w", err)
	}
	return nil
}

func (r *repo) UnusedFallbackAlgorithms(ctx context.Context, scope entity.TenantScope, userID, deviceID string) ([]string, error) {
	rows, err := r.db.Querier(ctx).QueryContext(ctx, unusedFallbackSQL, scope.ID().String(), userID, deviceID)
	if err != nil {
		return nil, fmt.Errorf("repository: list unused fallback keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var algorithm string
		if err := rows.Scan(&algorithm); err != nil {
			return nil, fmt.Errorf("repository: scan fallback algorithm: %w", err)
		}
		out = append(out, algorithm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: list unused fallback keys: %w", err)
	}
	return out, nil
}
