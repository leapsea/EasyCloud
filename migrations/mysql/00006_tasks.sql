-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_tasks (
  id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  owner_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  params MEDIUMTEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  done_bytes BIGINT NOT NULL DEFAULT 0,
  total_bytes BIGINT NOT NULL DEFAULT 0,
  message TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  result TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  INDEX easycloud_tasks_owner (owner_id, created_at),
  INDEX easycloud_tasks_status (status)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS easycloud_tasks;
