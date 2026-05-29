#!/usr/bin/env bash
set -euo pipefail

first_mount="${DATA_DIR:-/data}/drive1"

systemctl is-active sshd >/dev/null
mountpoint -q "$first_mount"
