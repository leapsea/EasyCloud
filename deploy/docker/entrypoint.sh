#!/bin/sh
# Two ways to pick the user files are written as:
#   user: "1000:2000" in compose -> we already run as that user (recommended)
#   started as root               -> drop to PUID:PGID
set -e

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$EASYCLOUD_APP_DATA"
  chown "$PUID:$PGID" "$EASYCLOUD_APP_DATA"
  [ -d "$EASYCLOUD_LOG_DIR" ] && chown "$PUID:$PGID" "$EASYCLOUD_LOG_DIR"
  exec su-exec "$PUID:$PGID" /usr/local/bin/easycloud "$@"
fi

# Non-root: we cannot fix ownership ourselves, so explain instead of failing later
# with a bare "permission denied". (Only for serve; CLI commands report their own errors.)
if [ "${1:-serve}" = "serve" ]; then
  for d in "$EASYCLOUD_DATA_ROOT" "$EASYCLOUD_APP_DATA"; do
    if [ ! -w "$d" ]; then
      echo "EasyCloud runs as $(id -u):$(id -g) but cannot write to $d." >&2
      echo "Fix on the host: sudo chown -R $(id -u):$(id -g) <the directory mounted at $d>" >&2
      exit 78
    fi
  done
fi
exec /usr/local/bin/easycloud "$@"
