-- +goose Up
-- Derived columns for the category views, so they use an index instead of
-- matching dozens of LIKE patterns against every row. category is NULL for
-- rows indexed before this migration until the background backfill fills it
-- ('' = no category, directories included); ext is the lower-case extension.
ALTER TABLE easycloud_files ADD COLUMN category TEXT DEFAULT NULL;
ALTER TABLE easycloud_files ADD COLUMN ext TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS easycloud_files_category ON easycloud_files(category, mtime);

-- +goose Down
DROP INDEX IF EXISTS easycloud_files_category;
ALTER TABLE easycloud_files DROP COLUMN ext;
ALTER TABLE easycloud_files DROP COLUMN category;
