#!/usr/bin/env bash
set -euo pipefail

DRIVES="${DRIVES:-1}"
DRIVE_SIZE="${DRIVE_SIZE:-1G}"
NODES="${NODES:-4}"
DATA_DIR="${DATA_DIR:-/data}"
BACKING_DIR="${BACKING_DIR:-/var/lib/buckit-drives}"
SSH_ROOT_PASSWORD="${SSH_ROOT_PASSWORD:-buckitadmin}"
MINIO_ROOT_USER="${MINIO_ROOT_USER:-buckitadmin}"
MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD:-buckitadmin}"
HOST_PREFIX="${HOST_PREFIX:-replacement-node}"
SERVICE_NAME="${SERVICE_NAME:-buckit.service}"

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

service_identity() {
  local user group
  user="$(awk -F= '$1=="User"{print $2}' /usr/lib/systemd/system/${SERVICE_NAME} 2>/dev/null | tail -n1)"
  group="$(awk -F= '$1=="Group"{print $2}' /usr/lib/systemd/system/${SERVICE_NAME} 2>/dev/null | tail -n1)"
  user="${user:-buckit}"
  group="${group:-$user}"
  printf '%s:%s\n' "$user" "$group"
}

mkdir -p "$BACKING_DIR" "$DATA_DIR" /etc/minio

for i in $(seq 1 "$DRIVES"); do
  img="${BACKING_DIR}/drive${i}.img"
  mountpoint_dir="${DATA_DIR}/drive${i}"
  mkdir -p "$mountpoint_dir"
  if [ ! -f "$img" ]; then
    fallocate -l "$DRIVE_SIZE" "$img"
    mkfs.xfs -f "$img"
  fi
  loop="$(attach_loop "$img")"
  if ! mountpoint -q "$mountpoint_dir"; then
    mount "$loop" "$mountpoint_dir"
  fi
done

service_owner="$(service_identity)"
service_user="${service_owner%%:*}"
service_group="${service_owner##*:}"

volumes=()
for node in $(seq 1 "$NODES"); do
  for drive in $(seq 1 "$DRIVES"); do
    volumes+=("http://${HOST_PREFIX}${node}:9000${DATA_DIR}/drive${drive}/buckit")
  done
done
MINIO_VOLUMES="${MINIO_VOLUMES:-${volumes[*]}}"
unset volumes

cat >/etc/default/minio <<EOF
MINIO_CONFIG_ENV_FILE="/etc/minio/config.env"
MINIO_VOLUMES="${MINIO_VOLUMES}"
MINIO_OPTS="--address :9000 --console-address :9001"
EOF

cat >/etc/minio/config.env <<EOF
MINIO_ROOT_USER="${MINIO_ROOT_USER}"
MINIO_ROOT_PASSWORD="${MINIO_ROOT_PASSWORD}"
EOF

for i in $(seq 1 "$DRIVES"); do
  install -d -o "$service_user" -g "$service_group" -m 755 "${DATA_DIR}/drive${i}/buckit"
done
chown "$service_user:$service_group" /etc/minio/config.env
chmod 600 /etc/minio/config.env

exec /sbin/init
