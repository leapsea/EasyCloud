-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_shares (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  path TEXT NOT NULL,
  is_dir INTEGER NOT NULL,
  password_hash TEXT NOT NULL DEFAULT '',
  expire_at INTEGER NOT NULL DEFAULT 0,
  max_downloads INTEGER NOT NULL DEFAULT 0,
  downloads INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS easycloud_shares_owner ON easycloud_shares(owner_id, created_at);
CREATE INDEX IF NOT EXISTS easycloud_shares_path ON easycloud_shares(path);

CREATE TABLE IF NOT EXISTS easycloud_app_passwords (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  hash TEXT NOT NULL,
  nt_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS easycloud_app_passwords_user ON easycloud_app_passwords(user_id);

-- +goose Down
DROP TABLE easycloud_app_passwords;
DROP TABLE easycloud_shares;
