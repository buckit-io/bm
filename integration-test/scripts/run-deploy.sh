#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOG_DIR="$ROOT_DIR/integration-test/.generated"

cleanup() {
  mkdir -p "$LOG_DIR"
  BM_E2E_SCENARIO=deploy \
    bash "$ROOT_DIR/integration-test/scripts/compose.sh" logs --no-color >"$LOG_DIR/compose.deploy.log" 2>&1 || true
  BM_E2E_SCENARIO=deploy bash "$ROOT_DIR/integration-test/scripts/down.sh"
}

if [[ "${BM_E2E_KEEP_CONTAINERS:-0}" != "1" ]]; then
  trap cleanup EXIT
fi

resolve_bm_base_url() {
  local mapping host_port
  mapping="$(BM_E2E_SCENARIO=deploy bash "$ROOT_DIR/integration-test/scripts/compose.sh" port bm 9443 | tail -n1)"
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
  local attempts="${2:-90}"
  local container_id health
  for _ in $(seq 1 "$attempts"); do
    if [[ -z "${container_id:-}" ]]; then
      container_id="$(BM_E2E_SCENARIO=deploy bash "$ROOT_DIR/integration-test/scripts/compose.sh" ps -q "$service" | tail -n1)"
      if [[ -z "$container_id" ]]; then
        sleep 2
        continue
      fi
    fi
    health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id" 2>/dev/null || true)"
    if [[ "$health" == "healthy" ]]; then
      return 0
    fi
    sleep 2
  done
  if [[ -z "${container_id:-}" ]]; then
    echo "Could not resolve container id for service $service" >&2
    return 1
  fi
  echo "Timed out waiting for $service to become healthy (last health: ${health:-unknown})" >&2
  return 1
}

if [[ "${SKIP_WEB_BUILD:-0}" != "1" ]]; then
  make web
fi
bash "$ROOT_DIR/integration-test/scripts/up-deploy.sh"

bm_base_url="$(resolve_bm_base_url)"
wait_for_url "$bm_base_url/api/v1/healthz"

for node in $(seq 1 "${BM_E2E_TARGET_NODES:-4}"); do
  wait_for_container_health "deploy-node${node}"
done

cd "$ROOT_DIR/integration-test/playwright"
npm install
if [[ "${CI:-}" == "true" ]]; then
  npx playwright install --with-deps chromium
else
  npx playwright install chromium
fi
PLAYWRIGHT_BASE_URL="${PLAYWRIGHT_BASE_URL:-$bm_base_url}" \
BM_E2E_DEPLOY_CLUSTER_NAME="${BM_E2E_DEPLOY_CLUSTER_NAME:-fixture-deploy}" \
BM_E2E_DEPLOY_HOSTS="${BM_E2E_DEPLOY_HOSTS:-deploy-node1,deploy-node2,deploy-node3,deploy-node4}" \
BM_E2E_DEPLOY_SSH_USER="${BM_E2E_DEPLOY_SSH_USER:-root}" \
BM_E2E_DEPLOY_SSH_PASSWORD="${BM_E2E_DEPLOY_SSH_PASSWORD:-${BM_E2E_TARGET_ROOT_PASSWORD:-buckitadmin}}" \
  npx playwright test tests/deploy.spec.ts
