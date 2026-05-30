#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVICE="${BM_E2E_REPLACEMENT_TARGET_SERVICE:-replacement-node4}"

container_id="$(BM_E2E_SCENARIO=replacement bash "$ROOT_DIR/integration-test/scripts/compose.sh" ps -q "$SERVICE" | tail -n1)"
if [[ -z "$container_id" ]]; then
  echo "Could not resolve container id for service $SERVICE" >&2
  exit 1
fi

docker exec "$container_id" bash -lc '
set -euo pipefail

systemctl stop buckit.service || true
systemctl disable buckit.service || true

pkg="$(rpm -qf /usr/local/bin/buckit 2>/dev/null || true)"
if [[ -n "$pkg" ]]; then
  dnf remove -y "$pkg"
fi

rm -f /etc/default/minio /etc/minio/config.env
rm -rf /etc/minio/certs /etc/systemd/system/buckit.service.d
find /data -mindepth 2 -maxdepth 2 -type d -name buckit -exec rm -rf {} +

systemctl daemon-reload || true

for user in buckit minio-user; do
  if id "$user" >/dev/null 2>&1; then
    userdel -r "$user" 2>/dev/null || userdel "$user" 2>/dev/null || true
  fi
done
for group in buckit minio-user; do
  if getent group "$group" >/dev/null 2>&1; then
    groupdel "$group" 2>/dev/null || true
  fi
done
'
