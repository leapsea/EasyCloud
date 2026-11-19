-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_plugins (
  id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  config MEDIUMTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  manifest MEDIUMTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  signed_by VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  installed_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS easycloud_plugins;
