-- +goose Up
CREATE TABLE IF NOT EXISTS easycloud_tasks (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  params TEXT NOT NULL,
  status TEXT NOT NULL,
  done_bytes INTEGER NOT NULL DEFAULT 0,
  total_bytes INTEGER NOT NULL DEFAULT 0,
  message TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS easycloud_tasks_owner ON easycloud_tasks(owner_id, created_at);
CREATE INDEX IF NOT EXISTS easycloud_tasks_status ON easycloud_tasks(status);

-- +goose Down
DROP TABLE easycloud_tasks;
