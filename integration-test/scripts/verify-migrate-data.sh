#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVICE="${BM_E2E_MIGRATE_VERIFY_SERVICE:-minio-node1}"
BUCKET="${BM_E2E_MIGRATE_BUCKET:-seed-bucket}"
OBJECT="${BM_E2E_MIGRATE_OBJECT:-seed.txt}"
ROOT_USER="${BM_E2E_MINIO_ROOT_USER:-minioadmin}"
ROOT_PASSWORD="${BM_E2E_MIGRATE_VERIFY_ROOT_PASSWORD:-${BM_E2E_MINIO_ROOT_PASSWORD:-minioadmin}}"

container_id="$(BM_E2E_SCENARIO=migrate bash "$ROOT_DIR/integration-test/scripts/compose.sh" ps -q "$SERVICE" | tail -n1)"
if [[ -z "$container_id" ]]; then
  echo "Could not resolve container id for service $SERVICE" >&2
  exit 1
fi

docker exec "$container_id" mc alias set local http://127.0.0.1:9000 "$ROOT_USER" "$ROOT_PASSWORD" >/dev/null
docker exec "$container_id" mc stat "local/${BUCKET}/${OBJECT}" >/dev/null
docker exec "$container_id" systemctl is-active buckit.service >/dev/null
