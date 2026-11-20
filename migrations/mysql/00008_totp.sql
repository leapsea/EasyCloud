-- +goose Up
-- One row per user who started or finished binding an authenticator app.
-- secret: the active TOTP secret ('' until enabled); pending: a secret being
-- set up, confirmed by the first valid code; last_step: the newest time step
-- accepted, so a code cannot be used twice.
CREATE TABLE IF NOT EXISTS easycloud_totp (
  user_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  secret VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  pending VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
  last_step BIGINT NOT NULL DEFAULT 0,
  updated_at BIGINT NOT NULL
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE IF EXISTS easycloud_totp;
