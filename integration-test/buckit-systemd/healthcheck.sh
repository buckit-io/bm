#!/usr/bin/env bash
set -euo pipefail

systemctl is-active sshd >/dev/null
systemctl is-active buckit.service >/dev/null
curl -fsS http://127.0.0.1:9000/minio/health/live >/dev/null
