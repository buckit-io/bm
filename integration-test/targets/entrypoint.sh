#!/usr/bin/env bash
set -euo pipefail

DRIVES="${DRIVES:-4}"
DATA_DIR="${DATA_DIR:-/data}"
SSH_ROOT_PASSWORD="${SSH_ROOT_PASSWORD:-buckitadmin}"
BACKING_DIR="${BACKING_DIR:-/var/lib/bm-target-drives}"
DRIVE_SIZE="${DRIVE_SIZE:-1G}"

echo "root:${SSH_ROOT_PASSWORD}" | chpasswd

# Ensure loop device support is present in the container namespace.
modprobe loop 2>/dev/null || true

# Pre-populate /dev/loop0.../dev/loop63 so losetup --find --show can open a
# device node immediately after the kernel allocates an index.
for _n in $(seq 0 63); do
  [ -e "/dev/loop${_n}" ] || mknod "/dev/loop${_n}" b 7 "${_n}" 2>/dev/null || true
done
unset _n

# Detach stale loop devices from deleted backing files (e.g. previous runs on
# Docker Desktop where the host VM kernel can retain orphaned attachments).
if command -v losetup >/dev/null 2>&1; then
  losetup -a 2>/dev/null | awk -F: '/\(deleted\)/{print $1}' | while read -r stale; do
    losetup -d "$stale" 2>/dev/null || true
  done
fi

mkdir -p "$BACKING_DIR" "$DATA_DIR"

attach_loop() {
  local img="$1"
  local loop num
  loop="$(losetup --find --show "$img")" || return 1
  num="${loop##/dev/loop}"
  [ -e "$loop" ] || mknod "$loop" b 7 "$num" 2>/dev/null || true
  printf '%s\n' "$loop"
}

for i in $(seq 1 "$DRIVES"); do
  backing_drive="${BACKING_DIR}/drive${i}.img"
  mountpoint_dir="${DATA_DIR}/drive${i}"
  if [ ! -f "$backing_drive" ]; then
    fallocate -l "$DRIVE_SIZE" "$backing_drive"
    mkfs.xfs -f "$backing_drive"
  fi
  loop_device="$(attach_loop "$backing_drive")"
  mkdir -p "$mountpoint_dir"
  if ! mountpoint -q "$mountpoint_dir"; then
    mount "$loop_device" "$mountpoint_dir"
  fi
done

exec /sbin/init
