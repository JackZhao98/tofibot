#!/usr/bin/env bash
# Install the computer for an existing, single-workspace Docker deployment.
set -euo pipefail
umask 077
[[ $(id -u) == 0 && $(uname -sm) == 'Linux x86_64' ]] || { echo 'Run on the Linux x86_64 Docker host with sudo' >&2; exit 2; }
source_dir=$(cd "$(dirname "$0")" && pwd)
repo_dir=$(cd "$source_dir/../.." && pwd)
deploy_root=${1:?Usage: setup.sh /absolute/tofi-deployment [prepared-release-directory]}
[[ $deploy_root == /* && -f $deploy_root/compose.yaml && -f $deploy_root/release.env && -d $deploy_root/data ]] || { echo 'Expected an existing Tofi deployment' >&2; exit 2; }
[[ -c /dev/kvm ]] || { echo 'This host must expose KVM (nested virtualization for a VPS)' >&2; exit 2; }
missing=0
for tool in python3 ip iptables mkfs.ext4 e2fsck curl tar flock; do command -v "$tool" >/dev/null || missing=1; done
if [[ $missing == 1 ]] && command -v apt-get >/dev/null; then
 echo 'Installing computer host dependencies…'
 apt-get update
 DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl python3 iproute2 iptables e2fsprogs util-linux tar
fi
for tool in docker python3 ip iptables mkfs.ext4 e2fsck curl tar flock; do
 command -v "$tool" >/dev/null || { echo "Missing host dependency: $tool" >&2; exit 2; }
done
docker compose version >/dev/null
exec 9>/run/tofi-computer-setup.lock
flock -n 9 || { echo 'Computer installation is already running' >&2; exit 2; }
computer_id=${TOFI_COMPUTER_ID:-personal}
slot=${TOFI_COMPUTER_SLOT:-1}
[[ $computer_id =~ ^[a-z0-9][a-z0-9-]{0,39}$ && $slot =~ ^[0-9]+$ && $slot -ge 1 && $slot -le 250 ]] || { echo 'Invalid fixed computer identity/slot' >&2; exit 2; }
release_dir=${2:-/opt/tofi-computer/releases/$(date -u +%Y%m%dT%H%M%SZ)}
if [[ ! -f "$release_dir/SHA256SUMS" ]]; then
 echo 'Preparing Linux and Google Chrome image…'
 docker build --target guest-artifact --output "type=local,dest=$source_dir" "$repo_dir"
 bash "$source_dir/build-images.sh" "$release_dir"
fi
if [[ ! -f /etc/tofi-computer/$computer_id.json ]]; then
 bash "$source_dir/install-service.sh" "$computer_id" "$slot" "$release_dir"
else
 python3 - "$computer_id" "$slot" "$release_dir" <<'PY'
import json,sys
from pathlib import Path
name,slot,release=sys.argv[1:]
c=json.loads(Path('/etc/tofi-computer',name+'.json').read_text())
if c['slot']!=int(slot) or c['image_dir']!=release:
    raise SystemExit('Existing computer preserved; use ops.py upgrade for an image change')
PY
 systemctl start "tofi-computer-$computer_id"
fi
# Save the connection configuration before attaching the new service. The
# existing compose file (including every port mapping) is never rewritten.
stamp=$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM
backup="$deploy_root/computer-install-$stamp"
mkdir -m 700 "$backup"
cp -p "$deploy_root/compose.yaml" "$deploy_root/release.env" "$backup/"
if [[ -f "$deploy_root/computer.overlay.yaml" ]]; then cp -p "$deploy_root/computer.overlay.yaml" "$backup/"; fi
install -m 644 "$source_dir/compose.overlay.yaml" "$deploy_root/computer.overlay.yaml"
python3 - "$deploy_root/release.env" "$computer_id" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1]); name=sys.argv[2]
lines=[line for line in p.read_text().splitlines() if not line.startswith('TOFI_COMPUTER_SOCKET_DIR=')]
lines.append('TOFI_COMPUTER_SOCKET_DIR=/run/tofi-computer/'+name)
temp=p.with_suffix('.computer.new');temp.write_text('\n'.join(lines)+'\n');temp.chmod(0o600);temp.replace(p)
PY
systemctl enable "tofi-computer-$computer_id"
export TOFI_DEPLOY_ROOT="$deploy_root"
docker compose --env-file "$deploy_root/release.env" -f "$deploy_root/compose.yaml" -f "$deploy_root/computer.overlay.yaml" up -d --no-build app
echo "Computer is preparing automatically. Open Tofi to see its current stage. Configuration rollback: $backup"
