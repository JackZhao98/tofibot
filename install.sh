#!/usr/bin/env bash
# TOFI one-command installer for a dedicated Linux x86_64 server with KVM.
#
#   curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh | sudo bash
#   curl -fsSL .../install.sh | sudo bash -s -- --domain tofi.example.com --email you@example.com
#
# Options:
#   --version vX.Y.Z   install this release (default: the latest release)
#   --domain NAME      serve https://NAME with automatic certificates (Caddy)
#   --email ADDRESS    contact address for the certificate authority (with --domain)
#   --local-only       listen on 127.0.0.1 only (still HTTPS; use an SSH tunnel)
#   --port PORT        App port (default 8321)
#   --yes              do not ask for confirmation
#
# By default TOFI serves HTTPS with a self-signed certificate on every
# interface, so the server opens directly at https://<server-ip>:8321.
#
# Steps 1-8 check the host, install prerequisites and download a verified
# release; the host tool (`tofi install`) does the rest. Re-running is safe: an
# existing installation is resumed or reported, never overwritten. The whole
# body runs from `main` on the last line, so a truncated download does nothing.
set -euo pipefail

RELEASES_URL=https://github.com/JackZhao98/tofibot/releases
TOTAL_STEPS=12
MIN_CPUS=2
MIN_MEMORY_MIB=3584   # "4 GiB" machines report slightly less in MemTotal
WARN_MEMORY_MIB=7680
SWAP_ADVISED_BELOW_MIB=15872   # advise swap below "16 GiB" (MemTotal reads a little less)
MIN_DISK_GIB=30
WARN_DISK_GIB=40
APT_PACKAGES=(ca-certificates curl tar zstd python3 e2fsprogs apparmor apparmor-utils openssl iproute2)

# Host paths. TOFI_TEST_ROOT and the TOFI_OS_RELEASE / TOFI_DEV_* overrides
# exist only for the installer's own tests.
ROOT=${TOFI_TEST_ROOT:-}
OS_RELEASE=${TOFI_OS_RELEASE:-$ROOT/etc/os-release}
DEV_KVM=${TOFI_DEV_KVM:-/dev/kvm}
DEV_TUN=${TOFI_DEV_TUN:-/dev/net/tun}
CGROUP_CONTROLLERS=$ROOT/sys/fs/cgroup/cgroup.controllers
APPARMOR_ENABLED=$ROOT/sys/module/apparmor/parameters/enabled
MEMINFO=$ROOT/proc/meminfo
PROC_NET=$ROOT/proc/net
VAR_LIB=$ROOT/var/lib
PREFIX=$ROOT/opt/tofi
BIN_DIR=$ROOT/usr/local/bin
STATE_FILE=$ROOT/etc/tofi/install-state.json
ENV_FILE=$ROOT/etc/tofi/tofi.env
APT_KEYRINGS=$ROOT/etc/apt/keyrings
APT_SOURCES=$ROOT/etc/apt/sources.list.d
# Package manager and Docker installer output goes here, not to the terminal.
LOG_FILE=$ROOT/var/log/tofi-install.log

VERSION=
DOMAIN=
EMAIL=
LOCAL_ONLY=0
PORT=
ASSUME_YES=0
EXISTING=0
OS_ID=
OS_VERSION=
OS_CODENAME=
WORK_DIR=

