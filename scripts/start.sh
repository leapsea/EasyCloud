#!/usr/bin/env bash
# Builds the frontend and backend, then starts EasyCloud in the foreground.
#
#   scripts/start.sh                 # build web + server, listen on 127.0.0.1:8090
#   SKIP_WEB=1 scripts/start.sh      # reuse the existing web/dist
#   LISTEN=0.0.0.0:8080 scripts/start.sh
#
# Data lives in .dev/ by default; override with DATA_ROOT / APP_DATA.
set -euo pipefail
cd "$(dirname "$0")/.."

DATA_ROOT=${DATA_ROOT:-.dev/data}
APP_DATA=${APP_DATA:-.dev/app}
LISTEN=${LISTEN:-127.0.0.1:8090}
BIN=.dev/easycloud

if [ -z "${SKIP_WEB:-}" ]; then
  (cd web && npm run build)
fi
go build -o "$BIN" ./cmd/easycloud
mkdir -p "$DATA_ROOT" "$APP_DATA"

# Local plugin signing key, so dev-built plugins can be installed.
if [ -f .dev/plugin-dev.pub ]; then
  export EASYCLOUD_PLUGINS_TRUSTED_KEYS="$(cat .dev/plugin-dev.pub)"
fi

export EASYCLOUD_DATA_ROOT="$DATA_ROOT" EASYCLOUD_APP_DATA="$APP_DATA" EASYCLOUD_HTTP_LISTEN="$LISTEN"
echo "EasyCloud: http://$LISTEN"
exec "$BIN" serve
