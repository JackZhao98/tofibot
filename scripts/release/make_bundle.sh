#!/usr/bin/env bash
# Build the host bundle tofi-host-<version>.tar.gz that install.sh unpacks to
# /opt/tofi/releases/<version>. Layout:
#   bin/tofi  lib/tofi_host.py  lib/microvm/*.py  compose.yaml  Caddyfile.tmpl
#   worker.apparmor.template  worker.seccomp.json  tofi.service  tofi.conf  VERSION
# The archive is reproducible: sorted entries, root ownership, fixed mtime
# (SOURCE_DATE_EPOCH, default the commit time of HEAD).
#
# Usage: scripts/release/make_bundle.sh v0.1.0 OUT_DIR
set -euo pipefail

version=${1:?usage: make_bundle.sh vX.Y.Z OUT_DIR}
out_dir=${2:?usage: make_bundle.sh vX.Y.Z OUT_DIR}
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] || { echo "invalid version '$version'" >&2; exit 1; }

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
self_host="$repo/deploy/self-host"
microvm="$repo/deploy/microvm"
microvm_modules=(account_release_check.py worker_entrypoint.py account_capacity.py manager.py
                 worker_cgroups.py account_provisioner.py account_adoption.py worker_supervisor.py)

if [[ -z ${SOURCE_DATE_EPOCH:-} ]]; then
  SOURCE_DATE_EPOCH=$(git -C "$repo" log -1 --format=%ct 2>/dev/null || echo 0)
fi
export SOURCE_DATE_EPOCH

stage=$(mktemp -d "${TMPDIR:-/tmp}/tofi-bundle.XXXXXX")
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/bin" "$stage/lib/microvm"
install -m 0755 "$self_host/bin/tofi" "$stage/bin/tofi"
install -m 0644 "$self_host/tofi_host.py" "$stage/lib/tofi_host.py"
for module in "${microvm_modules[@]}"; do
  install -m 0644 "$microvm/$module" "$stage/lib/microvm/$module"
done
for name in compose.yaml Caddyfile.tmpl worker.apparmor.template worker.seccomp.json tofi.service tofi.conf; do
  install -m 0644 "$self_host/$name" "$stage/$name"
done
printf '%s\n' "$version" > "$stage/VERSION"
chmod 0644 "$stage/VERSION"

mkdir -p "$out_dir"
archive="$out_dir/tofi-host-$version.tar.gz"
python3 - "$stage" "$archive" <<'PY'
import gzip, io, os, sys, tarfile
stage, archive = sys.argv[1:3]
mtime = int(os.environ['SOURCE_DATE_EPOCH'])
entries = []
for directory, dirs, files in os.walk(stage):
    dirs.sort()
    for name in sorted(dirs + files):
        entries.append(os.path.join(directory, name))
buffer = io.BytesIO()
with tarfile.open(fileobj=buffer, mode='w', format=tarfile.PAX_FORMAT) as tar:
    for path in sorted(entries, key=lambda p: os.path.relpath(p, stage)):
        info = tar.gettarinfo(path, arcname=os.path.relpath(path, stage))
        info.uid = info.gid = 0
        info.uname = info.gname = 'root'
        info.mtime = mtime
        info.mode = 0o755 if info.isdir() or info.mode & 0o111 else 0o644
        if info.isfile():
            with open(path, 'rb') as stream:
                tar.addfile(info, stream)
        else:
            tar.addfile(info)
with open(archive, 'wb') as raw:
    with gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=mtime) as compressed:
        compressed.write(buffer.getvalue())
PY
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$archive" | awk '{ print $1 }' > "$archive.sha256"
else
  shasum -a 256 "$archive" | awk '{ print $1 }' > "$archive.sha256"
fi
echo "$archive"
