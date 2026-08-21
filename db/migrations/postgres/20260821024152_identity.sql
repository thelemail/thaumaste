-- +goose Up
CREATE TABLE users (
    tenant_id      UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id        TEXT        NOT NULL,
    localpart      TEXT        NOT NULL,
    display_name   TEXT        NOT NULL DEFAULT '',
    avatar_url     TEXT        NOT NULL DEFAULT '',
    deactivated_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id),
    CONSTRAINT users_user_id_len_chk CHECK (length(user_id) BETWEEN 1 AND 255),
    CONSTRAINT users_localpart_len_chk CHECK (length(localpart) BETWEEN 1 AND 255),
    CONSTRAINT users_display_name_len_chk CHECK (length(display_name) <= 256),
    CONSTRAINT users_avatar_url_len_chk CHECK (length(avatar_url) <= 1024)
);

CREATE UNIQUE INDEX users_tenant_localpart_uidx ON users (tenant_id, localpart);
CREATE INDEX users_tenant_id_idx ON users (tenant_id);

CREATE TABLE user_credentials (
    tenant_id  UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    algorithm  TEXT        NOT NULL,
    params     TEXT        NOT NULL,
    salt       BYTEA       NOT NULL,
    hash       BYTEA       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT user_credentials_algorithm_chk CHECK (algorithm IN ('argon2id')),
    CONSTRAINT user_credentials_salt_len_chk CHECK (length(salt) BETWEEN 16 AND 64),
    CONSTRAINT user_credentials_hash_len_chk CHECK (length(hash) BETWEEN 16 AND 64)
);

CREATE INDEX user_credentials_tenant_id_idx ON user_credentials (tenant_id);

CREATE TABLE devices (
    tenant_id    UUID        NOT NULL,
    user_id      TEXT        NOT NULL,
    device_id    TEXT        NOT NULL,
    display_name TEXT        NOT NULL DEFAULT '',
    last_seen_ip TEXT        NOT NULL DEFAULT '',
    last_seen_ts TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, device_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, user_id) ON DELETE CASCADE,
    CONSTRAINT devices_device_id_len_chk CHECK (length(device_id) BETWEEN 1 AND 255),
    CONSTRAINT devices_display_name_len_chk CHECK (length(display_name) <= 256)
);

CREATE INDEX devices_tenant_id_idx ON devices (tenant_id);
CREATE INDEX devices_tenant_user_idx ON devices (tenant_id, user_id);

CREATE TABLE refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    token_hash BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    used_at    TIMESTAMPTZ,
    CONSTRAINT refresh_tokens_hash_len_chk CHECK (length(token_hash) = 32)
);

CREATE UNIQUE INDEX refresh_tokens_hash_uidx ON refresh_tokens (token_hash);
CREATE INDEX refresh_tokens_tenant_id_idx ON refresh_tokens (tenant_id);
CREATE INDEX refresh_tokens_device_idx ON refresh_tokens (tenant_id, user_id, device_id);

CREATE TABLE uia_sessions (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL,
    user_id    TEXT        NOT NULL DEFAULT '',
    completed  TEXT[]      NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT uia_sessions_kind_chk
        CHECK (kind IN ('register', 'password', 'deactivate', 'delete_device'))
);

CREATE INDEX uia_sessions_tenant_id_idx ON uia_sessions (tenant_id);
CREATE INDEX uia_sessions_expires_at_idx ON uia_sessions (expires_at);

CREATE TABLE auth_attempts (
    tenant_id         UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    subject           TEXT        NOT NULL,
    kind              TEXT        NOT NULL,
    failures          INTEGER     NOT NULL DEFAULT 0,
    window_started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until      TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, subject, kind),
    CONSTRAINT auth_attempts_kind_chk CHECK (kind IN ('login', 'register')),
    CONSTRAINT auth_attempts_subject_len_chk CHECK (length(subject) BETWEEN 1 AND 255)
);

CREATE INDEX auth_attempts_tenant_id_idx ON auth_attempts (tenant_id);

ALTER TABLE access_tokens
    ADD COLUMN device_id        TEXT   NOT NULL DEFAULT '',
    ADD COLUMN refresh_token_id UUID   REFERENCES refresh_tokens (id) ON DELETE SET NULL;

CREATE INDEX access_tokens_device_idx ON access_tokens (tenant_id, user_id, device_id);

ALTER TABLE tenants
    DROP CONSTRAINT tenants_registration_mode_chk,
    ADD CONSTRAINT tenants_registration_mode_chk
        CHECK (registration_mode IN ('closed', 'invite', 'open', 'external'));

-- +goose Down
ALTER TABLE tenants
    DROP CONSTRAINT tenants_registration_mode_chk,
    ADD CONSTRAINT tenants_registration_mode_chk
        CHECK (registration_mode IN ('closed', 'invite', 'open'));

DROP INDEX access_tokens_device_idx;
ALTER TABLE access_tokens
    DROP COLUMN refresh_token_id,
    DROP COLUMN device_id;

DROP TABLE auth_attempts;
DROP TABLE uia_sessions;
DROP TABLE refresh_tokens;
DROP TABLE devices;
DROP TABLE user_credentials;
DROP TABLE users;