step() {
  printf '[%d/%d] %s\n' "$1" "$TOTAL_STEPS" "$2"
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

warn() {
  printf 'WARNING: %s\n' "$*" >&2
}

start_log() {
  mkdir -p "$(dirname "$LOG_FILE")"
  printf '\n=== TOFI install.sh %s ===\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$LOG_FILE"
}

logged() {
  # Run a noisy command with its output appended to the install log.
  printf '+ %s\n' "$*" >> "$LOG_FILE"
  "$@" >> "$LOG_FILE" 2>&1 < /dev/null
}

apt_get() {
  # Quiet, non-interactive apt; waits for another apt/dpkg (e.g. unattended
  # upgrades on a fresh VM) instead of failing on the lock.
  logged env DEBIAN_FRONTEND=noninteractive apt-get -qq -o Dpkg::Use-Pty=0 -o DPkg::Lock::Timeout=300 \
    -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "$@"
}

see_log() {
  printf 'see %s' "${LOG_FILE#"$ROOT"}"
}

usage() {
  cat <<'USAGE'
Usage: curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh | sudo bash -s -- [options]

  --version vX.Y.Z   install this release (default: the latest release)
  --domain NAME      serve https://NAME with automatic certificates (Caddy)
  --email ADDRESS    contact address for the certificate authority (with --domain)
  --local-only       listen on 127.0.0.1 only (still HTTPS; use an SSH tunnel)
  --port PORT        App port (default 8321)

By default TOFI serves HTTPS with a self-signed certificate on every interface:
open https://<server-ip>:8321 and accept the browser's certificate warning.
  --yes              do not ask for confirmation
USAGE
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case $1 in
      --version) [[ $# -ge 2 ]] || die "--version needs a value such as v0.1.0."; VERSION=$2; shift 2 ;;
      --domain) [[ $# -ge 2 ]] || die "--domain needs a host name."; DOMAIN=$2; shift 2 ;;
      --email) [[ $# -ge 2 ]] || die "--email needs an address."; EMAIL=$2; shift 2 ;;
      --port) [[ $# -ge 2 ]] || die "--port needs a number."; PORT=$2; shift 2 ;;
      --local-only) LOCAL_ONLY=1; shift ;;
      # --lan used to mean plain HTTP on every interface; HTTPS on every
      # interface is now the default, so it is accepted as a no-op alias.
      --lan) shift ;;
      --yes|-y) ASSUME_YES=1; shift ;;
      --help|-h) usage; exit 0 ;;
      *) die "Unknown option $1 (see --help)." ;;
    esac
  done
  if [[ -n $VERSION && ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]]; then
    die "--version must look like v0.1.0 or v0.1.0-rc.1; got '$VERSION'."
  fi
  if [[ -n $PORT ]] && { [[ ! $PORT =~ ^[0-9]+$ ]] || (( PORT < 1024 || PORT > 65535 )); }; then
    die "--port must be a number between 1024 and 65535; got '$PORT'."
  fi
  if [[ -n $DOMAIN && $LOCAL_ONLY == 1 ]]; then
    die "Use either --domain or --local-only, not both."
  fi
  if [[ -n $EMAIL && -z $DOMAIN ]]; then
    die "--email is only used together with --domain."
  fi
  if [[ -n $DOMAIN && ! $DOMAIN =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]]; then
    die "--domain must be a fully qualified host name such as tofi.example.com; got '$DOMAIN'."
  fi
}

# --- Step 1 -----------------------------------------------------------------
check_platform() {
  step 1 "Checking platform"
  local uid platform
  uid=$(id -u)
  [[ $uid == 0 ]] || die "Run as root: curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh | sudo bash (current uid $uid)."
  platform=$(uname -sm)
  [[ $platform == "Linux x86_64" ]] || die "TOFI servers need Linux x86_64; this machine is '$platform'."
}

