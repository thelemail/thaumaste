-- +goose Up
CREATE TABLE tenants (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    server_name         TEXT        NOT NULL,
    state               TEXT        NOT NULL DEFAULT 'active',
    registration_mode   TEXT        NOT NULL DEFAULT 'closed',
    encryption_required BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tenants_state_chk CHECK (state IN ('active', 'suspended')),
    CONSTRAINT tenants_registration_mode_chk CHECK (registration_mode IN ('closed', 'invite', 'open')),
    CONSTRAINT tenants_server_name_len_chk CHECK (length(server_name) BETWEEN 1 AND 230)
);

CREATE UNIQUE INDEX tenants_server_name_uidx ON tenants (server_name);

CREATE TABLE tenant_hosts (
    host       TEXT        PRIMARY KEY,
    tenant_id  UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tenant_hosts_host_len_chk CHECK (length(host) BETWEEN 1 AND 255)
);

CREATE INDEX tenant_hosts_tenant_id_idx ON tenant_hosts (tenant_id);

CREATE TABLE tenant_signing_keys (
    tenant_id   UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    key_id      TEXT        NOT NULL,
    public_key  BYTEA       NOT NULL,
    private_key BYTEA       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expired_at  TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, key_id),
    CONSTRAINT tenant_signing_keys_public_key_len_chk CHECK (length(public_key) = 32)
);

CREATE UNIQUE INDEX tenant_signing_keys_active_uidx
    ON tenant_signing_keys (tenant_id)
    WHERE expired_at IS NULL;

CREATE TABLE access_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    token_hash BYTEA       NOT NULL,
    user_id    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT access_tokens_token_hash_len_chk CHECK (length(token_hash) = 32)
);

CREATE UNIQUE INDEX access_tokens_token_hash_uidx ON access_tokens (token_hash);
CREATE INDEX access_tokens_tenant_id_idx ON access_tokens (tenant_id);
CREATE INDEX access_tokens_user_id_idx ON access_tokens (tenant_id, user_id);

-- +goose Down
DROP TABLE access_tokens;
DROP TABLE tenant_signing_keys;
DROP TABLE tenant_hosts;
DROP TABLE tenants;
