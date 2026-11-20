-- +goose Up
-- One row per user who started or finished binding an authenticator app.
-- secret: the active TOTP secret ('' until enabled); pending: a secret being
-- set up, confirmed by the first valid code; last_step: the newest time step
-- accepted, so a code cannot be used twice.
CREATE TABLE IF NOT EXISTS easycloud_totp (
  user_id TEXT PRIMARY KEY,
  secret TEXT NOT NULL DEFAULT '',
  pending TEXT NOT NULL DEFAULT '',
  last_step INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS easycloud_totp;
