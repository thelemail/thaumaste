-- +goose Up
ALTER TABLE rooms
    ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private',
    ADD CONSTRAINT rooms_visibility_chk CHECK (visibility IN ('public', 'private'));

CREATE INDEX rooms_public_idx ON rooms (tenant_id) WHERE visibility = 'public';

CREATE TABLE room_aliases (
    tenant_id  UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    alias      TEXT        NOT NULL,
    room_nid   BIGINT      NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    creator    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, alias),
    CONSTRAINT room_aliases_alias_len_chk CHECK (length(alias) BETWEEN 3 AND 255),
    CONSTRAINT room_aliases_creator_len_chk CHECK (length(creator) BETWEEN 1 AND 255)
);

CREATE INDEX room_aliases_tenant_id_idx ON room_aliases (tenant_id);
CREATE INDEX room_aliases_room_nid_idx ON room_aliases (room_nid);

CREATE TABLE room_memberships (
    tenant_id  UUID   NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    room_nid   BIGINT NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    user_id    TEXT   NOT NULL,
    membership TEXT   NOT NULL,
    event_nid  BIGINT NOT NULL REFERENCES events (event_nid) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, room_nid, user_id),
    CONSTRAINT room_memberships_membership_chk
        CHECK (membership IN ('join', 'invite', 'leave', 'ban', 'knock')),
    CONSTRAINT room_memberships_user_id_len_chk CHECK (length(user_id) BETWEEN 1 AND 255)
);

CREATE INDEX room_memberships_tenant_id_idx ON room_memberships (tenant_id);
CREATE INDEX room_memberships_user_idx ON room_memberships (tenant_id, user_id, membership);
CREATE INDEX room_memberships_room_idx ON room_memberships (room_nid, membership);

-- +goose Down
DROP TABLE room_memberships;
DROP TABLE room_aliases;

DROP INDEX rooms_public_idx;
ALTER TABLE rooms
    DROP CONSTRAINT rooms_visibility_chk,
    DROP COLUMN visibility;
