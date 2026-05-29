#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE_TAG="${BM_E2E_TARGET_IMAGE:-bm-e2e-target:local}"

docker build \
  -f "$ROOT_DIR/integration-test/targets/Dockerfile" \
  -t "$IMAGE_TAG" \
  "$ROOT_DIR"
