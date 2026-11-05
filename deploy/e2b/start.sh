#!/bin/bash
# E2B entrypoint: serve tofi-guest on TCP 8331 behind a bearer token. The token
# is written to /var/lib/tofi/guest-token by the server after the sandbox starts.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

guest_args=(/usr/local/bin/tofi-guest --listen tcp://0.0.0.0:8331 --token-file /var/lib/tofi/guest-token --desktop-idle-timeout=900s)
guest_home=$(getent passwd 1000 | cut -d: -f6 || true)
guest_home=${guest_home:-/home/bot}

if [[ $(id -u) -eq 0 ]]; then
  mkdir -p /run/dbus
  if [[ -f /usr/share/dbus-1/system.conf ]] && ! pgrep -x dbus-daemon >/dev/null 2>&1; then
    dbus-daemon --system --fork
  fi
  mkdir -p /var/lib/tofi /workspace
  chown 1000:1000 /var/lib/tofi /workspace
  exec setpriv --reuid=1000 --regid=1000 --clear-groups env "HOME=$guest_home" "${guest_args[@]}"
elif [[ $(id -u) -eq 1000 ]]; then
  exec "${guest_args[@]}"
else
  command -v sudo >/dev/null 2>&1 || { echo "tofi-e2b-start: running as uid $(id -u) and sudo is not installed; start as root or uid 1000" >&2; exit 1; }
  exec sudo -n setpriv --reuid=1000 --regid=1000 --clear-groups env "HOME=$guest_home" "${guest_args[@]}"
fi
