#!/bin/sh
# Inventory Exporter Deployment Script — one command to install everything.
# Built by CI, pushed to bin-server as inventory-exporter-deploy.
#
# Usage:
#   curl -fsSL https://files.example.com/f/inventory-exporter-deploy/raw | sh
#   # or
#   ./deploy.sh

set -e

BIN_DIR=${BIN_DIR:-/usr/local/bin}
CONF_DIR=${CONF_DIR:-/etc/inventory-exporter}
SYSTEMD_DIR=${SYSTEMD_DIR:-/etc/systemd/system}
BIN_SERVER_URL=${BIN_SERVER_URL:-https://files.example.com}
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
URL=${BIN_SERVER_URL:-https://files.example.com}
REMOTE=$(curl -fsS "${URL}/f/${NAME}/hash") || { echo "bin-update: failed to fetch remote hash" >&2; exit 1; }
if [ -f "${BIN}" ]; then
  LOCAL=$(sha256sum "${BIN}" | cut -d' ' -f1)
  [ "${REMOTE}" = "${LOCAL}" ] && echo "bin-update: ${NAME} up to date (${LOCAL})" && exit 0
  echo "bin-update: ${NAME} outdated (local=${LOCAL}, remote=${REMOTE})"
else
  echo "bin-update: ${NAME} not found, downloading"
fi
mkdir -p "$(dirname "${BIN}")"
curl -fsSLo "${BIN}" "${URL}/f/${NAME}/raw"
chmod +x "${BIN}"
echo "bin-update: ${NAME} updated to ${REMOTE}"
UPDATE_EOF
chmod +x "$BIN_DIR/bin-update.sh"

# 2. Download exporter binary.
echo "--- Downloading inventory-exporter ---"
"$BIN_DIR/bin-update.sh" inventory-exporter "$BIN_DIR/inventory-exporter"

# 3. Write example configs (always overwritten for reference).
echo "--- Writing example configs ---"
mkdir -p "$CONF_DIR"
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
    insecure_skip_verify: false
ESXI_EOF

# 4. Install systemd units.
echo "--- Installing systemd units ---"
cat > "$SYSTEMD_DIR/inventory-exporter.service" << 'UNIT_EOF'
[Unit]
Description=Infrastructure Inventory Exporter
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/inventory-exporter
Restart=always
RestartSec=30
MemoryMax=256M
CPUQuota=50%
TasksMax=20

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
systemctl enable inventory-exporter.service
systemctl enable --now inventory-exporter-update.timer

echo
echo "=== Deploy complete ==="
echo "Service:  systemctl start inventory-exporter"
echo "Status:   systemctl status inventory-exporter"
echo "Config:   $CONF_DIR/config.yaml"
echo "Update:   systemctl list-timers inventory-exporter-update"
