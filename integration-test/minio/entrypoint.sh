#!/usr/bin/env bash
set -euo pipefail

DRIVES="${DRIVES:-4}"
DRIVE_SIZE="${DRIVE_SIZE:-1G}"
MINIO_ROOT_USER="${MINIO_ROOT_USER:-minioadmin}"
MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD:-minioadmin}"
SSH_ROOT_PASSWORD="${SSH_ROOT_PASSWORD:-minioadmin}"
SEED_BUCKET="${BM_E2E_SEED_BUCKET:-seed-bucket}"
SEED_OBJECT="${BM_E2E_SEED_OBJECT:-seed.txt}"
SEED_BODY="${BM_E2E_SEED_BODY:-bm migration fixture object}"

echo "root:${SSH_ROOT_PASSWORD}" | chpasswd

modprobe loop 2>/dev/null || true
for _n in $(seq 0 63); do
  [ -e "/dev/loop${_n}" ] || mknod "/dev/loop${_n}" b 7 "${_n}" 2>/dev/null || true
done
unset _n

if command -v losetup >/dev/null 2>&1; then
  losetup -a 2>/dev/null | awk -F: '/\(deleted\)/{print $1}' | while read -r stale; do
    losetup -d "$stale" 2>/dev/null || true
  done
fi

attach_loop() {
  local img="$1"
  local loop num
  loop="$(losetup --find --show "$img")" || return 1
  num="${loop##/dev/loop}"
  [ -e "$loop" ] || mknod "$loop" b 7 "$num" 2>/dev/null || true
  printf '%s\n' "$loop"
}

for i in $(seq 0 $((DRIVES - 1))); do
  img="/var/lib/minio-drives/drive${i}.img"
  mnt="/mnt/data/drive${i}"
  mkdir -p "$mnt"
  if [ ! -f "$img" ]; then
    fallocate -l "$DRIVE_SIZE" "$img"
    mkfs.xfs -f "$img"
  fi
  loop="$(attach_loop "$img")"
  if ! mountpoint -q "$mnt"; then
    mount "$loop" "$mnt"
  fi
done

chown -R minio-user:minio-user /mnt/data

if [ -z "${MINIO_VOLUMES:-}" ]; then
  if [ "$DRIVES" -eq 1 ]; then
    MINIO_VOLUMES="/mnt/data/drive0"
  else
    MINIO_VOLUMES="/mnt/data/drive{0...$((DRIVES - 1))}"
  fi
fi

cat >/etc/default/minio <<EOF
MINIO_VOLUMES="${MINIO_VOLUMES}"
MINIO_OPTS="--console-address :9001"
MINIO_ROOT_USER=${MINIO_ROOT_USER}
MINIO_ROOT_PASSWORD=${MINIO_ROOT_PASSWORD}
EOF

cat >/etc/default/bm-e2e-minio-seed <<EOF
BM_E2E_SEED_BUCKET="${SEED_BUCKET}"
BM_E2E_SEED_OBJECT="${SEED_OBJECT}"
BM_E2E_SEED_BODY="${SEED_BODY}"
MINIO_ROOT_USER="${MINIO_ROOT_USER}"
MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD}"
EOF

rm -f /var/lib/bm-e2e-minio-seeded

exec /sbin/init
