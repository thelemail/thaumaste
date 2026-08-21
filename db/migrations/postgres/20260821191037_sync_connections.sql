-- +goose Up
CREATE TABLE sync_state_configs (
    config_nid  BIGSERIAL PRIMARY KEY,
    config_hash BYTEA     NOT NULL,
    config      BYTEA     NOT NULL,
    CONSTRAINT sync_state_configs_hash_len_chk CHECK (length(config_hash) = 32),
    CONSTRAINT sync_state_configs_config_len_chk CHECK (length(config) <= 65536)
);

CREATE UNIQUE INDEX sync_state_configs_hash_uidx ON sync_state_configs (config_hash);

CREATE TABLE sync_connections (
    connection_nid   BIGSERIAL   PRIMARY KEY,
    tenant_id        UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id          TEXT        NOT NULL,
    device_id        TEXT        NOT NULL,
    conn_id          TEXT        NOT NULL,
    confirmed        BIGINT      NOT NULL DEFAULT 0,
    confirmed_stream BIGINT      NOT NULL DEFAULT 0,
    pending          BIGINT,
    pending_stream   BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT sync_connections_user_id_len_chk CHECK (length(user_id) BETWEEN 1 AND 255),
    CONSTRAINT sync_connections_device_id_len_chk CHECK (length(device_id) BETWEEN 1 AND 255),
    CONSTRAINT sync_connections_conn_id_len_chk CHECK (length(conn_id) <= 255),
    CONSTRAINT sync_connections_generations_chk CHECK (pending IS NULL OR pending > confirmed),
    FOREIGN KEY (tenant_id, user_id, device_id)
        REFERENCES devices (tenant_id, user_id, device_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX sync_connections_identity_uidx
    ON sync_connections (tenant_id, user_id, device_id, conn_id);
CREATE INDEX sync_connections_tenant_id_idx ON sync_connections (tenant_id);
CREATE INDEX sync_connections_last_seen_at_idx ON sync_connections (last_seen_at);

CREATE TABLE sync_connection_rooms (
    connection_nid BIGINT  NOT NULL REFERENCES sync_connections (connection_nid) ON DELETE CASCADE,
    room_nid       BIGINT  NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    pending        BOOLEAN NOT NULL,
    sent_to        BIGINT  NOT NULL,
    timeline_limit INTEGER NOT NULL,
    config_nid     BIGINT  NOT NULL REFERENCES sync_state_configs (config_nid),
    CONSTRAINT sync_connection_rooms_timeline_limit_chk CHECK (timeline_limit BETWEEN 0 AND 1000),
    PRIMARY KEY (connection_nid, room_nid, pending)
);

CREATE INDEX sync_connection_rooms_room_nid_idx ON sync_connection_rooms (room_nid);

ALTER TABLE rooms
    ADD COLUMN last_stream BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN bump_stream BIGINT NOT NULL DEFAULT 0;

CREATE INDEX events_room_stream_idx ON events (room_nid, stream_ordering);

ALTER TABLE stream_positions
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE stream_positions DROP COLUMN updated_at;

DROP INDEX events_room_stream_idx;

ALTER TABLE rooms
    DROP COLUMN bump_stream,
    DROP COLUMN last_stream;

DROP TABLE sync_connection_rooms;
DROP TABLE sync_connections;
DROP TABLE sync_state_configs;
