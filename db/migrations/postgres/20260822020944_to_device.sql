-- +goose Up
CREATE SEQUENCE to_device_stream_seq;
CREATE SEQUENCE device_list_stream_seq;

CREATE TABLE to_device_messages (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    stream_id  BIGINT      NOT NULL,
    sender     TEXT        NOT NULL,
    event_type TEXT        NOT NULL,
    content    BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, device_id, stream_id),
    FOREIGN KEY (tenant_id, user_id, device_id)
        REFERENCES devices (tenant_id, user_id, device_id) ON DELETE CASCADE,
    CONSTRAINT to_device_messages_type_len_chk CHECK (length(event_type) BETWEEN 1 AND 255),
    CONSTRAINT to_device_messages_content_len_chk CHECK (length(content) BETWEEN 1 AND 65536)
);

CREATE INDEX to_device_messages_tenant_id_idx ON to_device_messages (tenant_id);
CREATE INDEX to_device_messages_created_at_idx ON to_device_messages (created_at);

CREATE TABLE to_device_transactions (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    txn_id     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, device_id, txn_id),
    FOREIGN KEY (tenant_id, user_id, device_id)
        REFERENCES devices (tenant_id, user_id, device_id) ON DELETE CASCADE,
    CONSTRAINT to_device_transactions_txn_id_len_chk CHECK (length(txn_id) BETWEEN 1 AND 255)
);

CREATE INDEX to_device_transactions_tenant_id_idx ON to_device_transactions (tenant_id);
CREATE INDEX to_device_transactions_created_at_idx ON to_device_transactions (created_at);

CREATE TABLE device_list_changes (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    stream_id  BIGINT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, stream_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE
);

CREATE INDEX device_list_changes_tenant_id_idx ON device_list_changes (tenant_id);
CREATE INDEX device_list_changes_stream_idx ON device_list_changes (tenant_id, stream_id);

ALTER TABLE sync_connections
    ADD COLUMN confirmed_account_data BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN pending_account_data   BIGINT,
    ADD COLUMN confirmed_receipts     BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN pending_receipts       BIGINT,
    ADD COLUMN confirmed_device_lists BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN pending_device_lists   BIGINT,
    ADD COLUMN confirmed_typing       BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN pending_typing         BIGINT;

-- +goose Down
ALTER TABLE sync_connections
    DROP COLUMN pending_typing,
    DROP COLUMN confirmed_typing,
    DROP COLUMN pending_device_lists,
    DROP COLUMN confirmed_device_lists,
    DROP COLUMN pending_receipts,
    DROP COLUMN confirmed_receipts,
    DROP COLUMN pending_account_data,
    DROP COLUMN confirmed_account_data;

DROP TABLE device_list_changes;
DROP TABLE to_device_transactions;
DROP TABLE to_device_messages;
DROP SEQUENCE device_list_stream_seq;
DROP SEQUENCE to_device_stream_seq;
