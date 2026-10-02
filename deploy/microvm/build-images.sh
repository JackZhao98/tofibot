#!/usr/bin/env bash
# Run on the Linux x86_64 Docker host. TOFI_ACCOUNT_RELEASE=1 emits a sealed bundle.
set -euo pipefail
umask 022
[[ $(uname -sm) == 'Linux x86_64' ]] || { echo 'Google Chrome image requires Linux x86_64' >&2; exit 1; }
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
firecracker_sha=06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558
kernel_key=firecracker-ci/20260916-dcfc69b625d0-0/x86_64/vmlinux-6.1.186
curl --retry 3 -fsSL "https://github.com/firecracker-microvm/firecracker/releases/download/$firecracker_version/firecracker-$firecracker_version-x86_64.tgz" -o "$scratch/firecracker.tgz"
echo "$firecracker_sha  $scratch/firecracker.tgz" | sha256sum --check
tar -xzf "$scratch/firecracker.tgz" -C "$scratch"
install -m 755 "$scratch/release-$firecracker_version-x86_64/firecracker-$firecracker_version-x86_64" "$build_dir/bin/firecracker"
install -m 755 "$scratch/release-$firecracker_version-x86_64/jailer-$firecracker_version-x86_64" "$build_dir/bin/jailer"
curl --retry 3 -fsSL "https://s3.amazonaws.com/spec.ccfc.min/$kernel_key" -o "$build_dir/vmlinux"
curl --retry 3 -fsSL "https://s3.amazonaws.com/spec.ccfc.min/$kernel_key.config" -o "$info_dir/kernel.config"
docker build --pull -t "tofi-computer:$(basename "$release_dir")" "$source_dir"
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
printf 'firecracker=%s\nkernel_source=https://s3.amazonaws.com/spec.ccfc.min/%s\n' "$firecracker_version" "$kernel_key" > "$info_dir/sources.txt"
if [[ $account_release == 1 ]]; then
  python3 "$source_dir/account_release_assemble.py" \
    --source-dir "$build_dir" --release-dir "$release_dir" \
    --guest-binary "$source_dir/tofi-guest"
  mv "$info_dir" "$release_info_dir"
  echo "Build diagnostics: $release_info_dir"
  cat "$release_info_dir/browser-version.txt"
else
  cat "$info_dir/browser-version.txt"
fi
echo "Computer release ready: $release_dir"
