#!/bin/sh
# EasyCloud installer / upgrader for Linux (systemd).
#
#   curl -fsSL https://github.com/leapsea/easycloud/releases/latest/download/install.sh | sudo sh
#
# Environment overrides:
#   EASYCLOUD_VERSION   release tag to install (default: latest)
#   EASYCLOUD_REPO      GitHub owner/repo (default: leapsea/easycloud)
#   EASYCLOUD_DATA_ROOT data directory for a fresh install (default: prompt, /srv/easycloud/data)
#   EASYCLOUD_BINARY    install this local binary instead of downloading
#   EASYCLOUD_NONINTERACTIVE=1  never prompt
# Flags: --uninstall (keeps data and configuration)
set -eu

REPO=${EASYCLOUD_REPO:-leapsea/easycloud}
VERSION=${EASYCLOUD_VERSION:-latest}
BIN=/usr/local/bin/easycloud
CONF_DIR=/etc/easycloud
ENV_FILE=$CONF_DIR/.env
APP_DATA=/var/lib/easycloud
UNIT=/etc/systemd/system/easycloud.service
SVC_USER=easycloud

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }
# Interactive when a terminal is reachable (also under "curl | sh", via /dev/tty).
interactive() { [ -z "${EASYCLOUD_NONINTERACTIVE:-}" ] && (: </dev/tty) 2>/dev/null; }
prompt() { # prompt "question" "default"
  if interactive; then printf '%s [%s]: ' "$1" "$2" >/dev/tty; read -r ans </dev/tty || ans=""; echo "${ans:-$2}"; else echo "$2"; fi
}

[ "$(id -u)" -eq 0 ] || die "please run as root (sudo)"
[ "$(uname -s)" = "Linux" ] || die "this installer supports Linux only; use Docker elsewhere"
command -v systemctl >/dev/null 2>&1 || die "systemd is required (or use the Docker image)"

if [ "${1:-}" = "--uninstall" ]; then
  say "stopping and removing the service (data and $CONF_DIR are kept)"
  systemctl disable --now easycloud 2>/dev/null || true
  rm -f "$UNIT" "$BIN" "$BIN.prev"
  systemctl daemon-reload
  say "uninstalled. Remove $CONF_DIR, $APP_DATA and your data directory manually if you no longer need them."
  exit 0
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m) (amd64 and arm64 are supported)" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# ---- obtain the binary ----
if [ -n "${EASYCLOUD_BINARY:-}" ]; then
  cp "$EASYCLOUD_BINARY" "$tmp/easycloud"
else
  if [ "$VERSION" = latest ]; then base="https://github.com/$REPO/releases/latest/download"; else base="https://github.com/$REPO/releases/download/$VERSION"; fi
  asset="easycloud-linux-$ARCH.tar.gz"
  say "downloading $asset ($VERSION)"
  fetch() { if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"; else wget -qO "$2" "$1"; fi; }
  fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
  fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: checksums.txt"
  (cd "$tmp" && grep " $asset\$" checksums.txt | sha256sum -c -) >/dev/null || die "checksum mismatch for $asset"
  tar -xzf "$tmp/$asset" -C "$tmp" easycloud
fi
chmod 755 "$tmp/easycloud"
NEW_VERSION=$("$tmp/easycloud" version)

UPGRADE=0
[ -x "$BIN" ] && UPGRADE=1

# ---- user, directories, configuration ----
if ! getent group "$SVC_USER" >/dev/null 2>&1; then
  groupadd --system "$SVC_USER" 2>/dev/null || addgroup -S "$SVC_USER"
fi
if ! id "$SVC_USER" >/dev/null 2>&1; then
  say "creating system user $SVC_USER"
  useradd --system --gid "$SVC_USER" --home-dir "$APP_DATA" --no-create-home --shell /usr/sbin/nologin "$SVC_USER" 2>/dev/null \
    || adduser -S -H -h "$APP_DATA" -G "$SVC_USER" -s /sbin/nologin "$SVC_USER"
fi
install -d -m 0750 -o "$SVC_USER" -g "$SVC_USER" "$APP_DATA"
# Owned by the service user: "easycloud install" may switch the driver to SQLite in .env.
install -d -m 0750 -o "$SVC_USER" -g "$SVC_USER" "$CONF_DIR"

if [ ! -f "$ENV_FILE" ]; then
  DATA_ROOT=${EASYCLOUD_DATA_ROOT:-$(prompt "data directory (your files)" /srv/easycloud/data)}
  install -d -o "$SVC_USER" -g "$SVC_USER" "$DATA_ROOT"
  ADMIN_PW=$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 16)
  umask 027
  cat >"$ENV_FILE" <<CONF
