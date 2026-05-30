#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/integration-test/.generated"
OUT_FILE="$OUT_DIR/compose.deploy.yml"
IMAGE_TAG="${BM_E2E_TARGET_IMAGE:-bm-e2e-target:local}"
NODES="${BM_E2E_TARGET_NODES:-4}"
DRIVES="${BM_E2E_TARGET_DRIVES:-1}"
DRIVE_SIZE="${BM_E2E_TARGET_DRIVE_SIZE:-1G}"
ROOT_PASSWORD="${BM_E2E_TARGET_ROOT_PASSWORD:-buckitadmin}"

mkdir -p "$OUT_DIR"

{
  cat <<EOF
services:
EOF

  for node in $(seq 1 "$NODES"); do
    cat <<EOF
  deploy-node${node}:
    image: ${IMAGE_TAG}
    container_name: bm-e2e-deploy-node${node}
    hostname: deploy-node${node}
    privileged: true
    environment:
      DRIVES: "${DRIVES}"
      DRIVE_SIZE: "${DRIVE_SIZE}"
      SSH_ROOT_PASSWORD: "${ROOT_PASSWORD}"
    tmpfs:
      - /run
      - /run/lock
      - /tmp
    networks:
      - e2e
EOF
  done
} >"$OUT_FILE"
