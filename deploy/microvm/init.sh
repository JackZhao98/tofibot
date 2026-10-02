#!/bin/bash
set -eu
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
mountpoint -q /dev || mount -t devtmpfs devtmpfs /dev
mkdir -p /dev/pts /dev/shm
ln -sfn /proc/self/fd /dev/fd
ln -sfn /proc/self/fd/0 /dev/stdin
ln -sfn /proc/self/fd/1 /dev/stdout
ln -sfn /proc/self/fd/2 /dev/stderr
mount_if_needed() {
  local target="${!#}"
  mountpoint -q "$target" || mount "$@"
}
mount_if_needed -t proc proc /proc
mount_if_needed -t sysfs sysfs /sys
mount_if_needed -t devpts devpts /dev/pts
mount_if_needed -t tmpfs -o mode=1777,size=512m tmpfs /dev/shm
mount_if_needed -t tmpfs -o mode=1777,size=512m tmpfs /tmp
mkdir -p /tmp/.X11-unix
chmod 1777 /tmp/.X11-unix
mount_if_needed -t tmpfs -o mode=755,size=128m tmpfs /run
mount_if_needed -t tmpfs -o mode=1777,size=128m tmpfs /var/tmp
mount_if_needed -t tmpfs -o mode=755,size=64m tmpfs /var/log
if mountpoint -q /workspace; then
  [[ "$(findmnt -n -o SOURCE /workspace)" == "/dev/vdb" ]] || { echo 'workspace is mounted from an unexpected device' >&2; exit 1; }
else
  mount -t ext4 -o nodev,nosuid /dev/vdb /workspace
fi
mkdir -p /workspace/{bots,shared,home,browser}
chown 1000:1000 /workspace /workspace/{bots,shared,home,browser}
chmod 755 /workspace
hostname tofi-computer
ip link set lo up
desktop_idle_seconds=900
for argument in $(cat /proc/cmdline); do
  case "$argument" in
    tofi_ip=*) guest_ip=${argument#tofi_ip=} ;;
    tofi_gateway=*) guest_gateway=${argument#tofi_gateway=} ;;
    tofi_desktop_idle=*) desktop_idle_seconds=${argument#tofi_desktop_idle=} ;;
  esac
done
[[ "$desktop_idle_seconds" =~ ^[0-9]+$ ]] || { echo "invalid desktop idle timeout" >&2; exit 1; }
ip addr add "${guest_ip:?guest IP is required}/30" dev eth0
ip link set eth0 up
ip route add default via "${guest_gateway:?guest gateway is required}"
# DNS is written while constructing the immutable image.
mkdir -p /run/dbus
if [[ -f /usr/share/dbus-1/system.conf ]]; then dbus-daemon --system --fork; fi
echo 'TOFI_GUEST_READY_START'
setpriv --reuid=1000 --regid=1000 --clear-groups /usr/local/bin/tofi-guest --desktop-idle-timeout="${desktop_idle_seconds}s" &
guest_pid=$!
trap 'kill -TERM "$guest_pid" 2>/dev/null || true' TERM INT
wait "$guest_pid" || true
sync
umount /workspace || true
/bin/busybox poweroff -f
