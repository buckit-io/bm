#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

bash "$ROOT_DIR/integration-test/scripts/build-minio-fixture.sh"
bash "$ROOT_DIR/integration-test/scripts/write-migrate-compose.sh"
BM_E2E_SCENARIO=migrate bash "$ROOT_DIR/integration-test/scripts/compose.sh" build
BM_E2E_SCENARIO=migrate bash "$ROOT_DIR/integration-test/scripts/compose.sh" up -d bm
for node in $(seq 1 "${BM_E2E_MINIO_NODES:-1}"); do
  BM_E2E_SCENARIO=migrate bash "$ROOT_DIR/integration-test/scripts/compose.sh" up -d "minio-node${node}"
done
