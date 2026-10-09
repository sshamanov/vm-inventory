#!/bin/sh
# Inventory Exporter Deployment Script — one command to install everything.
# CI uploads it to the file service as inventory-exporter-deploy, with the
# file-service URL (repo variable BIN_SERVER_URL) written in place of the
# @BIN_SERVER_URL@ placeholder, so the published copy needs no settings.
#
# Usage:
#   curl -fsSL <file-service>/f/inventory-exporter-deploy/raw | sh
#   # or, from a checkout:
#   BIN_SERVER_URL=<file-service> ./deploy.sh

set -e

BIN_DIR=${BIN_DIR:-/usr/local/bin}
CONF_DIR=${CONF_DIR:-/etc/inventory-exporter}
SYSTEMD_DIR=${SYSTEMD_DIR:-/etc/systemd/system}
BIN_SERVER_URL=${BIN_SERVER_URL:-@BIN_SERVER_URL@}
case "$BIN_SERVER_URL" in
	@*@) echo "deploy: set BIN_SERVER_URL to the file service base URL" >&2; exit 1 ;;
esac
export BIN_SERVER_URL

echo "=== Inventory Exporter Deploy ==="
echo "Binary dir:  $BIN_DIR"
echo "Config dir:  $CONF_DIR"
echo "Systemd dir: $SYSTEMD_DIR"
echo

# 1. Install self-update script.
echo "--- Installing bin-update.sh ---"
mkdir -p "$BIN_DIR"
cat > "$BIN_DIR/bin-update.sh" << 'UPDATE_EOF'
#!/bin/sh
set -e
NAME=${1:?package name required}
BIN=${2:?binary path required}
URL=${BIN_SERVER_URL:-@BIN_SERVER_URL@}
REMOTE=$(curl -fsS "${URL}/f/${NAME}/hash") || { echo "bin-update: failed to fetch remote hash" >&2; exit 1; }
if [ -f "${BIN}" ]; then
  LOCAL=$(sha256sum "${BIN}" | cut -d' ' -f1)
  [ "${REMOTE}" = "${LOCAL}" ] && echo "bin-update: ${NAME} up to date (${LOCAL})" && exit 0
  echo "bin-update: ${NAME} outdated (local=${LOCAL}, remote=${REMOTE})"
else
  echo "bin-update: ${NAME} not found, downloading"
fi
mkdir -p "$(dirname "${BIN}")"
TMP="${BIN}.tmp.$$"
curl -fsSLo "${TMP}" "${URL}/f/${NAME}/raw"
chmod +x "${TMP}"
mv "${TMP}" "${BIN}"
echo "bin-update: ${NAME} updated to ${REMOTE}"
UPDATE_EOF
# The updater runs from a systemd timer with no environment, so it carries the
# file-service URL itself.
sed -i "s#@BIN_SERVER_URL@#${BIN_SERVER_URL}#" "$BIN_DIR/bin-update.sh"
chmod +x "$BIN_DIR/bin-update.sh"

# 2. Download exporter binary.
echo "--- Downloading inventory-exporter ---"
"$BIN_DIR/bin-update.sh" inventory-exporter "$BIN_DIR/inventory-exporter"

# 3. Write example configs — only create if missing, never overwrite user edits.
echo "--- Writing example configs ---"
mkdir -p "$CONF_DIR"
if [ ! -f "$CONF_DIR/config.yaml" ]; then
	cat > "$CONF_DIR/config.yaml" << 'CONF_EOF'
# Inventory Exporter — Linux host configuration.
# All fields are optional; defaults are shown below.

mode: linux

host:
  id: ""          # defaults to system hostname
  description: "" # free-text description
  geo: general    # geographic grouping label

collection:
  interval: 15m   # how often to collect
  timeout: 5m     # max collection duration
  max_snapshot_age: 8h  # stop serving expired snapshots

collectors:
  libvirt:
    enabled: true  # collect KVM domains and LVM pools
  lxd:
    enabled: true  # collect LXD containers and storage pools
CONF_EOF
else
	echo "  $CONF_DIR/config.yaml exists — keeping existing config"
fi

if [ ! -f "$CONF_DIR/esxi.yaml" ]; then
	cat > "$CONF_DIR/esxi.yaml" << 'ESXI_EOF'
# Inventory Exporter — ESXi collector configuration.
# One process collects multiple standalone ESXi hosts.

mode: esxi

collection:
  interval: 15m
  timeout: 5m
  max_snapshot_age: 8h

targets:
  - host_id: esxi-01
    host_description: Primary ESXi host
    geo: general
    address: https://esxi-01.internal
    username: inventory-reader
    password: changeme
    insecure_skip_verify: true  # defaults to true (ESXi uses self-signed certs)
ESXI_EOF
else
	echo "  $CONF_DIR/esxi.yaml exists — keeping existing config"
fi

# 4. Install systemd units (always overwrite to pick up fixes).
echo "--- Installing systemd units ---"
cat > "$SYSTEMD_DIR/inventory-exporter.service" << 'UNIT_EOF'
[Unit]
Description=Infrastructure Inventory Exporter
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/inventory-exporter -config.file=/etc/inventory-exporter/config.yaml
# For ESXi mode, change config.file to /etc/inventory-exporter/esxi.yaml
# and add: --web.listen-address=:9172
Restart=always
RestartSec=30
MemoryMax=256M
CPUQuota=50%

[Install]
WantedBy=multi-user.target
UNIT_EOF

cat > "$SYSTEMD_DIR/inventory-exporter-update.service" << 'UPDATE_UNIT_EOF'
[Unit]
Description=Inventory Exporter Self-Update Check
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/bin-update.sh inventory-exporter /usr/local/bin/inventory-exporter
ExecStartPost=/bin/systemctl try-restart inventory-exporter
UPDATE_UNIT_EOF

cat > "$SYSTEMD_DIR/inventory-exporter-update.timer" << 'TIMER_EOF'
[Unit]
Description=Hourly Inventory Exporter Self-Update Check
After=network-online.target

[Timer]
OnBootSec=2min
OnUnitActiveSec=1h
RandomizedDelaySec=5min

[Install]
WantedBy=timers.target
TIMER_EOF

systemctl daemon-reload
systemctl enable --now inventory-exporter.service
systemctl enable --now inventory-exporter-update.timer

echo
echo "=== Deploy complete ==="
echo "Status:   systemctl status inventory-exporter"
echo "Config:   $CONF_DIR/config.yaml"
echo "Update:   systemctl list-timers inventory-exporter-update"
