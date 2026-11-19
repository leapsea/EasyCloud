-- Path columns use utf8mb4_bin: Linux file names are case-sensitive.
-- Every statement is idempotent because MariaDB DDL commits implicitly and cannot roll back.
-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_instance (
  id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  created_at BIGINT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_settings (
  name VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  value TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  updated_by VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  updated_at BIGINT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_admin (
  id INT PRIMARY KEY,
  password_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_sessions (
  token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  expires_at BIGINT NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_trash (
  id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  owner_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  origin_path VARCHAR(4096) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  trash_path VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  is_dir TINYINT(1) NOT NULL,
  size BIGINT NOT NULL,
  deleted_at BIGINT NOT NULL,
  expire_at BIGINT NOT NULL,
  INDEX easycloud_trash_owner (owner_id, deleted_at),
  INDEX easycloud_trash_expire (expire_at)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS easycloud_trash;
DROP TABLE IF EXISTS easycloud_sessions;
DROP TABLE IF EXISTS easycloud_admin;
DROP TABLE IF EXISTS easycloud_settings;
DROP TABLE IF EXISTS easycloud_instance;
