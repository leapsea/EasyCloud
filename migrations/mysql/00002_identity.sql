-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_users (
  id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  username VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  display_name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT '',
  password_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  role VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'user',
  disabled TINYINT(1) NOT NULL DEFAULT 0,
  quota_bytes BIGINT NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE KEY easycloud_users_username (username)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_groups (
  id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  parent_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  created_at BIGINT NOT NULL,
  INDEX easycloud_groups_parent (parent_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_group_members (
  group_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  PRIMARY KEY (group_id, user_id),
  INDEX easycloud_group_members_user (user_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_grants (
  id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  subject_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  subject_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  path VARCHAR(3000) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  perm TINYINT NOT NULL,
  created_by VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  INDEX easycloud_grants_subject (subject_type, subject_id),
  UNIQUE KEY easycloud_grants_unique (subject_type, subject_id, path(700))
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_audit (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  at BIGINT NOT NULL,
  user_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  username VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  action VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  path VARCHAR(4096) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  detail TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  ip VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  INDEX easycloud_audit_at (at),
  INDEX easycloud_audit_user (user_id, at)
) ENGINE=InnoDB;

ALTER TABLE easycloud_sessions ADD COLUMN IF NOT EXISTS user_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'admin';

INSERT IGNORE INTO easycloud_users (id, username, display_name, password_hash, role, created_at, updated_at)
SELECT 'admin', 'admin', '管理员', password_hash, 'admin', UNIX_TIMESTAMP(), UNIX_TIMESTAMP()
FROM easycloud_admin WHERE id = 1;

-- +goose Down
DROP TABLE IF EXISTS easycloud_audit;
DROP TABLE IF EXISTS easycloud_grants;
DROP TABLE IF EXISTS easycloud_group_members;
DROP TABLE IF EXISTS easycloud_groups;
DROP TABLE IF EXISTS easycloud_users;
