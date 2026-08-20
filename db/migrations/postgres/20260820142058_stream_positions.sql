-- +goose Up
CREATE TABLE stream_positions (
    stream_name   TEXT   NOT NULL,
    instance_name TEXT   NOT NULL,
    stream_id     BIGINT NOT NULL,
    PRIMARY KEY (stream_name, instance_name)
);

CREATE SEQUENCE events_stream_seq;
CREATE SEQUENCE events_backfill_stream_seq;

-- +goose Down
DROP SEQUENCE events_backfill_stream_seq;
DROP SEQUENCE events_stream_seq;
DROP TABLE stream_positions;
