#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GENERATED="$ROOT_DIR/integration-test/.generated/compose.import.yml"

if [[ ! -f "$GENERATED" ]]; then
  exit 0
fi

bash "$ROOT_DIR/integration-test/scripts/compose.sh" down -v
