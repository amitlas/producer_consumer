-- migrate:up
ALTER TABLE tasks ADD COLUMN comment TEXT;

-- migrate:down
ALTER TABLE tasks DROP COLUMN comment;
