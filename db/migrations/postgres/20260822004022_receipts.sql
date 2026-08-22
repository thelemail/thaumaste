-- +goose Up
CREATE SEQUENCE receipts_stream_seq;

CREATE TABLE receipts (
    tenant_id    UUID   NOT NULL,
    room_nid     BIGINT NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    user_id      TEXT   NOT NULL,
    receipt_type TEXT   NOT NULL,
    thread_id    TEXT   NOT NULL,
    event_nid    BIGINT NOT NULL REFERENCES events (event_nid) ON DELETE CASCADE,
    ts           BIGINT NOT NULL,
    stream_id    BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, room_nid, user_id, receipt_type, thread_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT receipts_type_chk CHECK (receipt_type IN ('m.read', 'm.read.private')),
    CONSTRAINT receipts_thread_id_len_chk CHECK (length(thread_id) <= 255),
    CONSTRAINT receipts_user_id_len_chk CHECK (length(user_id) BETWEEN 1 AND 255)
);

CREATE INDEX receipts_tenant_id_idx ON receipts (tenant_id);
CREATE INDEX receipts_room_nid_idx ON receipts (room_nid);
CREATE INDEX receipts_stream_idx ON receipts (tenant_id, room_nid, stream_id);

ALTER TABLE tenants ADD COLUMN presence_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE user_presence (
    tenant_id      UUID        NOT NULL,
    user_id        TEXT        NOT NULL,
    presence       TEXT        NOT NULL,
    status_msg     TEXT        NOT NULL DEFAULT '',
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT user_presence_state_chk CHECK (presence IN ('online', 'unavailable', 'offline')),
    CONSTRAINT user_presence_status_msg_len_chk CHECK (length(status_msg) <= 2048)
);

CREATE INDEX user_presence_tenant_id_idx ON user_presence (tenant_id);

-- +goose Down
DROP TABLE user_presence;
ALTER TABLE tenants DROP COLUMN presence_enabled;
DROP TABLE receipts;
DROP SEQUENCE receipts_stream_seq;
