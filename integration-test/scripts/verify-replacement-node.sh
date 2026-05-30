#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TARGET_FILE="$ROOT_DIR/integration-test/.generated/replacement-target-host"
SERVICE="${BM_E2E_REPLACEMENT_TARGET_SERVICE:-}"
if [[ -z "$SERVICE" && -f "$TARGET_FILE" ]]; then
  SERVICE="$(tr -d '\r\n' <"$TARGET_FILE")"
fi
SERVICE="${SERVICE:-replacement-node4}"

container_id="$(BM_E2E_SCENARIO=replacement bash "$ROOT_DIR/integration-test/scripts/compose.sh" ps -q "$SERVICE" | tail -n1)"
if [[ -z "$container_id" ]]; then
  echo "Could not resolve container id for service $SERVICE" >&2
  exit 1
fi

docker exec "$container_id" systemctl is-active buckit.service >/dev/null
docker exec "$container_id" curl -fsS http://127.0.0.1:9000/minio/health/live >/dev/null
