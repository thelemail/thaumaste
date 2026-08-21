-- +goose Up
CREATE TABLE device_keys (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    key_json   BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, device_id),
    FOREIGN KEY (tenant_id, user_id, device_id)
        REFERENCES devices (tenant_id, user_id, device_id) ON DELETE CASCADE,
    CONSTRAINT device_keys_key_json_len_chk CHECK (length(key_json) BETWEEN 1 AND 65536)
);

CREATE INDEX device_keys_tenant_id_idx ON device_keys (tenant_id);

CREATE SEQUENCE device_one_time_keys_ordinal_seq;

CREATE TABLE device_one_time_keys (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    algorithm  TEXT        NOT NULL,
    key_id     TEXT        NOT NULL,
    key_json   BYTEA       NOT NULL,
    ordinal    BIGINT      NOT NULL DEFAULT nextval('device_one_time_keys_ordinal_seq'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, device_id, algorithm, key_id),
    FOREIGN KEY (tenant_id, user_id, device_id)
        REFERENCES devices (tenant_id, user_id, device_id) ON DELETE CASCADE,
    CONSTRAINT device_one_time_keys_algorithm_len_chk CHECK (length(algorithm) BETWEEN 1 AND 64),
    CONSTRAINT device_one_time_keys_key_id_len_chk CHECK (length(key_id) BETWEEN 1 AND 255),
    CONSTRAINT device_one_time_keys_key_json_len_chk CHECK (length(key_json) BETWEEN 1 AND 65536)
);

CREATE INDEX device_one_time_keys_tenant_id_idx ON device_one_time_keys (tenant_id);
CREATE INDEX device_one_time_keys_claim_idx
    ON device_one_time_keys (tenant_id, user_id, device_id, algorithm, ordinal);

CREATE TABLE device_fallback_keys (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    algorithm  TEXT        NOT NULL,
    key_id     TEXT        NOT NULL,
    key_json   BYTEA       NOT NULL,
    used       BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, device_id, algorithm),
    FOREIGN KEY (tenant_id, user_id, device_id)
        REFERENCES devices (tenant_id, user_id, device_id) ON DELETE CASCADE,
    CONSTRAINT device_fallback_keys_algorithm_len_chk CHECK (length(algorithm) BETWEEN 1 AND 64),
    CONSTRAINT device_fallback_keys_key_id_len_chk CHECK (length(key_id) BETWEEN 1 AND 255),
    CONSTRAINT device_fallback_keys_key_json_len_chk CHECK (length(key_json) BETWEEN 1 AND 65536)
);

CREATE INDEX device_fallback_keys_tenant_id_idx ON device_fallback_keys (tenant_id);

-- +goose Down
DROP TABLE device_fallback_keys;
DROP TABLE device_one_time_keys;
DROP SEQUENCE device_one_time_keys_ordinal_seq;
DROP TABLE device_keys;
