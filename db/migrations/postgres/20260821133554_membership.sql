-- +goose Up
ALTER TABLE room_memberships ADD COLUMN forgotten BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE room_memberships DROP COLUMN forgotten;