# EasyCloud configuration. Restart after editing: systemctl restart easycloud
EASYCLOUD_DATA_ROOT=$DATA_ROOT
EASYCLOUD_APP_DATA=$APP_DATA
EASYCLOUD_HTTP_LISTEN=:8080
# Initial password of the "admin" account (used only when no user exists yet).
EASYCLOUD_ADMIN_PASSWORD=$ADMIN_PW

# MariaDB 10.11+ (leave commented to use the built-in SQLite database):
# EASYCLOUD_DB_HOST=127.0.0.1
# EASYCLOUD_DB_PORT=3306
# EASYCLOUD_DB_USER=easycloud
# EASYCLOUD_DB_PASSWORD=
# EASYCLOUD_DB_NAME=easycloud
# EASYCLOUD_DB_CONNECT_TIMEOUT=60

# Plugins: comma-separated base64 ed25519 public keys you trust.
# EASYCLOUD_PLUGINS_TRUSTED_KEYS=
CONF
  chown "$SVC_USER:$SVC_USER" "$ENV_FILE"
  chmod 0640 "$ENV_FILE"
  say "wrote $ENV_FILE"
else
  DATA_ROOT=$(sed -n 's/^EASYCLOUD_DATA_ROOT=//p' "$ENV_FILE" | tail -n1)
  [ -n "$DATA_ROOT" ] || die "EASYCLOUD_DATA_ROOT missing in $ENV_FILE"
fi

# ---- stop for upgrade, then install the binary (keeping the previous one) ----
if [ "$UPGRADE" -eq 1 ]; then
  OLD_VERSION=$("$BIN" version 2>/dev/null || echo unknown)
  say "upgrading $OLD_VERSION -> $NEW_VERSION"
  systemctl stop easycloud 2>/dev/null || true
  cp -p "$BIN" "$BIN.prev"
fi
install -m 0755 "$tmp/easycloud" "$BIN"

# ---- database check (MariaDB connection, three-way choice, existing-data compatibility) ----
say "checking the database"
check_db() {
  if interactive; then
    su -s /bin/sh "$SVC_USER" -c "$BIN install --env $ENV_FILE" </dev/tty
  else
    su -s /bin/sh "$SVC_USER" -c "$BIN install --env $ENV_FILE --non-interactive"
  fi
}
if ! check_db; then
  if [ "$UPGRADE" -eq 1 ] && [ -f "$BIN.prev" ]; then
    warn "database check failed; restoring the previous binary"
    mv "$BIN.prev" "$BIN"
    systemctl start easycloud 2>/dev/null || true
  fi
  die "database check failed. Fix $ENV_FILE and run this installer again (the service uses the same settings, so it will not start until then)."
fi

# ---- systemd unit ----
mkdir -p "$(dirname "$UNIT")"
cat >"$UNIT" <<UNITEOF
[Unit]
Description=EasyCloud file manager
After=network-online.target mariadb.service mysql.service
Wants=network-online.target

[Service]
User=$SVC_USER
Group=$SVC_USER
EnvironmentFile=$ENV_FILE
ExecStart=$BIN serve
Restart=on-failure
RestartSec=5
# Exit code 78 = configuration error (bad credentials, unknown database, schema too new):
# restarting cannot help, so do not loop. 69 = temporarily unavailable: keep retrying.
RestartPreventExitStatus=78
LimitNOFILE=1048576
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=$APP_DATA $DATA_ROOT
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
UNITEOF
systemctl daemon-reload
systemctl enable easycloud >/dev/null 2>&1
systemctl restart easycloud

sleep 2
if ! systemctl is-active --quiet easycloud; then
  if [ "$UPGRADE" -eq 1 ] && [ -f "$BIN.prev" ]; then
    warn "the new version did not start; rolling back"
    mv "$BIN.prev" "$BIN"
    systemctl restart easycloud || true
  fi
  journalctl -u easycloud -n 30 --no-pager >&2 || true
  die "EasyCloud failed to start (see the log above)"
fi
rm -f "$BIN.prev"

PORT=$(sed -n 's/^EASYCLOUD_HTTP_LISTEN=.*://p' "$ENV_FILE" | tail -n1)
ADDR=$(hostname -I 2>/dev/null | awk '{print $1}')
say "EasyCloud $NEW_VERSION is running: http://${ADDR:-localhost}:${PORT:-8080}/"
if [ "$UPGRADE" -eq 0 ]; then
  say "sign in as admin with the password in $ENV_FILE (EASYCLOUD_ADMIN_PASSWORD); change it after the first login."
fi
