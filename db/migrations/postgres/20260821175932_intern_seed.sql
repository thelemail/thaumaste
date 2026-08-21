-- +goose Up
INSERT INTO event_types (event_type) VALUES
    ('m.room.create'),
    ('m.room.member'),
    ('m.room.power_levels'),
    ('m.room.join_rules'),
    ('m.room.history_visibility'),
    ('m.room.third_party_invite'),
    ('m.room.redaction'),
    ('m.room.message'),
    ('m.room.encryption'),
    ('m.room.name'),
    ('m.room.topic'),
    ('m.room.avatar'),
    ('m.room.canonical_alias'),
    ('m.room.guest_access'),
    ('m.room.tombstone'),
    ('m.room.server_acl'),
    ('m.reaction')
ON CONFLICT (event_type) DO NOTHING;

INSERT INTO event_state_keys (event_state_key) VALUES ('')
ON CONFLICT (event_state_key) DO NOTHING;

-- +goose Down
DELETE FROM event_state_keys
 WHERE event_state_key = ''
   AND NOT EXISTS (SELECT 1 FROM events WHERE events.event_state_key_nid = event_state_keys.event_state_key_nid);

DELETE FROM event_types
 WHERE (event_type LIKE 'm.room.%' OR event_type = 'm.reaction')
   AND NOT EXISTS (SELECT 1 FROM events WHERE events.event_type_nid = event_types.event_type_nid);
