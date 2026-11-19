-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_shares (
  id VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  owner_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  path VARCHAR(4096) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  is_dir TINYINT(1) NOT NULL,
  password_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  expire_at BIGINT NOT NULL DEFAULT 0,
  max_downloads INT NOT NULL DEFAULT 0,
  downloads INT NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL,
  INDEX easycloud_shares_owner (owner_id, created_at),
  INDEX easycloud_shares_path (path(512))
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_app_passwords (
  id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  user_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  name VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  nt_hash CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_at BIGINT NOT NULL,
  last_used_at BIGINT NOT NULL DEFAULT 0,
  INDEX easycloud_app_passwords_user (user_id)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS easycloud_app_passwords;
DROP TABLE IF EXISTS easycloud_shares;
