#!/bin/sh
# Self-update script for inventory binaries from files.example.com.
#
# Compares local binary hash against remote, downloads only if changed.
# Exits 0 if up to date or download succeeds, 1 on failure.
#
# Usage:
#   bin-update.sh <package-name> <binary-path>
#   BIN_SERVER_URL=https://files.example.com bin-update.sh inventory-exporter /usr/local/bin/inventory-exporter
#
# systemd:
#   ExecStartPre=/usr/local/bin/bin-update.sh inventory-exporter /usr/local/bin/inventory-exporter

set -e

NAME=${1:?package name required}
BIN=${2:?binary path required}
URL=${BIN_SERVER_URL:-https://files.example.com}

REMOTE=$(curl -fsS "${URL}/f/${NAME}/hash") || {
  echo "bin-update: failed to fetch remote hash for ${NAME}" >&2
  exit 1
}

if [ -f "${BIN}" ]; then
  LOCAL=$(sha256sum "${BIN}" | cut -d' ' -f1)
  if [ "${REMOTE}" = "${LOCAL}" ]; then
    echo "bin-update: ${NAME} is up to date (${LOCAL})"
    exit 0
  fi
  echo "bin-update: ${NAME} is outdated (local=${LOCAL}, remote=${REMOTE})"
else
  echo "bin-update: ${NAME} not found at ${BIN}, downloading"
fi

mkdir -p "$(dirname "${BIN}")"
curl -fsSLo "${BIN}" "${URL}/f/${NAME}/raw"
chmod +x "${BIN}"
echo "bin-update: ${NAME} updated to ${REMOTE}"
