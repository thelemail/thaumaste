-- +goose Up
CREATE TABLE event_relations (
    child_nid       BIGINT NOT NULL PRIMARY KEY REFERENCES events (event_nid) ON DELETE CASCADE,
    room_nid        BIGINT NOT NULL REFERENCES rooms (room_nid) ON DELETE CASCADE,
    parent_id       TEXT   NOT NULL,
    rel_type        TEXT   NOT NULL,
    sender          TEXT   NOT NULL,
    event_type_nid  BIGINT NOT NULL REFERENCES event_types (event_type_nid),
    aggregation_key TEXT   NOT NULL DEFAULT '',
    CONSTRAINT event_relations_parent_id_len_chk CHECK (length(parent_id) BETWEEN 1 AND 255),
    CONSTRAINT event_relations_rel_type_len_chk CHECK (length(rel_type) BETWEEN 1 AND 255),
    CONSTRAINT event_relations_sender_len_chk CHECK (length(sender) BETWEEN 1 AND 255),
    CONSTRAINT event_relations_key_len_chk CHECK (length(aggregation_key) <= 255)
);

CREATE INDEX event_relations_parent_idx ON event_relations (room_nid, parent_id, rel_type);
CREATE INDEX event_relations_rel_type_idx ON event_relations (room_nid, rel_type);
CREATE UNIQUE INDEX event_relations_annotation_uidx
    ON event_relations (room_nid, parent_id, sender, event_type_nid, aggregation_key)
    WHERE rel_type = 'm.annotation';

ALTER TABLE events
    ADD COLUMN redacted_by_nid BIGINT REFERENCES events (event_nid) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE events DROP COLUMN redacted_by_nid;

DROP TABLE event_relations;
