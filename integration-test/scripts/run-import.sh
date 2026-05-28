#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOG_DIR="$ROOT_DIR/integration-test/.generated"

cleanup() {
  mkdir -p "$LOG_DIR"
  bash "$ROOT_DIR/integration-test/scripts/compose.sh" logs --no-color >"$LOG_DIR/compose.import.log" 2>&1 || true
  bash "$ROOT_DIR/integration-test/scripts/down.sh"
}

trap cleanup EXIT

resolve_bm_base_url() {
  local mapping host_port
  mapping="$(bash "$ROOT_DIR/integration-test/scripts/compose.sh" port bm 9443 | tail -n1)"
  if [[ -z "$mapping" ]]; then
    echo "Could not resolve published bm port" >&2
    return 1
  fi
  host_port="${mapping##*:}"
  printf 'http://127.0.0.1:%s' "$host_port"
}

wait_for_url() {
  local url="$1"
  local attempts="${2:-60}"
  for _ in $(seq 1 "$attempts"); do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  echo "Timed out waiting for $url" >&2
  return 1
}

wait_for_container_health() {
  local service="$1"
  local attempts="${2:-60}"
  local container_id health
  container_id="$(bash "$ROOT_DIR/integration-test/scripts/compose.sh" ps -q "$service" | tail -n1)"
  if [[ -z "$container_id" ]]; then
    echo "Could not resolve container id for service $service" >&2
    return 1
  fi
  for _ in $(seq 1 "$attempts"); do
    health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id" 2>/dev/null || true)"
    if [[ "$health" == "healthy" ]]; then
      return 0
    fi
    if [[ "$health" == "unhealthy" ]]; then
      echo "Container $service became unhealthy" >&2
      return 1
    fi
    sleep 2
  done
  echo "Timed out waiting for $service to become healthy" >&2
  return 1
}

if [[ "${SKIP_WEB_BUILD:-0}" != "1" ]]; then
  make web
fi
bash "$ROOT_DIR/integration-test/scripts/up-import.sh"

bm_base_url="$(resolve_bm_base_url)"
wait_for_url "$bm_base_url/api/v1/healthz"
wait_for_container_health "buckit-node1"

cd "$ROOT_DIR/integration-test/playwright"
npm install
if [[ "${CI:-}" == "true" ]]; then
  npx playwright install --with-deps chromium
else
  npx playwright install chromium
fi
PLAYWRIGHT_BASE_URL="${PLAYWRIGHT_BASE_URL:-$bm_base_url}" \
BM_E2E_IMPORT_URL="${BM_E2E_IMPORT_URL:-http://buckit-node1:9000}" \
  npx playwright test tests/import.spec.ts
