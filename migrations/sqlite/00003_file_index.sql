-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_files (
  path_hash TEXT PRIMARY KEY,
  path TEXT NOT NULL,
  parent_hash TEXT NOT NULL,
  name TEXT NOT NULL,
  is_dir INTEGER NOT NULL,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL,
  dev INTEGER NOT NULL,
  inode INTEGER NOT NULL,
  mime TEXT NOT NULL DEFAULT '',
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  scan_gen INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS easycloud_files_path ON easycloud_files(path);
CREATE INDEX IF NOT EXISTS easycloud_files_parent ON easycloud_files(parent_hash);
CREATE INDEX IF NOT EXISTS easycloud_files_inode ON easycloud_files(dev, inode);
CREATE INDEX IF NOT EXISTS easycloud_files_name ON easycloud_files(name);

-- +goose Down
DROP TABLE easycloud_files;
