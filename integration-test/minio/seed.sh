#!/usr/bin/env bash
set -euo pipefail

source /etc/default/bm-e2e-minio-seed

for _ in $(seq 1 60); do
  if curl -fsS --max-time 2 http://127.0.0.1:9000/minio/health/live >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

mc alias set local http://127.0.0.1:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null
mc mb --ignore-existing "local/${BM_E2E_SEED_BUCKET}" >/dev/null
tmpfile="$(mktemp)"
printf '%s\n' "$BM_E2E_SEED_BODY" >"$tmpfile"
mc cp "$tmpfile" "local/${BM_E2E_SEED_BUCKET}/${BM_E2E_SEED_OBJECT}" >/dev/null
rm -f "$tmpfile"
touch /var/lib/bm-e2e-minio-seeded
