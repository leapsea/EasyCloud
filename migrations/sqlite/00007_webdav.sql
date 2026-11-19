-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_dav_configs (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  slug TEXT NOT NULL UNIQUE,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS easycloud_dav_dirs (
  config_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  path TEXT NOT NULL,
  alias TEXT NOT NULL DEFAULT '',
  perm INTEGER NOT NULL,
  PRIMARY KEY (config_id, position)
);

-- +goose Down
DROP TABLE easycloud_dav_dirs;
DROP TABLE easycloud_dav_configs;
