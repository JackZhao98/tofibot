#!/usr/bin/env bash
# Installs one fixed microVM endpoint. No Tofi data or model credentials are read.
set -euo pipefail
umask 077
[[ $(id -u) == 0 ]] || { echo 'Run with sudo' >&2; exit 1; }
computer_id=${1:?Usage: install-service.sh ID SLOT RELEASE_DIR [MEMORY_MIB]}
slot=${2:?Provide a unique slot 1..250}
release_dir=${3:?Provide the completed computer release directory}
memory_mib=${4:-4096}
[[ $computer_id =~ ^[a-z0-9][a-z0-9-]{0,39}$ && $slot =~ ^[0-9]+$ && $slot -ge 1 && $slot -le 250 ]] || exit 2
[[ $release_dir == /opt/tofi-computer/releases/* && -c /dev/kvm ]] || { echo 'Invalid release or KVM unavailable' >&2; exit 2; }
release_dir=$(realpath -e "$release_dir")
[[ $release_dir == /opt/tofi-computer/releases/* && -f $release_dir/SHA256SUMS ]] || { echo 'Invalid release directory' >&2; exit 2; }
(cd "$release_dir" && sha256sum -c SHA256SUMS)
config_dir=/etc/tofi-computer
config=$config_dir/$computer_id.json
[[ ! -e $config ]] || { echo "Existing config preserved: $config" >&2; exit 2; }
mkdir -p "$config_dir" /var/lib/tofi-computer
python3 - "$computer_id" "$slot" "$release_dir" "$memory_mib" "$config" <<'PY'
import json,sys
from pathlib import Path
name,slot,release,memory,path=sys.argv[1:]
slot=int(slot);memory=int(memory)
assert 512 <= memory <= 32768
for p in Path('/etc/tofi-computer').glob('*.json'):
    if json.loads(p.read_text())['slot']==slot:
        raise SystemExit('Slot is already allocated')
config={'id':name,'slot':slot,'state_dir':'/var/lib/tofi-computer/'+name,
        'image_dir':release,'bin_dir':release+'/bin','socket_dir':'/run/tofi-computer/'+name,
        'socket_gid':10001,'memory_mib':memory,'vcpus':2,'disk_gib':8}
Path(path).write_text(json.dumps(config,indent=2)+'\n')
PY
chmod 600 "$config"
service=/etc/systemd/system/tofi-computer-$computer_id.service
[[ ! -e $service ]] || { echo 'Existing service preserved' >&2; exit 2; }
cat > "$service" <<EOF
[Unit]
Description=Tofi Firecracker computer $computer_id
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/bin/python3 $release_dir/manager.py $config
KillMode=control-group
TimeoutStopSec=60
Restart=no
UMask=0077
LimitNOFILE=4096
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
chmod 644 "$service"
systemctl daemon-reload
systemctl start "tofi-computer-$computer_id"
echo "Started isolated computer; inspect /run/tofi-computer/$computer_id/control.sock"
echo 'Autostart is an explicit promotion step after acceptance.'
