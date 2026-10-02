#!/usr/bin/env bash
# Ordinary isolated artifact build: no VM, mounts, device grants or service changes.
set -euo pipefail
[[ $(uname -sm) == 'Linux x86_64' ]] || { echo 'Linux x86_64 required' >&2; exit 2; }
image_id=${1:?Provide an immutable Docker image ID}
binary_release=${2:?Provide the read-only kernel/VM binary source release}
release_dir=${3:?Provide a new absolute destination}
[[ $image_id =~ ^sha256:[0-9a-f]{64}$ && $binary_release == /* && $release_dir == /* && ! -e $release_dir ]] || { echo 'Invalid immutable inputs/new destination' >&2; exit 2; }
source_dir=$(cd "$(dirname "$0")" && pwd)
scratch=$(mktemp -d /tmp/tofi-account-image.XXXXXX)
container_id=
cleanup() {
  if [[ -n $container_id ]]; then docker rm "$container_id" >/dev/null; fi
  rm -rf "$scratch"
}
trap cleanup EXIT
mkdir -p "$scratch/rootfs" "$scratch/source/bin"
container_id=$(docker create --network none --label tofi.scope=isolated-account-artifact "$image_id")
docker export "$container_id" | tar -xpf - -C "$scratch/rootfs"
docker rm "$container_id" >/dev/null
container_id=
# Docker's generated resolver file is excluded from exports. Match the existing
# guest-image build: DNS must live in the immutable rootfs, not Docker state.
rm -f "$scratch/rootfs/etc/resolv.conf"
printf 'nameserver 1.1.1.1\nnameserver 8.8.8.8\n' > "$scratch/rootfs/etc/resolv.conf"
# The existing guest init mounts devtmpfs; no host device nodes are created.
truncate -s 5G "$scratch/source/rootfs.ext4"
mkfs.ext4 -q -F -d "$scratch/rootfs" "$scratch/source/rootfs.ext4"
e2fsck -fn "$scratch/source/rootfs.ext4"
cp "$source_dir/manager.py" "$scratch/source/manager.py"
cp "$binary_release/vmlinux" "$scratch/source/vmlinux"
cp "$binary_release/bin/firecracker" "$scratch/source/bin/firecracker"
cp "$binary_release/bin/jailer" "$scratch/source/bin/jailer"
python3 "$source_dir/account_release_assemble.py" --source-dir "$scratch/source" --release-dir "$release_dir" --guest-binary "$scratch/rootfs/usr/local/bin/tofi-guest"
