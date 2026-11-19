-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_files (
  path_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  path VARCHAR(4096) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  parent_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  is_dir TINYINT(1) NOT NULL,
  size BIGINT NOT NULL,
  mtime BIGINT NOT NULL,
  dev BIGINT UNSIGNED NOT NULL,
  inode BIGINT UNSIGNED NOT NULL,
  mime VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  width INT NOT NULL DEFAULT 0,
  height INT NOT NULL DEFAULT 0,
  scan_gen BIGINT NOT NULL DEFAULT 0,
  INDEX easycloud_files_path (path(512)),
  INDEX easycloud_files_parent (parent_hash),
  INDEX easycloud_files_inode (dev, inode),
  INDEX easycloud_files_name (name)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS easycloud_files;
