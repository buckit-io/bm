#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/integration-test/.generated"
OUT_FILE="$OUT_DIR/compose.migrate.yml"
IMAGE_TAG="${BM_E2E_MINIO_IMAGE:-bm-e2e-minio:local}"
NODES="${BM_E2E_MINIO_NODES:-1}"
DRIVES="${BM_E2E_MINIO_DRIVES:-4}"
DRIVE_SIZE="${BM_E2E_MINIO_DRIVE_SIZE:-1G}"
ROOT_USER="${BM_E2E_MINIO_ROOT_USER:-minioadmin}"
ROOT_PASSWORD="${BM_E2E_MINIO_ROOT_PASSWORD:-minioadmin}"
SSH_PASSWORD="${BM_E2E_MINIO_SSH_PASSWORD:-$ROOT_PASSWORD}"
SEED_BUCKET="${BM_E2E_MIGRATE_BUCKET:-seed-bucket}"
SEED_OBJECT="${BM_E2E_MIGRATE_OBJECT:-seed.txt}"
SEED_BODY="${BM_E2E_MIGRATE_BODY:-bm migration fixture object}"

mkdir -p "$OUT_DIR"

if [[ "$NODES" -eq 1 ]]; then
  if [[ "$DRIVES" -eq 1 ]]; then
    minio_volumes="/mnt/data/drive0"
  else
    minio_volumes="/mnt/data/drive{0...$((DRIVES - 1))}"
  fi
else
  if [[ "$DRIVES" -eq 1 ]]; then
    minio_volumes="http://minio-node{1...${NODES}}:9000/mnt/data/drive0"
  else
    minio_volumes="http://minio-node{1...${NODES}}:9000/mnt/data/drive{0...$((DRIVES - 1))}"
  fi
fi

{
  cat <<EOF
services:
EOF

  for node in $(seq 1 "$NODES"); do
    cat <<EOF
  minio-node${node}:
    image: ${IMAGE_TAG}
    container_name: bm-e2e-minio-node${node}
    hostname: minio-node${node}
    privileged: true
    environment:
      DRIVES: "${DRIVES}"
      DRIVE_SIZE: "${DRIVE_SIZE}"
      MINIO_VOLUMES: "${minio_volumes}"
      MINIO_ROOT_USER: "${ROOT_USER}"
      MINIO_ROOT_PASSWORD: "${ROOT_PASSWORD}"
      SSH_ROOT_PASSWORD: "${SSH_PASSWORD}"
      BM_E2E_SEED_BUCKET: "${SEED_BUCKET}"
      BM_E2E_SEED_OBJECT: "${SEED_OBJECT}"
      BM_E2E_SEED_BODY: "${SEED_BODY}"
    volumes:
      - minio-node${node}-drives:/var/lib/minio-drives
    tmpfs:
      - /run
      - /run/lock
      - /tmp
    networks:
      - e2e

EOF
  done

  cat <<EOF
volumes:
EOF
  for node in $(seq 1 "$NODES"); do
    printf '  minio-node%s-drives:\n' "$node"
  done
} >"$OUT_FILE"
