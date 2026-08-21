-- +goose Up
CREATE TABLE event_transactions (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    endpoint   TEXT        NOT NULL,
    room_id    TEXT        NOT NULL,
    txn_id     TEXT        NOT NULL,
    event_id   TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, device_id, endpoint, room_id, txn_id),
    FOREIGN KEY (tenant_id, user_id, device_id)
        REFERENCES devices (tenant_id, user_id, device_id) ON DELETE CASCADE,
    CONSTRAINT event_transactions_endpoint_len_chk CHECK (length(endpoint) BETWEEN 1 AND 64),
    CONSTRAINT event_transactions_room_id_len_chk CHECK (length(room_id) BETWEEN 1 AND 255),
    CONSTRAINT event_transactions_txn_id_len_chk CHECK (length(txn_id) BETWEEN 1 AND 255),
    CONSTRAINT event_transactions_event_id_len_chk CHECK (length(event_id) BETWEEN 1 AND 255)
);

CREATE INDEX event_transactions_tenant_id_idx ON event_transactions (tenant_id);
CREATE INDEX event_transactions_event_id_idx ON event_transactions (event_id);
CREATE INDEX event_transactions_created_at_idx ON event_transactions (created_at);

-- +goose Down
DROP TABLE event_transactions;
