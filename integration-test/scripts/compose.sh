#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCENARIO="${BM_E2E_SCENARIO:-import}"
GENERATED="$ROOT_DIR/integration-test/.generated/compose.${SCENARIO}.yml"

if [[ ! -f "$GENERATED" ]]; then
  echo "Generated compose file not found: $GENERATED" >&2
  echo "Run the matching integration-test/scripts/write-${SCENARIO}-compose.sh first." >&2
  exit 1
fi

exec docker compose \
  -f "$ROOT_DIR/integration-test/compose.yml" \
  -f "$GENERATED" \
  "$@"
