#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/integration-test/.generated"
OUT_FILE="$OUT_DIR/compose.import.yml"
IMAGE_TAG="${BM_E2E_BUCKIT_IMAGE:-bm-e2e-buckit:local}"
NODES="${BM_E2E_BUCKIT_NODES:-4}"
DRIVES="${BM_E2E_BUCKIT_DRIVES:-4}"

mkdir -p "$OUT_DIR"

endpoints=()
for node in $(seq 1 "$NODES"); do
  for drive in $(seq 1 "$DRIVES"); do
    endpoints+=("http://buckit-node${node}:9000/data/drive${drive}")
  done
done

{
  cat <<EOF
services:
EOF

  for node in $(seq 1 "$NODES"); do
    cat <<EOF
  buckit-node${node}:
    image: ${IMAGE_TAG}
    container_name: bm-e2e-buckit-node${node}
    hostname: buckit-node${node}
    command:
      - server
EOF
    for endpoint in "${endpoints[@]}"; do
      printf '      - %s\n' "$endpoint"
    done
    cat <<EOF
      - --console-address
      - :9001
    environment:
      MINIO_ROOT_USER: buckitadmin
      MINIO_ROOT_PASSWORD: buckitadmin
    volumes:
EOF
    for drive in $(seq 1 "$DRIVES"); do
      printf '      - buckit-node%s-drive%s:/data/drive%s\n' "$node" "$drive" "$drive"
    done
    cat <<EOF
    networks:
      - e2e

EOF
  done

  cat <<EOF
volumes:
EOF
  for node in $(seq 1 "$NODES"); do
    for drive in $(seq 1 "$DRIVES"); do
      printf '  buckit-node%s-drive%s:\n' "$node" "$drive"
    done
  done
} >"$OUT_FILE"
