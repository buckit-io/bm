#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GENERATED="$ROOT_DIR/integration-test/.generated/compose.import.yml"

if [[ ! -f "$GENERATED" ]]; then
  echo "Generated compose file not found: $GENERATED" >&2
  echo "Run integration-test/scripts/write-import-compose.sh first." >&2
  exit 1
fi

exec docker compose \
  -f "$ROOT_DIR/integration-test/compose.yml" \
  -f "$GENERATED" \
  "$@"
