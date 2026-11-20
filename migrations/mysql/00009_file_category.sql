-- +goose Up
-- Derived columns for the category views, so they use an index instead of
-- matching dozens of LIKE patterns against every row. category is NULL for
-- rows indexed before this migration until the background backfill fills it
-- ('' = no category, directories included); ext is the lower-case extension.
ALTER TABLE easycloud_files
  ADD COLUMN category VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL,
  ADD COLUMN ext VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  ADD INDEX easycloud_files_category (category, mtime);

-- +goose Down
ALTER TABLE easycloud_files DROP INDEX easycloud_files_category, DROP COLUMN ext, DROP COLUMN category;
