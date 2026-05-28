#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

bash "$ROOT_DIR/integration-test/scripts/build-buckit-fixture.sh"
bash "$ROOT_DIR/integration-test/scripts/write-import-compose.sh"
bash "$ROOT_DIR/integration-test/scripts/compose.sh" up -d --build
