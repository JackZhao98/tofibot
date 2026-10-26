#!/usr/bin/env bash
# Run on a Linux x86_64 or aarch64 Docker host; it builds natively for the
# host (no cross-building). TOFI_ACCOUNT_RELEASE=1 emits a sealed bundle.
# TOFI_GUEST_ARCH may name the expected arch and must equal the host's.
set -euo pipefail
umask 022
[[ $(uname -s) == Linux ]] || { echo 'The computer image must be built on Linux' >&2; exit 1; }
case $(uname -m) in
  x86_64) arch=x86_64 ;;
  aarch64|arm64) arch=aarch64 ;;
  *) echo "Unsupported host architecture $(uname -m); need x86_64 or aarch64" >&2; exit 1 ;;
esac
[[ -z ${TOFI_GUEST_ARCH:-} || $TOFI_GUEST_ARCH == "$arch" ]] || { echo "TOFI_GUEST_ARCH=$TOFI_GUEST_ARCH differs from host $arch; cross-building is not supported" >&2; exit 1; }
[[ $(id -u) == 0 ]] || { echo 'Run the image assembler with sudo' >&2; exit 1; }
# Protect an in-progress release from concurrent host artifact retention.
exec 9>/run/lock/tofi-maintenance.lock
flock 9
source_dir=$(cd "$(dirname "$0")" && pwd)
release_dir=${1:?Provide a new absolute release directory}
[[ $release_dir == /* && $release_dir != / && ! -e $release_dir ]] || { echo 'Release directory must be new and absolute' >&2; exit 1; }
account_release=${TOFI_ACCOUNT_RELEASE:-0}
[[ $account_release == 0 || $account_release == 1 ]] || { echo 'TOFI_ACCOUNT_RELEASE must be 0 or 1' >&2; exit 1; }
release_info_dir="${release_dir}.build-info"
if [[ $account_release == 1 ]]; then
  [[ -d "$(dirname "$release_dir")" ]] || { echo 'Release parent directory must already exist' >&2; exit 1; }
  [[ ! -e "$release_info_dir" && ! -L "$release_info_dir" ]] || { echo 'Release build-info directory must be new' >&2; exit 1; }
else
  mkdir -p "$(dirname "$release_dir")"
fi
[[ -x "$source_dir/tofi-guest" ]] || { echo 'Build the Linux cmd/tofi-guest binary into this directory first' >&2; exit 1; }
python3 - "$source_dir/tofi-guest" "$arch" <<'PY' || { echo 'tofi-guest was not built for this host architecture' >&2; exit 1; }
import sys
machine = {62: 'x86_64', 183: 'aarch64'}
with open(sys.argv[1], 'rb') as f:
    head = f.read(20)
sys.exit(0 if head[:4] == b'\x7fELF' and machine.get(int.from_bytes(head[18:20], 'little')) == sys.argv[2] else 1)
PY
scratch=$(mktemp -d /var/tmp/tofi-image.XXXXXX)
container_id=
trap 'if [[ -n "$container_id" ]]; then docker rm "$container_id" >/dev/null 2>&1 || true; fi; rm -rf "$scratch"' EXIT
if [[ $account_release == 1 ]]; then
  mkdir -p "$scratch/source/bin" "$scratch/build-info"
  build_dir="$scratch/source"
  info_dir="$scratch/build-info"
else
  mkdir -p "$release_dir/bin"
  build_dir="$release_dir"
  info_dir="$release_dir"
fi
firecracker_version=v1.17.0
# The kernel keeps the file name vmlinux on both architectures; on aarch64 the
# CI object is an arm64 boot "Image" (Firecracker loads it directly).
case $arch in
  x86_64)
    firecracker_sha=06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558
    kernel_key=firecracker-ci/20260916-dcfc69b625d0-0/x86_64/vmlinux-6.1.186
    base_args=()
    ;;
  aarch64)
    firecracker_sha=e351ebe4f7a16b5873bbd51005d2e6767103cff4d5ebc829df2d3f95a93e2256
    kernel_key=firecracker-ci/20260916-dcfc69b625d0-0/aarch64/vmlinux-6.1.186
    # Google publishes no Linux arm64 Chrome; Debian provides chromium.
    base_args=(--build-arg COMPUTER_BASE=mirror.gcr.io/library/debian:trixie-slim)
    ;;
esac
curl --retry 3 -fsSL "https://github.com/firecracker-microvm/firecracker/releases/download/$firecracker_version/firecracker-$firecracker_version-$arch.tgz" -o "$scratch/firecracker.tgz"
echo "$firecracker_sha  $scratch/firecracker.tgz" | sha256sum --check
tar -xzf "$scratch/firecracker.tgz" -C "$scratch"
install -m 755 "$scratch/release-$firecracker_version-$arch/firecracker-$firecracker_version-$arch" "$build_dir/bin/firecracker"
install -m 755 "$scratch/release-$firecracker_version-$arch/jailer-$firecracker_version-$arch" "$build_dir/bin/jailer"
curl --retry 3 -fsSL "https://s3.amazonaws.com/spec.ccfc.min/$kernel_key" -o "$build_dir/vmlinux"
curl --retry 3 -fsSL "https://s3.amazonaws.com/spec.ccfc.min/$kernel_key.config" -o "$info_dir/kernel.config"
docker build --pull ${base_args[@]+"${base_args[@]}"} -t "tofi-computer:$(basename "$release_dir")" "$source_dir"
container_id=$(docker create "tofi-computer:$(basename "$release_dir")")
mkdir "$scratch/rootfs"
docker export "$container_id" | tar -xpf - -C "$scratch/rootfs"
docker rm "$container_id" >/dev/null
container_id=
rm -f "$scratch/rootfs/etc/resolv.conf"
printf 'nameserver 1.1.1.1\nnameserver 8.8.8.8\n' > "$scratch/rootfs/etc/resolv.conf"
mkdir -p "$scratch/rootfs/dev"
[[ -e "$scratch/rootfs/dev/console" ]] || mknod -m 600 "$scratch/rootfs/dev/console" c 5 1
[[ -e "$scratch/rootfs/dev/null" ]] || mknod -m 666 "$scratch/rootfs/dev/null" c 1 3
truncate -s 5G "$build_dir/rootfs.ext4"
mkfs.ext4 -q -F -d "$scratch/rootfs" "$build_dir/rootfs.ext4"
cp "$scratch/rootfs/etc/tofi-browser-version" "$info_dir/browser-version.txt"
cp "$scratch/rootfs/etc/tofi-image-packages" "$info_dir/packages.txt"
cp "$source_dir/manager.py" "$build_dir/manager.py"
chmod 644 "$build_dir"/{vmlinux,rootfs.ext4,manager.py}
(cd "$build_dir" && sha256sum vmlinux rootfs.ext4 bin/firecracker bin/jailer manager.py > "$info_dir/SHA256SUMS")
printf 'arch=%s\nfirecracker=%s\nkernel_source=https://s3.amazonaws.com/spec.ccfc.min/%s\n' "$arch" "$firecracker_version" "$kernel_key" > "$info_dir/sources.txt"
if [[ $account_release == 1 ]]; then
  python3 "$source_dir/account_release_assemble.py" \
    --source-dir "$build_dir" --release-dir "$release_dir" \
    --guest-binary "$source_dir/tofi-guest" --arch "$arch"
  mv "$info_dir" "$release_info_dir"
  echo "Build diagnostics: $release_info_dir"
  cat "$release_info_dir/browser-version.txt"
else
  cat "$info_dir/browser-version.txt"
fi
echo "Computer release ready: $release_dir"
