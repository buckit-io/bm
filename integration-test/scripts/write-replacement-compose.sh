#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/integration-test/.generated"
OUT_FILE="$OUT_DIR/compose.replacement.yml"
IMAGE_TAG="${BM_E2E_REPLACEMENT_IMAGE:-bm-e2e-buckit-systemd:local}"
NODES="${BM_E2E_REPLACEMENT_NODES:-4}"
DRIVES="${BM_E2E_REPLACEMENT_DRIVES:-1}"
DRIVE_SIZE="${BM_E2E_REPLACEMENT_DRIVE_SIZE:-1G}"
ROOT_PASSWORD="${BM_E2E_REPLACEMENT_ROOT_PASSWORD:-buckitadmin}"

mkdir -p "$OUT_DIR"

{
  cat <<EOF
services:
EOF

  for node in $(seq 1 "$NODES"); do
    cat <<EOF
  replacement-node${node}:
    image: ${IMAGE_TAG}
    container_name: bm-e2e-replacement-node${node}
    hostname: replacement-node${node}
    privileged: true
    environment:
      NODES: "${NODES}"
      DRIVES: "${DRIVES}"
      DRIVE_SIZE: "${DRIVE_SIZE}"
      HOST_PREFIX: "replacement-node"
      SSH_ROOT_PASSWORD: "${ROOT_PASSWORD}"
      MINIO_ROOT_USER: "buckitadmin"
      MINIO_ROOT_PASSWORD: "${ROOT_PASSWORD}"
    tmpfs:
      - /run
      - /run/lock
      - /tmp
    networks:
      - e2e

EOF
  done
} >"$OUT_FILE"
