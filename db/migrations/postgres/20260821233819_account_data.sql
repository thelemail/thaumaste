-- +goose Up
CREATE SEQUENCE account_data_stream_seq;

CREATE TABLE account_data (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    type       TEXT        NOT NULL,
    content    BYTEA       NOT NULL,
    stream_id  BIGINT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, type),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT account_data_type_len_chk CHECK (length(type) BETWEEN 1 AND 255),
    CONSTRAINT account_data_content_len_chk CHECK (length(content) BETWEEN 1 AND 65536)
);

CREATE INDEX account_data_tenant_id_idx ON account_data (tenant_id);
CREATE INDEX account_data_stream_idx ON account_data (tenant_id, user_id, stream_id);

CREATE TABLE room_account_data (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    room_nid   BIGINT      NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    type       TEXT        NOT NULL,
    content    BYTEA       NOT NULL,
    stream_id  BIGINT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, room_nid, type),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT room_account_data_type_len_chk CHECK (length(type) BETWEEN 1 AND 255),
    CONSTRAINT room_account_data_content_len_chk CHECK (length(content) BETWEEN 1 AND 65536)
);

CREATE INDEX room_account_data_tenant_id_idx ON room_account_data (tenant_id);
CREATE INDEX room_account_data_room_nid_idx ON room_account_data (room_nid);
CREATE INDEX room_account_data_stream_idx ON room_account_data (tenant_id, user_id, stream_id);

CREATE TABLE user_filters (
    tenant_id   UUID        NOT NULL,
    user_id     TEXT        NOT NULL,
    filter_id   TEXT        NOT NULL,
    filter_hash BYTEA       NOT NULL,
    filter      BYTEA       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, filter_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT user_filters_filter_id_len_chk CHECK (length(filter_id) BETWEEN 1 AND 64),
    CONSTRAINT user_filters_hash_len_chk CHECK (length(filter_hash) = 32),
    CONSTRAINT user_filters_filter_len_chk CHECK (length(filter) BETWEEN 1 AND 65536)
);

CREATE INDEX user_filters_tenant_id_idx ON user_filters (tenant_id);
CREATE UNIQUE INDEX user_filters_hash_uidx ON user_filters (tenant_id, user_id, filter_hash);

CREATE INDEX users_display_name_idx ON users (tenant_id, lower(display_name));
CREATE INDEX users_localpart_idx ON users (tenant_id, lower(localpart));

-- +goose Down
DROP INDEX users_localpart_idx;
DROP INDEX users_display_name_idx;
DROP TABLE user_filters;
DROP TABLE room_account_data;
DROP TABLE account_data;
DROP SEQUENCE account_data_stream_seq;
