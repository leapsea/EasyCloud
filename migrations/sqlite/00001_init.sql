-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_instance (id TEXT PRIMARY KEY, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS easycloud_settings (name TEXT PRIMARY KEY, value TEXT NOT NULL, updated_by TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS easycloud_admin (id INTEGER PRIMARY KEY CHECK (id = 1), password_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS easycloud_sessions (token_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS easycloud_trash (
  id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, origin_path TEXT NOT NULL, trash_path TEXT NOT NULL,
  is_dir INTEGER NOT NULL, size INTEGER NOT NULL, deleted_at INTEGER NOT NULL, expire_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS easycloud_trash_owner ON easycloud_trash(owner_id, deleted_at);
CREATE INDEX IF NOT EXISTS easycloud_trash_expire ON easycloud_trash(expire_at);

-- +goose Down
DROP TABLE easycloud_trash; DROP TABLE easycloud_sessions; DROP TABLE easycloud_admin;
DROP TABLE easycloud_settings; DROP TABLE easycloud_instance;
