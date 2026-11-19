-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'user',
  disabled INTEGER NOT NULL DEFAULT 0,
  quota_bytes INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS easycloud_groups (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  parent_id TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS easycloud_groups_parent ON easycloud_groups(parent_id);
CREATE TABLE IF NOT EXISTS easycloud_group_members (
  group_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS easycloud_group_members_user ON easycloud_group_members(user_id);
CREATE TABLE IF NOT EXISTS easycloud_grants (
  id TEXT PRIMARY KEY,
  subject_type TEXT NOT NULL,
  subject_id TEXT NOT NULL,
  path TEXT NOT NULL,
  perm INTEGER NOT NULL,
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  UNIQUE (subject_type, subject_id, path)
);
CREATE INDEX IF NOT EXISTS easycloud_grants_subject ON easycloud_grants(subject_type, subject_id);
CREATE TABLE IF NOT EXISTS easycloud_audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  user_id TEXT NOT NULL,
  username TEXT NOT NULL,
  action TEXT NOT NULL,
  path TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS easycloud_audit_at ON easycloud_audit(at);
CREATE INDEX IF NOT EXISTS easycloud_audit_user ON easycloud_audit(user_id, at);

-- Sessions now belong to a user; V1 sessions belonged to the single admin.
ALTER TABLE easycloud_sessions ADD COLUMN user_id TEXT NOT NULL DEFAULT 'admin';

-- Carry the V1 administrator over (id 'admin' keeps existing trash records valid).
INSERT INTO easycloud_users (id, username, display_name, password_hash, role, created_at, updated_at)
SELECT 'admin', 'admin', '管理员', password_hash, 'admin', CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER)
FROM easycloud_admin WHERE id = 1
ON CONFLICT(id) DO NOTHING;

-- +goose Down
DROP TABLE easycloud_audit; DROP TABLE easycloud_grants; DROP TABLE easycloud_group_members;
DROP TABLE easycloud_groups; DROP TABLE easycloud_users;
