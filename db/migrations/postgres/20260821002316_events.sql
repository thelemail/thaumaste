-- +goose Up
CREATE TABLE rooms (
    room_nid     BIGSERIAL   PRIMARY KEY,
    tenant_id    UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    room_id      TEXT        NOT NULL,
    room_version TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT rooms_room_id_len_chk CHECK (length(room_id) BETWEEN 1 AND 255)
);

CREATE UNIQUE INDEX rooms_room_id_uidx ON rooms (room_id);
CREATE INDEX rooms_tenant_id_idx ON rooms (tenant_id);

CREATE TABLE event_types (
    event_type_nid BIGSERIAL PRIMARY KEY,
    event_type     TEXT      NOT NULL,
    CONSTRAINT event_types_len_chk CHECK (length(event_type) BETWEEN 1 AND 255)
);

CREATE UNIQUE INDEX event_types_event_type_uidx ON event_types (event_type);

CREATE TABLE event_state_keys (
    event_state_key_nid BIGSERIAL PRIMARY KEY,
    event_state_key     TEXT      NOT NULL,
    CONSTRAINT event_state_keys_len_chk CHECK (length(event_state_key) <= 255)
);

CREATE UNIQUE INDEX event_state_keys_event_state_key_uidx ON event_state_keys (event_state_key);

CREATE TABLE state_snapshots (
    state_snapshot_nid BIGSERIAL PRIMARY KEY,
    room_nid           BIGINT    NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    snapshot_hash      BYTEA     NOT NULL,
    CONSTRAINT state_snapshots_hash_len_chk CHECK (length(snapshot_hash) = 32)
);

CREATE UNIQUE INDEX state_snapshots_room_hash_uidx ON state_snapshots (room_nid, snapshot_hash);

CREATE TABLE state_blocks (
    state_block_nid BIGSERIAL PRIMARY KEY,
    room_nid        BIGINT    NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    block_hash      BYTEA     NOT NULL,
    CONSTRAINT state_blocks_hash_len_chk CHECK (length(block_hash) = 32)
);

CREATE UNIQUE INDEX state_blocks_room_hash_uidx ON state_blocks (room_nid, block_hash);

CREATE TABLE events (
    event_nid            BIGSERIAL   PRIMARY KEY,
    room_nid             BIGINT      NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    event_id             TEXT        NOT NULL,
    event_type_nid       BIGINT      NOT NULL REFERENCES event_types (event_type_nid),
    event_state_key_nid  BIGINT      REFERENCES event_state_keys (event_state_key_nid),
    sender               TEXT        NOT NULL,
    sender_is_local      BOOLEAN     NOT NULL,
    depth                BIGINT      NOT NULL,
    stream_ordering      BIGINT      NOT NULL,
    topological_ordering BIGINT      NOT NULL,
    instance_name        TEXT        NOT NULL,
    origin_server_ts     BIGINT      NOT NULL,
    state_snapshot_nid   BIGINT      REFERENCES state_snapshots (state_snapshot_nid) ON DELETE SET NULL,
    disposition          TEXT        NOT NULL DEFAULT 'accepted',
    event_json           BYTEA       NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT events_event_id_len_chk CHECK (length(event_id) BETWEEN 1 AND 255),
    CONSTRAINT events_sender_len_chk CHECK (length(sender) BETWEEN 1 AND 255),
    CONSTRAINT events_json_len_chk CHECK (length(event_json) <= 65536),
    CONSTRAINT events_disposition_chk
        CHECK (disposition IN ('accepted', 'redacted', 'rejected', 'soft_failed', 'outlier'))
);

CREATE UNIQUE INDEX events_event_id_uidx ON events (event_id);
CREATE UNIQUE INDEX events_stream_ordering_uidx ON events (stream_ordering);
CREATE INDEX events_room_nid_idx ON events (room_nid);
CREATE INDEX events_room_topological_idx ON events (room_nid, topological_ordering, stream_ordering);
CREATE INDEX events_room_state_idx ON events (room_nid, event_type_nid, event_state_key_nid)
    WHERE event_state_key_nid IS NOT NULL;

ALTER TABLE rooms
    ADD COLUMN create_event_nid BIGINT REFERENCES events (event_nid) ON DELETE SET NULL;

CREATE TABLE event_prev_edges (
    room_nid   BIGINT NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    child_nid  BIGINT NOT NULL REFERENCES events (event_nid) ON DELETE CASCADE,
    parent_id  TEXT   NOT NULL,
    PRIMARY KEY (child_nid, parent_id)
);

CREATE INDEX event_prev_edges_room_nid_idx ON event_prev_edges (room_nid);
CREATE INDEX event_prev_edges_parent_idx ON event_prev_edges (room_nid, parent_id);

CREATE TABLE event_auth_edges (
    room_nid   BIGINT NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    child_nid  BIGINT NOT NULL REFERENCES events (event_nid) ON DELETE CASCADE,
    parent_id  TEXT   NOT NULL,
    PRIMARY KEY (child_nid, parent_id)
);

CREATE INDEX event_auth_edges_room_nid_idx ON event_auth_edges (room_nid);
CREATE INDEX event_auth_edges_parent_idx ON event_auth_edges (room_nid, parent_id);

CREATE TABLE room_extremities (
    room_nid  BIGINT NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    event_nid BIGINT NOT NULL REFERENCES events (event_nid) ON DELETE CASCADE,
    PRIMARY KEY (room_nid, event_nid)
);

CREATE TABLE state_block_entries (
    state_block_nid     BIGINT NOT NULL REFERENCES state_blocks (state_block_nid) ON DELETE CASCADE,
    event_type_nid      BIGINT NOT NULL REFERENCES event_types (event_type_nid),
    event_state_key_nid BIGINT NOT NULL REFERENCES event_state_keys (event_state_key_nid),
    event_nid           BIGINT NOT NULL REFERENCES events (event_nid) ON DELETE CASCADE,
    PRIMARY KEY (state_block_nid, event_type_nid, event_state_key_nid)
);

CREATE TABLE state_snapshot_blocks (
    state_snapshot_nid BIGINT NOT NULL REFERENCES state_snapshots (state_snapshot_nid) ON DELETE CASCADE,
    ordinal            INTEGER NOT NULL,
    state_block_nid    BIGINT NOT NULL REFERENCES state_blocks (state_block_nid),
    PRIMARY KEY (state_snapshot_nid, ordinal)
);

-- +goose Down
DROP TABLE state_snapshot_blocks;
DROP TABLE state_block_entries;
DROP TABLE room_extremities;
DROP TABLE event_auth_edges;
DROP TABLE event_prev_edges;
ALTER TABLE rooms DROP COLUMN create_event_nid;
DROP TABLE events;
DROP TABLE state_blocks;
DROP TABLE state_snapshots;
DROP TABLE event_state_keys;
DROP TABLE event_types;
DROP TABLE rooms;
