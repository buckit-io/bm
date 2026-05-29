#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE_TAG="${BM_E2E_MINIO_IMAGE:-bm-e2e-minio:local}"

docker build \
  -f "$ROOT_DIR/integration-test/minio/Dockerfile" \
  -t "$IMAGE_TAG" \
  "$ROOT_DIR"
