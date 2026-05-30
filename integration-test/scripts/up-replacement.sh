#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

bash "$ROOT_DIR/integration-test/scripts/build-buckit-systemd-fixture.sh"
bash "$ROOT_DIR/integration-test/scripts/write-replacement-compose.sh"
BM_E2E_SCENARIO=replacement \
  bash "$ROOT_DIR/integration-test/scripts/compose.sh" up -d --build