# --- Step 2 -----------------------------------------------------------------
os_release_value() {
  # Read KEY=value from os-release without executing it.
  local key=$1 line value
  line=$(grep -E "^${key}=" "$OS_RELEASE" | head -n 1 || true)
  value=${line#*=}
  value=${value%\"}
  value=${value#\"}
  printf '%s' "$value"
}

check_os() {
  step 2 "Checking operating system"
  [[ -r $OS_RELEASE ]] || die "Cannot read $OS_RELEASE; supported systems are Ubuntu 22.04/24.04 and Debian 12/13."
  OS_ID=$(os_release_value ID)
  OS_VERSION=$(os_release_value VERSION_ID)
  OS_CODENAME=$(os_release_value VERSION_CODENAME)
  case "$OS_ID:$OS_VERSION" in
    ubuntu:22.04|ubuntu:24.04|debian:12|debian:13) ;;
    *)
      if [[ ${TOFI_ALLOW_UNSUPPORTED:-0} == 1 ]]; then
        warn "Unsupported system $OS_ID $OS_VERSION; continuing because TOFI_ALLOW_UNSUPPORTED=1."
      else
        die "Unsupported system '$OS_ID $OS_VERSION'; TOFI supports Ubuntu 22.04/24.04 and Debian 12/13 (set TOFI_ALLOW_UNSUPPORTED=1 to try anyway)."
      fi
      ;;
  esac
}

# --- Step 3 -----------------------------------------------------------------
check_virtualization() {
  step 3 "Checking KVM, TUN, cgroup v2 and AppArmor"
  [[ -c $DEV_KVM ]] || die "KVM is not available (/dev/kvm). Use bare metal or a VM with nested virtualization enabled."
  [[ -c $DEV_TUN ]] || die "The TUN device (/dev/net/tun) is missing; load it with 'modprobe tun' and retry."
  [[ -f $CGROUP_CONTROLLERS ]] || die "cgroup v2 is required (no $CGROUP_CONTROLLERS); boot with systemd.unified_cgroup_hierarchy=1."
  local apparmor=missing
  [[ -r $APPARMOR_ENABLED ]] && apparmor=$(tr -d '[:space:]' < "$APPARMOR_ENABLED")
  [[ $apparmor == Y ]] || die "AppArmor must be enabled in the kernel (found '$apparmor'); enable it and reboot."
}

# --- Step 4 -----------------------------------------------------------------
detect_existing() {
  if [[ -f $STATE_FILE ]]; then
    EXISTING=1
    local installed
    installed=$(grep -E '^TOFI_VERSION=' "$ENV_FILE" 2>/dev/null | head -n 1 | cut -d= -f2 || true)
    [[ -n $installed ]] || die "Found $STATE_FILE but no TOFI_VERSION in $ENV_FILE; run 'sudo tofi status' to inspect."
    if [[ -n $VERSION && $VERSION != "$installed" ]]; then
      die "TOFI $installed is already installed; to change versions run: sudo tofi update --version $VERSION"
    fi
    VERSION=$installed
    printf 'Existing TOFI %s found; it will be resumed or reported, keeping its data.\n' "$VERSION"
  fi
}

port_listening() {
  # True when a TCP socket listens on port $1 (IPv4 or IPv6), from /proc/net.
  local hex table
  hex=$(printf '%04X' "$1")
  for table in "$PROC_NET/tcp" "$PROC_NET/tcp6"; do
    [[ -r $table ]] || continue
    if awk -v port="$hex" 'NR > 1 && $4 == "0A" { split($2, local_address, ":"); if (local_address[2] == port) found = 1 } END { exit !found }' "$table"; then
      return 0
    fi
  done
  return 1
}

check_resources() {
  step 4 "Checking CPU, memory, disk and ports"
  local cpus memory_kib memory_mib free_kib free_gib
  cpus=$(nproc)
  (( cpus >= MIN_CPUS )) || die "TOFI needs at least $MIN_CPUS vCPUs; this host has $cpus."
  memory_kib=$(awk '$1 == "MemTotal:" { print $2 }' "$MEMINFO")
  memory_mib=$(( memory_kib / 1024 ))
  (( memory_mib >= MIN_MEMORY_MIB )) || die "TOFI needs at least 4 GiB of RAM; this host has $memory_mib MiB."
  (( memory_mib >= WARN_MEMORY_MIB )) || warn "This host has $memory_mib MiB of RAM; 8 GiB or more is recommended."
  local swap_kib
  swap_kib=$(awk '$1 == "SwapTotal:" { print $2 }' "$MEMINFO")
  if [[ -n $swap_kib ]] && (( swap_kib == 0 && memory_mib < SWAP_ADVISED_BELOW_MIB )); then
    warn "This host has $memory_mib MiB of RAM and no swap; a computer that runs out of memory can freeze the whole host. Add swap (for example a 4 GiB swap file) or use 16 GiB of RAM or more."
  fi
  local probe=$VAR_LIB
  while [[ ! -e $probe ]]; do
    probe=$(dirname "$probe")
  done
  free_kib=$(df -Pk "$probe" | awk 'NR == 2 { print $4 }')
  free_gib=$(( free_kib / 1024 / 1024 ))
  if (( EXISTING == 0 )); then
    (( free_gib >= MIN_DISK_GIB )) || die "TOFI needs at least $MIN_DISK_GIB GiB free under /var/lib; $free_gib GiB is free."
  fi
  (( free_gib >= WARN_DISK_GIB )) || warn "Only $free_gib GiB is free under /var/lib; $WARN_DISK_GIB GiB or more is recommended."
  if (( EXISTING == 0 )); then
    local port=${PORT:-8321}
    if [[ -n $DOMAIN ]]; then
      port_listening 80 && die "--domain needs port 80 free for HTTPS certificates; another service is listening on it."
      port_listening 443 && die "--domain needs port 443 free; another service is listening on it."
    fi
    if port_listening "$port"; then
      die "Port $port is already in use; choose another with --port."
    fi
  fi
}

# --- Step 5 -----------------------------------------------------------------
install_packages() {
  step 5 "Installing system packages"
  start_log
  apt_get update || die "apt-get update failed; check the network and your apt sources ($(see_log))."
  apt_get install -y --no-install-recommends "${APT_PACKAGES[@]}" \
    || die "Could not install ${APT_PACKAGES[*]} with apt-get ($(see_log))."
}

# --- Step 6 -----------------------------------------------------------------
docker_ready() {
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1
}

install_docker_from_repo() {
  install -m 0755 -d "$APT_KEYRINGS"
  curl -fsSL --proto '=https' --tlsv1.2 "https://download.docker.com/linux/$OS_ID/gpg" -o "$APT_KEYRINGS/docker.asc" || return 1
  chmod a+r "$APT_KEYRINGS/docker.asc"
  printf 'deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
    "$OS_ID" "$OS_CODENAME" > "$APT_SOURCES/docker.list"
  if ! apt_get update || ! apt_get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin; then
    rm -f "$APT_SOURCES/docker.list"
    apt_get update || true
    return 1
  fi
}

install_docker() {
  step 6 "Checking Docker"
  if ! docker_ready; then
    printf 'Installing Docker Engine and the Compose plugin from download.docker.com (a few minutes; log: %s)\n' \
      "${LOG_FILE#"$ROOT"}"
    if ! install_docker_from_repo; then
      warn "Docker's apt repository has no packages for $OS_ID $OS_CODENAME yet; using get.docker.com instead."
      curl -fsSL --proto '=https' --tlsv1.2 https://get.docker.com -o "$WORK_DIR/get-docker.sh" \
        || die "Could not download https://get.docker.com."
      logged sh "$WORK_DIR/get-docker.sh" \
        || die "Docker installation failed ($(see_log)); install Docker Engine with the Compose plugin and re-run."
    fi
    logged systemctl enable --now docker || true
    printf 'Docker installed\n'
  fi
  docker_ready || die "Docker with the Compose v2 plugin is required but 'docker compose version' failed."
  local cgroup security
  cgroup=$(docker info --format '{{.CgroupVersion}}' 2>/dev/null || true)
  [[ $cgroup == 2 ]] || die "Docker must use cgroup v2; 'docker info' reports '${cgroup:-unavailable}'."
  security=$(docker info --format '{{json .SecurityOptions}}' 2>/dev/null || true)
  [[ $security == *apparmor* ]] || die "Docker must run with AppArmor; 'docker info' security options are '${security:-unavailable}'."
}

# --- Step 7 -----------------------------------------------------------------
fetch() {
  # fetch URL FILE: HTTPS only, follows GitHub's redirect to its object store.
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --retry 3 -o "$2" "$1"
}

validate_manifest() {
  # Prints VERSION, BUNDLE_URL and BUNDLE_SHA256 as KEY=value lines.
  python3 - "$1" "$RELEASES_URL" "$VERSION" <<'PY'
import json, re, sys
path, releases, wanted = sys.argv[1:4]
sha = re.compile(r'^[0-9a-f]{64}$')
def fail(message):
    print('ERROR: invalid release manifest: ' + message, file=sys.stderr)
    sys.exit(1)
try:
    manifest = json.load(open(path))
except ValueError as error:
    fail('not JSON (%s)' % error)
if not isinstance(manifest, dict) or manifest.get('schema') != 1:
    fail('unsupported schema')
version = manifest.get('version', '')
if not re.match(r'^v\d+\.\d+\.\d+(-rc\.\d+)?$', str(version)):
    fail('bad version %r' % version)
if wanted and version != wanted:
    fail('asked for %s but the manifest is %s' % (wanted, version))
repos = {'app': 'ghcr.io/jackzhao98/tofi', 'worker': 'ghcr.io/jackzhao98/tofi-worker',
         'caddy': 'docker.io/library/caddy'}
images = manifest.get('images')
if not isinstance(images, dict) or set(images) != set(repos):
    fail('images must be exactly app, worker and caddy')
for role, repo in repos.items():
    name, _, digest = str(images[role]).partition('@sha256:')
    if name != repo or not sha.match(digest):
        fail('%s image is not pinned by digest' % role)
guest = manifest.get('guest')
if not isinstance(guest, dict) or set(guest) != {'version', 'url', 'sha256', 'manifest_sha256', 'guest_binary_sha256'}:
    fail('incomplete guest entry')
for key in ('sha256', 'manifest_sha256', 'guest_binary_sha256'):
    if not sha.match(str(guest[key])):
        fail('guest %s is not a SHA-256' % key)
bundle = manifest.get('bundle')
if not isinstance(bundle, dict) or set(bundle) != {'url', 'sha256'} or not sha.match(str(bundle['sha256'])):
    fail('incomplete bundle entry')
for url, tag in ((bundle['url'], version), (guest['url'], guest['version'])):
    if not str(url).startswith('%s/download/%s/' % (releases, tag)):
        fail('artifact %s is not from %s' % (url, releases))
if not isinstance(manifest.get('min_host'), dict) or not manifest.get('data_schema'):
    fail('missing min_host or data_schema')
print('VERSION=' + version)
print('BUNDLE_URL=' + bundle['url'])
print('BUNDLE_SHA256=' + bundle['sha256'])
PY
}

MANIFEST_FILE=
BUNDLE_URL=
BUNDLE_SHA256=

fetch_manifest() {
  step 7 "Downloading the release manifest"
  local url fields
  if [[ -n $VERSION ]]; then
    url="$RELEASES_URL/download/$VERSION/manifest.json"
  else
    url="$RELEASES_URL/latest/download/manifest.json"
  fi
  MANIFEST_FILE="$WORK_DIR/manifest.json"
  if (( EXISTING == 1 )) && [[ -f $PREFIX/releases/$VERSION/manifest.json ]]; then
    cp "$PREFIX/releases/$VERSION/manifest.json" "$MANIFEST_FILE"
  else
    if ! fetch "$url" "$MANIFEST_FILE"; then
      [[ -n $VERSION ]] || die "Could not download $url. GitHub's 'latest' skips pre-releases, so if only release candidates are published pass one with --version (see $RELEASES_URL); otherwise check network access to github.com."
      die "Could not download $url; check the version and network access to github.com."
    fi
  fi
  fields=$(validate_manifest "$MANIFEST_FILE") || exit 1
  VERSION=$(printf '%s\n' "$fields" | sed -n 's/^VERSION=//p')
  BUNDLE_URL=$(printf '%s\n' "$fields" | sed -n 's/^BUNDLE_URL=//p')
  BUNDLE_SHA256=$(printf '%s\n' "$fields" | sed -n 's/^BUNDLE_SHA256=//p')
  printf 'Release %s\n' "$VERSION"
}

# --- Step 8 -----------------------------------------------------------------
install_bundle() {
  step 8 "Installing the TOFI host tools"
  local target="$PREFIX/releases/$VERSION"
  local partial="$PREFIX/releases/.$VERSION.partial"
  local archive="$WORK_DIR/tofi-host-$VERSION.tar.gz"
  local actual
  if [[ -f $target/.bundle.sha256 && $(cat "$target/.bundle.sha256") == "$BUNDLE_SHA256" ]]; then
    printf 'Host tools %s already present\n' "$VERSION"
  else
    fetch "$BUNDLE_URL" "$archive" || die "Could not download $BUNDLE_URL."
    actual=$(sha256sum "$archive" | awk '{ print $1 }')
    [[ $actual == "$BUNDLE_SHA256" ]] || die "Checksum mismatch for $BUNDLE_URL: expected $BUNDLE_SHA256, got $actual. Nothing was installed."
    mkdir -p "$PREFIX/releases"
    rm -rf "$partial"
    mkdir -m 0755 "$partial"
    tar -xzf "$archive" --no-same-owner -C "$partial" || die "Could not unpack $archive."
    [[ -x $partial/bin/tofi && -f $partial/lib/tofi_host.py ]] || die "The host bundle $VERSION is incomplete (missing bin/tofi or lib/tofi_host.py)."
    printf '%s\n' "$BUNDLE_SHA256" > "$partial/.bundle.sha256"
    rm -rf "$target"
    mv "$partial" "$target"
  fi
  cp "$MANIFEST_FILE" "$target/manifest.json"
  chmod 0644 "$target/manifest.json"
  # Atomic switch of /opt/tofi/current, then the /usr/local/bin/tofi link.
  rm -f "$PREFIX/.current.tmp"
  ln -s "releases/$VERSION" "$PREFIX/.current.tmp"
  python3 -c 'import os, sys; os.replace(sys.argv[1], sys.argv[2])' "$PREFIX/.current.tmp" "$PREFIX/current"
  mkdir -p "$BIN_DIR"
  ln -sfn "$PREFIX/current/bin/tofi" "$BIN_DIR/tofi"
}

# --- Step 9 -----------------------------------------------------------------
hand_off() {
  step 9 "Handing over to 'tofi install'"
  local args=(install --manifest "$PREFIX/releases/$VERSION/manifest.json")
  [[ -n $DOMAIN ]] && args+=(--domain "$DOMAIN")
  [[ -n $EMAIL ]] && args+=(--email "$EMAIL")
  [[ $LOCAL_ONLY == 1 ]] && args+=(--local-only)
  [[ -n $PORT ]] && args+=(--port "$PORT")
  [[ $ASSUME_YES == 1 ]] && args+=(--yes)
  exec "$BIN_DIR/tofi" "${args[@]}"
}

cleanup() {
  if [[ -n $WORK_DIR && -d $WORK_DIR ]]; then
    rm -rf "$WORK_DIR"
  fi
}

main() {
  parse_args "$@"
  check_platform
  check_os
  check_virtualization
  detect_existing
  check_resources
  install_packages
  WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/tofi-install.XXXXXX")
  trap cleanup EXIT
  install_docker
  fetch_manifest
  install_bundle
  cleanup
  trap - EXIT
  hand_off
}

main "$@"
