-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_dav_configs (
  id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  name VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  slug VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  username VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  password_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_by VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE KEY easycloud_dav_configs_slug (slug),
  UNIQUE KEY easycloud_dav_configs_username (username)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS easycloud_dav_dirs (
  config_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  position INT NOT NULL,
  path VARCHAR(3000) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  alias VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  perm TINYINT NOT NULL,
  PRIMARY KEY (config_id, position)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS easycloud_dav_dirs;
DROP TABLE IF EXISTS easycloud_dav_configs;
