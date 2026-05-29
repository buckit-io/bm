#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCENARIO="${BM_E2E_SCENARIO:-import}"
GENERATED="$ROOT_DIR/integration-test/.generated/compose.${SCENARIO}.yml"

if [[ ! -f "$GENERATED" ]]; then
  for candidate in deploy import migrate; do
    if [[ -f "$ROOT_DIR/integration-test/.generated/compose.${candidate}.yml" ]]; then
      SCENARIO="$candidate"
      GENERATED="$ROOT_DIR/integration-test/.generated/compose.${candidate}.yml"
      break
    fi
  done
fi

if [[ ! -f "$GENERATED" ]]; then
  exit 0
fi

BM_E2E_SCENARIO="$SCENARIO" \
  bash "$ROOT_DIR/integration-test/scripts/compose.sh" down -v
