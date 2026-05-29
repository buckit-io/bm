#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

bash "$ROOT_DIR/integration-test/scripts/build-target-fixture.sh"
bash "$ROOT_DIR/integration-test/scripts/write-deploy-compose.sh"
BM_E2E_SCENARIO=deploy bash "$ROOT_DIR/integration-test/scripts/compose.sh" build
BM_E2E_SCENARIO=deploy bash "$ROOT_DIR/integration-test/scripts/compose.sh" up -d bm
for node in $(seq 1 "${BM_E2E_TARGET_NODES:-4}"); do
  BM_E2E_SCENARIO=deploy bash "$ROOT_DIR/integration-test/scripts/compose.sh" up -d "deploy-node${node}"
done
