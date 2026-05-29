#!/usr/bin/env bash
set -euo pipefail

systemctl is-active minio.service >/dev/null 2>&1
curl -fsS --max-time 2 http://127.0.0.1:9000/minio/health/live >/dev/null
test -f /var/lib/bm-e2e-minio-seeded
