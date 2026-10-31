#!/usr/bin/env python3
"""TOFI host lifecycle: install, start/stop, update/rollback, uninstall.

Runs on a dedicated Linux x86_64 or aarch64 host with KVM as root, using only the Python
standard library. install.sh downloads and verifies a release bundle, then hands
off to `tofi install --manifest ...`; everything after that lives here.

Every mutating command holds one host lock (/run/tofi-installer.lock) and
records its progress in /etc/tofi/install-state.json so an interrupted install,
update or uninstall can be resumed by running `tofi install` again. Account
data under /var/lib/tofi is never deleted except by `tofi uninstall --purge`.
"""
import argparse
import calendar
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import secrets
import shutil
import socket
import sqlite3
import ssl
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

HERE = Path(__file__).resolve().parent
if (HERE / 'microvm').is_dir():
    # Installed bundle: /opt/tofi/releases/<ver>/lib/tofi_host.py
    MICROVM = HERE / 'microvm'
    ASSETS = HERE.parent
else:
    # Source tree: deploy/self-host/tofi_host.py
    MICROVM = HERE.parent / 'microvm'
    ASSETS = HERE
sys.path.insert(0, str(MICROVM))
# The terminal UI module ships beside this file (lib/ in the bundle).
sys.path.insert(0, str(HERE))

from account_release_check import PROTOCOL, validate_release  # noqa: E402
from manager import snapshot_release  # noqa: E402
from worker_entrypoint import validate_config  # noqa: E402
import tofi_tui  # noqa: E402
# Palette, art and colour rules live in tofi_tui (shared with install.sh).
from tofi_tui import (BANNER_ART, BANNER_MIN_WIDTH, BANNER_REGIONS, COLORS, TAGLINE,  # noqa: E402,F401
                      Paint, color_mode, render_art, terminal_width)

GIB = 1024 ** 3
MIB = 1024 ** 2
APP_UID = 10001
PROJECT = 'tofi'
DATA_SCHEMA_LABEL = 'io.tofi.data-schema'
RELEASE_BASE = 'https://github.com/JackZhao98/tofibot/releases'
IMAGE_REPOS = {
    'app': 'ghcr.io/jackzhao98/tofi',
    'worker': 'ghcr.io/jackzhao98/tofi-worker',
    'caddy': 'docker.io/library/caddy',
}
# The local MCP Runner image is pinned by manifests that carry it; older manifests do not.
OPTIONAL_IMAGE_REPOS = {'runner': 'ghcr.io/jackzhao98/tofi-mcp-runner'}
APP_ENTRYPOINT = ['/app/tofi']
WORKER_ENTRYPOINT = ['python3', '/opt/tofi-worker/worker_entrypoint.py',
                     '--config', '/etc/tofi-worker/config.json']

# Host requirements (owner decisions).
MIN_CPUS = 2
# A "4 GiB" machine reports a little less in MemTotal (kernel/firmware reserve).
MIN_MEMORY_MIB = 3584
WARN_MEMORY_MIB = 7680
# Without swap, a computer that exhausts guest memory can stall the whole host
# (a frozen host leaves every task hanging). Below this much RAM TOFI advises
# swap; a "16 GiB" machine reports a little less in MemTotal.
SWAP_ADVISED_BELOW_MIB = 16 * 1024 - 512
# One computer: its guest memory (worker.json memory_mib) plus the Firecracker
# and cgroup overhead the manager adds to memory.max.
COMPUTER_MEMORY_MIB = 1024
COMPUTER_MEMORY_MIN_MIB = 1024
COMPUTER_MEMORY_DEFAULT_MIB = 2048
# Hosts whose memory budget cannot hold the default computer step down this
# ladder (all above the 1 GiB floor, which Chromium barely fits in).
COMPUTER_MEMORY_SMALL_LADDER_MIB = (1536, 1280)
# Release architectures: platform.machine() -> (manifest name, Docker name).
HOST_ARCHES = {'x86_64': ('x86_64', 'amd64'), 'AMD64': ('x86_64', 'amd64'),
               'aarch64': ('aarch64', 'arm64'), 'arm64': ('aarch64', 'arm64')}
RELEASE_ARCHES = ('x86_64', 'aarch64')
# At or below this MemTotal the host gets the small-host advisory.
SMALL_HOST_MEMORY_MIB = 4608
COMPUTER_MEMORY_MAX_MIB = 4096
COMPUTER_OVERHEAD_MIB = 512
MIN_DISK_BYTES = 30 * GIB
WARN_DISK_BYTES = 40 * GIB
# A server without a computer (TOFI_COMPUTER_BACKEND=none) needs far less: no
# Worker, no Guest, no account disks.
MIN_MEMORY_NONE_MIB = 1792
WARN_MEMORY_NONE_MIB = 3584
MIN_DISK_NONE_BYTES = 10 * GIB
WARN_DISK_NONE_BYTES = 20 * GIB
# The first account needs an 8 GiB workspace disk plus 8 GiB internal reserve.
FIRST_ACCOUNT_BYTES = 16 * GIB
DEFAULT_PORT = 8321
# Self-signed certificate for direct HTTPS. Browsers reject leaf certificates
# valid for more than 825 days; renew when fewer than 30 days remain.
CERT_DAYS = 825
CERT_RENEW_SECONDS = 30 * 86400
# Interfaces whose addresses are never how a person reaches this server.
VIRTUAL_INTERFACE_PREFIXES = ('lo', 'docker', 'br-', 'veth', 'virbr', 'tap', 'fc', 'tun', 'cni', 'flannel')

REQUIRED_TOOLS = ['docker', 'systemd-tmpfiles', 'systemctl', 'tar', 'openssl', 'iptables']
# Only the computer (Worker + Guest) needs these.
COMPUTER_TOOLS = ['apparmor_parser', 'debugfs', 'e2fsck', 'zstd']
COMPUTER_APT_PACKAGES = ['zstd', 'e2fsprogs', 'apparmor', 'apparmor-utils']
# compose.yaml starts the Worker only under this profile.
COMPUTER_PROFILE = 'computer-kvm'
# compose.yaml starts the local MCP Runner only under this profile (tofi.env names its image).
RUNNER_PROFILE = 'runner'
# The Runner's AppArmor profile: docker-default plus the remount of its own cgroup view
# (runner.apparmor.template). compose.yaml names it through TOFI_RUNNER_APPARMOR.
RUNNER_APPARMOR = 'tofi-runner'
RUNNER_ENV_KEYS = ('TOFI_RUNNER_IMAGE', 'TOFI_RUNNER_URL', 'TOFI_RUNNER_MASTER_KEY_FILE')
# The Runner's own bridge (outbound only, filtered below) and the internal App-to-Runner link.
# The egress subnet is fixed in compose.yaml so the firewall rules can name it; the link network's
# subnet is read from Docker (compose_network_subnets).
RUNNER_EGRESS_SUBNET = '172.30.77.0/24'
RUNNER_EGRESS_CHAIN = 'TOFI-RUNNER-EGRESS'
# Where the Runner container may not connect: private ranges (other hosts, other containers, the
# hypervisor's metadata and management addresses), link-local (cloud metadata), carrier-grade NAT.
RUNNER_BLOCKED_RANGES = ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', '169.254.0.0/16', '100.64.0.0/10')

PHASES = {'prepared', 'installing', 'install-failed', 'installed',
          'upgrading', 'upgrade-failed', 'rolling-back', 'rollback-failed',
          'uninstalling', 'uninstall-failed', 'stopped-retained'}
INSTALL_PHASES = {'prepared', 'installing', 'install-failed'}
UPGRADE_PHASES = {'upgrading', 'upgrade-failed', 'rolling-back', 'rollback-failed'}
UNINSTALL_PHASES = {'uninstalling', 'uninstall-failed'}

ENV_KEYS = ['TOFI_VERSION', 'TOFI_DOMAIN', 'TOFI_EMAIL', 'TOFI_HTTP_PORT', 'TOFI_BIND',
            'TOFI_PUBLIC_ORIGIN', 'TOFI_APP_IMAGE', 'TOFI_WORKER_IMAGE',
            'TOFI_CADDY_IMAGE', 'TOFI_GUEST_VERSION', 'TOFI_CPU_BUDGET',
            'TOFI_MEMORY_BUDGET_MIB', 'TOFI_WORKER_MEMORY_LIMIT', 'TOFI_TLS_CERT_FILE',
            'TOFI_TLS_KEY_FILE', 'TOFI_OWNER_ALLOW_LAN_HTTP', 'TOFI_COMPUTER_BACKEND',
            'TOFI_TRUSTED_PROXIES', 'TOFI_AUTO_UPDATE', 'TOFI_ACCOUNT_PROVISIONER_SOCKET',
            'TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT', 'TOFI_ACCOUNT_COMPUTER_DISK_GIB']
# Reverse proxies whose X-Forwarded-For the App believes when it keys sign-in
# rate limits. With --domain the only peer is the Caddy container, whose address
# is somewhere in Docker's default bridge pools (the App port is bound to
# loopback, so nothing else can reach it). Direct installs trust nobody.
LOCAL_PROXY_CIDRS = '172.16.0.0/12,192.168.0.0/16'
# How account computers run. `none` is a server and web app without any
# computer (the Worker never starts); KVM (Firecracker) is the only computer
# there is today. The value is recorded so later releases can add backends
# without guessing.
COMPUTER_BACKENDS = {'none': None, 'kvm': None, 'gvisor': 'not supported yet (coming in a later release)',
                     'container': 'not supported yet (coming in a later release)'}


AUTO_UPDATE_SERVICE = 'tofi-auto-update.service'
AUTO_UPDATE_TIMER = 'tofi-auto-update.timer'
AUTO_UPDATE_MODES = ('patch', 'off')
# The web page asks for an update by dropping request.json; a root path unit runs
# `tofi update --from-request`. D69 (docs/decisions/0069-web-update-request.md).
UPDATE_REQUEST_SERVICE = 'tofi-update-request.service'
UPDATE_REQUEST_PATH = 'tofi-update-request.path'
UPDATE_REQUEST_MAX_BYTES = 4096
UPDATE_REQUEST_MAX_AGE = 3600
UPDATE_REQUEST_WAIT_MINUTES = 60
UPDATE_NONCES_KEEP = 50
UPDATE_REQUEST_COOLDOWN = 600  # seconds between web attempts, whatever the last one's outcome
UPDATE_REJECTED_KEEP = 20
UPDATE_HISTORY_KEEP = 500
UPDATE_CHANNELS = ('stable', 'rc')
# Pre-update backups kept on disk; older ones are pruned after a good update.
BACKUP_KEEP = 3
BACKUP_FREE_FACTOR = 2
BACKUP_FREE_MARGIN = GIB
# Bot runs in these states are lost when the services stop.
ACTIVE_RUN_STATES = ('queued', 'running', 'waiting')
RUN_POLL_SECONDS = 15
DEFAULT_WAIT_MINUTES = 10


class HostError(Exception):
    """A refusal or failure with one actionable sentence for the operator."""


class LockBusy(HostError):
    """Another tofi operation holds the lifecycle lock."""


class ActiveRunsError(HostError):
    """Bot runs are still active, so the services must not be stopped."""

    def __init__(self, message, count):
        super().__init__(message)
        self.count = count


class Advisory(str):
    """A doctor result that is worth a warning but is not a failure."""


class Paths:
    """Host file locations. Tests point `root` at a temporary directory.

    Paths written into compose.yaml and worker.json are always the real
    absolute container-visible paths (see the *_REAL constants below); this
    class only decides where this process reads and writes on the host.
    """

    def __init__(self, root='/'):
        root = Path(root)
        self.root = root
        self.etc = root / 'etc/tofi'
        self.env_file = self.etc / 'tofi.env'
        self.worker_json = self.etc / 'worker.json'
        self.seccomp = self.etc / 'worker.seccomp.json'
        self.caddyfile = self.etc / 'Caddyfile'
        self.tls = self.etc / 'tls'
        self.tls_cert = self.tls / 'cert.pem'
        self.tls_key = self.tls / 'key.pem'
        self.state_file = self.etc / 'install-state.json'
        self.telemetry = self.etc / 'telemetry'
        self.apparmor_profile = root / 'etc/apparmor.d/tofi-worker'
        self.runner_apparmor_profile = root / 'etc/apparmor.d/tofi-runner'
        self.tmpfiles = root / 'etc/tmpfiles.d/tofi.conf'
        self.unit = root / 'etc/systemd/system/tofi.service'
        self.var = root / 'var/lib/tofi'
        self.data = self.var / 'data'
        self.bootstrap_secret = self.data / 'owner-bootstrap.secret'
        self.worker = self.var / 'worker'
        self.guest = self.var / 'guest'
        self.caddy = self.var / 'caddy'
        self.worker_state = self.worker / 'state'
        self.latest_cache = root / 'var/cache/tofi/latest-release.json'
        self.backups = self.var / 'backups'
        self.update_history = self.var / 'update-history.jsonl'
        self.auto_service = root / 'etc/systemd/system' / AUTO_UPDATE_SERVICE
        self.auto_timer = root / 'etc/systemd/system' / AUTO_UPDATE_TIMER
        self.update_request_service = root / 'etc/systemd/system' / UPDATE_REQUEST_SERVICE
        self.update_request_path = root / 'etc/systemd/system' / UPDATE_REQUEST_PATH
        # The only host directory the App may write (request.json), and the one it may only read (status.json).
        self.update_request_dir = self.var / 'update-request'
        self.update_status_dir = self.var / 'update-status'
        self.update_nonces = self.var / 'update-nonces.json'
        self.update_last_attempt = self.var / 'update-last-attempt.json'
        self.opt = root / 'opt/tofi'
        self.releases = self.opt / 'releases'
        self.current = self.opt / 'current'
        self.bin_link = root / 'usr/local/bin/tofi'
        self.run_dir = root / 'run/tofi'
        self.lockfile = root / 'run/tofi-installer.lock'
        # Host probes.
        self.dev_kvm = Path('/dev/kvm')
        self.dev_tun = Path('/dev/net/tun')
        self.cgroup_controllers = root / 'sys/fs/cgroup/cgroup.controllers'
        self.apparmor_enabled = root / 'sys/module/apparmor/parameters/enabled'
        self.apparmor_profiles = root / 'sys/kernel/security/apparmor/profiles'
        self.meminfo = root / 'proc/meminfo'
        self.disk_probe = root / 'var/lib'


P = Paths()

# Real paths as seen by containers (bind mounts use identical paths).
VAR_REAL = '/var/lib/tofi'
RUN_REAL = '/run/tofi'
# compose.yaml mounts /etc/tofi/tls read-only here inside the App container.
TLS_REAL = '/etc/tofi-tls'


# --------------------------------------------------------------------------
# Small helpers


def run(args, check=True, timeout=300, **kwargs):
    return subprocess.run(args, check=check, capture_output=True, text=True,
                          timeout=timeout, **kwargs)


def say(message=''):
    tofi_tui.clear_spinner_line()
    print(message, flush=True)


def warn(message):
    tofi_tui.clear_spinner_line()
    print('WARNING: ' + message, file=sys.stderr, flush=True)


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as stream:
        for block in iter(lambda: stream.read(MIB), b''):
            digest.update(block)
    return digest.hexdigest()


def write_file(path, content, mode=0o600):
    """Atomically replace `path` (fsync, then rename) with the given mode."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    data = content.encode() if isinstance(content, str) else content
    fd, temporary = tempfile.mkstemp(prefix='.tofi-write-', dir=str(path.parent))
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def write_json(path, value, mode=0o600):
    write_file(path, json.dumps(value, indent=2, sort_keys=True) + '\n', mode)


def remove_tree(path):
    """Remove a tree that may contain read-only (0555/0444) entries."""
    path = Path(path)
    if path.is_symlink() or path.is_file():
        path.unlink()
        return
    if not path.exists():
        return
    for directory, _dirs, files in os.walk(path):
        os.chmod(directory, 0o700)
        for name in files:
            entry = Path(directory) / name
            if not entry.is_symlink():
                os.chmod(entry, 0o600)
    shutil.rmtree(path)


def require_root():
    if os.geteuid() != 0:
        raise HostError('Run this command as root (for example with sudo).')


# --------------------------------------------------------------------------
# Lock and journal


@contextmanager
def lifecycle_lock():
    """Hold the single host lifecycle lock for one mutating operation."""
    require_root()
    lockfile = P.lockfile
    lockfile.parent.mkdir(parents=True, exist_ok=True)
    flags = os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK
    try:
        fd = os.open(str(lockfile), flags, 0o600)
    except OSError as error:
        raise HostError('Cannot open the installer lock %s (%s).' % (lockfile, error)) from error
    try:
        info = os.fstat(fd)
        unsafe = (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                  or info.st_mode & 0o077 or info.st_nlink != 1)
        if unsafe:
            raise HostError('The installer lock %s is not a private root-owned file; remove it and retry.' % lockfile)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise LockBusy('Another tofi operation is running (lock %s); wait for it to finish.' % lockfile)
        current = os.stat(str(lockfile), follow_symlinks=False)
        if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino):
            raise HostError('The installer lock changed while it was being taken; retry.')
        yield
    finally:
        # Never unlink the lock inode: another process may be waiting on it.
        os.close(fd)


def settle_interrupted_restores():
    """Put back /etc/tofi or the data directory if a restore was cut between
    its two renames (nothing else can find the journal without /etc/tofi)."""
    for target in (P.etc, P.data):
        try:
            restore_leftovers(target)
        except OSError as error:
            warn('Could not settle an interrupted restore of %s (%s).' % (target, error))


def load_state():
    settle_interrupted_restores()
    try:
        state = json.loads(P.state_file.read_text())
    except FileNotFoundError:
        return None
    if not isinstance(state, dict) or state.get('schema') != 1 or state.get('phase') not in PHASES:
        raise HostError('The install journal %s is not readable; inspect it before continuing.' % P.state_file)
    return state


def save_state(state):
    state['updated_at'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    write_json(P.state_file, state)


def rejected_versions(state):
    """Versions that were rolled back on this host: the kept list plus the older single field."""
    found = [v for v in (state.get('rejected_versions') or []) if isinstance(v, str)]
    old = (state.get('last_update_failure') or {}).get('version')
    if isinstance(old, str) and old not in found:
        found.append(old)
    return found


def remember_rejected(state, version):
    found = [v for v in rejected_versions(state) if v != version] + [version]
    state['rejected_versions'] = found[-UPDATE_REJECTED_KEEP:]


def new_state(version):
    return {'schema': 1, 'phase': 'prepared', 'version': version,
            'transaction': {'kind': 'install', 'step': 'prepared'}, 'last_error': None}


def transition(state, phase, step):
    state['phase'] = phase
    state['transaction']['step'] = step
    state['last_error'] = None
    save_state(state)


def failed(state, phase, error):
    step = (state.get('transaction') or {}).get('step')
    state['phase'] = phase
    state['last_error'] = {'step': step, 'message': str(error)[:500]}
    save_state(state)


def complete(state, phase='installed', version=None):
    if version is not None:
        state['version'] = version
    state['phase'] = phase
    state['transaction'] = None
    state['last_error'] = None
    save_state(state)


# --------------------------------------------------------------------------
# tofi.env


def read_env(path=None):
    return parse_env(Path(path or P.env_file).read_text(), path or P.env_file)


def parse_env(text, path='tofi.env'):
    values = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        key, separator, value = line.partition('=')
        if not separator:
            raise HostError('Malformed line in %s: %r' % (path, line))
        values[key.strip()] = value.strip()
    return values


def format_env(values):
    """Render tofi.env: managed keys first (ENV_KEYS order), then every other
    key of `values` verbatim in its original order, so operator-added lines
    (a custom TOFI_* variable, a proxy list) survive install-resume, update,
    rollback and restore. Comments other than the header are not kept."""
    lines = ['# Managed by tofi. Edit with care; run `sudo tofi stop && sudo tofi start` after changes.']
    for key in ENV_KEYS:
        value = str(values.get(key, ''))
        if '\n' in value or ' ' in value:
            raise HostError('Invalid value for %s' % key)
        lines.append('%s=%s' % (key, value))
    for key, value in values.items():
        if key in ENV_KEYS:
            continue
        value = str(value)
        if '\n' in value or '\r' in value or '\n' in key or '=' in key or not key:
            raise HostError('Invalid line for %s in tofi.env' % (key or '(empty key)'))
        lines.append('%s=%s' % (key, value))
    return '\n'.join(lines) + '\n'


def write_env(values):
    write_file(P.env_file, format_env(values), 0o600)


# --------------------------------------------------------------------------
# Release manifest

SHA_RE = re.compile(r'^[0-9a-f]{64}$')
VERSION_RE = re.compile(r'^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$')
DOMAIN_RE = re.compile(r'^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$')
EMAIL_RE = re.compile(r'^[^@\s]+@[^@\s]+\.[^@\s]+$')


# Upgrade acceptance only: with a LOCAL manifest file (never a downloaded one)
# and this variable set to 1, the App image may come from a loopback registry,
# so a test VM can stage a release whose App deliberately fails its health
# check and prove the rollback. See upgrade_acceptance.py.
ACCEPTANCE_LOCAL_APP_ENV = 'TOFI_ACCEPTANCE_LOCAL_APP_IMAGE'
LOOPBACK_REGISTRIES = ('localhost:', '127.0.0.1:')


def manifest_image(manifest, role):
    """An image a release pins: in `images`, or for newer optional roles in `optional_images`."""
    return (manifest.get('images') or {}).get(role) or (manifest.get('optional_images') or {}).get(role)


def validate_manifest(manifest, allow_loopback_app=False):
    """Check the shape of a release manifest.json and return it."""
    if not isinstance(manifest, dict) or manifest.get('schema') != 1:
        raise HostError('The release manifest has an unsupported schema (expected 1).')
    version = manifest.get('version')
    if not isinstance(version, str) or not VERSION_RE.match(version):
        raise HostError('The release manifest version %r is not vMAJOR.MINOR.PATCH[-rc.N].' % version)
    images = manifest.get('images')
    optional = manifest.get('optional_images', {})
    if not isinstance(images, dict) or set(images) - set(OPTIONAL_IMAGE_REPOS) != set(IMAGE_REPOS):
        raise HostError('The release manifest must pin exactly the app, worker and caddy images (and optionally the runner).')
    if not isinstance(optional, dict) or set(optional) - set(OPTIONAL_IMAGE_REPOS) or set(optional) & set(images):
        raise HostError('The release manifest optional_images may pin only the runner image, once.')
    every = {**images, **optional}
    for role, repo in {**IMAGE_REPOS, **{k: v for k, v in OPTIONAL_IMAGE_REPOS.items() if k in every}}.items():
        reference = every[role]
        name, _, digest = str(reference).partition('@sha256:')
        loopback_app = allow_loopback_app and role == 'app' and name.startswith(LOOPBACK_REGISTRIES)
        if (name != repo and not loopback_app) or not SHA_RE.match(digest):
            raise HostError('The %s image must be pinned as %s@sha256:<digest>; got %r.' % (role, repo, reference))
    download = '%s/download/%s/' % (RELEASE_BASE, version)
    guest = manifest.get('guest')
    validate_guest_entry(guest, 'guest')
    bundle = manifest.get('bundle')
    if not isinstance(bundle, dict) or set(bundle) != {'url', 'sha256'} or not SHA_RE.match(str(bundle['sha256'])):
        raise HostError('The release manifest bundle entry is incomplete.')
    if not str(bundle['url']).startswith(download):
        raise HostError('Release artifacts must be downloaded from %s over HTTPS.' % RELEASE_BASE)
    # Optional per-architecture Guest entries. The top-level `guest` stays the
    # x86_64 one so hosts that predate `arches` read the same manifest.
    arches = manifest.get('arches')
    if arches is not None:
        if not isinstance(arches, dict) or not set(arches) <= set(RELEASE_ARCHES):
            raise HostError('The release manifest arches entry is invalid.')
        for arch, entry in arches.items():
            if not isinstance(entry, dict) or set(entry) != {'guest'}:
                raise HostError('The release manifest %s entry is incomplete.' % arch)
            validate_guest_entry(entry['guest'], 'guest (%s)' % arch)
            if entry['guest']['version'] != guest['version']:
                raise HostError('The %s guest version differs from the release guest version.' % arch)
        if 'x86_64' in arches and arches['x86_64']['guest'] != guest:
            raise HostError('The x86_64 guest entry must match the top-level guest entry.')
    min_host = manifest.get('min_host')
    if not isinstance(min_host, dict):
        raise HostError('The release manifest has no min_host requirements.')
    if not isinstance(manifest.get('data_schema'), str) or not manifest['data_schema']:
        raise HostError('The release manifest has no data_schema.')
    return manifest


def validate_guest_entry(guest, label):
    guest_keys = {'version', 'url', 'sha256', 'manifest_sha256', 'guest_binary_sha256'}
    if not isinstance(guest, dict) or set(guest) != guest_keys:
        raise HostError('The release manifest %s entry is incomplete.' % label)
    if not VERSION_RE.match(str(guest['version'])):
        raise HostError('The guest version %r is invalid.' % guest['version'])
    for key in ('sha256', 'manifest_sha256', 'guest_binary_sha256'):
        if not SHA_RE.match(str(guest[key])):
            raise HostError('The guest %s is not a SHA-256 digest.' % key)
    if not str(guest['url']).startswith('%s/download/%s/' % (RELEASE_BASE, guest['version'])):
        raise HostError('Release artifacts must be downloaded from %s over HTTPS.' % RELEASE_BASE)


def host_arch():
    """(release arch, Docker arch) of this host, or None when unsupported."""
    if platform.system() != 'Linux':
        return None
    return HOST_ARCHES.get(platform.machine())


def guest_for_host(manifest, arch=None):
    """The Guest entry of `manifest` for this host's architecture.

    A manifest without `arches` (every release before arm64) carries x86_64 only.
    """
    if arch is None:
        found = host_arch()
        arch = found[0] if found else 'x86_64'
    entry = (manifest.get('arches') or {}).get(arch)
    if entry:
        return entry['guest']
    if arch == 'x86_64':
        return manifest['guest']
    raise HostError('Release %s has no Guest for %s hosts; it was built for x86_64 only. '
                    'Install a newer release.' % (manifest['version'], arch))


def manifest_notes_url(manifest):
    """The release notes link, or None. notes_url is optional (older releases
    have none) and cosmetic, so a malformed one is ignored, never an error."""
    notes = manifest.get('notes_url')
    if isinstance(notes, str) and notes.startswith('https://') and len(notes) <= 300 and not any(
            c.isspace() or ord(c) < 32 for c in notes):
        return notes
    return None


def load_manifest_file(path):
    try:
        manifest = json.loads(Path(path).read_text())
    except (OSError, ValueError) as error:
        raise HostError('Cannot read release manifest %s (%s).' % (path, error)) from error
    return validate_manifest(manifest, allow_loopback_app=os.environ.get(ACCEPTANCE_LOCAL_APP_ENV) == '1')


def manifest_url(version=None):
    if version:
        return '%s/download/%s/manifest.json' % (RELEASE_BASE, version)
    return '%s/latest/download/manifest.json' % RELEASE_BASE


class HTTPSOnlyRedirects(urllib.request.HTTPRedirectHandler):
    """Follow GitHub's redirect to its object store, but never to plain HTTP."""

    def redirect_request(self, request, fp, code, message, headers, new_url):
        if not new_url.startswith('https://'):
            raise HostError('Refusing a redirect to a non-HTTPS location: %s' % new_url)
        return super().redirect_request(request, fp, code, message, headers, new_url)


def https_open(url, timeout=60):
    if not url.startswith('https://'):
        raise HostError('Refusing a non-HTTPS download: %s' % url)
    opener = urllib.request.build_opener(urllib.request.HTTPSHandler(), HTTPSOnlyRedirects())
    return opener.open(url, timeout=timeout)


def fetch_manifest(version=None, timeout=60):
    url = manifest_url(version)
    try:
        with https_open(url, timeout=timeout) as response:
            manifest = json.loads(response.read(MIB))
    except (OSError, ValueError) as error:
        raise HostError('Cannot download the release manifest %s (%s).' % (url, error)) from error
    return validate_manifest(manifest)


def download(url, destination, expected_sha256):
    """Stream `url` to `destination`; remove it and fail on a digest mismatch."""
    digest = hashlib.sha256()
    try:
        with https_open(url, timeout=120) as response, open(destination, 'wb') as output:
            for block in iter(lambda: response.read(MIB), b''):
                digest.update(block)
                output.write(block)
            output.flush()
            os.fsync(output.fileno())
    except BaseException:
        Path(destination).unlink(missing_ok=True)
        raise
    if digest.hexdigest() != expected_sha256:
        Path(destination).unlink(missing_ok=True)
        raise HostError('Checksum mismatch for %s: expected %s, got %s.' % (url, expected_sha256, digest.hexdigest()))


# --------------------------------------------------------------------------
# Preflight


def meminfo_mib():
    values = {}
    for line in P.meminfo.read_text().splitlines():
        key, _, rest = line.partition(':')
        if key in ('MemTotal', 'MemAvailable', 'SwapTotal'):
            values[key] = int(rest.split()[0]) // 1024
    return values


def computer_memory_need_mib(config=None):
    """Host memory one running computer takes, from worker.json when known."""
    memory = (config or {}).get('memory_mib') or COMPUTER_MEMORY_MIB
    return int(memory) + COMPUTER_OVERHEAD_MIB


def memory_report(per_computer_mib=None):
    """Return (warning or None, detail): host memory against computer needs.

    Warning only: a host without swap and under 16 GiB of RAM can freeze as a
    whole when one computer runs out of memory.
    """
    info = meminfo_mib()
    need = per_computer_mib or computer_memory_need_mib()
    total = info['MemTotal']
    available = info.get('MemAvailable', total)
    swap = info.get('SwapTotal')
    detail = '%d MiB available of %d MiB; each computer needs about %d MiB (room for %d now); swap %s' % (
        available, total, need, available // need, 'unknown' if swap is None else '%d MiB' % swap)
    warning = None
    if swap == 0 and total < SWAP_ADVISED_BELOW_MIB:
        warning = ('This host has %d MiB of RAM and no swap; a computer that runs out of memory can freeze '
                   'the whole host. Add swap (for example a 4 GiB swap file) or use 16 GiB of RAM or more. '
                   'Memory: %s.') % (total, detail)
    return warning, detail


def free_disk_bytes():
    probe = P.disk_probe
    while not probe.exists():
        probe = probe.parent
    info = os.statvfs(str(probe))
    return info.f_bavail * info.f_frsize


def port_in_use(host, port):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            probe.bind((host, port))
        except OSError:
            return True
    return False


def own_project_running():
    result = run(['docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + PROJECT], check=False)
    return bool(result.stdout.strip())


def domain_resolves_here(domain):
    try:
        addresses = {info[4][0] for info in socket.getaddrinfo(domain, 443)}
    except OSError:
        return False
    return bool(addresses & set(host_addresses()))


def host_addresses():
    result = run(['hostname', '-I'], check=False)
    return result.stdout.split()


def interface_addresses():
    """Addresses people can reach this server on: [(family, address)].

    Global-scope IPv4/IPv6 of physical-looking interfaces from `ip -o addr`;
    loopback, link-local and container/VM bridges (docker0, br-*, veth*, tap*)
    are left out. Falls back to `hostname -I`. No packet is sent.
    """
    found = []
    try:
        output = run(['ip', '-o', 'addr', 'show'], check=False).stdout
    except (OSError, subprocess.SubprocessError):
        output = ''
    for line in output.splitlines():
        fields = line.split()
        if len(fields) < 4 or fields[2] not in ('inet', 'inet6'):
            continue
        name = fields[1].split('@')[0]
        scope = fields[fields.index('scope') + 1] if 'scope' in fields[:-1] else 'global'
        if name.startswith(VIRTUAL_INTERFACE_PREFIXES) or scope != 'global':
            continue
        entry = (fields[2], fields[3].split('/')[0])
        if entry not in found:
            found.append(entry)
    if not found:
        for address in host_addresses():
            family = 'inet6' if ':' in address else 'inet'
            if address.startswith(('127.', '169.254.', '172.17.', 'fe80')) or address == '::1':
                continue
            found.append((family, address))
    return found


def access_urls(env):
    """Browser URLs for the App, one per reachable IPv4 address."""
    if env.get('TOFI_DOMAIN'):
        return ['https://' + env['TOFI_DOMAIN']]
    scheme = app_scheme(env)
    port = env.get('TOFI_HTTP_PORT') or str(DEFAULT_PORT)
    if env.get('TOFI_BIND') != '0.0.0.0':
        return ['%s://127.0.0.1:%s' % (scheme, port)]
    urls = ['%s://%s:%s' % (scheme, address, port) for family, address in interface_addresses() if family == 'inet']
    return urls or ['%s://<server-ip>:%s' % (scheme, port)]


# --------------------------------------------------------------------------
# Self-signed HTTPS certificate (default direct mode)


def app_scheme(env):
    return 'https' if env.get('TOFI_TLS_CERT_FILE') else 'http'


def certificate_hostname():
    name = socket.gethostname().strip().lower().rstrip('.')
    if re.match(r'^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$', name or ''):
        return name
    return 'tofi'


def certificate_names():
    """(DNS names, IP addresses) the self-signed certificate covers."""
    hostname = certificate_hostname()
    dns = [hostname] + (['localhost'] if hostname != 'localhost' else [])
    ips = ['127.0.0.1', '::1']
    for _family, address in interface_addresses():
        if address not in ips:
            ips.append(address)
    return dns, ips


def certificate_usable():
    """True when cert and key exist and the cert has more than 30 days left."""
    if not (P.tls_cert.is_file() and P.tls_key.is_file()):
        return False
    result = run(['openssl', 'x509', '-checkend', str(CERT_RENEW_SECONDS), '-noout',
                  '-in', str(P.tls_cert)], check=False)
    return result.returncode == 0


def prepare_tls_directory():
    P.tls.mkdir(parents=True, exist_ok=True)
    os.chown(P.tls, 0, APP_UID)
    os.chmod(P.tls, 0o750)


def generate_certificate():
    """Create an EC P-256 self-signed certificate for this host's names.

    The App (uid 10001) reads the key through group 10001; nothing else can.
    Files are written beside the old pair and renamed into place.
    """
    prepare_tls_directory()
    hostname = certificate_hostname()
    dns, ips = certificate_names()
    alt = ['DNS:' + name for name in dns] + ['IP:' + address for address in ips]
    config = '\n'.join([
        '[req]', 'distinguished_name = dn', 'prompt = no', 'x509_extensions = v3', '',
        '[dn]', 'CN = ' + hostname, 'O = TOFI self-signed', '',
        '[v3]', 'subjectAltName = ' + ','.join(alt), 'basicConstraints = critical,CA:FALSE',
        'keyUsage = critical,digitalSignature', 'extendedKeyUsage = serverAuth',
        'subjectKeyIdentifier = hash', ''])
    work = Path(tempfile.mkdtemp(prefix='.tofi-tls-', dir=str(P.tls)))
    try:
        key, cert, conf = work / 'key.pem', work / 'cert.pem', work / 'openssl.cnf'
        conf.write_text(config)
        try:
            run(['openssl', 'ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', str(key)])
            run(['openssl', 'req', '-new', '-x509', '-sha256', '-days', str(CERT_DAYS), '-key', str(key),
                 '-out', str(cert), '-config', str(conf)])
        except (OSError, subprocess.CalledProcessError) as error:
            detail = getattr(error, 'stderr', None) or str(error)
            raise HostError('Cannot create the HTTPS certificate with openssl: %s' % detail.strip()[-300:]) from error
        os.chown(key, 0, APP_UID)
        os.chmod(key, 0o640)
        os.chown(cert, 0, APP_UID)
        os.chmod(cert, 0o644)
        os.replace(key, P.tls_key)
        os.replace(cert, P.tls_cert)
    finally:
        shutil.rmtree(work, ignore_errors=True)
    return certificate_fingerprint()


def ensure_tls(env):
    """Keep /etc/tofi/tls present (compose mounts it); create the cert once."""
    prepare_tls_directory()
    if app_scheme(env) == 'https' and not certificate_usable():
        generate_certificate()
        return True
    return False


def certificate_fingerprint(path=None):
    """SHA-256 fingerprint as browsers show it (AB:CD:...), or None."""
    try:
        der = ssl.PEM_cert_to_DER_cert(Path(path or P.tls_cert).read_text())
    except (OSError, ValueError):
        return None
    digest = hashlib.sha256(der).hexdigest().upper()
    return ':'.join(digest[i:i + 2] for i in range(0, len(digest), 2))


def regenerate_certificate():
    """`tofi regenerate-cert`: new self-signed pair, then restart the App."""
    with lifecycle_lock():
        state = load_state()
        if state is None:
            raise HostError('TOFI is not installed; run install.sh.')
        env = read_env()
        if app_scheme(env) != 'https':
            raise HostError('This installation does not serve its own certificate '
                            '(--domain uses Caddy and Let\'s Encrypt).')
        fingerprint = generate_certificate()
        if state['phase'] == 'installed' and project_containers():
            compose('restart', 'app', env=env)
            health(env)
        say('New certificate SHA-256 fingerprint: %s' % fingerprint)
        return {'regenerated': True, 'fingerprint': fingerprint}


def backend_of(env):
    """The computer backend recorded in tofi.env: kvm or none. A missing value
    is kvm, the only mode installs had before computers became optional."""
    return (env.get('TOFI_COMPUTER_BACKEND') or 'kvm')


def has_computer(env):
    return backend_of(env) == 'kvm'


def computer_host_problems():
    """Why this kernel cannot run KVM computers (empty: it can), most basic first."""
    problems = []
    if not P.dev_kvm.exists() or not stat.S_ISCHR(P.dev_kvm.stat().st_mode):
        problems.append('KVM is not available (/dev/kvm). Use bare metal or a VM with nested virtualization enabled.')
    if not P.dev_tun.exists() or not stat.S_ISCHR(P.dev_tun.stat().st_mode):
        problems.append('The TUN device /dev/net/tun is missing; load the tun kernel module (modprobe tun).')
    if not P.cgroup_controllers.exists():
        problems.append('cgroup v2 is required; boot with systemd.unified_cgroup_hierarchy=1.')
    try:
        apparmor = P.apparmor_enabled.read_text().strip()
    except OSError:
        apparmor = 'missing'
    if apparmor != 'Y':
        problems.append('AppArmor must be enabled in the kernel (found %s); enable it and reboot.' % apparmor)
    return problems


def default_computer():
    """kvm when this host can run computers, otherwise none."""
    return 'none' if computer_host_problems() else 'kvm'


def computer_memory_warnings(memory):
    """Advisories about running computers on a host with `memory` MiB of RAM."""
    warnings = []
    if memory < WARN_MEMORY_MIB:
        warnings.append('This host has %d MiB of RAM; 8 GiB or more is recommended.' % memory)
    if memory <= SMALL_HOST_MEMORY_MIB:
        _, reason = default_computer_resources(1, runtime_budgets()['memory_mib'])
        warnings.append('This is a small host (%d MiB of RAM): it runs a single computer at a time. %s'
                        % (memory, reason or ''))
    swap_warning, _ = memory_report()
    if swap_warning:
        warnings.append(swap_warning)
    return warnings


def preflight(options):
    """Refuse an unsupported host before anything is written; return warnings.

    The KVM, TUN, cgroup v2 and AppArmor checks, the computer tools and the
    computer-sized memory and disk minimums apply only to a `kvm` install; a
    `none` install needs just Docker and a small machine."""
    warnings = []
    computer = options.get('computer') or 'kvm'
    kvm = computer == 'kvm'
    if host_arch() is None:
        raise HostError('TOFI needs Linux x86_64 or aarch64; this host is %s %s.'
                        % (platform.system(), platform.machine()))
    require_root()
    if kvm:
        problems = computer_host_problems()
        if problems:
            raise HostError(problems[0])
    tools = REQUIRED_TOOLS + (COMPUTER_TOOLS if kvm else [])
    missing = [tool for tool in tools if not shutil.which(tool)]
    if missing:
        raise HostError('Missing host tools: %s. Re-run install.sh, which installs them.' % ', '.join(missing))
    try:
        info = json.loads(run(['docker', 'info', '--format', '{{json .}}']).stdout)
    except (subprocess.SubprocessError, ValueError) as error:
        raise HostError('Docker is not running; start it with `systemctl start docker`.') from error
    if kvm:
        if str(info.get('CgroupVersion')) != '2':
            raise HostError('Docker must use cgroup v2 (found %s).' % info.get('CgroupVersion'))
        if not any('apparmor' in item for item in info.get('SecurityOptions') or []):
            raise HostError('Docker must run with AppArmor enabled (security options: %s).' % info.get('SecurityOptions'))
    try:
        run(['docker', 'compose', 'version'])
    except subprocess.SubprocessError as error:
        raise HostError('Docker Compose v2 is missing; install docker-compose-plugin.') from error
    cpus = os.cpu_count() or 0
    min_cpus = MIN_CPUS if kvm else 1
    if cpus < min_cpus:
        raise HostError('TOFI needs at least %d vCPUs; this host has %d.' % (min_cpus, cpus))
    memory = meminfo_mib()['MemTotal']
    if kvm:
        if memory < MIN_MEMORY_MIB:
            raise HostError('TOFI needs at least 4 GiB of RAM; this host has %d MiB.' % memory)
        warnings += computer_memory_warnings(memory)
    else:
        if memory < MIN_MEMORY_NONE_MIB:
            raise HostError('TOFI needs at least 2 GiB of RAM; this host has %d MiB.' % memory)
        if memory < WARN_MEMORY_NONE_MIB:
            warnings.append('This host has %d MiB of RAM; 4 GiB or more is recommended.' % memory)
    free = free_disk_bytes()
    min_disk, warn_disk = (MIN_DISK_BYTES, WARN_DISK_BYTES) if kvm else (MIN_DISK_NONE_BYTES, WARN_DISK_NONE_BYTES)
    if free < min_disk:
        raise HostError('TOFI needs at least %d GiB free under /var/lib; %.1f GiB is free.' % (min_disk // GIB, free / GIB))
    if free < warn_disk:
        warnings.append('Only %.1f GiB is free under /var/lib; %d GiB or more is recommended.' % (free / GIB, warn_disk // GIB))
    if not own_project_running():
        bind = options['bind']
        port = options['port']
        if port_in_use(bind, port):
            raise HostError('Port %d on %s is already in use; pick another with --port.' % (port, bind))
        if options.get('domain'):
            for tls_port in (80, 443):
                if port_in_use('0.0.0.0', tls_port):
                    raise HostError('--domain needs ports 80 and 443 free; port %d is in use.' % tls_port)
    if options.get('domain') and not domain_resolves_here(options['domain']):
        warnings.append('%s does not resolve to an address of this host yet; HTTPS certificates '
                        'will be issued once DNS points here.' % options['domain'])
    others = run(['docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project!=' + PROJECT], check=False)
    if others.stdout.strip():
        warnings.append('Other containers are running on this host; TOFI sizes its budgets for the whole machine.')
    if kvm and firecracker_running():
        warnings.append('Firecracker processes not managed by TOFI are running; leave spare capacity for them.')
    return warnings


def firecracker_running():
    proc = P.root / 'proc'
    if not proc.is_dir():
        return False
    for entry in proc.iterdir():
        if not entry.name.isdecimal():
            continue
        try:
            if (entry / 'comm').read_text().strip() == 'firecracker':
                return True
        except OSError:
            continue
    return False


# --------------------------------------------------------------------------
# Rendering


def runtime_budgets():
    cpus = os.cpu_count() or 1
    memory = meminfo_mib()['MemTotal']
    return {'cpu': max(1, cpus - 1), 'memory_mib': max(1536, memory - 2048)}


def default_computer_resources(cpu_budget, memory_budget_mib):
    """Return ((vcpus, memory_mib), reason or None) for a new computer.

    2 vCPU when the CPU budget allows it; memory is a quarter of the install's
    memory budget clamped to 2..4 GiB. A computer claims memory + overhead of
    the budget, so a budget too small for the target falls back to the largest
    of 2048 / 1024 MiB that fits, and the reason is reported.
    """
    vcpus = 2 if int(cpu_budget) >= 2 else 1
    budget = int(memory_budget_mib)
    target = min(COMPUTER_MEMORY_MAX_MIB, max(COMPUTER_MEMORY_DEFAULT_MIB, budget // 4))
    ladder = (target, COMPUTER_MEMORY_DEFAULT_MIB) + COMPUTER_MEMORY_SMALL_LADDER_MIB + (COMPUTER_MEMORY_MIN_MIB,)
    for memory in ladder:
        if memory + COMPUTER_OVERHEAD_MIB <= budget:
            break
    reason = None
    if memory == COMPUTER_MEMORY_MIN_MIB:
        reason = ('The memory budget (%d MiB) cannot hold a %d MiB computer; using the %d MiB minimum, '
                  'which is tight for Chromium.' % (budget, COMPUTER_MEMORY_DEFAULT_MIB, memory))
    elif memory < COMPUTER_MEMORY_DEFAULT_MIB:
        reason = ('The memory budget (%d MiB) cannot hold a %d MiB computer; using %d MiB, enough for Chromium '
                  'with one or two pages. Add swap for headroom.' % (budget, COMPUTER_MEMORY_DEFAULT_MIB, memory))
    elif memory < target:
        reason = 'The memory budget (%d MiB) limits the computer to %d MiB.' % (budget, memory)
    if vcpus < 2:
        reason = (reason + ' ' if reason else '') + 'Only 1 CPU is budgeted for computers.'
    return (vcpus, memory), reason


def render_worker_config(guest_version, cpu_budget, memory_budget_mib, guest_binary_sha256,
                         release_manifest_sha256):
    """Render worker.json for the given Guest release and runtime budgets."""
    (vcpus, memory_mib), _ = default_computer_resources(cpu_budget, memory_budget_mib)
    worker = VAR_REAL + '/worker'
    config = {
        'release_dir': '%s/guest/%s' % (VAR_REAL, guest_version),
        'state_root': worker + '/state',
        'config_root': worker + '/config',
        'unit_root': worker + '/unused-units',
        'ledger_root': worker + '/ledger',
        'socket_root': RUN_REAL + '/accounts',
        'broker_socket': RUN_REAL + '/broker.sock',
        'socket_gid': APP_UID,
        'app_uid': APP_UID,
        'headroom_bytes': GIB,
        'warning_bytes': 15 * GIB,
        'reserved_slots': [],
        'vcpus': vcpus,
        'memory_mib': memory_mib,
        'runtime_vcpu_budget': int(cpu_budget),
        'runtime_memory_mib_budget': int(memory_budget_mib),
        'host_memory_headroom_mib': 1024,
        'cgroup_root': '/run/tofi-worker/cgroup/tofi-vms',
        'external_reserved_bytes': 0,
        'per_account_internal_reserved_bytes': 8 * GIB,
        'external_disks': [],
        'expected_guest_sha256': guest_binary_sha256,
        'release_manifest_sha256': release_manifest_sha256,
    }
    if int(memory_budget_mib) < 1536:
        raise HostError('The Worker memory budget must be at least 1536 MiB.')
    validate_config(config)
    return config


def render_env(manifest, options, budgets):
    domain = options.get('domain') or ''
    if domain:
        # Caddy terminates TLS; the App speaks plain HTTP on the Docker network
        # and its loopback port only serves `tofi status` health checks.
        bind = '127.0.0.1'
        origin = 'https://' + domain
        cert = key = ''
        allow_lan_http = '1'
        trusted_proxies = LOCAL_PROXY_CIDRS
    else:
        # Direct mode always serves HTTPS with the host's self-signed certificate.
        bind = options['bind']
        origin = ''
        cert, key = TLS_REAL + '/cert.pem', TLS_REAL + '/key.pem'
        allow_lan_http = '0'
        # Behind a reverse proxy the user already runs: trust exactly the
        # addresses they named; otherwise nobody.
        trusted_proxies = options.get('behind_proxy') or ''
    computer = options.get('computer') or 'kvm'
    values = {
        'TOFI_VERSION': manifest['version'],
        'TOFI_DOMAIN': domain,
        'TOFI_EMAIL': options.get('email') or '',
        'TOFI_HTTP_PORT': str(options['port']),
        'TOFI_BIND': bind,
        'TOFI_PUBLIC_ORIGIN': origin,
        'TOFI_APP_IMAGE': manifest['images']['app'],
        'TOFI_CADDY_IMAGE': manifest['images']['caddy'],
        'TOFI_TLS_CERT_FILE': cert,
        'TOFI_TLS_KEY_FILE': key,
        'TOFI_OWNER_ALLOW_LAN_HTTP': allow_lan_http,
        'TOFI_TRUSTED_PROXIES': trusted_proxies,
        'TOFI_AUTO_UPDATE': options.get('auto_update') if options.get('auto_update') in AUTO_UPDATE_MODES else 'off',
    }
    values.update(computer_env(computer, manifest, budgets))
    values.update(runner_env(manifest))
    return values


def runner_env(manifest):
    """The tofi.env keys of the local MCP Runner container; none when the
    release does not carry its image (the App then keeps local connections on
    the computer, as before)."""
    image = manifest_image(manifest, 'runner')
    if not image:
        return {}
    return {'TOFI_RUNNER_IMAGE': image, 'TOFI_RUNNER_URL': 'http://runner:8390',
            'TOFI_RUNNER_MASTER_KEY_FILE': '/app/data/runner/master.key'}


# What the App needs to reach the Worker (the same values compose.yaml falls
# back to when tofi.env predates these keys).
COMPUTER_APP_ENV = {'TOFI_ACCOUNT_PROVISIONER_SOCKET': '/run/tofi/broker.sock',
                    'TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT': '/run/tofi/accounts',
                    'TOFI_ACCOUNT_COMPUTER_DISK_GIB': '8'}


def computer_env(backend, manifest=None, budgets=None):
    """The tofi.env keys that belong to the computer. With backend `none` every
    one is empty: the Worker never starts, and the App gets no broker or
    control sockets (it then runs without computer tools)."""
    if backend != 'kvm':
        return {'TOFI_COMPUTER_BACKEND': 'none', 'TOFI_WORKER_IMAGE': '', 'TOFI_GUEST_VERSION': '',
                'TOFI_CPU_BUDGET': '', 'TOFI_MEMORY_BUDGET_MIB': '', 'TOFI_WORKER_MEMORY_LIMIT': '',
                'TOFI_ACCOUNT_PROVISIONER_SOCKET': '', 'TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT': '',
                'TOFI_ACCOUNT_COMPUTER_DISK_GIB': ''}
    return {'TOFI_COMPUTER_BACKEND': 'kvm',
            'TOFI_WORKER_IMAGE': manifest['images']['worker'],
            'TOFI_GUEST_VERSION': manifest['guest']['version'],
            'TOFI_CPU_BUDGET': str(budgets['cpu']),
            'TOFI_MEMORY_BUDGET_MIB': str(budgets['memory_mib']),
            'TOFI_WORKER_MEMORY_LIMIT': '%dm' % (budgets['memory_mib'] + 256),
            **COMPUTER_APP_ENV}


def complete_computer_env(env):
    """Fill the socket keys an install from before they were written lacks, so
    an update never turns an existing computer off. No-op for backend none."""
    if has_computer(env):
        for key, value in COMPUTER_APP_ENV.items():
            if not env.get(key):
                env[key] = value
        env['TOFI_COMPUTER_BACKEND'] = 'kvm'
    return env


def render_caddyfile(bundle, domain, email):
    template = (Path(bundle) / 'Caddyfile.tmpl').read_text()
    if not DOMAIN_RE.match(domain):
        raise HostError('%r is not a valid domain name.' % domain)
    global_block = ''
    if email:
        if not EMAIL_RE.match(email):
            raise HostError('%r is not a valid email address.' % email)
        global_block = '{\n\temail %s\n}\n\n' % email
    return template.replace('{{GLOBAL}}', global_block).replace('{{DOMAIN}}', domain)


def render_apparmor(bundle):
    template = (Path(bundle) / 'worker.apparmor.template').read_text()
    return (template.replace('tofi-account-worker', 'tofi-worker')
                    .replace('/var/lib/tofi-worker', VAR_REAL + '/worker')
                    .replace('/var/lib/tofi-guest', VAR_REAL + '/guest'))


def bundle_manager(bundle):
    bundle = Path(bundle)
    if (bundle / 'lib/microvm/manager.py').exists():
        return bundle / 'lib/microvm/manager.py'
    return MICROVM / 'manager.py'


def computer_config(env):
    """worker.json for the Guest release `env` pins (reads its release manifest)."""
    guest_dir = P.guest / env['TOFI_GUEST_VERSION']
    release_manifest = guest_dir / 'account-release.json'
    guest_manifest = json.loads(release_manifest.read_text())
    config = render_worker_config(env['TOFI_GUEST_VERSION'], env['TOFI_CPU_BUDGET'],
                                  env['TOFI_MEMORY_BUDGET_MIB'], guest_manifest['guest_binary_sha256'],
                                  sha256_file(release_manifest))
    _, reason = default_computer_resources(env['TOFI_CPU_BUDGET'], env['TOFI_MEMORY_BUDGET_MIB'])
    if reason:
        warn(reason)
    return config


def apply_computer_files(bundle, config):
    """Write worker.json, the seccomp profile and the AppArmor profile (kvm only)."""
    write_json(P.worker_json, config, 0o600)
    write_file(P.seccomp, (Path(bundle) / 'worker.seccomp.json').read_bytes(), 0o600)
    load_apparmor(bundle)


def apply_host_config(bundle, env):
    """Write tofi.env, TLS, Caddyfile, tmpfiles and unit from `bundle`; with a
    computer (backend kvm) also worker.json, seccomp and AppArmor."""
    bundle = Path(bundle)
    complete_computer_env(env)
    config = computer_config(env) if has_computer(env) else None
    ensure_tls(env)
    write_env(env)
    if config is not None:
        write_json(P.worker_json, config, 0o600)
        write_file(P.seccomp, (bundle / 'worker.seccomp.json').read_bytes(), 0o600)
    if env.get('TOFI_DOMAIN'):
        write_file(P.caddyfile, render_caddyfile(bundle, env['TOFI_DOMAIN'], env.get('TOFI_EMAIL')), 0o644)
    if config is not None:
        load_apparmor(bundle)
    write_file(P.tmpfiles, (bundle / 'tofi.conf').read_text(), 0o644)
    run(['systemd-tmpfiles', '--create', str(P.tmpfiles)])
    write_file(P.unit, (bundle / 'tofi.service').read_text(), 0o644)
    write_auto_update_units()
    prepare_update_directories()
    write_update_units()
    run(['systemctl', 'daemon-reload'], check=False)
    return config


def install_apparmor_profile(profile, target, candidate):
    """Check `profile` with the parser, write it to `target` and load it (idempotent)."""
    write_file(candidate, profile, 0o644)
    try:
        run(['apparmor_parser', '-Q', '-T', str(candidate)])
    finally:
        candidate.unlink(missing_ok=True)
    write_file(target, profile, 0o644)
    run(['apparmor_parser', '-r', '-W', str(target)])


def load_apparmor(bundle):
    """Check, write and load the Worker's AppArmor profile."""
    install_apparmor_profile(render_apparmor(bundle), P.apparmor_profile, P.etc / '.worker.apparmor.candidate')


def apparmor_active():
    """Does this kernel enforce AppArmor? Docker ignores a profile name when it does not."""
    try:
        return P.apparmor_enabled.read_text().strip() == 'Y'
    except OSError:
        return False


def apparmor_profile_loaded(name):
    try:
        text = P.apparmor_profiles.read_text()
    except OSError:
        return False
    return any(line.split(' (')[0] == name for line in text.splitlines())


def load_runner_apparmor(bundle):
    """Check, write and load the Runner's AppArmor profile (runner.apparmor.template, verbatim)."""
    profile = (Path(bundle) / 'runner.apparmor.template').read_text()
    install_apparmor_profile(profile, P.runner_apparmor_profile, P.etc / '.runner.apparmor.candidate')


def prepare_runner_sandbox(env):
    """Give the Runner container what per-account cgroups need, before it starts.

    The Runner remounts its own (private) cgroup view read-write to create one
    cgroup per account. Docker's default AppArmor profile forbids every mount, so
    on a host with AppArmor this loads the tofi-runner profile and has compose use
    it (TOFI_RUNNER_APPARMOR); without AppArmor Docker applies no profile and the
    remount needs nothing. TOFI_RUNNER_CGROUPS defaults to `required`: a Runner
    that cannot confine memory and processes per account does not start, and says
    why. An operator's own TOFI_RUNNER_CGROUPS=auto|off in tofi.env stays.
    A release bundle without the template (a rollback target) is left alone.
    Returns True when tofi.env changed (it is written)."""
    if not env.get('TOFI_RUNNER_IMAGE') or not (P.current / 'runner.apparmor.template').exists():
        return False
    before = dict(env)
    explicit = env.get('TOFI_RUNNER_CGROUPS')
    if apparmor_active():
        if shutil.which('apparmor_parser') is None:
            if explicit not in ('auto', 'off'):
                raise HostError('The Runner needs its AppArmor profile (per-account memory and process limits) and '
                                'apparmor_parser is missing. Install it with `apt-get install apparmor apparmor-utils`, '
                                'then run `sudo tofi start`; or set TOFI_RUNNER_CGROUPS=auto in %s to run the Runner '
                                'without those limits (not recommended).' % P.env_file)
            warn('apparmor_parser is missing: the Runner starts without per-account cgroups (TOFI_RUNNER_CGROUPS=%s).' % explicit)
            env.pop('TOFI_RUNNER_APPARMOR', None)
        else:
            load_runner_apparmor(P.current)
            env['TOFI_RUNNER_APPARMOR'] = RUNNER_APPARMOR
    else:
        env.pop('TOFI_RUNNER_APPARMOR', None)
    env.setdefault('TOFI_RUNNER_CGROUPS', 'required')
    if env == before:
        return False
    write_env(env)
    return True


def prepare_directories(computer=True):
    dirs = [(P.etc, 0o700), (P.var, 0o755), (P.caddy, 0o700)]
    if computer:
        dirs += [(P.worker, 0o700), (P.guest, 0o755)]
    for path, mode in dirs:
        path.mkdir(parents=True, exist_ok=True)
        os.chmod(path, mode)
    if computer:
        prepare_computer_directories()
    for name in ('data', 'config'):
        (P.caddy / name).mkdir(exist_ok=True)
    P.data.mkdir(exist_ok=True)
    os.chown(P.data, APP_UID, APP_UID)
    os.chmod(P.data, 0o700)


def prepare_computer_directories():
    for path, mode in [(P.worker, 0o700), (P.guest, 0o755)]:
        path.mkdir(parents=True, exist_ok=True)
        os.chmod(path, mode)
    for name in ('state', 'config', 'ledger', 'unused-units'):
        child = P.worker / name
        child.mkdir(exist_ok=True)
        os.chmod(child, 0o700)


# --------------------------------------------------------------------------
# Images and Guest release


def inspect_image(reference, role, data_schema=None):
    """Check a pulled image's platform, entrypoint and TOFI labels."""
    try:
        data = json.loads(run(['docker', 'image', 'inspect', reference]).stdout)[0]
    except (subprocess.SubprocessError, ValueError, IndexError) as error:
        raise HostError('Image %s is not available locally.' % reference) from error
    found = host_arch()
    docker_arch = found[1] if found else 'amd64'
    if data.get('Architecture') != docker_arch or data.get('Os') != 'linux':
        raise HostError('Image %s is %s/%s; linux/%s is required.'
                        % (reference, data.get('Os'), data.get('Architecture'), docker_arch))
    # Docker shortens Docker Hub names in RepoDigests (caddy@sha256:...), so
    # compare the pinned digest itself.
    pinned = '@' + reference.partition('@')[2]
    if not any(entry.endswith(pinned) for entry in data.get('RepoDigests') or []):
        raise HostError('Image %s was not pulled by its pinned digest.' % reference)
    config = data.get('Config') or {}
    labels = config.get('Labels') or {}
    if role == 'app':
        if labels.get('io.tofi.account-runtime') != '1' or config.get('Entrypoint') != APP_ENTRYPOINT:
            raise HostError('Image %s is not a TOFI account App image.' % reference)
        if data_schema is not None and labels.get(DATA_SCHEMA_LABEL) != data_schema:
            raise HostError('Image %s has data schema %r; the release declares %r.'
                            % (reference, labels.get(DATA_SCHEMA_LABEL), data_schema))
    elif role == 'worker':
        if labels.get('io.tofi.account-worker') != '1' or config.get('Entrypoint') != WORKER_ENTRYPOINT:
            raise HostError('Image %s is not a TOFI account Worker image.' % reference)
    if role in ('app', 'worker') and labels.get('io.tofi.account-guest-protocol') != PROTOCOL:
        raise HostError('Image %s does not speak Guest protocol %s.' % (reference, PROTOCOL))
    return data


def pull_images(env, data_schema):
    roles = [('app', env['TOFI_APP_IMAGE'])]
    if has_computer(env):
        roles.append(('worker', env['TOFI_WORKER_IMAGE']))
    if env.get('TOFI_DOMAIN'):
        roles.append(('caddy', env['TOFI_CADDY_IMAGE']))
    if env.get('TOFI_RUNNER_IMAGE'):
        roles.append(('runner', env['TOFI_RUNNER_IMAGE']))
    for role, reference in roles:
        try:
            run(['docker', 'pull', '--quiet', reference], timeout=1800)
        except subprocess.CalledProcessError as error:
            # Name Docker's own reason: an unknown digest and a network
            # failure need different fixes.
            reason = ((error.stderr or '').strip().splitlines() or ['no output'])[-1][-300:]
            raise HostError('Cannot pull %s: %s' % (reference, reason)) from error
        except subprocess.SubprocessError as error:
            raise HostError('Cannot pull %s; check network access to the registry.' % reference) from error
        inspect_image(reference, role, data_schema if role == 'app' else None)


def image_label(reference, label):
    data = json.loads(run(['docker', 'image', 'inspect', reference]).stdout)[0]
    return ((data.get('Config') or {}).get('Labels') or {}).get(label)


def fetch_guest(guest, manager_source):
    """Download, verify and seal the Guest release into /var/lib/tofi/guest/<ver>.

    Idempotent: an existing valid directory is reused. A failed download or
    validation leaves neither the partial nor the final directory behind.
    """
    version = guest['version']
    final = P.guest / version
    if final.exists():
        validate_release(str(final), str(manager_source), guest['guest_binary_sha256'],
                         expected_manifest_sha256=guest['manifest_sha256'])
        return final
    P.guest.mkdir(parents=True, exist_ok=True)
    partial = P.guest / ('.%s.partial' % version)
    archive = P.guest / ('.%s.partial.tar.zst' % version)
    remove_tree(partial)
    archive.unlink(missing_ok=True)
    try:
        download(guest['url'], archive, guest['sha256'])
        partial.mkdir(mode=0o700)
        try:
            run(['tar', '--zstd', '-x', '--no-same-owner', '-f', str(archive), '-C', str(partial)],
                timeout=1800)
        except subprocess.CalledProcessError as error:
            raise HostError('Cannot extract the Guest release (%s).' % error.stderr.strip()[-300:]) from error
        os.chmod(partial, 0o555)
        validate_release(str(partial), str(manager_source), guest['guest_binary_sha256'],
                         expected_manifest_sha256=guest['manifest_sha256'])
        os.rename(partial, final)
    except BaseException:
        remove_tree(partial)
        raise
    finally:
        archive.unlink(missing_ok=True)
    return final


# --------------------------------------------------------------------------
# Compose and readiness


def compose_file():
    return P.current / 'compose.yaml'


def compose(*args, env=None, timeout=600, profiles=()):
    env = env if env is not None else read_env()
    return run(compose_command(env, profiles) + list(args), timeout=timeout)


def compose_command(env, extra_profiles=()):
    """`docker compose` with the project's profiles: tls with a domain, and
    computer-kvm (the Worker) only when this install has a computer."""
    command = ['docker', 'compose', '--project-name', PROJECT, '--env-file', str(P.env_file),
               '-f', str(compose_file())]
    profiles = list(extra_profiles)
    if env.get('TOFI_DOMAIN'):
        profiles.append('tls')
    if has_computer(env):
        profiles.append(COMPUTER_PROFILE)
    if env.get('TOFI_RUNNER_IMAGE'):
        profiles.append(RUNNER_PROFILE)
    for profile in dict.fromkeys(profiles):
        command += ['--profile', profile]
    return command


def compose_network_subnets():
    """The subnets of this project's Docker networks other than the Runner's own
    egress bridge. They are blocked as well, whatever range Docker picked."""
    subnets = []
    try:
        ids = run(['docker', 'network', 'ls', '-q', '--filter',
                   'label=com.docker.compose.project=' + PROJECT], check=False).stdout.split()
        if not ids:
            return subnets
        for network in json.loads(run(['docker', 'network', 'inspect'] + ids, check=False).stdout or '[]'):
            for config in ((network.get('IPAM') or {}).get('Config') or []):
                subnet = config.get('Subnet')
                if isinstance(subnet, str) and subnet != RUNNER_EGRESS_SUBNET and subnet not in subnets:
                    subnets.append(subnet)
    except (OSError, ValueError, subprocess.SubprocessError, AttributeError):
        pass
    return subnets


def runner_egress_rules():
    """The rules of RUNNER_EGRESS_CHAIN, in order: drop what the Runner must not
    reach, return for everything else (the internet)."""
    blocked = list(RUNNER_BLOCKED_RANGES)
    for subnet in compose_network_subnets():
        if subnet not in blocked:
            blocked.append(subnet)
    return [['-d', destination, '-j', 'DROP'] for destination in blocked] + [['-j', 'RETURN']]


def _iptables(*args):
    return run(['iptables', '-w', '5'] + list(args), check=False)


def ensure_runner_egress_filter():
    """Keep the Runner container off the host and the private networks.

    Docker filters nothing between a container and the rest of the machine's
    networks, so the Runner (which runs code a person installed) would otherwise
    reach the host's services, the LAN and the cloud metadata address. Traffic
    leaving the Runner's bridge is checked in DOCKER-USER (forwarded) and INPUT
    (to the host itself). The rules are idempotent and are applied before the
    Runner starts. A host without iptables, or one where Docker keeps no
    DOCKER-USER chain, gets a warning and False; `require_runner_fence` turns
    that into a refusal to start the Runner.
    Returns True when the filter is in place."""
    if shutil.which('iptables') is None:
        reason = 'nft is present but iptables is not' if shutil.which('nft') else 'neither iptables nor nft is installed'
        warn('The Runner container could not be fenced off from the host and the LAN (%s). '
             'Install iptables, or block %s -> private ranges yourself; see deploy/self-host/README.md.'
             % (reason, RUNNER_EGRESS_SUBNET))
        return False
    if _iptables('-S', 'DOCKER-USER').returncode != 0:
        warn('Docker has no DOCKER-USER chain here (iptables disabled in Docker, or the nftables backend), '
             'so the Runner container is not fenced off from the host and the LAN. See deploy/self-host/README.md.')
        return False
    wanted = runner_egress_rules()
    current = _iptables('-S', RUNNER_EGRESS_CHAIN)
    expected = ['-A %s %s' % (RUNNER_EGRESS_CHAIN, ' '.join(rule)) for rule in wanted]
    present = [line for line in current.stdout.splitlines() if line.startswith('-A ')] if current.returncode == 0 else None
    if present != expected:
        if current.returncode != 0:
            _iptables('-N', RUNNER_EGRESS_CHAIN)
        _iptables('-F', RUNNER_EGRESS_CHAIN)
        for rule in wanted:
            if _iptables('-A', RUNNER_EGRESS_CHAIN, *rule).returncode != 0:
                warn('Could not write the Runner firewall rules; the Runner container is not fenced off.')
                return False
    for chain, position_rule in (('DOCKER-USER', ['-s', RUNNER_EGRESS_SUBNET, '-j', RUNNER_EGRESS_CHAIN]),
                                 ('INPUT', ['-s', RUNNER_EGRESS_SUBNET, '-j', 'DROP'])):
        if _iptables('-C', chain, *position_rule).returncode != 0:
            if _iptables('-I', chain, '1', *position_rule).returncode != 0:
                warn('Could not hook the Runner firewall rules into %s.' % chain)
                return False
    return True


def require_runner_fence(env):
    """Apply the Runner's egress fence; refuse to go on when it cannot be applied.

    The Runner runs code a person installed. Without the fence it reaches the
    host's services, the LAN and the cloud metadata address. TOFI_RUNNER_UNFENCED=1
    in tofi.env is the operator's explicit choice to run it anyway."""
    if ensure_runner_egress_filter():
        return True
    if env.get('TOFI_RUNNER_UNFENCED') == '1':
        warn('TOFI_RUNNER_UNFENCED=1: the Runner container starts without its network fence.')
        return False
    raise HostError('The Runner container was not started: its network fence could not be applied (see the warning above). '
                    'Install iptables (apt-get install iptables) and make sure Docker keeps its DOCKER-USER chain, '
                    'or set TOFI_RUNNER_UNFENCED=1 in %s to run it unfenced (not recommended).' % P.env_file)


def remove_runner_egress_filter():
    """Take the Runner's rules out again (uninstall). Nothing happens, quietly,
    on a host that never had them."""
    if shutil.which('iptables') is None:
        return
    for chain, rule in (('DOCKER-USER', ['-s', RUNNER_EGRESS_SUBNET, '-j', RUNNER_EGRESS_CHAIN]),
                        ('INPUT', ['-s', RUNNER_EGRESS_SUBNET, '-j', 'DROP'])):
        for _ in range(8):  # more than one only if something added a duplicate
            if _iptables('-C', chain, *rule).returncode != 0:
                break
            _iptables('-D', chain, *rule)
    if _iptables('-S', RUNNER_EGRESS_CHAIN).returncode == 0:
        _iptables('-F', RUNNER_EGRESS_CHAIN)
        _iptables('-X', RUNNER_EGRESS_CHAIN)


def project_containers():
    ids = run(['docker', 'ps', '-a', '-q', '--filter',
               'label=com.docker.compose.project=' + PROJECT]).stdout.split()
    if not ids:
        return []
    return json.loads(run(['docker', 'inspect'] + ids).stdout)


def service_of(container):
    return ((container.get('Config') or {}).get('Labels') or {}).get('com.docker.compose.service')


def stopped(tolerate_unclean=False):
    """Stop every project container and prove the Worker and App exited cleanly.

    `tolerate_unclean` is for paths that discard or replace what ran (rollback,
    restore, uninstall, purge, a plain stop): a container that crashed must
    not strand the host down, so its exit is reported and the path continues.
    """
    containers = project_containers()
    if containers:
        run(['docker', 'stop', '--time', '180'] + [c['Id'] for c in containers], timeout=600)
    for container in project_containers():
        state = container['State']
        service = service_of(container)
        if state.get('Running') or state.get('Restarting'):
            raise HostError('Container %s is still running after stop; inspect it with `docker ps`.' % service)
        if service in ('app', 'worker') and state.get('ExitCode') not in (0, None):
            if tolerate_unclean:
                warn('The %s container had exited uncleanly (exit %s); continuing.' % (service, state.get('ExitCode')))
                continue
            raise HostError('The %s container stopped uncleanly (exit %s); data is retained. '
                            'Inspect `tofi logs %s` before starting again.' % (service, state.get('ExitCode'), service))


BROKER_PROBE = r"""import http.client, json, socket, struct, sys
try:
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(5)
        connection.connect(sys.argv[1])
        pid, uid, gid = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if (pid, uid) != (int(sys.argv[2]), 0):
            raise ValueError('broker peer is not the Worker process')
        client = http.client.HTTPConnection('account-worker', timeout=5)
        client.sock = connection
        client.request('POST', '/v1/accounts', body='{"op":"capacity"}',
                       headers={'Content-Type': 'application/json'})
        response = client.getresponse()
        body = response.read(1048577)
        if response.status != 200 or len(body) > 1048576:
            raise ValueError('broker capacity request failed')
        value = json.loads(body)
        if type(value.get('admission_remaining_bytes')) is not int or not isinstance(value.get('accounts'), list):
            raise ValueError('unexpected broker capacity reply')
        print(json.dumps({'remaining_bytes': value['admission_remaining_bytes'], 'empty': not value['accounts']}))
except (FileNotFoundError, ConnectionRefusedError, TimeoutError, socket.timeout) as error:
    print('broker starting: ' + type(error).__name__, file=sys.stderr)
    sys.exit(75)
except (OSError, ValueError, KeyError) as error:
    print('broker probe rejected: ' + str(error), file=sys.stderr)
    sys.exit(78)
"""


def worker_ready(first_install=False, attempts=120):
    """Probe the broker socket from the host as the App uid (not docker exec).

    `docker exec` would race the Worker's private cgroup initialization, so the
    probe connects to /run/tofi/broker.sock as uid 10001 and checks that the
    peer is the Worker's own PID.
    """
    last = None
    for _ in range(attempts):
        try:
            workers = [c for c in project_containers() if service_of(c) == 'worker']
            if len(workers) != 1 or not workers[0]['State'].get('Running'):
                raise HostError('the Worker container is not running')
            pid = str(workers[0]['State']['Pid'])
            reply = json.loads(run([sys.executable, '-I', '-c', BROKER_PROBE, RUN_REAL + '/broker.sock', pid],
                                   user=APP_UID, group=APP_UID, extra_groups=[], cwd='/', timeout=15).stdout)
            if first_install and reply['empty'] and reply['remaining_bytes'] < FIRST_ACCOUNT_BYTES:
                raise HostError('The Worker can admit only %.1f GiB; the first account needs 16 GiB '
                                '(8 GiB workspace + 8 GiB internal). Free disk space under /var/lib.'
                                % (reply['remaining_bytes'] / GIB))
            return reply
        except subprocess.CalledProcessError as error:
            if error.returncode != 75:
                raise HostError('The Worker broker probe failed: %s' % error.stderr.strip()) from error
            last = error
        except HostError as error:
            if 'first account' in str(error):
                raise
            last = error
        except (OSError, ValueError, subprocess.TimeoutExpired) as error:
            last = error
        time.sleep(1)
    raise HostError('The Worker did not become ready; see `tofi logs worker`.') from last


def health(env=None, attempts=60):
    env = env if env is not None else read_env()
    scheme = app_scheme(env)
    url = '%s://127.0.0.1:%s/health' % (scheme, env['TOFI_HTTP_PORT'])
    handlers = [urllib.request.ProxyHandler({})]
    if scheme == 'https':
        # Loopback probe of our own self-signed certificate: no verification.
        context = ssl.create_default_context()
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        handlers.append(urllib.request.HTTPSHandler(context=context))
    opener = urllib.request.build_opener(*handlers)
    for attempt in range(attempts):
        try:
            with opener.open(url, timeout=2) as response:
                if json.loads(response.read(65536)).get('ok') is True:
                    if attempts == 1 or app_docker_health() in (None, 'healthy'):
                        return True
        except (OSError, ValueError):
            pass
        time.sleep(1)
    raise HostError('The App did not report healthy within %d s; see `tofi logs app`.' % attempts)


def app_docker_health():
    """Docker's own health verdict for the App ('starting', 'healthy', ...).

    `tofi status` reports this value, so a start only counts as done once
    Docker agrees with the /health probe. None when Docker cannot say.
    """
    try:
        for container in project_containers():
            if service_of(container) == 'app':
                return (container.get('State') or {}).get('Health', {}).get('Status')
    except (subprocess.SubprocessError, ValueError, OSError):
        return None
    return None


RUNNER_READY_SECONDS = 90


def runner_container_state():
    """What Docker says about the Runner container: 'healthy', 'starting',
    'unhealthy', 'exited', ... or 'missing' when the project has none."""
    try:
        for container in project_containers():
            if service_of(container) == 'runner':
                state = container.get('State') or {}
                status = state.get('Status') or 'unknown'
                # A stopped container keeps the health of its last run; only a running one has a current health.
                if status != 'running':
                    return status
                return (state.get('Health') or {}).get('Status') or status
    except (subprocess.SubprocessError, ValueError, OSError):
        return 'docker unavailable'
    return 'missing'


def runner_ready(env, attempts=RUNNER_READY_SECONDS):
    """Wait until the Runner container is healthy; do nothing when this install
    has no Runner. A Runner that does not come up fails the start, so an update
    is rolled back instead of reporting success with every local connection down."""
    if not env.get('TOFI_RUNNER_IMAGE'):
        return True
    state = 'missing'
    for _ in range(attempts):
        state = runner_container_state()
        if state == 'healthy' or state == 'running':
            return True
        # Docker can report a starting container as unhealthy for a moment (slow hosts, a check that ran before the
        # listener): keep waiting. Only a container that stopped is final.
        if state in ('exited', 'dead'):
            break
        time.sleep(1)
    raise HostError('The Runner container did not become healthy (it is %s); see `tofi logs runner`. '
                    'Local connections such as Gmail need it.' % state)


def start_worker(env, first_install=False):
    """Bring the Worker up and wait for its broker; nothing else is touched."""
    compose('up', '-d', '--no-build', '--pull', 'never', '--remove-orphans', 'worker', env=env)
    worker_ready(first_install=first_install)


def start_services(env, first_install=False, state=None, runner_required=True):
    if env.get('TOFI_RUNNER_IMAGE'):
        require_runner_fence(env)  # before the Runner can run, and again after every reboot
        prepare_runner_sandbox(env)  # its AppArmor profile is loaded before compose names it
    if has_computer(env):
        start_worker(env, first_install=first_install)
    if state is not None and state.get('transaction') and state['transaction']['kind'] == 'install':
        transition(state, 'installing', 'start-app')
    services = ['app', 'caddy'] if env.get('TOFI_DOMAIN') else ['app']
    if env.get('TOFI_RUNNER_IMAGE'):
        services.append('runner')  # its profile alone does not start it: `up` names the services it starts
    compose('up', '-d', '--no-build', '--pull', 'never', *services, env=env)
    health(env)
    try:
        runner_ready(env)
    except HostError as error:
        if runner_required:
            raise
        # A rollback must bring the App back even when the Runner does not come up; say so instead of failing it.
        say('WARNING: %s' % error)


# --------------------------------------------------------------------------
# Active-run guard: never stop the services under a running Bot


def run_databases():
    """The App databases that hold Bot runs: the root one and, in multi-account
    mode (self-host), one per account under data/accounts/<id>/tofi.db."""
    found = []
    root = P.data / 'tofi.db'
    if root.exists():
        found.append(root)
    accounts = P.data / 'accounts'
    try:
        entries = sorted(accounts.iterdir()) if accounts.is_dir() and not accounts.is_symlink() else []
    except OSError as error:
        warn('Could not list %s (%s); account runs are not counted.' % (accounts, error))
        entries = []
    for entry in entries:
        database = entry / 'tofi.db'
        if entry.is_dir() and not entry.is_symlink() and database.exists():
            found.append(database)
    return found


def count_active_runs(database):
    uri = 'file:%s?mode=ro' % urllib.parse.quote(str(database))
    connection = sqlite3.connect(uri, uri=True, timeout=5)
    try:
        row = connection.execute(
            'SELECT COUNT(*) FROM runs WHERE status IN (%s)' % ','.join('?' * len(ACTIVE_RUN_STATES)),
            ACTIVE_RUN_STATES).fetchone()
    finally:
        connection.close()
    return int(row[0])


def active_runs():
    """Bot runs queued, running or waiting, summed over every App database.

    Each database is opened read-only (mode=ro), so this can never write to or
    lock the App's data. Anything unexpected in one file (no table, unreadable
    file, an old schema) counts that file as 0 with a warning: a broken
    database must never block recovery.
    """
    total = 0
    for database in run_databases():
        try:
            total += count_active_runs(database)
        except (sqlite3.Error, OSError, ValueError, TypeError) as error:
            warn('Could not count active Bot runs in %s (%s); assuming none.' % (database, error))
    return total


RUN_HEALTH_DAYS = 7
# Same test as internal/app/usage_runs.go: a failed shell-style tool call that
# timed out or was killed (exit 137).
TOOL_TIMEOUT_SQL = ("status='failed' AND (result LIKE '[Timed out after%' OR result LIKE '%\"timed_out\":true%' "
                    "OR result LIKE '%\"timed_out\": true%' OR result LIKE '%[Exit 137:%' "
                    "OR result LIKE '%[Exit code: 137]%' OR result LIKE '%\"exit_code\":137%' "
                    "OR result LIKE '%\"exit_code\": 137%')")


def run_health(days=RUN_HEALTH_DAYS, now=None):
    """Runs by status and tool time-outs over the last `days`, summed over every
    App database (read-only, like active_runs). None when no database is
    readable; counts only, never any run text."""
    since = time.strftime('%Y-%m-%dT%H:%M:%S.000000000Z', time.gmtime((now or time.time()) - days * 86400))
    by_status, timeouts, seen = {}, 0, False
    for database in run_databases():
        try:
            connection = sqlite3.connect('file:%s?mode=ro' % urllib.parse.quote(str(database)), uri=True, timeout=5)
            try:
                for status, count in connection.execute(
                        'SELECT status, COUNT(*) FROM runs WHERE updated_at >= ? GROUP BY status', (since,)):
                    by_status[status] = by_status.get(status, 0) + int(count)
                timeouts += int(connection.execute(
                    'SELECT COUNT(*) FROM tool_activities WHERE started_at >= ? AND ' + TOOL_TIMEOUT_SQL,
                    (since,)).fetchone()[0])
                seen = True
            finally:
                connection.close()
        except (sqlite3.Error, OSError, ValueError, TypeError):
            continue
    if not seen:
        return None
    return {'days': days, 'by_status': by_status, 'tool_timeouts': timeouts}


def count_guest_copies(database):
    """Files of one App database that exist only on the computer: rows whose
    bytes have not been copied to the server file store yet (`vm:` rows)."""
    uri = 'file:%s?mode=ro' % urllib.parse.quote(str(database))
    connection = sqlite3.connect(uri, uri=True, timeout=5)
    try:
        row = connection.execute("SELECT COUNT(*) FROM attachments WHERE disk_name LIKE 'vm:%'").fetchone()
    except sqlite3.OperationalError as error:
        if str(error).startswith('no such table'):
            return 0  # an App older than the attachments table has none
        raise
    finally:
        connection.close()
    return int(row[0])


def pending_guest_copies():
    """(files, unreadable): the files that live only on a computer, summed over
    every App database (read-only), and the databases that could not be read.
    An unreadable database is never counted as 0; the caller must treat the
    answer as unknown."""
    total, unreadable = 0, []
    for database in run_databases():
        try:
            total += count_guest_copies(database)
        except (sqlite3.Error, OSError, ValueError, TypeError):
            unreadable.append(str(database))
    return total, unreadable


def vault_files():
    """Every secret vault: the root one and one per account (multi-account)."""
    found = [P.data / 'secrets' / 'vault.json']
    accounts = P.data / 'accounts'
    try:
        entries = sorted(accounts.iterdir()) if accounts.is_dir() and not accounts.is_symlink() else []
    except OSError:
        entries = []
    found += [entry / 'secrets' / 'vault.json' for entry in entries if entry.is_dir() and not entry.is_symlink()]
    return [path for path in found if path.is_file()]


def computer_configuration():
    """(env_names, key_names, unreadable): the environment variables and SSH
    keys saved in Settings for the computer, by name. Only the record metadata
    is read (id, kind, target); a value stays sealed and is never decrypted.
    An unreadable vault is reported, never counted as empty."""
    env_names, key_names, unreadable = [], [], []
    for path in vault_files():
        try:
            records = json.loads(path.read_text())
            if not isinstance(records, dict):
                raise ValueError('not an object')
        except (OSError, ValueError):
            unreadable.append(str(path))
            continue
        for record in records.values():
            if not isinstance(record, dict) or record.get('run_id'):
                continue
            name = re.sub(r'[^A-Za-z0-9_.-]', '?', str(record.get('target') or record.get('name') or ''))[:80]
            if record.get('kind') == 'env' and name:
                env_names.append(name)
            elif record.get('kind') == 'ssh' and name:
                key_names.append(name)
    return sorted(set(env_names)), sorted(set(key_names)), unreadable


def computer_loss_lines(env_names, key_names, unreadable=(), unsaved=0):
    """What a wipe takes with it, one line each (docs/design/computer-decoupling.md
    section 7.5): the class C list printed by `tofi computer remove --purge`."""
    def names(items):
        shown = ', '.join(items[:10]) + (' and %d more' % (len(items) - 10) if len(items) > 10 else '')
        return ' (%s)' % shown if items else ''
    if unreadable:
        config = 'Configuration: could not be read (%s), so it may include environment variables and SSH keys.' % ', '.join(unreadable)
    elif env_names or key_names:
        config = 'Configuration: %s%s and %s%s are deleted with the disk.' % (
            plural(len(env_names), 'environment variable'), names(env_names), plural(len(key_names), 'SSH key'), names(key_names))
    else:
        config = 'Configuration: no environment variables or SSH keys were saved in Settings. Tool sign-ins the Bot made on the computer (git, gh, cloud CLIs) are deleted too.'
    lines = [
        'Browser sign-ins: the Bot is signed out of every website; nothing is exported.',
        'Installed tools: the Bot installs them again when it needs them.',
        'Work files: files the Bot made on the computer and did not send or save to Files are deleted.',
        config,
    ]
    if unsaved:
        lines.append('Files not yet copied to the server: %s.' % plural(unsaved, 'file'))
    return lines


def plural(count, noun):
    return '%d %s%s' % (count, noun, '' if count == 1 else 's')


def count_command_waits(database):
    """Command-exit waits still pending: a Bot started a command and waits for
    its end. A restart of the services or of the computer interrupts it."""
    uri = 'file:%s?mode=ro' % urllib.parse.quote(str(database))
    connection = sqlite3.connect(uri, uri=True, timeout=5)
    try:
        row = connection.execute("SELECT COUNT(*) FROM waits WHERE kind='command_exit' AND status='pending'").fetchone()
    except sqlite3.OperationalError:
        return 0  # an App older than the wait table has none
    finally:
        connection.close()
    return int(row[0])


def command_waits(account_id=None):
    """Pending command-exit waits, over every App database (read-only, like
    active_runs), or only the one of `account_id`. A file that cannot be read
    counts 0: a broken database must never block recovery."""
    total = 0
    for database in run_databases():
        if account_id is not None and database.parent.name != account_id:
            continue
        try:
            total += count_command_waits(database)
        except (sqlite3.Error, OSError, ValueError, TypeError):
            continue
    return total


def busy_phrase(count, waits):
    """`count` is runs plus command waits; name each part."""
    runs = count - waits
    parts = ([plural(runs, 'Bot run')] if runs or not waits else []) + (
        [plural(waits, 'command') + ' a Bot waits on'] if waits else [])
    return ' and '.join(parts)


def wait_for_idle(action, wait_minutes=0, force=False):
    """Return 0 when no Bot run is active and no Bot waits on a command, else
    wait up to `wait_minutes` (polling every 15 s) and raise ActiveRunsError.
    `force` proceeds and returns the count that will be interrupted."""
    waits = command_waits()
    count = active_runs() + waits
    if not count:
        return 0
    if force:
        warn('%s active; --force interrupts them.' % busy_phrase(count, waits))
        return count
    attempts = max(0, int(wait_minutes)) * 60 // RUN_POLL_SECONDS
    if attempts:
        report_phase('waiting_for_runs')
        say('%s %s active; waiting up to %s for them to finish before %s.'
            % (busy_phrase(count, waits), 'is' if count == 1 else 'are', plural(int(wait_minutes), 'minute'), action))
    for _ in range(attempts):
        time.sleep(RUN_POLL_SECONDS)
        waits = command_waits()
        count = active_runs() + waits
        if not count:
            say('Bot runs finished.')
            return 0
    raise ActiveRunsError(
        '%s still active; %s would interrupt %s. Nothing was changed. Wait for %s to finish, allow longer with '
        '--wait MINUTES, or add --force to interrupt %s.'
        % (busy_phrase(count, waits), action, 'it' if count == 1 else 'them', 'it' if count == 1 else 'them',
           'it' if count == 1 else 'them'), count)


# --------------------------------------------------------------------------
# Backups: /var/lib/tofi/backups/<UTC timestamp>-<version>/{data.tar.*,etc.tar.gz,meta.json}
#
# Taken only while the services are stopped (SQLite is quiescent; the Worker
# hibernates every computer on the way down, so each workspace disk is flushed).
# The computer disks under /var/lib/tofi/worker/state/<account>/workspace.ext4
# are included only in a manual `tofi backup` (sparse copies under disks/); the
# automatic pre-update and pre-restore backups never carry them.


def human_bytes(value):
    value = float(value)
    for unit in ('B', 'KiB', 'MiB', 'GiB'):
        if value < 1024 or unit == 'GiB':
            return ('%d %s' % (value, unit)) if unit == 'B' else '%.1f %s' % (value, unit)
        value /= 1024


def tree_bytes(path):
    """Apparent size of the regular files under `path` (links are not followed)."""
    total = 0
    for directory, _dirs, files in os.walk(str(path)):
        for name in files:
            try:
                info = os.lstat(os.path.join(directory, name))
            except OSError:
                continue
            if stat.S_ISREG(info.st_mode):
                total += info.st_size
    return total


def free_bytes(path):
    probe = Path(path)
    while not probe.exists() and probe != probe.parent:
        probe = probe.parent
    info = os.statvfs(str(probe))
    return info.f_bavail * info.f_frsize


def check_backup_space(action='the update', disks=0):
    """Refuse, before anything is stopped, when a backup would not fit.

    Needs 2x the data and settings size plus 1 GiB free under /var/lib/tofi,
    plus `disks` (the expected compressed size of the computer disks; an
    estimate, see disks_estimate). Returns the size estimate.
    """
    estimate = tree_bytes(P.data) + tree_bytes(P.etc)
    need = BACKUP_FREE_FACTOR * estimate + BACKUP_FREE_MARGIN + disks
    free = free_bytes(P.var)
    if free < need:
        raise HostError(
            'Not enough free disk space for the backup before %s: TOFI data and settings take %s%s, so a backup needs '
            'an estimated %s free under /var/lib/tofi (2x plus 1 GiB%s) but only %s is free. Nothing was changed; free some '
            'space%s and run it again.'
            % (action, human_bytes(estimate), (' and the computer disks about %s compressed' % human_bytes(disks)) if disks else '',
               human_bytes(need), ' plus the disks' if disks else '', human_bytes(free),
               ' or add --no-disks' if disks else ''))
    return estimate


def zstd_available():
    return shutil.which('zstd') is not None


@contextmanager
def archive_writer(path, compression):
    """A tarfile writing `path` compressed with 'zst' (zstd binary) or 'gz'."""
    descriptor = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        if compression == 'zst':
            process = subprocess.Popen(['zstd', '-q', '-T0', '-c'], stdin=subprocess.PIPE, stdout=output,
                                       stderr=subprocess.DEVNULL)
            try:
                tar = tarfile.open(fileobj=process.stdin, mode='w|', format=tarfile.PAX_FORMAT)
                yield tar
                tar.close()
                process.stdin.close()
                if process.wait() != 0:
                    raise HostError('zstd failed while compressing %s.' % path)
            except BaseException:
                process.kill()
                process.wait()
                raise
        else:
            with tarfile.open(fileobj=output, mode='w:gz', compresslevel=6, format=tarfile.PAX_FORMAT) as tar:
                yield tar
        output.flush()
        os.fsync(output.fileno())


WORKER_META_DIRS = ('config', 'ledger', 'state')
WORKER_META_MAX_FILE = 64 * MIB
# Computer disks, jails, snapshots: never part of a backup (they can be huge and
# a restore must not replace a disk with an older copy).
WORKER_SKIP_PARTS = {'snapshot', 'snapshots', 'jail', 'rootfs', 'disks'}
WORKER_SKIP_SUFFIXES = ('.ext4', '.img', '.raw', '.qcow2', '.mem', '.vmstate', '.snap', '.rootfs')


def write_archive(path, compression, roots, exclude=None, max_file_bytes=None):
    """Archive trees as './...' (a single root) or '<arcname>/...', owners kept numerically.

    Only directories, regular files and links go in: FIFOs, devices (and
    sockets, which tar skips) are left out with a warning, because a restore
    refuses such entries. Files over `max_file_bytes` are left out with a warning.
    """
    if not isinstance(roots, list):
        roots = [(roots, '.')]

    def keep(info):
        relative = info.name[2:] if info.name.startswith('./') else info.name
        if exclude is not None and relative != '.' and exclude(relative):
            return None
        if not (info.isreg() or info.isdir() or info.issym() or info.islnk()):
            warn('Backup skips %s: not a regular file, directory or link.' % relative)
            return None
        if max_file_bytes is not None and info.isreg() and info.size > max_file_bytes:
            warn('Backup skips %s: %s is over the %s limit for host metadata.'
                 % (relative, human_bytes(info.size), human_bytes(max_file_bytes)))
            return None
        info.uname = info.gname = ''
        return info

    with archive_writer(path, compression) as tar:
        for root, arcname in roots:
            tar.add(str(root), arcname=arcname, filter=keep)


def worker_meta_excluded(relative):
    parts = relative.split('/')
    return bool(WORKER_SKIP_PARTS & set(parts)) or parts[-1].endswith(WORKER_SKIP_SUFFIXES)


def worker_meta_roots():
    return [(P.worker / name, name) for name in WORKER_META_DIRS
            if (P.worker / name).is_dir() and not (P.worker / name).is_symlink()]


def etc_excluded(relative):
    """Not part of an /etc/tofi backup: the live journal and half-written files."""
    return relative == 'install-state.json' or relative.startswith(('.tofi-write-', '.worker.apparmor', '.runner.apparmor'))


@contextmanager
def archive_reader(path):
    path = str(path)
    if path.endswith('.zst'):
        if not zstd_available():
            raise HostError('Restoring %s needs the zstd tool; install it (apt-get install zstd).' % path)
        process = subprocess.Popen(['zstd', '-dc', path], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        try:
            with tarfile.open(fileobj=process.stdout, mode='r|') as tar:
                yield tar
            # Drain what tarfile left unread so zstd can report a truncated input.
            while process.stdout.read(MIB):
                pass
            if process.wait() != 0:
                raise HostError('The backup archive %s is damaged (zstd failed).' % path)
        except BaseException:
            process.kill()
            process.wait()
            raise
        finally:
            process.stdout.close()
    else:
        with tarfile.open(path, mode='r|gz') as tar:
            yield tar


def safe_members(tar):
    """Yield archive members that stay inside the destination; refuse the rest."""
    links = []
    for member in tar:
        parts = [part for part in member.name.split('/') if part not in ('', '.')]
        if member.name.startswith('/') or '..' in parts:
            raise HostError('The backup archive contains an unsafe path (%r); nothing was restored.' % member.name)
        if not (member.isfile() or member.isdir() or member.issym() or member.islnk()):
            raise HostError('The backup archive contains an unsupported entry (%r); nothing was restored.'
                            % member.name)
        if member.islnk():
            target = [part for part in member.linkname.split('/') if part not in ('', '.')]
            if member.linkname.startswith('/') or '..' in target:
                raise HostError('The backup archive contains an unsafe link (%r); nothing was restored.' % member.name)
        path = '/'.join(parts)
        if any(path == link or path.startswith(link + '/') for link in links):
            raise HostError('The backup archive writes through a symbolic link (%r); nothing was restored.'
                            % member.name)
        if member.issym():
            links.append(path)
        yield member


def extract_archive(archive, destination):
    options = {'filter': 'fully_trusted'} if hasattr(tarfile, 'fully_trusted_filter') else {}
    try:
        with archive_reader(archive) as tar:
            tar.extractall(str(destination), members=safe_members(tar), numeric_owner=True, **options)
    except HostError:
        raise
    except (tarfile.TarError, EOFError, OSError, ValueError) as error:
        raise HostError('Cannot extract %s (%s).' % (Path(archive).name, error)) from error


def backup_timestamp():
    return time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())


def backup_data(reason, env=None, note=None, check_space=True, disks=False):
    """Archive /var/lib/tofi/data and /etc/tofi into a new backup; return its meta.

    Call only while the services are stopped. The backup is assembled in a
    hidden .partial directory and renamed when complete, so an interrupted
    backup is never listed or restored. `disks` also copies every computer's
    workspace disk (only a manual backup asks for it).
    """
    env = env if env is not None else read_env()
    estimate = (check_backup_space('this backup', disks_estimate() if disks else 0) if check_space
                else tree_bytes(P.data) + tree_bytes(P.etc))
    P.backups.mkdir(parents=True, exist_ok=True)
    os.chmod(P.backups, 0o700)
    stamp = backup_timestamp()
    backup_id = '%s-%s' % (stamp, env.get('TOFI_VERSION') or 'unknown')
    final = P.backups / backup_id
    counter = 1
    while final.exists():
        counter += 1
        final = P.backups / ('%s-%d' % (backup_id, counter))
    backup_id = final.name
    partial = P.backups / ('.%s.partial' % backup_id)
    remove_tree(partial)
    partial.mkdir(mode=0o700)
    compression = 'zst' if zstd_available() else 'gz'
    data_name, etc_name = 'data.tar.' + compression, 'etc.tar.gz'
    try:
        write_archive(partial / data_name, compression, P.data)
        write_archive(partial / etc_name, 'gz', P.etc, exclude=etc_excluded)
        worker_name = 'worker-meta.tar.gz'
        write_archive(partial / worker_name, 'gz', worker_meta_roots(), exclude=worker_meta_excluded,
                      max_file_bytes=WORKER_META_MAX_FILE)
        disk_entries = backup_disks(partial) if disks else None
        meta = {
            'schema': 1, 'id': backup_id, 'version': env.get('TOFI_VERSION'),
            'app_image': env.get('TOFI_APP_IMAGE'), 'worker_image': env.get('TOFI_WORKER_IMAGE'),
            'caddy_image': env.get('TOFI_CADDY_IMAGE'), 'guest': env.get('TOFI_GUEST_VERSION'),
            'created_at': iso_now(), 'reason': reason, 'note': note or '',
            'archives': {'data': data_name, 'etc': etc_name, 'worker': worker_name},
            'sizes': {'data_bytes': tree_bytes(P.data), 'etc_bytes': tree_bytes(P.etc),
                      'data_archive': (partial / data_name).stat().st_size,
                      'etc_archive': (partial / etc_name).stat().st_size,
                      'worker_archive': (partial / worker_name).stat().st_size, 'estimate': estimate},
            'sha256': {data_name: sha256_file(partial / data_name), etc_name: sha256_file(partial / etc_name),
                       worker_name: sha256_file(partial / worker_name)},
        }
        if disk_entries is not None:
            meta['disks'] = disk_entries
            meta['sizes']['disks_allocated'] = sum(entry['compressed'] for entry in disk_entries)
        write_json(partial / 'meta.json', meta, 0o600)
        os.rename(partial, final)
    except BaseException as error:
        remove_tree(partial)
        if isinstance(error, OSError):
            raise HostError('Cannot write the backup under %s (%s); nothing was changed.' % (P.backups, error)) from error
        raise
    say('Backup %s saved (%s%s).' % (
        backup_id, human_bytes(meta['sizes']['data_archive'] + meta['sizes']['etc_archive']),
        (', %s' % plural(len(disk_entries), 'computer disk')) if disk_entries else ''))
    return meta


def read_backup_meta(directory):
    try:
        meta = json.loads((Path(directory) / 'meta.json').read_text())
    except (OSError, ValueError) as error:
        raise HostError('Backup %s has no readable meta.json (%s).' % (Path(directory).name, error)) from error
    archives = meta.get('archives') if isinstance(meta, dict) else None
    if (not isinstance(meta, dict) or meta.get('schema') != 1 or not isinstance(archives, dict)
            or set(archives) not in ({'data', 'etc'}, {'data', 'etc', 'worker'}) or not isinstance(meta.get('sha256'), dict)
            or not isinstance(meta.get('sizes'), dict)):
        raise HostError('Backup %s has an unsupported meta.json.' % Path(directory).name)
    for name in archives.values():
        if not isinstance(name, str) or '/' in name or name.startswith('.'):
            raise HostError('Backup %s names an unsafe archive file.' % Path(directory).name)
    for entry in meta.get('disks') or []:
        if (not isinstance(entry, dict) or not isinstance(entry.get('account'), str)
                or not DISK_ACCOUNT_RE.match(entry['account']) or entry.get('file') != 'disks/%s.ext4.zst' % entry['account']
                or not isinstance(entry.get('sha256'), str) or not SHA_RE.match(entry['sha256'])
                or type(entry.get('bytes')) is not int or type(entry.get('allocated')) is not int
                or type(entry.get('compressed')) is not int):
            raise HostError('Backup %s lists an unusable computer disk.' % Path(directory).name)
    if 'disks' in meta and not isinstance(meta['disks'], list):
        raise HostError('Backup %s lists an unusable computer disk.' % Path(directory).name)
    return meta


def verify_backup(directory):
    """Check every archive and computer disk against meta.json's sha256; return meta."""
    meta = read_backup_meta(directory)
    recorded = dict(meta['sha256'])
    recorded.update({entry['file']: entry['sha256'] for entry in meta.get('disks') or []})
    for name in list(meta['archives'].values()) + [entry['file'] for entry in meta.get('disks') or []]:
        expected = recorded.get(name)
        archive = Path(directory) / name
        if not isinstance(expected, str) or not SHA_RE.match(expected):
            raise HostError('Backup %s records no checksum for %s.' % (Path(directory).name, name))
        try:
            actual = sha256_file(archive)
        except OSError as error:
            raise HostError('Backup %s is missing %s (%s).' % (Path(directory).name, name, error)) from error
        if actual != expected:
            raise HostError('Backup %s is damaged: %s does not match its recorded checksum; nothing was restored.'
                            % (Path(directory).name, name))
    return meta


def list_backups():
    """Complete backups, newest first: [{'id', 'path', 'meta', 'size'}]."""
    found = []
    for entry in sorted(P.backups.iterdir()) if P.backups.is_dir() else []:
        if entry.name.startswith('.') or not entry.is_dir():
            continue
        try:
            meta = read_backup_meta(entry)
        except HostError:
            continue
        size = sum(meta['sizes'].get(key) or 0
                   for key in ('data_archive', 'etc_archive', 'worker_archive', 'disks_allocated'))
        found.append({'id': entry.name, 'path': entry, 'meta': meta, 'size': size})
    found.sort(key=lambda item: (item['meta'].get('created_at') or '', item['id']), reverse=True)
    return found


def find_backup(backup_id):
    if not backup_id or '/' in backup_id or backup_id.startswith('.'):
        raise HostError('%r is not a backup id; `sudo tofi backups` lists them.' % backup_id)
    backups = list_backups()
    exact = [item for item in backups if item['id'] == backup_id]
    matches = exact or [item for item in backups if item['id'].startswith(backup_id)]
    if not matches:
        raise HostError('No backup matches %r; `sudo tofi backups` lists them.' % backup_id)
    if len(matches) > 1:
        raise HostError('%r matches several backups; give more of the id.' % backup_id)
    return matches[0]


DISK_NAME = 'workspace.ext4'
DISK_ACCOUNT_RE = re.compile(r'^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$')
ARCHIVE_SIZE_KEYS = {'data': 'data_archive', 'etc': 'etc_archive', 'worker': 'worker_archive'}


def account_disks():
    """[(account directory name, disk path)] for every computer with a workspace disk on this host."""
    root = P.worker_state
    found = []
    for entry in sorted(root.iterdir()) if root.is_dir() and not root.is_symlink() else []:
        disk = entry / DISK_NAME
        if entry.name.startswith('.') or entry.is_symlink() or not entry.is_dir():
            continue
        if disk.is_symlink() or not disk.is_file():
            continue
        if not DISK_ACCOUNT_RE.match(entry.name):
            warn('Backup skips the computer disk of %r: not a usable account directory name.' % entry.name)
            continue
        found.append((entry.name, disk))
    return found


def allocated_bytes(path):
    """Bytes a (sparse) file really occupies on disk."""
    return os.lstat(str(path)).st_blocks * 512


DISK_COMPRESSION_RATIO = 0.5  # assumed zstd ratio on allocated ext4 blocks; a guess, not a measurement
DISK_BACKUPS_KEEP = 2


def disks_allocated():
    return sum(allocated_bytes(disk) for _name, disk in account_disks())


def disks_estimate():
    """Expected compressed size of all computer disks: allocated bytes times an assumed ratio. An estimate."""
    return int(disks_allocated() * DISK_COMPRESSION_RATIO)


def compress_disk(source, target):
    """zstd the (sparse) disk file into `target`; zeros compress to almost nothing."""
    if not zstd_available():
        raise HostError('Backing up computer disks needs the zstd tool; install it (apt-get install zstd) or add --no-disks.')
    descriptor = os.open(str(target), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        result = subprocess.run(['zstd', '-q', '-T0', '-c', str(source)], stdout=output, stderr=subprocess.PIPE)
        if result.returncode != 0:
            raise HostError('zstd failed while compressing %s.' % source)
        output.flush()
        os.fsync(output.fileno())


def decompress_disk(source, target):
    """Expand a .zst disk into `target`, keeping holes (zstd --sparse)."""
    if not zstd_available():
        raise HostError('Restoring computer disks needs the zstd tool; install it (apt-get install zstd).')
    result = subprocess.run(['zstd', '-d', '-q', '-f', '--sparse', '-o', str(target), str(source)],
                            stderr=subprocess.PIPE)
    if result.returncode != 0:
        Path(target).unlink(missing_ok=True)
        raise HostError('zstd failed while expanding %s.' % Path(source).name)


def prune_disk_backups(keep=DISK_BACKUPS_KEEP):
    """Keep the computer disks of only the newest `keep` backups that carry them; strip the disks (not the
    data, settings or Worker archives) from older ones. Returns [(backup id, bytes freed)]."""
    carrying = [item for item in list_backups() if item['meta'].get('disks')]
    stripped = []
    for item in carrying[keep:]:
        meta = dict(item['meta'])
        freed = sum(entry['compressed'] for entry in meta.pop('disks'))
        sizes = dict(meta['sizes'])
        sizes.pop('disks_allocated', None)
        meta['sizes'] = sizes
        meta['disks_removed_at'] = iso_now()
        remove_tree(item['path'] / 'disks')
        write_json(item['path'] / 'meta.json', meta, 0o600)
        stripped.append((item['id'], freed))
    return stripped


def assert_disks_quiet(disks):
    """Refuse to copy workspace disks a computer could still be writing.

    The services must be stopped (the Worker hibernates every computer on
    the way down, which flushes its disk), and no computer manager may still
    hold its state directory's manager.lock (an flock, visible across containers).
    """
    if any((c.get('State') or {}).get('Running') or (c.get('State') or {}).get('Restarting')
           for c in project_containers()):
        raise HostError('A TOFI container is still running; computer disks are only backed up while TOFI is '
                        'stopped. Nothing was copied.')
    for name, disk in disks:
        try:
            descriptor = os.open(str(disk.parent / 'manager.lock'), os.O_RDONLY | os.O_NOFOLLOW)
        except FileNotFoundError:
            continue
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as error:
            raise HostError('The computer of %s is still running (its manager holds the lock); its disk is not '
                            'copied while it could change. Nothing was copied.' % name) from error
        finally:
            os.close(descriptor)


def backup_disks(partial):
    """Compress each computer's workspace disk into <partial>/disks/<account>.ext4.zst (zstd on the sparse
    file); return the meta entries. Only call with the services stopped."""
    found = account_disks()
    if not found:
        return []
    assert_disks_quiet(found)
    directory = partial / 'disks'
    directory.mkdir(mode=0o700)
    entries = []
    for name, disk in found:
        info = disk.stat()
        target = directory / (name + '.ext4.zst')
        compress_disk(disk, target)
        entries.append({'account': name, 'file': 'disks/%s.ext4.zst' % name, 'bytes': info.st_size,
                        'compression': 'zstd', 'compressed': target.stat().st_size,
                        'allocated': allocated_bytes(disk), 'sha256': sha256_file(target),
                        'mode': stat.S_IMODE(info.st_mode), 'uid': info.st_uid, 'gid': info.st_gid,
                        'hibernated': (disk.parent / 'snapshot' / 'meta.json').is_file()})
    return entries


def check_backup(directory):
    """Audit a backup without restoring it: every file present and the size and sha256 meta.json records,
    every archive readable end to end, tofi.env usable, every listed disk intact, nothing unlisted.

    Returns {'id', 'meta', 'checks': [str], 'problems': [str]}; never raises for a damaged backup.
    """
    directory = Path(directory)
    report = {'id': directory.name, 'meta': None, 'checks': [], 'problems': []}
    try:
        meta = read_backup_meta(directory)
    except HostError as error:
        report['problems'].append(str(error))
        return report
    report['meta'] = meta
    listed = {'meta.json'}

    def file_problem(name, expected_sha, expected_size, label):
        listed.add(name)
        path = directory / name
        try:
            info = path.lstat()
        except OSError as error:
            report['problems'].append('%s is missing (%s).' % (label, error))
            return False
        if not stat.S_ISREG(info.st_mode):
            report['problems'].append('%s is not a regular file.' % label)
            return False
        if expected_size is not None and info.st_size != expected_size:
            report['problems'].append('%s is %d bytes; meta.json records %d.' % (label, info.st_size, expected_size))
            return False
        if not isinstance(expected_sha, str) or not SHA_RE.match(expected_sha):
            report['problems'].append('%s has no recorded checksum.' % label)
            return False
        if sha256_file(path) != expected_sha:
            report['problems'].append('%s does not match its recorded checksum (damaged).' % label)
            return False
        report['checks'].append('%s: checksum ok (%s)' % (label, human_bytes(info.st_size)))
        return True

    for key, name in sorted(meta['archives'].items()):
        sound = file_problem(name, meta['sha256'].get(name), meta['sizes'].get(ARCHIVE_SIZE_KEYS[key]), name)
        if not sound:
            continue
        try:
            members = 0
            with archive_reader(directory / name) as tar:
                for _member in safe_members(tar):
                    members += 1
            report['checks'].append('%s: readable, %d entries' % (name, members))
        except HostError as error:
            report['problems'].append('%s: %s' % (name, error))
        except (tarfile.TarError, EOFError, OSError, ValueError) as error:
            report['problems'].append('%s is not a readable archive (%s).' % (name, error))
    if not any(p.startswith(meta['archives']['etc']) for p in report['problems']):
        try:
            backup_env({'id': directory.name, 'path': directory, 'meta': meta})
            report['checks'].append('tofi.env: present and usable')
        except (HostError, OSError, tarfile.TarError, EOFError, ValueError) as error:
            report['problems'].append('The settings archive has no usable tofi.env (%s).' % error)
    disks = meta.get('disks')
    if disks is None:
        report['checks'].append('computer disks: not part of this backup')
    for entry in disks or []:
        label = 'disk ' + entry['account']
        if file_problem(entry['file'], entry['sha256'], entry['compressed'], label):
            if not zstd_available():
                report['problems'].append('%s cannot be tested: the zstd tool is missing.' % label)
            elif subprocess.run(['zstd', '-t', '-q', str(directory / entry['file'])],
                                stderr=subprocess.DEVNULL).returncode != 0:
                report['problems'].append('%s is not a valid zstd stream (zstd -t failed).' % label)
            else:
                report['checks'].append('%s: zstd stream intact (expands to %s)' % (label, human_bytes(entry['bytes'])))
    if disks is not None:
        listed.add('disks')
        report['checks'].append('computer disks: %s listed' % plural(len(disks), 'disk'))
    try:
        present = {entry.name for entry in directory.iterdir()}
        if (directory / 'disks').is_dir():
            present |= {'disks/' + entry.name for entry in (directory / 'disks').iterdir()}
    except OSError as error:
        report['problems'].append('The backup directory cannot be listed (%s).' % error)
        present = set()
    for name in sorted(present - listed):
        report['problems'].append('%s is in the backup but meta.json does not list it.' % name)
    return report


def verify_backups(selector):
    """`tofi backup --verify [ID|all]`: audit the newest backup, one by id, or all. Returns (reports, ok)."""
    with lifecycle_lock():
        backups = list_backups()
        if selector == 'all':
            items = backups
        elif selector == 'latest':
            items = backups[:1]
        else:
            items = [find_backup(selector)]
        if not items:
            raise HostError('No backups to verify; `sudo tofi backup` takes one.')
        reports = [check_backup(item['path']) for item in items]
    return reports, all(not report['problems'] for report in reports)


def render_verify(reports):
    lines = []
    for report in reports:
        lines.append('Backup %s: %s' % (report['id'], 'OK' if not report['problems'] else 'FAILED'))
        lines += ['  ok    ' + check for check in report['checks']]
        lines += ['  FAIL  ' + problem for problem in report['problems']]
    return '\n'.join(lines) + '\n'


AUTOMATIC_REASONS = ('pre-update', 'pre-restore')


def prune_backups(keep=BACKUP_KEEP):
    """Keep the newest `keep` automatic (pre-update / pre-restore) backups and
    drop the older ones and half-written leftovers. Manual backups are never
    pruned; `tofi backups --remove <id>` deletes them."""
    removed = []
    for entry in sorted(P.backups.iterdir()) if P.backups.is_dir() else []:
        if entry.name.startswith('.') and entry.name.endswith('.partial'):
            remove_tree(entry)
    automatic = [item for item in list_backups() if item['meta'].get('reason') != 'manual']
    for item in automatic[keep:]:
        remove_tree(item['path'])
        removed.append(item['id'])
    return removed


def remove_backup(backup_id):
    """`tofi backups --remove <id>`."""
    with lifecycle_lock():
        item = find_backup(backup_id)
        remove_tree(item['path'])
        return item['id']


def render_backups(backups):
    if not backups:
        return 'No backups yet. One is taken before every update; `sudo tofi backup` takes one now.\n'
    rows = [[item['id'], item['meta'].get('created_at') or '-', item['meta'].get('version') or '-',
             human_bytes(item['size']), item['meta'].get('reason') or '-'
             ] for item in backups]
    lines = format_table(['ID', 'CREATED (UTC)', 'VERSION', 'SIZE', 'REASON'], rows)
    lines += ['', '  Restore one with: sudo tofi restore <ID>',
              '  Automatic backups (pre-update, pre-restore): the last %d are kept. Manual ones stay until you '
              'remove them: sudo tofi backups --remove <ID>' % BACKUP_KEEP]
    return '\n'.join(lines) + '\n'


# --------------------------------------------------------------------------
# Restoring a backup: extract beside the live directory, then swap by rename


def restore_leftovers(target):
    """Settle what an interrupted restore left beside `target`.

    A missing `target` with a `.restore-old` beside it means the swap was cut
    between its two renames: the old directory is the data, put it back.
    """
    target = Path(target)
    old = target.with_name(target.name + '.restore-old')
    temporary = target.with_name(target.name + '.restore-tmp')
    if old.exists() or old.is_symlink():
        if not (target.exists() or target.is_symlink()):
            os.rename(old, target)
            warn('Recovered %s from an interrupted restore.' % target)
        else:
            remove_tree(old)
    remove_tree(temporary)


def stage_restore(target, archive, finish=None):
    """Extract `archive` into <target>.restore-tmp; the live directory is untouched."""
    target = Path(target)
    restore_leftovers(target)
    temporary = target.with_name(target.name + '.restore-tmp')
    temporary.mkdir(mode=0o700)
    try:
        extract_archive(archive, temporary)
        if finish is not None:
            finish(temporary)
    except BaseException:
        remove_tree(temporary)
        raise
    return temporary


def commit_restore(target, temporary):
    """Rename the live directory aside, rename the staged one in, then delete
    the old one. The old copy is deleted only after the new one is in place."""
    target = Path(target)
    old = target.with_name(target.name + '.restore-old')
    if target.exists() or target.is_symlink():
        os.rename(target, old)
    try:
        os.rename(temporary, target)
    except BaseException:
        if old.exists():
            os.rename(old, target)
        raise
    try:
        remove_tree(old)
    except OSError as error:
        warn('Restored, but could not remove the old copy %s (%s); delete it by hand.' % (old, error))


def overlay_restore(source, target):
    """Copy every file under `source` over `target`, one atomic rename each.

    Nothing in `target` is deleted: computer disks and snapshots live there
    and are not in the backup. A restored SQLite file drops its stale -wal/-shm.
    """
    source, target = Path(source), Path(target)
    for directory, dirs, files in os.walk(str(source)):
        relative = Path(directory).relative_to(source)
        destination = target / relative
        destination.mkdir(mode=0o700, parents=True, exist_ok=True)
        for name in files:
            origin, final = Path(directory) / name, destination / name
            if origin.is_symlink():
                if final.is_symlink() or final.exists():
                    final.unlink()
                os.symlink(os.readlink(origin), final)
                continue
            staged = destination / ('.%s.restore-new' % name)
            shutil.copy2(origin, staged)
            info = origin.stat()
            os.chown(staged, info.st_uid, info.st_gid)
            if name.endswith(('.sqlite', '.db')):
                for suffix in ('-wal', '-shm'):
                    Path(str(final) + suffix).unlink(missing_ok=True)
            os.replace(staged, final)


def restore_files(backup_id):
    """Replace /var/lib/tofi/data and /etc/tofi from backup `backup_id`.

    The archives are checked against meta.json's sha256 first; both trees are
    extracted beside the live ones before either is touched; /etc/tofi keeps
    the live install-state.json (the journal belongs to this host, not to the
    backup). Returns the backup's meta.
    """
    item = find_backup(backup_id)
    meta = verify_backup(item['path'])

    def data_finish(directory):
        os.chown(directory, APP_UID, APP_UID)
        os.chmod(directory, 0o700)

    def etc_finish(directory):
        os.chmod(directory, 0o700)
        if P.state_file.exists():
            shutil.copy2(P.state_file, directory / P.state_file.name)

    data_tmp = etc_tmp = worker_tmp = None
    try:
        data_tmp = stage_restore(P.data, item['path'] / meta['archives']['data'], data_finish)
        etc_tmp = stage_restore(P.etc, item['path'] / meta['archives']['etc'], etc_finish)
        if 'worker' in meta['archives']:
            worker_tmp = stage_restore(P.var / 'worker-meta', item['path'] / meta['archives']['worker'])
    except BaseException:
        for staged in (data_tmp, etc_tmp, worker_tmp):
            if staged is not None:
                remove_tree(staged)
        raise
    commit_restore(P.data, data_tmp)
    commit_restore(P.etc, etc_tmp)
    if worker_tmp is not None:
        try:
            overlay_restore(worker_tmp, P.worker)
        finally:
            remove_tree(worker_tmp)
    return meta


def restore_disks(item, meta, state):
    """Put the backup's computer disks back, one workspace.ext4 per account.

    Every disk is copied beside its live one first; only then is each live disk
    renamed to workspace-before-restore-<ns>.ext4 (kept: nothing is deleted)
    and the copy renamed in. Each swap is journalled in the transaction so a
    failed restore puts the live disks back (undo_disk_swaps). A computer's
    saved snapshot belongs to the disk it was taken with, so it is moved aside
    (journalled like the disk) and deleted only once the restore succeeded:
    the computer cold-boots on the restored disk. Returns the aside snapshots. Services must be stopped.
    """
    staged = []
    try:
        for entry in meta.get('disks') or []:
            account_dir = P.worker_state / entry['account']
            if account_dir.is_symlink():
                raise HostError('Computer directory %s is a symbolic link; nothing was restored.' % entry['account'])
            account_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
            temporary = account_dir / ('.%s.restore-new' % DISK_NAME)
            temporary.unlink(missing_ok=True)
            decompress_disk(item['path'] / entry['file'], temporary)
            os.chmod(temporary, entry.get('mode', 0o600) & 0o777)
            try:
                os.chown(temporary, entry.get('uid', 0), entry.get('gid', 0))
            except PermissionError:
                pass
            staged.append((account_dir, temporary))
    except BaseException:
        for _directory, temporary in staged:
            temporary.unlink(missing_ok=True)
        raise
    swaps = state['transaction'].setdefault('disk_swaps', [])
    asides = []
    for account_dir, temporary in staged:
        live = account_dir / DISK_NAME
        aside = account_dir / ('workspace-before-restore-%d.ext4' % time.time_ns()) if live.exists() else None
        snapshot = account_dir / 'snapshot'
        snapshot_aside = (account_dir / ('snapshot-before-restore-%d' % time.time_ns())
                          if snapshot.exists() or snapshot.is_symlink() else None)
        swaps.append({'live': str(live), 'aside': str(aside) if aside else None,
                      'snapshot': str(snapshot) if snapshot_aside else None,
                      'snapshot_aside': str(snapshot_aside) if snapshot_aside else None})
        save_state(state)
        if aside:
            os.rename(live, aside)
        os.rename(temporary, live)
        if snapshot_aside:
            os.rename(snapshot, snapshot_aside)
        asides.append(snapshot_aside)
    say('Restored %s; each replaced disk is kept beside it as workspace-before-restore-*.ext4 '
        '(delete them by hand when you are sure).' % plural(len(staged), 'computer disk'))
    return [aside for aside in asides if aside]


def undo_disk_swaps(transaction):
    """Put back the live disks a failed restore replaced."""
    for swap in reversed(transaction.get('disk_swaps') or []):
        live = Path(swap['live'])
        if swap['aside']:
            aside = Path(swap['aside'])
            if aside.exists():
                os.replace(aside, live)
        elif live.exists():
            live.unlink()
        if swap.get('snapshot_aside'):
            snapshot_aside = Path(swap['snapshot_aside'])
            if snapshot_aside.exists():
                remove_tree(Path(swap['snapshot']))
                os.replace(snapshot_aside, swap['snapshot'])
    transaction['disk_swaps'] = []


def backup_env(item):
    """tofi.env as stored in a backup, parsed and checked as format_env would write it."""
    meta = item['meta']
    with archive_reader(item['path'] / meta['archives']['etc']) as tar:
        for member in tar:
            if member.name in ('./tofi.env', 'tofi.env') and member.isfile():
                values = parse_env(tar.extractfile(member).read().decode(), 'backup ' + item['id'])
                break
        else:
            raise HostError('Backup %s has no tofi.env.' % item['id'])
    try:
        format_env(values)
    except HostError as error:
        raise HostError('Backup %s has an unusable tofi.env (%s).' % (item['id'], error)) from error
    return values


# --------------------------------------------------------------------------
# Update history and automatic updates


def record_update_history(entry):
    """Append one JSON line to /var/lib/tofi/update-history.jsonl (never fatal)."""
    entry = dict(entry, at=iso_now())
    try:
        P.update_history.parent.mkdir(parents=True, exist_ok=True)
        descriptor = os.open(str(P.update_history), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
        with os.fdopen(descriptor, 'a') as stream:
            stream.write(json.dumps(entry, sort_keys=True) + '\n')
        lines = P.update_history.read_text().splitlines()
        if len(lines) > UPDATE_HISTORY_KEEP:
            write_file(P.update_history, '\n'.join(lines[-UPDATE_HISTORY_KEEP:]) + '\n', 0o600)
    except OSError as error:
        warn('Could not append to %s (%s).' % (P.update_history, error))


def version_parts(version):
    match = re.match(r'^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-rc\.([0-9]+))?$', str(version or ''))
    if not match:
        return None
    major, minor, patch, rc = match.groups()
    return int(major), int(minor), int(patch), rc


def auto_update_allowed(installed, candidate):
    """Automatic updates install stable releases only, within the installed
    major.minor: a higher patch, or the stable release of the installed rc
    (v0.1.0-rc.N -> v0.1.0). Never an rc, a minor or major bump, or a downgrade."""
    old, new = version_parts(installed), version_parts(candidate)
    if old is None or new is None or new[3] is not None or new[:2] != old[:2]:
        return False
    return new[2] > old[2] or (new[2] == old[2] and old[3] is not None)


AUTO_UPDATE_SERVICE_TEXT = """[Unit]
Description=TOFI automatic fix-release update
After=network-online.target docker.service tofi.service
Wants=network-online.target
ConditionPathExists=/etc/tofi/install-state.json

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tofi update --auto
TimeoutStartSec=3h
"""

AUTO_UPDATE_TIMER_TEXT = """[Unit]
Description=Daily check for TOFI fix releases

[Timer]
OnCalendar=*-*-* 04:00:00
RandomizedDelaySec=45min
Persistent=true
Unit=tofi-auto-update.service

[Install]
WantedBy=timers.target
"""


def write_auto_update_units():
    write_file(P.auto_service, AUTO_UPDATE_SERVICE_TEXT, 0o644)
    write_file(P.auto_timer, AUTO_UPDATE_TIMER_TEXT, 0o644)


def remove_auto_update_units():
    run(['systemctl', 'disable', '--now', AUTO_UPDATE_TIMER], check=False)
    for path in (P.auto_timer, P.auto_service):
        if path.is_symlink() or path.exists():
            path.unlink()


def sync_auto_update_timer(env):
    """Enable the timer exactly when TOFI_AUTO_UPDATE=patch."""
    if not P.auto_timer.exists():
        return
    if env.get('TOFI_AUTO_UPDATE') == 'patch':
        run(['systemctl', 'enable', '--now', AUTO_UPDATE_TIMER], check=False)
    else:
        run(['systemctl', 'disable', '--now', AUTO_UPDATE_TIMER], check=False)


def auto_update_next_check():
    """'HH:MM' of the next scheduled check, or None."""
    try:
        text = run(['systemctl', 'show', AUTO_UPDATE_TIMER, '-p', 'NextElapseUSecRealtime', '--value'],
                   check=False, timeout=10).stdout
    except (OSError, subprocess.SubprocessError):
        return None
    match = re.search(r'\b(\d{2}:\d{2}):\d{2}\b', text or '')
    return match.group(1) if match else None


def configure_auto_update(mode):
    """`tofi config auto-update on|off`."""
    if mode not in ('on', 'off'):
        raise HostError('auto-update takes on or off.')
    with lifecycle_lock():
        state = load_state()
        if state is None:
            raise HostError('TOFI is not installed; run install.sh.')
        env = read_env()
        env['TOFI_AUTO_UPDATE'] = 'patch' if mode == 'on' else 'off'
        write_env(env)
        write_auto_update_units()
        run(['systemctl', 'daemon-reload'], check=False)
        sync_auto_update_timer(env)
    if mode == 'on':
        say('Automatic updates are on: fix releases (same major.minor, newer patch) install overnight '
            'when no Bot run is active. Turn off with: sudo tofi config auto-update off')
    else:
        say('Automatic updates are off. Update by hand with: sudo tofi update')
    say('The web page shows the new setting after the App restarts (sudo tofi stop && sudo tofi start).')
    return {'auto_update': env['TOFI_AUTO_UPDATE']}


def telemetry_enabled():
    """True only when the person said yes (install.sh writes `on`). A missing file means off."""
    try:
        return P.telemetry.read_text().strip() == 'on'
    except OSError:
        return False


def configure_telemetry(mode):
    """`tofi config telemetry on|off|status`. Only the installer's one-time report reads this."""
    if mode == 'status':
        say('Anonymous install report: %s.' % ('on' if telemetry_enabled() else 'off'))
        return {'telemetry': telemetry_enabled()}
    if mode not in ('on', 'off'):
        raise HostError('telemetry takes on, off or status.')
    with lifecycle_lock():
        write_file(P.telemetry, mode + '\n', 0o644)
    if mode == 'on':
        say('The anonymous install report is on for later installer runs. What it sends: deploy/self-host/README.md. '
            'Turn off with: sudo tofi config telemetry off')
    else:
        say('The anonymous install report is off. TOFI sends nothing.')
    return {'telemetry': mode == 'on'}


def proxy_upstream(env):
    """(address, port) a reverse proxy on another machine should forward to."""
    port = env.get('TOFI_HTTP_PORT') or str(DEFAULT_PORT)
    for family, address in interface_addresses():
        if family == 'inet':
            return address, port
    return '<tofi-server-ip>', port


def render_proxy_notes(env):
    """Ready-to-paste Caddy and Nginx configuration, or '' when TOFI is not set up for a proxy."""
    if env.get('TOFI_DOMAIN') or not env.get('TOFI_TRUSTED_PROXIES') or env.get('TOFI_BIND') != '0.0.0.0':
        return ''
    address, port = proxy_upstream(env)
    snippets = tofi_tui.proxy_snippets(address, port)
    return '\n'.join([
        'Reverse proxy: TOFI trusts X-Forwarded-For only from %s.' % env['TOFI_TRUSTED_PROXIES'],
        'Point your proxy at https://%s:%s (self-signed certificate; verification is skipped for that hop only).'
        % (address, port),
        'Replace tofi.example.com with the name your proxy serves.',
        '', '# Caddy', snippets['caddy'], '', '# Nginx', snippets['nginx'], ''])


def configure_trusted_proxies(proxies, clear=False):
    """`tofi config trusted-proxies [IP|CIDR ...] [--clear]`: show, set or clear TOFI_TRUSTED_PROXIES."""
    if clear and proxies:
        raise HostError('Give proxy addresses or --clear, not both.')
    with lifecycle_lock():
        state = load_state()
        if state is None:
            raise HostError('TOFI is not installed; run install.sh.')
        env = read_env()
        if env.get('TOFI_DOMAIN'):
            raise HostError('This install uses the bundled Caddy (--domain), which is already the only trusted '
                            'proxy. Reinstall without --domain to sit behind your own proxy.')
        if not proxies and not clear:
            current = env.get('TOFI_TRUSTED_PROXIES') or ''
            say('Trusted proxies: %s' % (current or 'none'))
            return {'trusted_proxies': current}
        if clear:
            value = ''
        else:
            entries, error = tofi_tui.parse_proxy_list(','.join(proxies))
            if error:
                raise HostError(error)
            value = ','.join(entries)
        env['TOFI_TRUSTED_PROXIES'] = value
        write_env(env)
    if value:
        say('Trusted proxies set to %s.' % value)
        notes = render_proxy_notes(env)
        if notes:
            say(notes)
    else:
        say('No proxy is trusted any more: every visitor is seen at the address it connects from.')
    say('Restart to apply: sudo tofi stop && sudo tofi start')
    return {'trusted_proxies': value}


def auto_update():
    """`tofi update --auto`, run daily by tofi-auto-update.timer.

    Does nothing unless TOFI_AUTO_UPDATE=patch. Installs only a newer patch of
    the installed major.minor (never an rc, minor or major bump); never waits
    for Bot runs (a busy host is skipped until tomorrow); one journal line and
    one update-history.jsonl line per run. Returns {'result', ...}; 'failed'
    means the update was rejected and rolled back.
    """
    require_root()
    state = load_state()
    if state is None:
        raise HostError('TOFI is not installed; run install.sh.')
    env = read_env()
    if env.get('TOFI_AUTO_UPDATE') != 'patch':
        say('tofi auto-update: off; nothing to do.')
        return {'result': 'off'}
    installed = env.get('TOFI_VERSION')

    def done(result, message, recorded=False, **extra):
        say('tofi auto-update: ' + message)
        if not recorded:
            record_update_history(dict(extra, trigger='auto', result=result, **{'from': installed}))
        return dict(extra, result=result)

    if state['phase'] != 'installed':
        return done('skipped', 'skipped, TOFI is in phase %s.' % state['phase'], reason='phase')
    try:
        manifest = fetch_manifest()
    except HostError as error:
        return done('check-failed', 'could not check for a new release (%s).' % error, reason=str(error)[:200])
    remember_latest(manifest)
    candidate = manifest['version']
    if not is_newer(candidate, installed):
        return done('up-to-date', '%s is current.' % installed, to=candidate)
    if not auto_update_allowed(installed, candidate):
        return done('not-eligible', '%s is available but is not a fix release for %s; update by hand with '
                    '`sudo tofi update` when ready.' % (candidate, installed), to=candidate)
    if candidate in rejected_versions(state):
        return done('skipped', '%s was rejected earlier on this host; not retrying.' % candidate,
                    to=candidate, reason='rejected-before')
    try:
        upgrade(manifest=manifest, wait=0, trigger='auto')
    except ActiveRunsError as error:
        return done('skipped', 'skipped %s, %s active; trying again tomorrow.'
                    % (candidate, plural(error.count, 'Bot run')), recorded=True, to=candidate, reason='active-runs')
    except LockBusy as error:
        return done('skipped', 'skipped %s, %s' % (candidate, error), to=candidate, reason='busy')
    except HostError as error:
        say('tofi auto-update: %s failed: %s' % (candidate, error))
        return {'result': 'failed', 'to': candidate, 'error': str(error)}
    say('tofi auto-update: updated %s -> %s.' % (installed, candidate))
    return {'result': 'updated', 'to': candidate}


# --------------------------------------------------------------------------
# Update requested from the web page (decision D69)
#
# Trust model: the App container (uid 10001, no capabilities) may write one small
# file, request.json, into a directory nothing else lives in, and read status.json
# from a read-only mount. The root side below treats the file as hostile input: a
# strict schema, a size cap, an age cap, a nonce seen once, and then the same
# checks `tofi update` already makes. The request never names a URL, tag source or
# flag: it can only ask for a published release of this repository that is newer
# than the installed one, and the host re-fetches the manifest from GitHub itself.

UPDATE_REQUEST_KEYS = {'version', 'channel', 'requested_at', 'nonce'}
NONCE_RE = re.compile(r'[0-9a-f]{32}')
# [0-9], not \d: str patterns match Arabic-Indic and other Unicode digits, which int() then accepts.
REQUEST_TIME_RE = re.compile(r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z')
# Progress shown for each state; only the download/verify/switch steps are steps we can observe.
UPDATE_PROGRESS = {'queued': 5, 'waiting_for_runs': 10, 'downloading': 30, 'verifying': 60, 'switching': 80,
                   'done': 100, 'failed': None, 'rolled_back': None}

UPDATE_REQUEST_SERVICE_TEXT = """[Unit]
Description=TOFI update requested from the web page
After=network-online.target docker.service tofi.service
Wants=network-online.target
ConditionPathExists=/etc/tofi/install-state.json
# Deliberate: at most 3 runs a minute. A request file the App keeps recreating must not
# make systemd loop the update service; past the limit the path unit fails and stays
# failed until `tofi start`/`update`/`config` runs ensure_update_request_channel (reset-failed).
StartLimitIntervalSec=60
StartLimitBurst=3

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tofi update --from-request
TimeoutStartSec=3h
"""

UPDATE_REQUEST_PATH_TEXT = """[Unit]
Description=Watch for a TOFI update request from the web page

[Path]
PathExists=/var/lib/tofi/update-request/request.json
Unit=tofi-update-request.service

[Install]
WantedBy=paths.target
"""


class RequestRefused(HostError):
    """A web update request that is refused; `code` is the stable code the page shows."""

    def __init__(self, code, detail=''):
        super().__init__(detail or code)
        self.code = code


_update_status = {}
_phase_hook = None


def report_phase(name):
    """Tell the web page which step an update is in (no-op outside a web request)."""
    if _phase_hook is not None:
        _phase_hook(name)


def write_update_status(state, **fields):
    """Atomically replace status.json (mode 0644, root-owned; the App mounts it read-only)."""
    _update_status.update(fields)
    _update_status['schema'] = 1
    _update_status['state'] = state
    _update_status['progress'] = UPDATE_PROGRESS.get(state, _update_status.get('progress'))
    _update_status['updated_at'] = iso_now()
    if state in ('done', 'failed', 'rolled_back'):
        _update_status['finished_at'] = _update_status['updated_at']
    try:
        P.update_status_dir.mkdir(parents=True, exist_ok=True)
        write_json(P.update_status_dir / 'status.json', _update_status, 0o644)
    except OSError as error:
        warn('Could not write the update status (%s).' % error)


def write_update_units():
    """True when a unit file changed."""
    changed = False
    for path, text in ((P.update_request_service, UPDATE_REQUEST_SERVICE_TEXT),
                       (P.update_request_path, UPDATE_REQUEST_PATH_TEXT)):
        try:
            same = path.read_text() == text
        except OSError:
            same = False
        if not same:
            write_file(path, text, 0o644)
            changed = True
    return changed


def prepare_update_directories():
    for path, mode, owner in ((P.update_request_dir, 0o750, APP_UID), (P.update_status_dir, 0o755, 0)):
        path.mkdir(parents=True, exist_ok=True)
        os.chmod(path, mode)
        os.chown(path, owner, owner)


def ensure_update_request_channel():
    """Install or refresh the request directories, the two units and the capability marker.

    Idempotent. The App reports `host_update_unavailable` until host.json exists, so
    an install that predates this feature shows no button until a command of this
    version has run once (`tofi start` at boot, `tofi update`, `tofi config ...`).
    """
    require_root()
    prepare_update_directories()
    changed = write_update_units()
    if changed:
        run(['systemctl', 'daemon-reload'], check=False)
    # A path unit that hit the start limit stays failed; clear it so re-running this repairs it.
    run(['systemctl', 'reset-failed', UPDATE_REQUEST_PATH, UPDATE_REQUEST_SERVICE], check=False)
    run(['systemctl', 'enable', '--now', UPDATE_REQUEST_PATH], check=False)
    write_json(P.update_status_dir / 'host.json', {'schema': 1, 'update_request': 1,
                                                    'path_unit': UPDATE_REQUEST_PATH}, 0o644)


def ensure_update_request_channel_quietly():
    """Called by commands that must not fail because of this convenience."""
    try:
        state = load_state()
        # Only a finished install: mid-install/upgrade/restore the units and directories are not ours to touch.
        if state is not None and state.get('phase') == 'installed':
            ensure_update_request_channel()
    except (HostError, OSError, subprocess.SubprocessError) as error:
        warn('Could not set up web updates (%s).' % error)


def remove_update_request_units():
    run(['systemctl', 'disable', '--now', UPDATE_REQUEST_PATH], check=False)
    for path in (P.update_request_path, P.update_request_service):
        if path.is_symlink() or path.exists():
            path.unlink()
    run(['systemctl', 'daemon-reload'], check=False)
    (P.update_status_dir / 'host.json').unlink(missing_ok=True)


def take_update_request():
    """Read and delete request.json. Returns bytes, or None when there is no request.

    The App (uid 10001) owns the request directory, so everything inside it is hostile:
    every step works on a descriptor of that directory and never follows a link. A
    regular file is read (capped) and unlinked; anything else is refused and renamed
    aside without recursing into it, so a bad request cannot loop the path unit and
    root never walks, chmods or deletes a tree the App built.
    """
    directory = os.open(str(P.update_request_dir), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        try:
            fd = os.open('request.json', os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
        except FileNotFoundError:
            return None
        except OSError:
            discard_update_request(directory)
            raise RequestRefused('request_invalid', 'request.json could not be opened as a plain file')
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode):
                raise RequestRefused('request_invalid', 'request.json is not a regular file')
            if info.st_size > UPDATE_REQUEST_MAX_BYTES:
                raise RequestRefused('request_too_large', 'request.json is larger than %d bytes' % UPDATE_REQUEST_MAX_BYTES)
            data = os.read(fd, UPDATE_REQUEST_MAX_BYTES + 1)
            if len(data) > UPDATE_REQUEST_MAX_BYTES:
                raise RequestRefused('request_too_large', 'request.json is larger than %d bytes' % UPDATE_REQUEST_MAX_BYTES)
            return data
        finally:
            os.close(fd)
            discard_update_request(directory)
    finally:
        os.close(directory)


def discard_update_request(directory):
    """Remove request.json relative to the directory descriptor; never follows or recurses."""
    try:
        os.unlink('request.json', dir_fd=directory)
        return
    except FileNotFoundError:
        return
    except (IsADirectoryError, PermissionError):
        pass
    # The renamed-aside entries (.rejected-*) stay in the App-owned request directory:
    # root never recurses into or deletes them (D69), however many pile up.
    aside = '.rejected-%d-%s' % (int(time.time()), secrets.token_hex(4))
    try:
        os.rename('request.json', aside, src_dir_fd=directory, dst_dir_fd=directory)
    except FileNotFoundError:
        pass


def parse_update_request(data, now=None):
    """Strictly validate request bytes; returns the request dict or raises RequestRefused."""
    now = time.time() if now is None else now
    try:
        request = json.loads(data.decode('utf-8'))
    except (UnicodeDecodeError, ValueError):
        raise RequestRefused('request_invalid', 'request.json is not valid JSON')
    if not isinstance(request, dict) or set(request) != UPDATE_REQUEST_KEYS:
        raise RequestRefused('request_invalid', 'request.json must hold exactly version, channel, requested_at, nonce')
    if not all(isinstance(request[key], str) for key in UPDATE_REQUEST_KEYS):
        raise RequestRefused('request_invalid', 'every request field is a string')
    version, channel = request['version'], request['channel']
    if not VERSION_RE.fullmatch(version):
        raise RequestRefused('request_invalid', 'the version is not a release tag')
    if channel not in UPDATE_CHANNELS:
        raise RequestRefused('request_invalid', 'unknown channel')
    if not NONCE_RE.fullmatch(request['nonce']) or not REQUEST_TIME_RE.fullmatch(request['requested_at']):
        raise RequestRefused('request_invalid', 'malformed nonce or time')
    if ('-rc.' in version) != (channel == 'rc'):
        raise RequestRefused('channel_mismatch', 'the version is not on the requested channel')
    try:
        stamp = calendar.timegm(time.strptime(request['requested_at'], '%Y-%m-%dT%H:%M:%SZ'))
    except ValueError:
        raise RequestRefused('request_invalid', 'malformed time')
    if stamp > now + 300 or now - stamp > UPDATE_REQUEST_MAX_AGE:
        raise RequestRefused('request_stale', 'the request is older than an hour or from the future')
    return request


def seen_update_nonce(nonce):
    """True when this nonce was handled before; otherwise record it."""
    try:
        seen = json.loads(P.update_nonces.read_text())
        if not isinstance(seen, list):
            seen = []
    except (OSError, ValueError):
        seen = []
    if nonce in seen:
        return True
    write_json(P.update_nonces, (seen + [nonce])[-UPDATE_NONCES_KEEP:])
    return False


def web_request_cooldown_left():
    """Seconds until a web update request may be tried again; 0 when it may."""
    try:
        last = float(json.loads(P.update_last_attempt.read_text()).get('at'))
    except (OSError, ValueError, TypeError, AttributeError):
        return 0
    elapsed = time.time() - last
    if elapsed < 0:  # clock moved back: do not lock the page out for longer than the cooldown
        return 0
    return max(0, UPDATE_REQUEST_COOLDOWN - elapsed)


def update_failure_code(error):
    text = str(error)
    if text.startswith('update rejected'):
        return 'update_rejected', True
    if 'rollback failed' in text:
        return 'rollback_failed', False
    if text.startswith('restore failed'):
        return 'update_rejected', True
    if text.startswith('Update refused: data schema'):
        return 'schema_change_blocked', False
    if text.startswith('Not enough free disk space'):
        return 'insufficient_disk', False
    return 'update_failed', False


def update_from_request():
    """`tofi update --from-request`, run by tofi-update-request.path as root.

    Returns {'result': none|refused|duplicate|done|failed}. Writes status.json at
    each step. The download, digest check, backup, switch and rollback are
    upgrade(): this function only decides whether the request may reach it.
    """
    global _phase_hook
    require_root()
    _update_status.clear()
    try:
        data = take_update_request()
        if data is None:
            say('tofi update --from-request: no request.')
            return {'result': 'none'}
        request = parse_update_request(data)
    except RequestRefused as error:
        write_update_status('failed', code=error.code, detail=str(error)[:300], progress=None)
        say('tofi update --from-request: refused (%s).' % error.code)
        record_update_history({'trigger': 'web', 'result': 'refused', 'reason': error.code})
        return {'result': 'refused', 'code': error.code}
    version, channel, nonce = request['version'], request['channel'], request['nonce']
    if seen_update_nonce(nonce):
        say('tofi update --from-request: duplicate request ignored.')
        return {'result': 'duplicate'}
    cooldown_left = web_request_cooldown_left()
    if cooldown_left <= 0:
        # Recorded before anything can refuse or fail, so every outcome counts; a request
        # refused for the cooldown does not move it (a flood cannot extend it).
        try:
            write_json(P.update_last_attempt, {'at': time.time()})
        except OSError as error:
            warn('Could not record the web update attempt (%s).' % error)
    state = load_state()
    installed = read_env().get('TOFI_VERSION') if state is not None else None
    _update_status.update({'version': version, 'from': installed, 'channel': channel, 'nonce': nonce,
                           'requested_at': request['requested_at'], 'started_at': iso_now(), 'code': None,
                           'detail': None, 'rolled_back_to': None, 'finished_at': None})

    def refuse(code, detail=''):
        write_update_status('failed', code=code, detail=detail[:300])
        say('tofi update --from-request: refused %s (%s).' % (version, code))
        record_update_history({'trigger': 'web', 'result': 'refused', 'from': installed, 'to': version, 'reason': code})
        return {'result': 'refused', 'code': code}

    if cooldown_left > 0:
        return refuse('request_cooldown', 'the last web update attempt was less than %d minutes ago'
                      % (UPDATE_REQUEST_COOLDOWN // 60))
    if state is None or state['phase'] != 'installed':
        return refuse('host_busy', 'TOFI is not in phase installed')
    if not is_newer(version, installed):
        return refuse('not_newer', '%s is not newer than %s' % (version, installed))
    if version in rejected_versions(state):
        return refuse('rejected_before', '%s was rejected earlier on this host' % version)
    write_update_status('queued')
    try:
        if channel == 'stable':
            try:
                manifest = fetch_manifest()
            except HostError as error:
                return refuse('release_check_failed', str(error))
            if manifest['version'] != version:
                return refuse('not_latest_stable', 'the latest stable release is %s' % manifest['version'])
        else:
            try:
                manifest = fetch_manifest(version)
            except HostError as error:
                return refuse('version_unpublished', str(error))
            if manifest['version'] != version:
                return refuse('version_unpublished', 'the release manifest names %s' % manifest['version'])
            # An rc is only offered ahead of the stable line: once a stable release at or past it
            # exists, installing the rc would be a step back from where `stable` already is.
            try:
                latest_stable = fetch_manifest()
            except HostError as error:
                return refuse('release_check_failed', str(error))
            if not is_newer(version, latest_stable['version']):
                return refuse('rc_superseded', '%s is not newer than the latest stable release %s'
                              % (version, latest_stable['version']))
    except BaseException as error:  # never leave the page showing "queued"
        write_update_status('failed', code='update_failed', detail=str(error)[:300])
        raise

    def hook(name):
        write_update_status(name)

    _phase_hook = hook
    try:
        upgrade(manifest=manifest, wait=UPDATE_REQUEST_WAIT_MINUTES, trigger='web')
    except ActiveRunsError as error:
        write_update_status('failed', code='runs_still_active', detail=str(error)[:300])
        say('tofi update --from-request: %s not installed, Bot runs still active.' % version)
        return {'result': 'failed', 'code': 'runs_still_active'}
    except LockBusy as error:
        write_update_status('failed', code='host_busy', detail=str(error)[:300])
        return {'result': 'failed', 'code': 'host_busy'}
    except HostError as error:
        code, rolled_back = update_failure_code(error)
        write_update_status('rolled_back' if rolled_back else 'failed', code=code, detail=str(error)[:300],
                            rolled_back_to=installed if rolled_back else None)
        say('tofi update --from-request: %s failed (%s).' % (version, code))
        return {'result': 'failed', 'code': code}
    except BaseException as error:
        write_update_status('failed', code='update_failed', detail=str(error)[:300])
        raise
    finally:
        _phase_hook = None
    write_update_status('done', code=None, detail=None)
    say('tofi update --from-request: updated %s -> %s.' % (installed, version))
    return {'result': 'done', 'version': version}


# --------------------------------------------------------------------------
# Lifecycle


def switch_current(version):
    target = P.releases / version
    if not target.is_dir():
        raise HostError('Host bundle %s is missing.' % target)
    temporary = P.opt / '.current.tmp'
    if temporary.is_symlink() or temporary.exists():
        temporary.unlink()
    os.symlink('releases/' + version, temporary)
    os.replace(temporary, P.current)


def current_release():
    try:
        return os.readlink(P.current).split('/')[-1]
    except OSError:
        return None


def install(manifest_path, options):
    """Fresh install, or resume/restart an existing one (idempotent)."""
    with lifecycle_lock():
        state = load_state()
        if state is not None:
            if state['phase'] == 'installed':
                if (options.get('domain') or options.get('behind_proxy') or options.get('lan') or options.get('local_only')
                        or options.get('port_given')):
                    warn('TOFI is already installed; exposure flags were ignored. Edit /etc/tofi/tofi.env to change them.')
                say('TOFI %s is already installed.' % state.get('version'))
                result = status()
                print(json.dumps(result, indent=2))
            else:
                result = resume(state)
        else:
            manifest = load_manifest_file(manifest_path)
            result = fresh_install(manifest, options)
    systemd_start()
    return result


def systemd_start():
    """Mark tofi.service active so ExecStop runs at shutdown."""
    if P.unit.exists():
        run(['systemctl', 'enable', 'tofi.service'], check=False)
        run(['systemctl', 'start', 'tofi.service'], check=False, timeout=900)
        try:
            sync_auto_update_timer(read_env())
        except (OSError, HostError):
            pass
        ensure_update_request_channel_quietly()


def fresh_install(manifest, options):
    progress = tofi_tui.Progress(tofi_tui.UI(sys.stdout))
    try:
        options = dict(options, computer=options.get('computer') or 'kvm')
        computer = options['computer'] == 'kvm'
        progress.begin(10, 'Preparing host configuration, images and the computer release' if computer
                       else 'Preparing host configuration and images')
        for message in preflight(options):
            warn(message)
        bundle = ASSETS
        budgets = runtime_budgets()
        env = render_env(manifest, options, budgets)
        prepare_directories(computer)
        pull_images(env, manifest['data_schema'])
        if computer:
            fetch_guest(guest_for_host(manifest), bundle_manager(bundle))
        apply_host_config(bundle, env)
        state = new_state(manifest['version'])
        save_state(state)
        progress.begin(11, 'Starting the Worker, then the App' if computer else 'Starting the App')
        finish_install(state, env)
        progress.end()
    except BaseException:
        progress.end(ok=False)
        raise
    return final_message(env)


def finish_install(state, env):
    try:
        transition(state, 'installing', 'start-worker')
        start_services(env, first_install=True, state=state)
        complete(state)
    except BaseException as error:
        failed(state, 'install-failed', error)
        say('Install did not finish. Data is retained. Check `sudo tofi status`; journal: %s' % P.state_file)
        raise


def final_message(env):
    ui = tofi_tui.UI(sys.stdout)
    if ui.fancy:
        ui.mark('ok', 'TOFI is running')
        ui.write()
    else:
        say('[12/12] TOFI is running')
    secret = read_setup_secret()
    urls = access_urls(env)
    info = {'version': env.get('TOFI_VERSION', ''), 'urls': urls, 'setup_key': secret,
            'domain': env.get('TOFI_DOMAIN', ''), 'port': env.get('TOFI_HTTP_PORT') or str(DEFAULT_PORT),
            'fingerprint': certificate_fingerprint() if app_scheme(env) == 'https' else None,
            'local_only': not env.get('TOFI_DOMAIN') and env.get('TOFI_BIND') != '0.0.0.0',
            'computer': backend_of(env)}
    sys.stdout.write(render_banner(info, color_mode(sys.stdout), terminal_width()))
    sys.stdout.flush()
    notes = render_proxy_notes(env)
    if notes:
        say(notes)
    return {'installed': True, 'url': urls[0], 'urls': urls, 'setup_key_pending': bool(secret)}


# --------------------------------------------------------------------------
# End-of-install banner (also the `tofi status` header)

def render_banner(info, mode=None, width=80):
    """The end-of-install summary. `mode` is color_mode(); None is plain text."""
    paint = Paint(mode)
    version = str(info.get('version') or '').lstrip('v')
    port = info.get('port') or str(DEFAULT_PORT)

    def row(label, value):
        return '  ' + (paint.dim(label.ljust(12)) if label else ' ' * 12) + value

    if width < BANNER_MIN_WIDTH:
        lines = ['tofi v%s · %s' % (version, TAGLINE)]
    else:
        lines = render_art(paint, version)
    lines.append('')
    urls = info.get('urls') or []
    for index, url in enumerate(urls):
        lines.append(row('Open' if index == 0 else '', paint.link(url)))
    if not info.get('domain') and not info.get('local_only'):
        lines.append(row('', paint.dim("or your server's public IP")))
    if info.get('local_only'):
        lines.append(row('', paint.dim('this server only; from your computer: ssh -L %s:127.0.0.1:%s <user>@<server>'
                                       % (port, port))))
    if info.get('setup_key'):
        lines.append(row('Setup key', paint.bold(info['setup_key']) + '   ' + paint.dim('(one time)')))
        lines.append(row('Next', 'enter the key, create your admin, connect a model'))
    else:
        lines.append(row('Sign in', 'with your existing admin account'))
    if info.get('computer') == 'none':
        lines.append(row('Computer', 'none · add one later: ' + paint.bold('sudo tofi computer add kvm')))
    lines.append('')
    if info.get('domain'):
        lines.append('  Allow TCP 80 and 443.')
    else:
        lines.append("  Browser warns about the certificate? That's expected — continue.")
        cloud = '' if info.get('local_only') else 'Cloud server? allow TCP %s' % port
        fingerprint = info.get('fingerprint') or 'unavailable'
        single = '  Fingerprint SHA256 %s   ·   %s' % (fingerprint, cloud)
        if cloud and len(single) <= width:
            lines.append('  %s %s   %s   %s' % (paint.dim('Fingerprint SHA256'), fingerprint,
                                               paint.dim('·'), cloud))
        elif len('  Fingerprint SHA256 ' + fingerprint) <= width:
            lines.append('  %s %s' % (paint.dim('Fingerprint SHA256'), fingerprint))
        else:
            # The full colon form (95 columns) gets a line of its own.
            lines.append('  ' + paint.dim('Fingerprint SHA256'))
            lines.append('  ' + fingerprint)
        if cloud and not (len(single) <= width):
            lines.append('  ' + cloud)
    lines.append('')
    lines.append('  ' + paint.dim(' · ').join(['tofi status', 'tofi update', 'tofi logs', 'tofi uninstall']))
    return '\n'.join(lines) + '\n'


def read_setup_secret():
    try:
        return P.bootstrap_secret.read_text().strip()
    except OSError:
        return None


def resume(state=None):
    """Finish or undo whatever transaction the journal says was interrupted."""
    state = state or load_state()
    if state is None:
        raise HostError('TOFI is not installed; run install.sh.')
    phase = state['phase']
    if phase in INSTALL_PHASES:
        say('Resuming an interrupted install (%s).' % phase)
        env = read_env()
        stopped()
        apply_host_config(P.current, env)
        finish_install(state, env)
        return final_message(env)
    if phase in UPGRADE_PHASES:
        say('Rolling back an interrupted update (%s).' % phase)
        return rollback(state)
    if phase in UNINSTALL_PHASES:
        say('Finishing an interrupted uninstall (%s).' % phase)
        return finish_uninstall(state)
    if phase == 'stopped-retained':
        say('Starting the retained installation with its existing data.')
        env = read_env()
        state['transaction'] = {'kind': 'install', 'step': 'prepared'}
        state['phase'] = 'prepared'
        save_state(state)
        apply_host_config(P.current, env)
        finish_install(state, env)
        return final_message(env)
    return status()


def start():
    with lifecycle_lock():
        state = load_state()
        if state is None:
            raise HostError('TOFI is not installed; run install.sh.')
        if state['phase'] != 'installed':
            raise HostError('TOFI is in phase %s; run `sudo tofi install` to resume.' % state['phase'])
        env = read_env()
        run(['systemd-tmpfiles', '--create', str(P.tmpfiles)], check=False)
        start_services(env)
        return {'started': True}


def stop(wait=0, force=False):
    with lifecycle_lock():
        if load_state() is None:
            raise HostError('TOFI is not installed.')
        wait_for_idle('stopping TOFI', wait, force)
        stopped(tolerate_unclean=True)
        return {'stopped': True, 'data_retained': True}


def snapshot():
    """What rollback needs to restore the running version exactly."""
    return {
        'env': read_env(),
        'worker_json': json.loads(P.worker_json.read_text()) if P.worker_json.exists() else None,
        'bundle': current_release(),
    }


def container_runner_accounts():
    """Accounts whose local connections already live in the Runner container
    (their local-runner.json says state=container), as data-directory names."""
    found = []
    for path in [P.data / 'local-runner.json'] + sorted(P.data.glob('accounts/*/local-runner.json')):
        try:
            if json.loads(path.read_text()).get('state') == 'container':
                found.append(path.parent.name if path.parent != P.data else 'owner')
        except (OSError, ValueError, AttributeError):
            continue
    return found


def check_runner_downgrade(manifest, allow):
    """Refuse a release that predates the Runner container once an account uses it.

    Such a release does not know the container: it would run those local
    connections on the account's computer again, from the copy made before the
    switch, and a later update would find two diverging copies. Only the
    command line can accept that (`--allow-runner-downgrade`); the unattended
    paths (auto and web update) never pass it."""
    if manifest_image(manifest, 'runner') or allow:
        return
    accounts = container_runner_accounts()
    if accounts:
        raise HostError(
            'Update refused: %s predates the Runner container, and %d account(s) already keep their local connections '
            '(Gmail, community packages) there. That release would run them from the older copy on the account\'s '
            'computer, without anything changed since the switch, and they would not work on an install without a '
            'computer; a later update would find two diverging copies (both are kept and flagged). '
            'Back up /var/lib/tofi, then re-run with --allow-runner-downgrade.' % (manifest['version'], len(accounts)))


def upgrade(version=None, manifest_path=None, allow_schema_change=False, manifest=None,
            wait=0, force=False, trigger='manual', confirm=None, allow_runner_downgrade=False):
    """Update to a release: back up, switch, start; on failure restore and roll back.

    `wait` minutes are given to active Bot runs to finish (then refuse unless
    `force`); `confirm(installed, manifest)` may cancel before anything is
    downloaded. The outcome is appended to update-history.jsonl.
    """
    info = {}
    try:
        result = _upgrade(info, version, manifest_path, allow_schema_change, manifest, wait, force, confirm,
                          allow_runner_downgrade)
    except LockBusy:
        raise
    except ActiveRunsError:
        if info.get('to'):
            record_update_history(dict(info, trigger=trigger, result='skipped' if trigger == 'auto' else 'refused',
                                       reason='active-runs'))
        raise
    except HostError as error:
        if info.get('to'):
            rolled_back = str(error).startswith(('update rejected', 'restore failed'))
            record_update_history(dict(info, trigger=trigger, result='rolled-back' if rolled_back else 'failed',
                                       error=str(error)[:300]))
        raise
    if result.get('upgraded'):
        record_update_history(dict(info, trigger=trigger, result='updated'))
    return result


def _upgrade(info, version, manifest_path, allow_schema_change, manifest, wait, force, confirm,
             allow_runner_downgrade=False):
    with lifecycle_lock():
        state = load_state()
        if state is None or state['phase'] != 'installed':
            raise HostError('Update needs an installed TOFI in phase installed; run `sudo tofi install` first.')
        if manifest is None:
            manifest = load_manifest_file(manifest_path) if manifest_path else fetch_manifest(version)
        previous = snapshot()
        info.update({'from': previous['env']['TOFI_VERSION'], 'to': manifest['version']})
        if manifest['version'] == previous['env']['TOFI_VERSION']:
            say('TOFI %s is already installed.' % manifest['version'])
            return {'upgraded': False, 'version': manifest['version']}
        check_runner_downgrade(manifest, allow_runner_downgrade)
        if confirm is not None:
            confirm(previous['env']['TOFI_VERSION'], manifest)
        wait_for_idle('the update', wait, force)
        report_phase('downloading')
        candidate = dict(previous['env'])
        computer = has_computer(candidate)
        candidate.update({
            'TOFI_VERSION': manifest['version'],
            'TOFI_APP_IMAGE': manifest['images']['app'],
            'TOFI_CADDY_IMAGE': manifest['images']['caddy'],
        })
        for key in RUNNER_ENV_KEYS:
            candidate.pop(key, None)  # a release without the Runner leaves no Runner settings behind
        candidate.update(runner_env(manifest))
        if computer:
            # An install without a computer keeps no Worker image or Guest pin: nothing to pull or fetch.
            candidate.update({'TOFI_WORKER_IMAGE': manifest['images']['worker'],
                              'TOFI_GUEST_VERSION': manifest['guest']['version']})
        # Everything that can be checked happens before anything is stopped.
        pull_images(candidate, None)
        report_phase('verifying')
        current_schema = image_label(previous['env']['TOFI_APP_IMAGE'], DATA_SCHEMA_LABEL)
        new_schema = image_label(candidate['TOFI_APP_IMAGE'], DATA_SCHEMA_LABEL)
        if new_schema != manifest['data_schema']:
            raise HostError('The new App image declares data schema %r but the release says %r.'
                            % (new_schema, manifest['data_schema']))
        if new_schema != current_schema and not allow_schema_change:
            raise HostError('Update refused: data schema changes from %r to %r. Back up /var/lib/tofi, '
                            'then re-run with --allow-schema-change.' % (current_schema, new_schema))
        install_bundle(manifest)
        bundle = P.releases / manifest['version']
        if computer:
            fetch_guest(guest_for_host(manifest), bundle_manager(bundle))
            announce_computer_impact(previous['env'], manifest)
        # A value tofi cannot write back must never be found after the services are down.
        format_env(candidate)
        format_env(previous['env'])
        # The backup must fit, and no run may have started during the downloads.
        check_backup_space('the update')
        wait_for_idle('the update', 0, force)
        report_phase('switching')
        state['transaction'] = {'kind': 'upgrade', 'step': 'stop-old', 'previous': previous,
                                'candidate_version': manifest['version'], 'backup': None, 'data_changed': False}
        state['phase'] = 'upgrading'
        save_state(state)
        try:
            stopped()
            # Services are down, so SQLite is quiescent: back up before anything changes.
            transition(state, 'upgrading', 'backup')
            state['transaction']['backup'] = backup_data('pre-update', previous['env'], check_space=False)['id']
            transition(state, 'upgrading', 'switch-config')
            apply_host_config(bundle, candidate)
            switch_current(manifest['version'])
            # From here the candidate may run migrations: rollback restores the backup.
            state['transaction']['data_changed'] = True
            transition(state, 'upgrading', 'start-candidate')
            start_services(candidate)
            complete(state, version=manifest['version'])
        except BaseException as error:
            reject_candidate(state, error, manifest['version'], candidate.get('TOFI_GUEST_VERSION'), previous,
                             'update rejected; previous version restored')
        state.pop('last_update_failure', None)
        save_state(state)
        prune_releases(keep=[manifest['version'], previous['bundle']],
                       keep_guests=[candidate.get('TOFI_GUEST_VERSION'), previous['env'].get('TOFI_GUEST_VERSION')]
                       if computer else None)
        try:
            removed = prune_backups()
        except OSError as error:
            removed = []
            warn('Could not prune old backups (%s).' % error)
        if removed:
            say('Removed older backups: %s.' % ', '.join(removed))
        say('Updated to %s.' % manifest['version'])
        older = sorted(guest_references() - {candidate['TOFI_GUEST_VERSION']}) if computer else []
        if older:
            say('Hibernated computers still hold snapshots from %s: each cold-boots on %s at its next use. '
                'See `sudo tofi computers`; switch them now with `sudo tofi computers upgrade --all`.'
                % (', '.join(older), candidate['TOFI_GUEST_VERSION']))
        return {'upgraded': True, 'version': manifest['version'], 'data_retained': True}


def reject_candidate(state, error, version, guest_version, previous, message):
    """A failed update or restore: roll back (restoring data if it may have
    changed), keep a record `tofi status` shows, drop the rejected release,
    and raise the operator-facing error."""
    failed(state, 'upgrade-failed', error)
    try:
        rollback(state)
    except BaseException as rollback_error:
        raise HostError('Update failed (%s) and rollback failed (%s); data is retained. '
                        'Run `sudo tofi install` to retry.' % (error, rollback_error)) from error
    # complete() cleared last_error; keep a record `tofi status` shows,
    # and drop the rejected candidate's bundle and Guest release.
    state['last_update_failure'] = {'version': version, 'message': str(error)[:500],
                                    'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
    remember_rejected(state, version)
    save_state(state)
    discard_candidate(version, guest_version, previous)
    raise HostError('%s (%s)' % (message, error)) from error


def restore_manifest(version):
    """The release manifest for `version`: the installed host bundle's copy, else GitHub's."""
    local = P.releases / version / 'manifest.json'
    if local.is_file():
        try:
            return load_manifest_file(local)
        except HostError:
            pass
    return fetch_manifest(version)


def restore_backup(backup_id, yes=False, wait=0, force=False, confirm=None, disks=True):
    """`tofi restore <id>`: put a backup's data and settings back and switch to its version.

    Runs as an update-shaped transaction: the current state is backed up first
    (reason pre-restore), and if the restored version does not start, rollback
    restores that backup and starts the version that was running. A backup that
    carries computer disks puts them back too (`disks=False` leaves them).
    Active Bot runs refuse it (wait_for_idle) unless --force.
    """
    with lifecycle_lock():
        state = load_state()
        if state is None or state['phase'] != 'installed':
            raise HostError('Restore needs an installed TOFI in phase installed; run `sudo tofi install` first.')
        item = find_backup(backup_id)
        meta = verify_backup(item['path'])
        backup_env(item)
        version = meta.get('version')
        if not isinstance(version, str) or not VERSION_RE.match(version):
            raise HostError('Backup %s records no valid version.' % item['id'])
        if not yes:
            if confirm is None:
                raise HostError('Restore replaces all current data and settings with backup %s; '
                                'add --yes to confirm.' % item['id'])
            confirm(item, meta)
        wait_for_idle('the restore', wait, force)
        previous = snapshot()
        manifest = restore_manifest(version)
        candidate = dict(previous['env'])
        # The restored settings decide the backend; a backup that held a computer
        # (it recorded a Guest) puts one back even on an install that has none now.
        computer = has_computer(previous['env']) or bool(meta.get('guest'))
        candidate.update({
            'TOFI_VERSION': version,
            'TOFI_APP_IMAGE': meta.get('app_image') or manifest['images']['app'],
            'TOFI_CADDY_IMAGE': meta.get('caddy_image') or manifest['images']['caddy'],
        })
        if computer:
            candidate.update({'TOFI_COMPUTER_BACKEND': 'kvm',
                              'TOFI_WORKER_IMAGE': meta.get('worker_image') or manifest['images']['worker'],
                              'TOFI_GUEST_VERSION': meta.get('guest') or manifest['guest']['version']})
        pull_images(candidate, None)
        install_bundle(manifest)
        bundle = P.releases / version
        if computer:
            prepare_computer_directories()
            fetch_guest(guest_for_host(manifest), bundle_manager(bundle))
        # Room for a backup of the current state plus the restored copy beside it.
        current = tree_bytes(P.data) + tree_bytes(P.etc)
        incoming = int(meta['sizes'].get('data_bytes') or 0) + int(meta['sizes'].get('etc_bytes') or 0)
        with_disks = bool(disks and meta.get('disks'))
        if with_disks:
            incoming += sum(entry['allocated'] for entry in meta['disks'])  # expands back to the allocated size
        need = current + incoming + BACKUP_FREE_MARGIN
        free = free_bytes(P.var)
        if free < need:
            raise HostError('Not enough free disk space for the restore: it needs %s free under /var/lib/tofi '
                            '(a backup of the current state, %s, plus the restored copy, %s, plus 1 GiB) but only %s '
                            'is free. Nothing was changed.'
                            % (human_bytes(need), human_bytes(current), human_bytes(incoming), human_bytes(free)))
        format_env(candidate)
        format_env(previous['env'])
        wait_for_idle('the restore', 0, force)
        state['transaction'] = {'kind': 'restore', 'step': 'stop-old', 'previous': previous,
                                'candidate_version': version, 'restore_from': item['id'],
                                'backup': None, 'data_changed': False}
        state['phase'] = 'upgrading'
        save_state(state)
        try:
            stopped(tolerate_unclean=True)
            transition(state, 'upgrading', 'backup')
            pre_restore = backup_data('pre-restore', previous['env'], check_space=False)['id']
            state['transaction']['backup'] = pre_restore
            # The swap is atomic per directory, but treat the data as changed from here.
            state['transaction']['data_changed'] = True
            transition(state, 'upgrading', 'restore-data')
            restore_files(item['id'])
            snapshot_asides = []
            if with_disks:
                transition(state, 'upgrading', 'restore-disks')
                snapshot_asides = restore_disks(item, meta, state)
            env = read_env()
            transition(state, 'upgrading', 'switch-config')
            apply_host_config(bundle, env)
            switch_current(version)
            transition(state, 'upgrading', 'start-candidate')
            start_services(env)
            complete(state, version=version)
            for snapshot_aside in snapshot_asides:
                try:
                    remove_tree(snapshot_aside)
                except OSError as error:
                    warn('Could not remove %s (%s); delete it by hand.' % (snapshot_aside, error))
        except BaseException as error:
            reject_candidate(state, error, version, candidate.get('TOFI_GUEST_VERSION'), previous,
                             'restore failed; previous state recovered')
        state.pop('last_update_failure', None)
        save_state(state)
        prune_releases(keep=[version, previous['bundle']],
                       keep_guests=[candidate.get('TOFI_GUEST_VERSION'), previous['env'].get('TOFI_GUEST_VERSION')]
                       if computer else None)
        say('Restored backup %s; TOFI %s is running with its data and settings.' % (item['id'], version))
        return {'restored': item['id'], 'version': version, 'pre_restore_backup': pre_restore}


def backup_now(note=None, wait=0, force=False, disks=True):
    """`tofi backup`: stop, back up, start again (only if it was running).

    Computer disks are included unless `disks` is False; the Worker's stop
    hibernates every computer first, so each disk is quiet when it is copied."""
    with lifecycle_lock():
        state = load_state()
        if state is None or state['phase'] != 'installed':
            raise HostError('Backup needs an installed TOFI in phase installed; run `sudo tofi install` first.')
        wait_for_idle('the backup', wait, force)
        expected = disks_estimate() if disks else 0
        small = tree_bytes(P.data) + tree_bytes(P.etc)
        say('Space estimate: about %s for this backup (data and settings %s%s). It is an estimate: the disks '
            'are assumed to compress to %d%% of their allocated size.'
            % (human_bytes(small + expected), human_bytes(small),
               (', %d computer disk(s) %s allocated -> about %s compressed'
                % (len(account_disks()), human_bytes(disks_allocated()), human_bytes(expected))) if disks else '',
               int(DISK_COMPRESSION_RATIO * 100)))
        check_backup_space('the backup', expected)
        if disks and account_disks() and not zstd_available():
            raise HostError('Backing up computer disks needs the zstd tool; install it (apt-get install zstd) or add '
                            '--no-disks. Nothing was changed.')
        env = read_env()
        was_running = any((c.get('State') or {}).get('Running') for c in project_containers())
        stopped()
        meta, failure = None, None
        try:
            meta = backup_data('manual', env, note, check_space=False, disks=disks)
        except BaseException as error:
            failure = error
        if was_running:
            try:
                start_services(env)
            except BaseException as error:
                if failure is None:
                    raise
                warn('Could not start TOFI again after the failed backup (%s); run `sudo tofi start`.' % error)
        if failure is not None:
            raise failure
        if meta.get('disks'):
            for backup_id, freed in prune_disk_backups():
                say('Removed the computer disks of backup %s (%s freed); only the newest %d backups keep their '
                    'disks, its data and settings stay.' % (backup_id, human_bytes(freed), DISK_BACKUPS_KEEP))
        return meta


def announce_computer_impact(env, manifest):
    """Before an update stops the Worker, say what happens to the computers.

    Best effort: an unreachable Worker never blocks an update.
    """
    try:
        rows = computers_report(env.get('TOFI_GUEST_VERSION'), busy=True)
    except (HostError, OSError, ValueError, subprocess.SubprocessError):
        return
    components = [{'name': 'guest', 'installed': env.get('TOFI_GUEST_VERSION'),
                   'available': manifest['guest']['version'],
                   'changes': env.get('TOFI_GUEST_VERSION') != manifest['guest']['version']}]
    for line in update_impact_lines(rows, components):
        say(line.strip())


def discard_candidate(version, guest_version, previous):
    """Remove a rejected update's bundle and Guest release, never the running ones."""
    if version != previous.get('bundle') and version != current_release():
        remove_tree(P.releases / version)
    if guest_version and guest_version != previous['env'].get('TOFI_GUEST_VERSION'):
        remove_tree(P.guest / guest_version)


def install_bundle(manifest):
    """Download and unpack the host bundle for `manifest` (verified)."""
    target = P.releases / manifest['version']
    marker = target / '.bundle.sha256'
    if marker.exists() and marker.read_text().strip() == manifest['bundle']['sha256']:
        return target
    P.releases.mkdir(parents=True, exist_ok=True)
    archive = P.releases / ('.%s.tar.gz' % manifest['version'])
    partial = P.releases / ('.%s.partial' % manifest['version'])
    remove_tree(partial)
    try:
        download(manifest['bundle']['url'], archive, manifest['bundle']['sha256'])
        partial.mkdir(mode=0o755)
        run(['tar', '-xzf', str(archive), '--no-same-owner', '-C', str(partial)])
        write_file(partial / '.bundle.sha256', manifest['bundle']['sha256'] + '\n', 0o644)
        write_json(partial / 'manifest.json', manifest, 0o644)
        remove_tree(target)
        os.rename(partial, target)
    finally:
        archive.unlink(missing_ok=True)
        remove_tree(partial)
    return target


def rollback(state):
    """Restore the previous images, Guest pin and host config.

    If the candidate was started (it may have run schema migrations), the data
    and settings are first restored from the pre-update backup, after its
    checksums verify; otherwise the data is untouched.
    """
    transaction = state['transaction']
    previous = transaction['previous']
    env = previous['env']
    try:
        transition(state, 'rolling-back', 'stop-candidate')
        stopped(tolerate_unclean=True)
        if transaction.get('disk_swaps'):
            undo_disk_swaps(transaction)
            save_state(state)
        if transaction.get('data_changed') and not transaction.get('data_restored'):
            backup_id = transaction.get('backup')
            if backup_id:
                transition(state, 'rolling-back', 'restore-data')
                restore_files(backup_id)
                transaction['data_restored'] = True
                save_state(state)
                say('Restored data and settings from backup %s.' % backup_id)
            else:
                warn('The candidate ran but no backup was taken (the update began with an older tofi); '
                     'data stays as the candidate left it. If the candidate migrated it, the previous version refuses to open it '
                     '(schema_too_new); run `sudo tofi update` again or `sudo tofi restore <backup>`.')
        transition(state, 'rolling-back', 'restore-config')
        bundle = P.releases / previous['bundle'] if previous.get('bundle') else ASSETS
        apply_host_config(bundle, env)
        if previous.get('worker_json') is not None:
            write_json(P.worker_json, previous['worker_json'])
        if previous.get('bundle'):
            switch_current(previous['bundle'])
        transition(state, 'rolling-back', 'start-previous')
        start_services(env, runner_required=False)
        complete(state, version=env['TOFI_VERSION'])
    except BaseException as error:
        failed(state, 'rollback-failed', error)
        raise
    return {'recovered': True, 'version': env['TOFI_VERSION'], 'data_retained': True}


def prune_releases(keep, keep_guests):
    """Drop host bundles outside `keep`, and Guest releases outside
    `keep_guests` that no hibernated computer's snapshot was taken with.
    `keep_guests` None (an install without a computer) leaves Guests alone."""
    for entry in P.releases.iterdir() if P.releases.is_dir() else []:
        if entry.name.startswith('.') or entry.name in keep:
            continue
        remove_tree(entry)
    if keep_guests is not None:
        prune_guests([guest for guest in keep_guests if guest])


def uninstall(purge=False, confirm=None):
    with lifecycle_lock():
        state = load_state()
        if state is None and not purge:
            raise HostError('TOFI is not installed.')
        if state is not None and state['phase'] in INSTALL_PHASES | UPGRADE_PHASES:
            raise HostError('A %s transaction is pending; run `sudo tofi install` first.' % state['phase'])
        if purge:
            return purge_everything(state, confirm)
        if state['phase'] == 'stopped-retained':
            say('TOFI is already uninstalled; data is retained under /var/lib/tofi.')
            return {'uninstalled': True, 'data_retained': True}
        state['transaction'] = {'kind': 'uninstall', 'step': 'stop-services'}
        state['phase'] = 'uninstalling'
        save_state(state)
        return finish_uninstall(state)


def finish_uninstall(state):
    try:
        transition(state, 'uninstalling', 'stop-services')
        run(['systemctl', 'disable', 'tofi.service'], check=False)
        remove_auto_update_units()
        remove_update_request_units()
        stopped(tolerate_unclean=True)
        transition(state, 'uninstalling', 'remove-services')
        if compose_file().exists():
            compose('rm', '-f', '-s')
        remove_runner_egress_filter()
        complete(state, 'stopped-retained')
    except BaseException as error:
        failed(state, 'uninstall-failed', error)
        raise
    say('TOFI is stopped and its containers are removed. Kept: /etc/tofi, /var/lib/tofi, images, AppArmor profile.')
    say('Re-run install.sh to start again with the same data, or `sudo tofi uninstall --purge` to delete everything.')
    return {'uninstalled': True, 'data_retained': True}


IMAGE_ENV_KEYS = ('TOFI_APP_IMAGE', 'TOFI_WORKER_IMAGE', 'TOFI_CADDY_IMAGE', 'TOFI_RUNNER_IMAGE')


def installation_images(state):
    """Image references that belong to this installation, current and previous.

    Sources, nothing else: the current tofi.env, the env saved for a pending
    rollback, and the manifest of every release bundle still under /opt/tofi.
    Manifest images must come from TOFI's own repositories; nothing is ever
    matched by name pattern or listed from `docker images`."""
    references = []

    def add(reference):
        if reference and isinstance(reference, str) and reference not in references:
            references.append(reference)

    try:
        env = read_env()
    except OSError:
        env = {}
    for key in IMAGE_ENV_KEYS:
        add(env.get(key))
    previous = ((state or {}).get('transaction') or {}).get('previous') or {}
    for key in IMAGE_ENV_KEYS:
        add((previous.get('env') or {}).get(key))
    try:
        manifests = sorted(P.releases.glob('*/manifest.json'))
    except OSError:
        manifests = []
    allowed = tuple(repository + '@' for repository in (*IMAGE_REPOS.values(), *OPTIONAL_IMAGE_REPOS.values()))
    for path in manifests:
        try:
            images = json.loads(path.read_text()).get('images') or {}
        except (OSError, ValueError, AttributeError):
            continue
        for role in (*IMAGE_REPOS, *OPTIONAL_IMAGE_REPOS):
            reference = images.get(role) if isinstance(images, dict) else None
            if isinstance(reference, str) and reference.startswith(allowed):
                add(reference)
    return references


def remove_installation_images(references):
    """`docker image rm` each reference, without force; say what happened."""
    removed, kept = [], []
    for reference in references:
        result = run(['docker', 'image', 'rm', reference], check=False)
        (removed if result.returncode == 0 else kept).append(reference)
    for reference in removed:
        say('Removed image %s' % reference)
    for reference in kept:
        say('Kept image %s (already gone, or still used by another container)' % reference)
    if not references:
        say('No TOFI images were recorded for this installation.')
    return removed


def purge_everything(state, confirm):
    hostname = socket.gethostname()
    if confirm is None:
        try:
            with open('/dev/tty') as tty:
                print('This permanently deletes all TOFI accounts, conversations, computer disks and backups.')
                print('Type the hostname (%s) to confirm: ' % hostname, end='', flush=True)
                confirm = tty.readline().strip()
        except OSError:
            confirm = ''
    if confirm != hostname:
        raise HostError('Purge cancelled: the typed hostname did not match %s.' % hostname)
    if state is not None:
        stopped(tolerate_unclean=True)
    if compose_file().exists() and P.env_file.exists():
        compose('down', '--remove-orphans')
    remove_runner_egress_filter()
    images = installation_images(state)
    run(['systemctl', 'disable', 'tofi.service'], check=False)
    remove_auto_update_units()
    remove_update_request_units()
    for profile in (P.apparmor_profile, P.runner_apparmor_profile):
        if profile.exists():
            run(['apparmor_parser', '-R', str(profile)], check=False)
    for path in (P.unit, P.tmpfiles, P.apparmor_profile, P.runner_apparmor_profile, P.bin_link):
        if path.is_symlink() or path.exists():
            path.unlink()
    run(['systemctl', 'daemon-reload'], check=False)
    for path in (P.var, P.etc, P.run_dir, P.opt, P.latest_cache.parent):
        remove_tree(path)
    remove_installation_images(images)
    say('TOFI and all of its data were removed.')
    return {'purged': True}


def last_update_attempt():
    """The newest line of update-history.jsonl, or None."""
    try:
        lines = P.update_history.read_text().splitlines()
    except OSError:
        return None
    for line in reversed(lines[-20:]):
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        if isinstance(entry, dict):
            return entry
    return None


def status(details=False):
    """Journal, services, health and URLs; with `details` (the `tofi status`
    command) also versions, the latest release and every account computer."""
    state = load_state()
    if state is None:
        return {'installed': False}
    result = {'phase': state['phase'], 'version': state.get('version'),
              'transaction': state.get('transaction') and {
                  'kind': state['transaction'].get('kind'), 'step': state['transaction'].get('step')},
              'last_error': state.get('last_error'), 'journal': str(P.state_file)}
    if state.get('last_update_failure'):
        result['last_update_failure'] = state['last_update_failure']
    attempt = last_update_attempt()
    if attempt and attempt.get('result') in ('rolled-back', 'failed'):
        result['last_update_attempt'] = attempt
    transaction = state.get('transaction') or {}
    if state['phase'] in UPGRADE_PHASES:
        previous = (transaction.get('previous') or {}).get('env') or {}
        result['recover'] = {'rollback_to': previous.get('TOFI_VERSION'), 'backup': transaction.get('backup')}
    try:
        containers = project_containers()
        result['services'] = {service_of(c): c['State'].get('Health', {}).get('Status') or c['State'].get('Status')
                              for c in containers}
    except (subprocess.SubprocessError, ValueError, OSError):
        result['services'] = 'docker unavailable'
    try:
        result['healthy'] = health(attempts=1)
    except HostError:
        result['healthy'] = False
    result['setup_key_pending'] = read_setup_secret() is not None
    try:
        env = read_env()
    except (OSError, HostError):
        env = None
    if env is not None:
        result['computer_backend'] = backend_of(env)
        result['runner'] = runner_container_state() if env.get('TOFI_RUNNER_IMAGE') else 'none'
        result['auto_update'] = env.get('TOFI_AUTO_UPDATE') == 'patch'
        if details and result['auto_update']:
            result['next_check'] = auto_update_next_check()
        try:
            result['urls'] = access_urls(env)
            if app_scheme(env) == 'https':
                result['certificate_sha256'] = certificate_fingerprint()
        except (OSError, HostError):
            pass
    if details and env is not None:
        result['versions'] = versions_report(env)
        result['run_health'] = run_health()
        if state.get('last_computers_upgrade'):
            result['last_computers_upgrade'] = state['last_computers_upgrade']
        if has_computer(env):
            try:
                result['computers'] = computers_report(env.get('TOFI_GUEST_VERSION'))
            except (HostError, OSError, ValueError, subprocess.SubprocessError) as error:
                result['computers_error'] = str(error)
    return result


def status_access(env):
    """What render_status needs to know about how this install is reached."""
    return {'domain': env.get('TOFI_DOMAIN', ''), 'port': env.get('TOFI_HTTP_PORT') or str(DEFAULT_PORT),
            'local_only': not env.get('TOFI_DOMAIN') and env.get('TOFI_BIND') != '0.0.0.0'}


def short_fingerprint(fingerprint):
    groups = str(fingerprint).split(':')
    if len(groups) <= 9:
        return str(fingerprint)
    return '%s … %s' % (':'.join(groups[:4]), ':'.join(groups[-4:]))


def version_summary(result, verbose):
    """The Version row: installed version and whether anything newer is out."""
    versions = result.get('versions') or {}
    text = str(result.get('version') or '?')
    latest, error = versions.get('latest'), versions.get('latest_error')
    if latest is None:
        # A 404 on latest/download means no stable (non-prerelease) release exists yet.
        if error and 'HTTP Error 404' in str(error):
            note = 'no stable release yet'
        else:
            note = "couldn't check for updates"
    elif versions.get('update_available'):
        note = '%s available · sudo tofi update' % latest
    else:
        note = 'up to date'
    text += ' · ' + note
    if verbose and latest is None:
        text += ' (%s)' % (error or 'not checked')
    elif verbose and error:
        text += ' · checked %s, offline now' % versions.get('latest_checked_at')
    return text


def run_health_summary(health):
    """One line for `tofi status`: runs by status and tool time-outs for the window."""
    if not health:
        return None
    by_status = health.get('by_status') or {}
    labels = [('done', 'completed'), ('failed', 'failed'), ('cancelled', 'cancelled'),
              ('interrupted', 'interrupted'), ('running', 'running'), ('waiting', 'waiting'), ('queued', 'queued')]
    parts = ['%d %s' % (by_status[key], label) for key, label in labels if by_status.get(key)]
    if health.get('tool_timeouts'):
        parts.append(plural(health['tool_timeouts'], 'tool timeout'))
    return '%s · last %d days' % (' · '.join(parts) if parts else 'none', health.get('days') or RUN_HEALTH_DAYS)


def render_status(result, mode=None, verbose=False, access=None):
    """`tofi status` for people (`--json` prints `result` itself). `mode` is color_mode()."""
    if not result.get('phase'):
        return 'TOFI is not installed. Install it with install.sh.\n'
    paint = Paint(mode)
    access = access or {}
    versions = result.get('versions') or {}
    healthy = bool(result.get('healthy'))
    service_states = result.get('services')
    starting = isinstance(service_states, dict) and 'starting' in service_states.values()
    if starting:
        health_text = paint.color('peach', 'starting')
    elif healthy:
        health_text = paint.color('teal', 'healthy')
    else:
        health_text = paint.color('peach', 'NOT healthy')
    header = 'tofi %s · %s' % (result.get('version') or '?', health_text)
    if result['phase'] != 'installed':
        header += ' · ' + result['phase']
    lines = [paint.bold(header), '']

    def row(label, value):
        lines.append('  ' + (paint.dim(label.ljust(13)) if label else ' ' * 13) + value)

    urls = result.get('urls') or []
    for index, url in enumerate(urls):
        row('Open' if index == 0 else '', url)
    if urls and access.get('local_only'):
        port = access.get('port') or str(DEFAULT_PORT)
        row('', paint.dim('this server only · ssh -L %s:127.0.0.1:%s <user>@<server>' % (port, port)))
    if result.get('certificate_sha256') and not access.get('domain'):
        fingerprint = result['certificate_sha256']
        row('Certificate', 'SHA256 ' + (fingerprint if verbose else short_fingerprint(fingerprint)))
    if result.get('setup_key_pending'):
        row('Admin', 'not created yet · sudo tofi setup-secret')
    if versions or result.get('version'):
        row('Version', version_summary(result, verbose))
    if 'auto_update' in result:
        if result['auto_update']:
            text = 'automatic (fix releases)'
            if result.get('next_check'):
                text += ' · next check ' + result['next_check']
            row('Updates', text)
        else:
            row('Updates', 'manual')
    services = result.get('services')
    if isinstance(services, dict):
        row('Services', ' · '.join('%s %s' % item for item in sorted(services.items())) or 'none running')
    elif services:
        row('Services', services)
    runner = result.get('runner')
    if runner == 'none':
        row('Runner', paint.dim('not installed · local connections run on the computer'))
    elif runner:
        row('Runner', paint.color('teal', 'healthy') if runner == 'healthy'
            else paint.color('peach', '%s · sudo tofi logs runner' % runner))
    row_runs = run_health_summary(result.get('run_health'))
    if row_runs:
        row('Runs', row_runs)
    no_computer = result.get('computer_backend') == 'none'
    if verbose and versions:
        row('App', short_image(versions.get('app_image')))
        if not no_computer:
            row('Worker', short_image(versions.get('worker_image')))
            row('Guest', versions.get('guest') or '-')
    if result.get('transaction'):
        row('Pending', '%(kind)s at step %(step)s · sudo tofi install resumes it' % result['transaction'])
    if result.get('last_error'):
        row('Last error', result['last_error'].get('message'))
    attempt = result.get('last_update_attempt') or {}
    if result.get('last_update_failure'):
        failure = result['last_update_failure']
        how = ' (automatic)' if attempt.get('trigger') == 'auto' and attempt.get('to') == failure.get('version') else ''
        row('Update', 'to %s rejected%s at %s: %s · previous version restored'
            % (failure.get('version'), how, failure.get('at'), failure.get('message')))
    elif attempt.get('result') == 'failed' and result['phase'] == 'installed':
        row('Update', 'to %s failed%s at %s: %s · %s' % (
            attempt.get('to'), ' (automatic)' if attempt.get('trigger') == 'auto' else '', attempt.get('at'),
            attempt.get('error') or attempt.get('reason'), 'nothing was changed'
            if 'rollback failed' not in str(attempt.get('error')) else 'rollback did not finish'))
    if result.get('recover'):
        target = result['recover'].get('rollback_to') or 'the previous version'
        row('Run', 'sudo tofi install   finishes the rollback to %s; your data and backups are kept '
            '(sudo tofi backups)' % target)
    computers = result.get('computers')
    if no_computer:
        row('Computer', 'none · server and web only · ' + paint.bold('sudo tofi computer add kvm') + ' adds one')
    elif 'computers_error' in result:
        row('Computers', 'unknown (%s)' % result['computers_error'])
    elif computers is not None:
        if not computers:
            row('Computers', 'none yet')
        else:
            counts = {}
            for item in computers:
                counts[item['state']] = counts.get(item['state'], 0) + 1
            order = [s for s in ('running', 'hibernated') if s in counts] + sorted(
                s for s in counts if s not in ('running', 'hibernated'))
            row('Computers', ' · '.join(['%d' % len(computers)] + ['%d %s' % (counts[s], s) for s in order]))
            lines.append('')
            lines += render_computers(computers, versions.get('guest'))
    lines += ['', '  ' + paint.dim('tofi update · tofi logs · %s · tofi doctor'
                                  % ('tofi computer add kvm' if no_computer else 'tofi computers'))]
    return '\n'.join(lines) + '\n'


# --------------------------------------------------------------------------
# Versions: what is installed and what `tofi update` would install

# Remember the latest published release this long; retry a failed check sooner.
LATEST_TTL_SECONDS = 6 * 3600
LATEST_RETRY_SECONDS = 600
UPDATE_AVAILABLE_EXIT = 10


def iso_now():
    return time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())


def version_key(version):
    """Sort key for vMAJOR.MINOR.PATCH[-rc.N]; a final release sorts after its rcs."""
    match = re.match(r'^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-rc\.([0-9]+))?$', str(version or ''))
    if not match:
        return None
    major, minor, patch, rc = match.groups()
    return (int(major), int(minor), int(patch), rc is None, int(rc or 0))


def is_newer(candidate, installed):
    new, old = version_key(candidate), version_key(installed)
    return new is not None and old is not None and new > old


def short_image(reference):
    """ghcr.io/x/tofi@sha256:<64 hex> -> ghcr.io/x/tofi@sha256:<12 hex>."""
    name, separator, digest = str(reference or '').partition('@sha256:')
    return '%s@sha256:%s' % (name, digest[:12]) if separator else str(reference or '-')


def short_digest(reference):
    """repo@sha256:<64 hex> -> sha256:<12 hex> (the repository is fixed per role)."""
    digest = str(reference or '').partition('@sha256:')[2]
    return 'sha256:' + digest[:12] if digest else str(reference or '-')


def latest_release(refresh=False, timeout=5):
    """The release `tofi update` would install, cached; never raises.

    Returns {'version', 'guest', 'checked_at', 'error'}. Offline, the last
    successful answer is kept (with the error); with none, version is None.
    """
    try:
        cached = json.loads(P.latest_cache.read_text())
        if not isinstance(cached, dict):
            cached = None
    except (OSError, ValueError):
        cached = None
    if cached and not refresh:
        age = time.time() - float(cached.get('attempted') or 0)
        if age < (LATEST_RETRY_SECONDS if cached.get('error') else LATEST_TTL_SECONDS):
            return cached
    try:
        manifest = fetch_manifest(timeout=timeout)
        entry = {'version': manifest['version'], 'guest': manifest['guest']['version'],
                 'checked_at': iso_now(), 'error': None}
    except HostError as error:
        entry = dict(cached or {'version': None, 'guest': None, 'checked_at': None})
        entry['error'] = str(error)[:300]
    entry['attempted'] = time.time()
    try:
        write_json(P.latest_cache, entry, 0o644)
    except OSError:
        pass
    return entry


def remember_latest(manifest):
    """Record a freshly downloaded latest manifest (from `update --check`)."""
    entry = {'version': manifest['version'], 'guest': manifest['guest']['version'],
             'checked_at': iso_now(), 'error': None, 'attempted': time.time()}
    try:
        write_json(P.latest_cache, entry, 0o644)
    except OSError:
        pass


def versions_report(env, refresh=False):
    latest = latest_release(refresh=refresh)
    installed = env.get('TOFI_VERSION')
    return {'installed': installed, 'latest': latest.get('version'),
            'latest_checked_at': latest.get('checked_at'), 'latest_error': latest.get('error'),
            'update_available': is_newer(latest.get('version'), installed),
            'app_image': env.get('TOFI_APP_IMAGE'), 'worker_image': env.get('TOFI_WORKER_IMAGE'),
            'guest': env.get('TOFI_GUEST_VERSION')}


def check_update(version=None, manifest_path=None):
    """`tofi update --check`: what an update would change; touches nothing."""
    require_root()
    state = load_state()
    if state is None:
        raise HostError('TOFI is not installed; run install.sh.')
    if state['phase'] != 'installed':
        raise HostError('TOFI is in phase %s; run `sudo tofi install` to resume first.' % state['phase'])
    env = read_env()
    if manifest_path:
        manifest = load_manifest_file(manifest_path)
    else:
        manifest = fetch_manifest(version)
        if not version:
            remember_latest(manifest)
    components = [('version', env.get('TOFI_VERSION'), manifest['version']),
                  ('app', env.get('TOFI_APP_IMAGE'), manifest['images']['app'])]
    if has_computer(env):
        components += [('worker', env.get('TOFI_WORKER_IMAGE'), manifest['images']['worker']),
                       ('guest', env.get('TOFI_GUEST_VERSION'), manifest['guest']['version'])]
    new_runner = manifest_image(manifest, 'runner')
    if env.get('TOFI_RUNNER_IMAGE') or new_runner:
        components.append(('runner', env.get('TOFI_RUNNER_IMAGE'), new_runner))
    if env.get('TOFI_DOMAIN'):
        components.append(('caddy', env.get('TOFI_CADDY_IMAGE'), manifest['images']['caddy']))
    try:
        installed_schema = image_label(env['TOFI_APP_IMAGE'], DATA_SCHEMA_LABEL)
    except (OSError, ValueError, IndexError, KeyError, subprocess.SubprocessError):
        installed_schema = None
    schema = {'installed': installed_schema, 'available': manifest['data_schema'],
              'compatible': None if installed_schema is None else installed_schema == manifest['data_schema']}
    report = {'installed': env.get('TOFI_VERSION'), 'available': manifest['version'],
              'notes_url': manifest_notes_url(manifest),
              'update_available': manifest['version'] != env.get('TOFI_VERSION'),
              'downgrade': is_newer(env.get('TOFI_VERSION'), manifest['version']),
              'components': [{'name': name, 'installed': old, 'available': new, 'changes': old != new}
                             for name, old, new in components],
              'data_schema': schema,
              'apply': 'sudo tofi update' + (' --version ' + manifest['version'] if version else
                                             ' --manifest ' + str(manifest_path) if manifest_path else '')}
    if not schema['compatible'] and report['update_available']:
        report['apply'] += ' --allow-schema-change'
    report['computer_backend'] = backend_of(env)
    report['adds_runner'] = bool(new_runner) and not env.get('TOFI_RUNNER_IMAGE')
    report['runner'] = runner_container_state() if env.get('TOFI_RUNNER_IMAGE') else 'none'
    if not has_computer(env):
        report['computers'] = []
        return report
    try:
        report['computers'] = computers_report(env.get('TOFI_GUEST_VERSION'), busy=True)
    except HostError as error:
        report['computers'] = None
        report['computers_error'] = str(error)
    return report


def render_update_check(report):
    state = 'up to date' if not report['update_available'] else (
        'older release' if report['downgrade'] else 'update available')
    lines = ['Installed    %s' % report['installed'], 'Available    %s  (%s)' % (report['available'], state)]
    if report.get('notes_url'):
        lines.append('Notes        %s' % report['notes_url'])
    lines.append('')
    labels = {'version': 'Host bundle', 'app': 'App image', 'worker': 'Worker image',
              'guest': 'Guest release', 'runner': 'Runner image', 'caddy': 'Caddy image'}
    table = []
    for item in report['components']:
        show = short_digest if item['name'] in ('app', 'worker', 'runner', 'caddy') else str
        table.append([labels[item['name']], show(item['installed']), show(item['available']),
                      'changes' if item['changes'] else 'same'])
    schema = report['data_schema']
    verdict = {True: 'compatible', False: 'CHANGES: back up /var/lib/tofi; needs --allow-schema-change',
               None: 'installed schema unknown'}[schema['compatible']]
    table.append(['Data schema', schema['installed'] or '?', schema['available'], verdict])
    lines += format_table(['COMPONENT', 'INSTALLED', 'AVAILABLE', ''], table)
    if report.get('runner') not in (None, 'none'):
        lines += ['', 'Runner       %s now' % report['runner']]
    if report['update_available']:
        lines.append('')
        if report.get('adds_runner'):
            lines += ['Runner       adds the local MCP Runner container: Gmail and other local connections move into it',
                      '             and need iptables on this host to fence it; if it does not start, the update rolls back']
        if report.get('computer_backend') == 'none':
            lines.append('Computer     none: nothing to restart or download for it')
        elif report.get('computers') is None:
            lines.append('Computers    unknown (%s)' % report.get('computers_error', 'Worker not reachable'))
        else:
            lines += update_impact_lines(report['computers'], report['components'])
        lines += ['', 'Apply with: ' + report['apply']]
    return '\n'.join(lines) + '\n'


def update_impact_lines(computers, components):
    """What an update does to the account computers, in plain sentences."""
    guest = next((c for c in components if c['name'] == 'guest'), None)
    running = [c for c in computers if c['running']]
    hibernated = [c for c in computers if c['state'] == 'hibernated']
    lines = ['Computers    %d total: %d running, %d hibernated' % (len(computers), len(running), len(hibernated))]
    if running:
        lines.append('  The update restarts the Worker: running computers are hibernated first.')
    for row in running:
        if row['busy'] != []:
            lines.append('  %s is busy (%s): its current task is interrupted.'
                         % (row['short'], ', '.join(row['busy'] or ['state unknown'])))
    if guest and guest['changes'] and (running or hibernated):
        lines.append('  The Guest changes to %s: each of them cold-boots on it at its next use '
                     '(open pages are not kept; the workspace disk is).' % guest['available'])
    return lines


# --------------------------------------------------------------------------
# Account computers: read through the Worker broker and each control socket

# Runs as the App uid (10001), the only identity the broker and the computer
# control sockets accept, like BROKER_PROBE. Each call is independent: an
# unreachable computer is reported, never fatal. Only sockets under /run/tofi.
CONTROL_PROBE = r"""import http.client, json, socket, struct, sys
replies = []
for call in json.loads(sys.argv[1]):
    try:
        if not call['socket'].startswith('/run/tofi/') or '..' in call['socket']:
            raise ValueError('socket outside /run/tofi')
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(call.get('timeout', 10))
            connection.connect(call['socket'])
            pid, uid, gid = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if uid != 0 or (call.get('peer_pid') and pid != call['peer_pid']):
                raise ValueError('socket peer is not the Worker')
            client = http.client.HTTPConnection('tofi-worker', timeout=call.get('timeout', 10))
            client.sock = connection
            body = json.dumps(call['body']) if 'body' in call else None
            client.request(call['method'], call['path'], body=body,
                           headers={'Content-Type': 'application/json'} if body is not None else {})
            response = client.getresponse()
            data = response.read(4194305)
            if len(data) > 4194304:
                raise ValueError('reply too large')
            replies.append({'status': response.status, 'body': json.loads(data)})
    except (FileNotFoundError, ConnectionRefusedError):
        replies.append({'error': 'not running'})
    except (OSError, ValueError, KeyError, http.client.HTTPException) as error:
        replies.append({'error': (type(error).__name__ + ': ' + str(error))[:200]})
print(json.dumps(replies))
"""

# Manager states as `tofi computers` shows them.
COMPUTER_STATES = {'ready': 'running', 'starting': 'starting', 'resuming': 'starting',
                   'restarting': 'restarting', 'purging': 'restarting', 'hibernating': 'hibernating',
                   'hibernated': 'hibernated', 'stopped': 'stopped', 'error': 'error'}
RUNNING_STATES = {'running', 'starting', 'restarting', 'hibernating', 'unresponsive'}


def worker_pid():
    workers = [c for c in project_containers() if service_of(c) == 'worker']
    if len(workers) != 1 or not workers[0]['State'].get('Running'):
        raise HostError('The Worker is not running; start TOFI with `sudo tofi start`.')
    return int(workers[0]['State']['Pid'])


def control_calls(calls):
    """Run CONTROL_PROBE as uid 10001; one reply per call."""
    budget = 15 + sum(call.get('timeout', 10) for call in calls)
    try:
        result = run([sys.executable, '-I', '-c', CONTROL_PROBE, json.dumps(calls)],
                     user=APP_UID, group=APP_UID, extra_groups=[], cwd='/', timeout=budget)
        replies = json.loads(result.stdout)
    except subprocess.CalledProcessError as error:
        raise HostError('The computer probe failed: %s' % (error.stderr or '').strip()[-300:]) from error
    except subprocess.TimeoutExpired as error:
        raise HostError('The computer probe did not finish within %d s.' % budget) from error
    except ValueError as error:
        raise HostError('The computer probe returned an unreadable reply.') from error
    if not isinstance(replies, list) or len(replies) != len(calls):
        raise HostError('The computer probe returned an unexpected reply.')
    return replies


def broker_request(body, timeout=60):
    """One request to the Worker broker (peer verified as the Worker process)."""
    reply = control_calls([{'socket': RUN_REAL + '/broker.sock', 'peer_pid': worker_pid(), 'method': 'POST',
                            'path': '/v1/accounts', 'body': body, 'timeout': timeout}])[0]
    if 'error' in reply:
        raise HostError('The Worker broker is unreachable (%s); see `tofi logs worker`.' % reply['error'])
    value = reply.get('body')
    if reply.get('status') != 200 or not isinstance(value, dict):
        detail = value.get('error') if isinstance(value, dict) else None
        raise HostError('The Worker refused %s: %s' % (body.get('op'), detail or 'HTTP %s' % reply.get('status')))
    return value


def control_socket(account):
    return '%s/accounts/%s/control.sock' % (RUN_REAL, account)


def reply_body(reply):
    if isinstance(reply, dict) and reply.get('status') == 200 and isinstance(reply.get('body'), dict):
        return reply['body']
    return None


def computers_report(installed_guest, busy=False):
    """One row per account computer (see computer_row).

    Raises HostError when the Worker cannot be asked; a single computer
    that does not answer only blanks its own fields.
    """
    inventory = broker_request({'op': 'computers'})
    rows = inventory.get('computers')
    if not isinstance(rows, list):
        raise HostError('The Worker returned no computer list; is it older than this tofi command?')
    installed_guest = installed_guest or inventory.get('release')
    paths = ['/v1/info', '/v1/resources', '/v1/health'] + (['/v1/busy'] if busy else [])
    calls, owners = [], []
    for index, row in enumerate(rows):
        if row.get('running') is False:
            continue  # no manager process: nothing to ask
        for path in paths:
            calls.append({'socket': control_socket(row['account_id']), 'method': 'GET', 'path': path,
                          'timeout': 45 if path == '/v1/busy' else 10})
            owners.append((index, path))
    replies = control_calls(calls) if calls else []
    answers = {}
    for (index, path), reply in zip(owners, replies):
        answers.setdefault(index, {})[path] = reply_body(reply)
    report = [computer_row(row, answers.get(index, {}), installed_guest, busy) for index, row in enumerate(rows)]
    if busy:
        for entry in report:
            waits = command_waits(entry['account_id']) if entry['running'] else 0
            if waits:
                # The guest cannot see these: a Bot waits on a command, kept in the App database.
                entry['busy'] = (entry['busy'] or []) + ['%s a Bot waits on' % plural(waits, 'command')]
    return report


def computer_row(row, answers, installed_guest, busy=False):
    """Join the broker's inventory with what the computer's manager says.

    release: the Guest a running computer runs, or the one a hibernated
    computer's snapshot was taken with; None for a stopped computer (its
    next start uses the installed Guest). busy: [] idle, a list of reasons,
    or None when a running computer could not be asked.
    """
    info = answers.get('/v1/info')
    health = answers.get('/v1/health') or {}
    snapshot = row.get('snapshot') or None
    release = None
    if info:
        state = COMPUTER_STATES.get(info.get('state'), str(info.get('state')))
        if state == 'running' and health.get('guest') == 'unresponsive':
            state = 'unresponsive'
        release = info.get('release') or row.get('config_release')
    elif snapshot:
        state = 'hibernated'
    else:
        state = 'stopped'
    if state == 'hibernated' and snapshot:
        release = snapshot.get('release')
    elif state in ('stopped', 'error'):
        release = None
    if row.get('state') == 'disabled':
        state = 'disabled'
    running = state in RUNNING_STATES
    memory = ((answers.get('/v1/resources') or {}).get('memory') or {}).get('host_rss_mib')
    report = answers.get('/v1/busy')
    return {
        'account_id': row['account_id'], 'short': row['account_id'][:8], 'state': state,
        'running': running, 'release': release,
        'upgrade_pending': bool(release and installed_guest and release != installed_guest),
        'memory_rss_mib': memory if running else None,
        'last_wake': (info or {}).get('last_wake'),
        'hibernated_at': ((info or {}).get('hibernated_at') or (snapshot or {}).get('created_at') or None)
        if state == 'hibernated' else None,
        'error': (info or {}).get('error') or None,
        'busy': (report.get('busy') if report else None) if busy and running else [],
    }


def describe_wake(wake):
    if not isinstance(wake, dict) or 'kind' not in wake:
        return '-'
    text = '%s %ss' % ('restore' if wake['kind'] == 'restore' else 'cold boot', wake.get('seconds', '?'))
    if wake.get('fallback_reason'):
        text += ' (%s)' % wake['fallback_reason']
    return text


def format_table(headers, rows):
    widths = [max(len(str(value)) for value in column) for column in zip(headers, *rows)]
    return ['  ' + '  '.join(str(value).ljust(width) for value, width in zip(line, widths)).rstrip()
            for line in [headers] + rows]


def render_computers(rows, installed_guest):
    if not rows:
        return ['  No account computers yet.']
    table = [[row['short'], row['state'], row['release'] or '-', 'pending' if row['upgrade_pending'] else '-',
              '%d MiB' % row['memory_rss_mib'] if row['memory_rss_mib'] is not None else '-',
              describe_wake(row['last_wake'])] for row in rows]
    lines = format_table(['ACCOUNT', 'STATE', 'GUEST', 'UPGRADE', 'MEMORY', 'LAST WAKE'], table)
    pending = [row for row in rows if row['upgrade_pending']]
    if pending:
        lines.append('')
        lines.append('  %d computer%s an older Guest than %s; a hibernated one cold-boots on %s at its next use.'
                     % (len(pending), ' is on' if len(pending) == 1 else 's are on', installed_guest, installed_guest))
        lines.append('  Switch now: sudo tofi computers upgrade --all')
    return lines


def select_computers(rows, account=None):
    if not account:
        return rows
    matches = [row for row in rows if row['account_id'] == account]
    if not matches and len(account) >= 8:
        matches = [row for row in rows if row['account_id'].startswith(account)]
    if not matches:
        raise HostError('No computer matches %r; `sudo tofi computers` lists them.' % account)
    if len(matches) > 1:
        raise HostError('%r matches several computers; give more of the id.' % account)
    return matches


def confirm_force(busy, confirm=None):
    """Ask on the terminal before interrupting busy computers; without a tty --force decides."""
    if confirm is None:
        try:
            with open('/dev/tty') as tty:
                print('%d busy computer%s will be restarted and running tasks interrupted: %s'
                      % (len(busy), '' if len(busy) == 1 else 's',
                         ', '.join('%s (%s)' % (row['short'], ', '.join(row['busy'] or ['state unknown']))
                                   for row in busy)))
                print('Type "yes" to continue: ', end='', flush=True)
                confirm = tty.readline().strip()
        except OSError:
            confirm = 'yes'
    if confirm != 'yes':
        raise HostError('Upgrade cancelled; nothing was changed.')


def upgrade_computers(account=None, all_computers=False, force=False, confirm=None):
    """`tofi computers upgrade`: move computers off older Guest releases.

    Idle, hibernated and stopped computers switch now; busy ones (Bot run,
    viewer, human control, terminal job, or not answering) are deferred
    unless --force. Nothing is started: the next start boots the installed
    Guest. Then Guest releases no computer references are removed. Holds the
    lifecycle lock; the installation stays in phase "installed" (services
    keep running) and the outcome is journalled as last_computers_upgrade.
    """
    if bool(account) == bool(all_computers):
        raise HostError('Choose the computers: --all, or --account <id>.')
    with lifecycle_lock():
        state = load_state()
        if state is None or state['phase'] != 'installed':
            raise HostError('Computers can be upgraded only in phase installed; '
                            'run `sudo tofi install` to resume first.')
        installed = read_env()['TOFI_GUEST_VERSION']
        targets = select_computers(computers_report(installed, busy=True), account)
        busy = [row for row in targets if row['upgrade_pending'] and row['running'] and row['busy'] != []]
        if busy and force:
            confirm_force(busy, confirm)
        results = []
        for row in targets:
            entry = {'account_id': row['account_id'], 'short': row['short'], 'state': row['state'],
                     'from': row['release'], 'to': installed}
            if not row['upgrade_pending']:
                entry.update(result='current', note='' if row['release'] else 'next start boots ' + installed)
            elif row in busy and not force:
                entry.update(result='deferred', note='busy (%s); retry when idle or use --force'
                             % ', '.join(row['busy'] or ['state unknown']))
            else:
                try:
                    reply = broker_request({'op': 'upgrade', 'account_id': row['account_id']}, timeout=300)
                    done = (['stopped'] if reply.get('stopped') else []) + (
                        ['snapshot discarded'] if reply.get('discarded_snapshot') else [])
                    entry.update(result='upgraded', note='; '.join(done + ['next start boots ' + installed]))
                except HostError as error:
                    entry.update(result='failed', note=str(error))
            results.append(entry)
        # A computer left on its release (deferred, failed) still uses it.
        removed = prune_guests([installed] + [item['from'] for item in results
                                              if item['result'] in ('deferred', 'failed') and item['from']])
        state['last_computers_upgrade'] = {
            'at': iso_now(), 'guest': installed, 'removed_releases': removed,
            'results': [{'account_id': item['account_id'], 'result': item['result']} for item in results]}
        save_state(state)
        return {'guest': installed, 'results': results, 'removed_releases': removed}


def render_upgrade(report):
    if not report['results']:
        return '  No account computers yet.\n'
    table = [[r['short'], r['state'], r['from'] or '-', r['to'], r['result'], r['note']] for r in report['results']]
    lines = format_table(['ACCOUNT', 'STATE', 'FROM', 'TO', 'RESULT', 'NOTE'], table)
    counts = {}
    for item in report['results']:
        counts[item['result']] = counts.get(item['result'], 0) + 1
    lines += ['', '  ' + ', '.join('%d %s' % (n, name) for name, n in sorted(counts.items()))]
    if report['removed_releases']:
        lines.append('  Removed unused Guest releases: ' + ', '.join(report['removed_releases']))
    return '\n'.join(lines) + '\n'


def upgrade_exit_code(report):
    """0 every chosen computer is on the installed Guest; 2 some deferred; 1 a failure."""
    results = {item['result'] for item in report['results']}
    if 'failed' in results:
        return 1
    return 2 if 'deferred' in results else 0


# --------------------------------------------------------------------------
# Guest release retention


def guest_references():
    """Guest releases that hibernated computers' snapshots were taken with.

    Read from the host files, so pruning never depends on the Worker being
    up. Such a release is kept: rolling back to it restores those computers
    with their open pages.
    """
    references = set()
    root = P.worker_state
    for entry in sorted(root.iterdir()) if root.is_dir() and not root.is_symlink() else []:
        if entry.is_symlink() or not entry.is_dir():
            continue
        snapshot = snapshot_release(entry)
        if snapshot:
            references.add(snapshot[0])
    return references


def prune_guests(keep):
    """Remove Guest releases outside `keep` that no computer references."""
    keep = set(keep) | guest_references()
    removed = []
    for entry in sorted(P.guest.iterdir()) if P.guest.is_dir() else []:
        if entry.name.startswith('.') or entry.name in keep:
            continue
        remove_tree(entry)
        removed.append(entry.name)
    return removed


# --------------------------------------------------------------------------
# Talking to the running App (hot attach and detach, computer-decoupling S6)
#
# `tofi computer add|remove` ask the App to attach or detach every account's
# computer instead of restarting it. The App answers /api/host/computer/* only to
# the token in HOST_CONTROL_TOKEN_NAME inside its data directory, on its loopback
# port; the Caddyfile does not proxy that path. A server that cannot do it (an
# older release has no such route, the App was started without the Worker's
# sockets, the call fails) makes the caller fall back to restarting the App.

HOST_CONTROL_TOKEN_NAME = 'host-control.token'
HOST_CONTROL_ROUTE = '/api/host/computer/'


class HostControlUnavailable(Exception):
    """The running App cannot do this without a restart (or did not answer)."""

    def __init__(self, reason, status=None):
        super().__init__(reason)
        self.status = status


def ensure_host_control_token():
    """The bearer token for the App's host routes; created (0600, App uid) on first use."""
    path = P.data / HOST_CONTROL_TOKEN_NAME
    try:
        token = path.read_text().strip()
        if len(token) >= 32:
            return token
    except FileNotFoundError:
        pass
    token = secrets.token_hex(32)
    temporary = path.with_name(path.name + '.tmp')
    temporary.unlink(missing_ok=True)
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(token + '\n')
    if os.geteuid() == 0:
        os.chown(str(temporary), APP_UID, APP_UID)
    os.replace(str(temporary), str(path))
    return token


def host_control(env, operation, body=None, timeout=60):
    """Call the App's /api/host/computer/<operation>; return the JSON answer on HTTP 200.
    Anything else raises HostControlUnavailable (with the HTTP status when there was one)."""
    try:
        token = ensure_host_control_token()
    except OSError as error:
        raise HostControlUnavailable('the host token could not be written (%s)' % error)
    scheme = app_scheme(env)
    url = '%s://127.0.0.1:%s%s%s' % (scheme, env.get('TOFI_HTTP_PORT') or DEFAULT_PORT, HOST_CONTROL_ROUTE, operation)
    handlers = [urllib.request.ProxyHandler({})]
    if scheme == 'https':
        # Loopback call to our own self-signed certificate: no verification, the bearer token is the credential.
        context = ssl.create_default_context()
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        handlers.append(urllib.request.HTTPSHandler(context=context))
    opener = urllib.request.build_opener(*handlers)
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(url, data=data, method='GET' if operation == 'state' else 'POST',
                                     headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    try:
        with opener.open(request, timeout=timeout) as response:
            status, raw = response.status, response.read(1 << 16)
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read(1 << 16)
    except (OSError, ValueError) as error:
        raise HostControlUnavailable('the App did not answer (%s)' % error)
    try:
        answer = json.loads(raw or b'{}')
    except ValueError:
        answer = {}
    if status != 200:
        raise HostControlUnavailable('the App answered %d %s' % (status, str(answer.get('code') or answer.get('error') or '')[:60]), status)
    return answer


def attach_computers_hot(env, quiet=False):
    """Attach every account's computer in the running App. True when it did, False when the App must be restarted."""
    try:
        answer = host_control(env, 'attach', timeout=600)
    except HostControlUnavailable as error:
        if not quiet:
            say('The running App cannot take the computer without a restart (%s); restarting it.' % error)
        return False
    if not quiet:
        say('The running App attached the computer (%s attached, %s already attached); no restart.'
            % (answer.get('attached', 0), answer.get('already_attached', 0)))
    return True


def detach_computers_hot(env, wait_seconds=60):
    """Detach every account's computer in the running App. True when it did, False when the App must be restarted."""
    try:
        answer = host_control(env, 'detach', {'wait_seconds': int(wait_seconds)}, timeout=int(wait_seconds) + 180)
    except HostControlUnavailable as error:
        say('The running App cannot drop the computer without a restart (%s); restarting it.' % error)
        return False
    say('The running App detached the computer (%s account%s, %s file%s saved from the outbox); no restart.'
        % (answer.get('detached', 0), '' if answer.get('detached', 0) == 1 else 's',
           answer.get('outbox_synced', 0), '' if answer.get('outbox_synced', 0) == 1 else 's'))
    return True


# --------------------------------------------------------------------------
# Adding and removing the computer (`tofi computer add kvm` / `remove`)


def ensure_computer_tools():
    """Install the host tools a computer needs (an install without one skipped them)."""
    missing = [tool for tool in COMPUTER_TOOLS if not shutil.which(tool)]
    if not missing:
        return
    if not shutil.which('apt-get'):
        raise HostError('A computer needs %s on this host; install %s and retry.'
                        % (', '.join(missing), ' '.join(COMPUTER_APT_PACKAGES)))
    say('Installing host tools for the computer (%s).' % ' '.join(COMPUTER_APT_PACKAGES))
    run(['env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', '-qq', '-o', 'DPkg::Lock::Timeout=300', 'install', '-y',
         '--no-install-recommends'] + COMPUTER_APT_PACKAGES, timeout=1800)
    still = [tool for tool in COMPUTER_TOOLS if not shutil.which(tool)]
    if still:
        raise HostError('Still missing host tools after installing %s: %s.' % (' '.join(COMPUTER_APT_PACKAGES), ', '.join(still)))


def computer_add_preflight():
    """Everything a computer needs that a server-and-web install did not check.
    Raises before anything changes; returns warnings."""
    problems = computer_host_problems()
    if problems:
        raise HostError('This host cannot run a computer, and nothing was changed. %s' % ' '.join(problems))
    ensure_computer_tools()
    try:
        info = json.loads(run(['docker', 'info', '--format', '{{json .}}']).stdout)
    except (subprocess.SubprocessError, ValueError) as error:
        raise HostError('Docker is not running; start it with `systemctl start docker`.') from error
    if str(info.get('CgroupVersion')) != '2':
        raise HostError('Docker must use cgroup v2 (found %s).' % info.get('CgroupVersion'))
    if not any('apparmor' in item for item in info.get('SecurityOptions') or []):
        raise HostError('Docker must run with AppArmor enabled (security options: %s).' % info.get('SecurityOptions'))
    cpus = os.cpu_count() or 0
    if cpus < MIN_CPUS:
        raise HostError('A computer needs at least %d vCPUs; this host has %d.' % (MIN_CPUS, cpus))
    memory = meminfo_mib()['MemTotal']
    if memory < MIN_MEMORY_MIB:
        raise HostError('A computer needs at least 4 GiB of RAM; this host has %d MiB.' % memory)
    free = free_disk_bytes()
    if free < FIRST_ACCOUNT_BYTES:
        raise HostError('The first computer needs 16 GiB free under /var/lib (8 GiB workspace + 8 GiB internal); '
                        '%.1f GiB is free.' % (free / GIB))
    return computer_memory_warnings(memory)


def stop_worker(env):
    """Stop and remove the Worker container (it hibernates the computers first)."""
    compose('stop', 'worker', env=env, timeout=900)
    compose('rm', '-f', 'worker', env=env)


def computer_add(backend='kvm', wait=0, force=False):
    """`tofi computer add kvm`: give a server-and-web install a KVM computer.

    Idempotent. Checks the host, fetches the Guest release of the installed
    version, writes worker.json and the AppArmor profile, switches tofi.env to
    backend kvm, starts the Worker and has the App attach the computers (a restart
    only for an App that cannot do it live: an older release, or one started
    without the Worker's sockets). A failure puts tofi.env back and restarts the
    App as it was."""
    if backend != 'kvm':
        raise HostError('Only kvm (Firecracker) computers exist today: tofi computer add kvm.')
    require_root()
    with lifecycle_lock():
        state = load_state()
        if state is None:
            raise HostError('TOFI is not installed; run install.sh.')
        if state['phase'] != 'installed':
            raise HostError('TOFI is in phase %s; run `sudo tofi install` to resume first.' % state['phase'])
        env = read_env()
        if has_computer(env):
            try:
                running = any(service_of(c) == 'worker' and c['State'].get('Running') for c in project_containers())
            except (subprocess.SubprocessError, ValueError, OSError):
                running = False
            if running:
                say('This server already has a computer (KVM); nothing to do. See `sudo tofi computers`.')
                return {'added': False, 'computer': 'kvm'}
            say('The computer is set up but its Worker is not running; finishing the setup.')
        for message in computer_add_preflight():
            warn(message)
        manifest = restore_manifest(env['TOFI_VERSION'])
        bundle = P.current.resolve() if P.current.exists() else ASSETS
        candidate = dict(env)
        candidate.update(computer_env('kvm', manifest, runtime_budgets()))
        format_env(candidate)
        wait_for_idle('adding the computer', wait, force)
        say('Adding a KVM computer to TOFI %s.' % env['TOFI_VERSION'])
        prepare_computer_directories()
        pull_images(candidate, manifest['data_schema'])
        fetch_guest(guest_for_host(manifest), bundle_manager(bundle))
        apply_computer_files(bundle, computer_config(candidate))
        write_env(candidate)
        try:
            # The Worker first. A running App that already carries the Worker's sockets attaches the
            # computer without a restart; any other App is restarted with the sockets in its environment.
            start_worker(candidate, first_install=True)
            if not attach_computers_hot(candidate):
                start_services(candidate, first_install=True)
                # Accounts whose computer an earlier hot remove detached stay detached across a restart.
                attach_computers_hot(candidate, quiet=True)
        except BaseException as error:
            write_env(env)
            try:
                stop_worker(candidate)
                start_services(env)
            except (HostError, OSError, subprocess.SubprocessError) as restore_error:
                warn('Could not restart TOFI as it was (%s); run `sudo tofi start`.' % restore_error)
            raise HostError('Adding the computer failed (%s); tofi.env is back to server and web only.' % error) from error
    say('Computer added (KVM, Firecracker). Each account gets one; open the web app to use it.')
    return {'added': True, 'computer': 'kvm'}


def computer_remove(purge=False, yes=False, wait=0, force=False, discard_unsaved_files=False):
    """`tofi computer remove`: detach the computers in the running App, stop the Worker, set backend none.
    An App that cannot detach live (an older release) is restarted instead.

    The computers' disks, state and the Guest release stay on disk (so
    `tofi computer add kvm` brings them back) unless `purge`. A purge refuses
    while a file exists only on a computer (not yet copied to the server file
    store), unless `discard_unsaved_files`."""
    require_root()
    with lifecycle_lock():
        state = load_state()
        if state is None:
            raise HostError('TOFI is not installed.')
        if state['phase'] != 'installed':
            raise HostError('TOFI is in phase %s; run `sudo tofi install` to resume first.' % state['phase'])
        env = read_env()
        leftovers = [path for path in (P.worker_state, P.guest) if path.is_dir() and any(path.iterdir())]
        if not has_computer(env) and not (purge and leftovers):
            say('This server has no computer (backend none); nothing to do.')
            return {'removed': False, 'computer': 'none'}
        unsaved, unreadable = pending_guest_copies()
        if purge and (unsaved or unreadable) and not discard_unsaved_files:
            if unreadable:
                raise HostError(
                    'Cannot tell whether files exist only on the computer: %s could not be read. Nothing was changed. '
                    'Fix that, or add --discard-unsaved-files to delete the disks anyway.' % ', '.join(unreadable))
            raise HostError(
                '%s only on the computer (not yet copied to the server). Purge would delete %s. Nothing was '
                'changed. Keep the computer running so the copy can finish, or add --discard-unsaved-files to delete '
                '%s anyway.' % (plural(unsaved, 'file') + (' exists' if unsaved == 1 else ' exist'),
                                'it' if unsaved == 1 else 'them', 'it' if unsaved == 1 else 'them'))
        if unsaved and not purge:
            warn('%s only on this computer and stays on its disk. %s cannot be opened until the computer is '
                 'attached again (`sudo tofi computer add kvm`).'
                 % (plural(unsaved, 'file') + (' exists' if unsaved == 1 else ' exist'), 'It' if unsaved == 1 else 'They'))
        if purge and discard_unsaved_files and unsaved:
            warn('Discarding %s that exist only on the computer.' % plural(unsaved, 'file'))
        if purge:
            env_names, key_names, unreadable_vaults = computer_configuration()
            say('Purging deletes everything that lives only on the computer:')
            for line in computer_loss_lines(env_names, key_names, unreadable_vaults, unsaved if discard_unsaved_files else 0):
                say('  - ' + line)
        if purge and not yes:
            if not sys.stdin.isatty():
                raise HostError('--purge deletes every computer disk and its state; add --yes to confirm.')
            if not ask_yes('Delete all computer disks, their state and the Guest release? This cannot be undone.'):
                raise HostError('Cancelled; nothing was changed.')
        if has_computer(env):
            wait_for_idle('removing the computer', wait, force)
            # The App detaches first (it drains calls and saves the outbox while the computers still
            # answer); then the Worker stops. An App that cannot do that live is restarted without computers.
            live = detach_computers_hot(env)
            candidate = dict(env)
            candidate.update(computer_env('none'))
            try:
                stop_worker(env)
                write_env(candidate)
                if not live:
                    start_services(candidate)
            except BaseException as error:
                write_env(env)
                try:
                    if live:
                        start_worker(env)
                        attach_computers_hot(env, quiet=True)
                    else:
                        start_services(env)
                except (HostError, OSError, subprocess.SubprocessError) as restore_error:
                    warn('Could not restart TOFI as it was (%s); run `sudo tofi start`.' % restore_error)
                raise HostError('Removing the computer failed (%s); tofi.env is back as it was.' % error) from error
        if purge:
            if P.apparmor_profile.exists():
                run(['apparmor_parser', '-R', str(P.apparmor_profile)], check=False)
            for path in (P.apparmor_profile, P.worker_json, P.seccomp):
                path.unlink(missing_ok=True)
            for path in (P.worker, P.guest):
                remove_tree(path)
            say('Computer removed; its disks, state and Guest release were deleted.')
        else:
            say('Computer removed. Computer disks and state are kept under /var/lib/tofi/worker; '
                '`sudo tofi computer add kvm` brings them back, `sudo tofi computer remove --purge` deletes them.')
    return {'removed': True, 'computer': 'none', 'purged': bool(purge)}


# --------------------------------------------------------------------------
# Diagnostics


def doctor():
    checks = []

    def check(name, function):
        try:
            detail = function()
            if isinstance(detail, Advisory):
                checks.append(('WARN', name, str(detail)))
            else:
                checks.append(('ok', name, detail or ''))
        except Exception as error:  # report every failing check, do not stop
            checks.append(('FAIL', name, str(error)))

    def kvm():
        if not stat.S_ISCHR(P.dev_kvm.stat().st_mode):
            raise HostError('/dev/kvm is not a character device')

    def apparmor_loaded():
        profiles = P.apparmor_profiles.read_text()
        if not any(line.split(' (')[0] == 'tofi-worker' for line in profiles.splitlines()):
            raise HostError('tofi-worker profile is not loaded; run `sudo tofi start`')

    def run_dir():
        info = P.run_dir.stat()
        if (info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode)) != (0, APP_UID, 0o750):
            raise HostError('/run/tofi must be root:10001 0750')

    def guest():
        env = read_env()
        manifest = json.loads((P.guest / env['TOFI_GUEST_VERSION'] / 'account-release.json').read_text())
        config = json.loads(P.worker_json.read_text())
        validate_release(config['release_dir'], str(bundle_manager(P.current.resolve())),
                         manifest['guest_binary_sha256'],
                         expected_manifest_sha256=config['release_manifest_sha256'])

    def images():
        env = read_env()
        inspect_image(env['TOFI_APP_IMAGE'], 'app')
        if has_computer(env):
            inspect_image(env['TOFI_WORKER_IMAGE'], 'worker')

    def disk():
        free = free_disk_bytes()
        if free < 2 * GIB:
            raise HostError('only %.1f GiB free under /var/lib' % (free / GIB))
        return '%.1f GiB free' % (free / GIB)

    def memory():
        try:
            config = json.loads(P.worker_json.read_text())
        except (OSError, ValueError):
            config = None
        warning, detail = memory_report(computer_memory_need_mib(config))
        return Advisory(warning) if warning else detail

    def certificate():
        if app_scheme(read_env()) != 'https':
            return 'served by Caddy'
        if not certificate_usable():
            raise HostError('missing or expires within 30 days; run `sudo tofi regenerate-cert`')
        return 'SHA256 ' + (certificate_fingerprint() or '?')

    def computer_backend():
        backend = backend_of(read_env())
        if backend == 'none':
            return 'none (server and web only; `sudo tofi computer add kvm` adds one)'
        if backend not in COMPUTER_BACKENDS:
            raise HostError('unknown TOFI_COMPUTER_BACKEND=%s in /etc/tofi/tofi.env' % backend)
        if COMPUTER_BACKENDS[backend]:
            raise HostError('%s is %s; set TOFI_COMPUTER_BACKEND=kvm' % (backend, COMPUTER_BACKENDS[backend]))
        return 'kvm (Firecracker)'

    try:
        computer = has_computer(read_env())
    except (OSError, HostError):
        computer = True
    host_card()
    check('Computer backend', computer_backend)
    # Without a computer nothing here needs KVM, cgroup v2 or AppArmor: a host
    # that lacks them is a healthy server-and-web install, not an error.
    if computer:
        check('KVM device', kvm)
        check('cgroup v2', lambda: P.cgroup_controllers.read_text() and None)
        check('AppArmor profile', apparmor_loaded)
    check('/run/tofi ownership', run_dir)
    if computer:
        check('Worker config', lambda: validate_config(json.loads(P.worker_json.read_text())) and None)
        check('Guest release', guest)
    check('Images', images)
    check('Disk', disk)
    if computer:
        check('Memory', memory)
    check('HTTPS certificate', certificate)
    check('App health', lambda: health(attempts=3) and None)
    for result, name, detail in checks:
        say('%-4s %-20s %s' % (result, name, detail))
    # Advisories are printed but never fail the doctor.
    return all(result in ('ok', 'WARN') for result, _, _ in checks)


def host_card(stream=None):
    """The installer's "This machine" card, for `tofi doctor`."""
    root = '' if str(P.root) == '/' else str(P.root)
    environ = {'TOFI_TEST_ROOT': root, 'TOFI_DEV_KVM': str(P.dev_kvm), 'TOFI_DEV_TUN': str(P.dev_tun)}
    try:
        port = int(read_env().get('TOFI_HTTP_PORT') or DEFAULT_PORT)
    except (OSError, ValueError, HostError):
        port = DEFAULT_PORT
    facts = tofi_tui.detect(tofi_tui.Host(environ), environ, port=port, lookup_public=False)
    ui = tofi_tui.UI(stream or sys.stdout)
    try:
        computer = backend_of(read_env())
    except (OSError, HostError):
        computer = 'kvm'
    ui.card('This machine', tofi_tui.machine_rows(facts, fresh=facts['phase'] is None, computer=computer))
    return facts


def version():
    info = {'host_bundle': (ASSETS / 'VERSION').read_text().strip() if (ASSETS / 'VERSION').exists() else 'source'}
    try:
        env = read_env()
        info.update(installed=env.get('TOFI_VERSION'), app_image=env.get('TOFI_APP_IMAGE'),
                    worker_image=env.get('TOFI_WORKER_IMAGE'), guest=env.get('TOFI_GUEST_VERSION'),
                    computer=backend_of(env))
    except OSError:
        pass
    return info


# --------------------------------------------------------------------------
# CLI


def parse_args(argv):
    parser = argparse.ArgumentParser(prog='tofi', description='Manage a self-hosted TOFI server.')
    sub = parser.add_subparsers(dest='command', required=True)
    install_parser = sub.add_parser('install', help='install, or resume an interrupted operation')
    install_parser.add_argument('--manifest', help='verified release manifest.json (install.sh passes this)')
    install_parser.add_argument('--domain')
    install_parser.add_argument('--behind-proxy', metavar='IP[,IP|CIDR...]',
                                help='a reverse proxy you already run sits in front: keep the self-signed HTTPS '
                                     'port and trust these proxy addresses for X-Forwarded-For')
    install_parser.add_argument('--email')
    install_parser.add_argument('--local-only', action='store_true',
                                help='listen on 127.0.0.1 only (still HTTPS); reach it with an SSH tunnel')
    install_parser.add_argument('--lan', action='store_true', help=argparse.SUPPRESS)
    install_parser.add_argument('--port', type=int)
    install_parser.add_argument('--computer', default=None,
                                help='none: server and web app only; kvm: also run account computers '
                                     '(Firecracker, needs /dev/kvm). Default: kvm when this host supports it, '
                                     'otherwise none. Add one later with `tofi computer add kvm`')
    install_parser.add_argument('--yes', action='store_true')
    auto = install_parser.add_mutually_exclusive_group()
    auto.add_argument('--auto-update', dest='auto_update', action='store_true', default=None,
                      help='install fix releases (same major.minor, newer patch) automatically overnight')
    auto.add_argument('--no-auto-update', dest='auto_update', action='store_false',
                      help='update only when you run `tofi update` (the default without --yes)')
    status_parser = sub.add_parser('status', help='versions, services, health and account computers')
    status_parser.add_argument('--json', action='store_true', help='machine-readable output')
    status_parser.add_argument('--verbose', '-v', action='store_true',
                               help='also image digests, the full certificate fingerprint and the update check')
    sub.add_parser('start')

    def add_guard_flags(parser):
        parser.add_argument('--wait', type=int, metavar='MINUTES',
                            help='wait this long for active Bot runs to finish (default 10 on a terminal, 0 otherwise)')
        parser.add_argument('--force', action='store_true', help='go ahead even if Bot runs are active (interrupts them)')

    stop_parser = sub.add_parser('stop', help='stop TOFI (data is kept)')
    add_guard_flags(stop_parser)
    update_parser = sub.add_parser('update')
    update_parser.add_argument('--yes', '-y', action='store_true', help='do not ask for confirmation')
    update_parser.add_argument('--auto', action='store_true',
                               help='what the daily timer runs: install a fix release if TOFI_AUTO_UPDATE=patch')
    add_guard_flags(update_parser)
    update_parser.add_argument('--from-request', dest='from_request', action='store_true',
                               help='what tofi-update-request.path runs: handle an update requested from the web page')
    update_parser.add_argument('--version')
    update_parser.add_argument('--manifest', help='use a local manifest instead of GitHub Releases')
    update_parser.add_argument('--allow-schema-change', action='store_true')
    update_parser.add_argument('--allow-runner-downgrade', action='store_true',
                               help='accept a release older than the Runner container although accounts already use it')
    update_parser.add_argument('--check', action='store_true',
                               help='report what would change and exit 0 (up to date), 10 (update available) '
                                    'or 1 (error); changes nothing')
    update_parser.add_argument('--json', action='store_true', help='with --check: machine-readable output')
    computer_parser = sub.add_parser('computer', help='add or remove this server\'s computer')
    computer_sub = computer_parser.add_subparsers(dest='computer_command', required=True)
    add_parser = computer_sub.add_parser(
        'add', help='install a computer (KVM, Firecracker) on a server that has none: checks the host, '
                    'downloads the Guest, starts the Worker and restarts the App')
    add_parser.add_argument('backend', choices=['kvm'])
    add_guard_flags(add_parser)
    remove_parser = computer_sub.add_parser(
        'remove', help='stop the Worker and go back to server and web only; computers\' disks are kept')
    remove_parser.add_argument('--purge', action='store_true',
                               help='also delete the computers\' disks, their state and the Guest release')
    remove_parser.add_argument('--yes', '-y', action='store_true', help='do not ask for confirmation (needed with --purge)')
    remove_parser.add_argument('--discard-unsaved-files', action='store_true',
                               help='with --purge: delete the disks even though some files exist only on a computer')
    add_guard_flags(remove_parser)
    computers_parser = sub.add_parser('computers', help='list account computers and their Guest release')
    computers_parser.add_argument('--json', action='store_true', help='machine-readable output')
    computers_sub = computers_parser.add_subparsers(dest='computers_command')
    upgrade_parser = computers_sub.add_parser(
        'upgrade', help='move computers on an older Guest release to the installed one')
    which = upgrade_parser.add_mutually_exclusive_group(required=True)
    which.add_argument('--all', action='store_true', help='every computer on an older Guest')
    which.add_argument('--account', help='one computer: its account id or an 8+ character prefix')
    upgrade_parser.add_argument('--force', action='store_true',
                                help='also restart busy computers (asks first on a terminal; tasks are interrupted)')
    upgrade_parser.add_argument('--json', action='store_true', help='machine-readable output')
    backup_parser = sub.add_parser('backup', help='stop, back up data and settings, start again')
    backup_parser.add_argument('--note', help='a short label stored with the backup')
    backup_parser.add_argument('--no-disks', action='store_true',
                               help='leave the computers\' workspace disks out (they can be large; compressed, and only the last 2 backups keep them)')
    backup_parser.add_argument('--verify', nargs='?', const='latest', metavar='ID',
                               help='check a backup (newest, an ID, or "all") without restoring it: files, '
                                    'checksums, readability; changes nothing and stops nothing')
    add_guard_flags(backup_parser)
    backups_parser = sub.add_parser('backups', help='list backups')
    backups_parser.add_argument('--json', action='store_true', help='machine-readable output')
    backups_parser.add_argument('--remove', metavar='ID', help='delete this backup')
    restore_parser = sub.add_parser('restore', help='restore a backup and switch to its version')
    restore_parser.add_argument('backup_id', help='an id from `tofi backups` (or a unique prefix)')
    restore_parser.add_argument('--yes', '-y', action='store_true', help='do not ask for confirmation')
    restore_parser.add_argument('--no-disks', action='store_true',
                                help='keep the current computer disks even if the backup carries them')
    add_guard_flags(restore_parser)
    config_parser = sub.add_parser('config', help='change a setting')
    config_sub = config_parser.add_subparsers(dest='config_command', required=True)
    auto_parser = config_sub.add_parser('auto-update', help='install fix releases automatically overnight')
    auto_parser.add_argument('mode', choices=['on', 'off'])
    telemetry_parser = config_sub.add_parser('telemetry', help='the opt-in anonymous install report (off by default)')
    telemetry_parser.add_argument('mode', choices=['on', 'off', 'status'])
    proxy_parser = config_sub.add_parser('trusted-proxies', help='set, show or clear the reverse proxies trusted '
                                         'for X-Forwarded-For')
    proxy_parser.add_argument('proxies', nargs='*', metavar='IP|CIDR',
                              help='proxy addresses (several allowed); none shows the current list')
    proxy_parser.add_argument('--clear', action='store_true', help='trust no proxy')
    uninstall_parser = sub.add_parser('uninstall')
    uninstall_parser.add_argument('--purge', action='store_true')
    uninstall_parser.add_argument('--confirm-hostname', help='non-interactive purge confirmation')
    sub.add_parser('setup-secret')
    sub.add_parser('regenerate-cert', help='replace the self-signed HTTPS certificate and restart the App')
    logs_parser = sub.add_parser('logs')
    logs_parser.add_argument('service', nargs='?', choices=['app', 'worker', 'caddy', 'runner'])
    sub.add_parser('doctor')
    security_parser = sub.add_parser('security', help='security checks')
    security_sub = security_parser.add_subparsers(dest='security_command', required=True)
    audit_parser = security_sub.add_parser(
        'audit', help='report where this install deviates from safe defaults (read-only; exit 2 on FAIL)')
    audit_parser.add_argument('--json', action='store_true', help='machine-readable output')
    audit_parser.add_argument('--online', action='store_true',
                              help='fetch the latest release instead of reading the stored check (no file is written)')
    sub.add_parser('version')
    return parser.parse_args(argv)


def auto_update_choice(args):
    """patch or off: the flag decides; without one, --yes means patch (the
    default answer of the installer's question) and anything else means off."""
    flag = getattr(args, 'auto_update', None)
    if flag is None:
        return 'patch' if getattr(args, 'yes', False) else 'off'
    return 'patch' if flag else 'off'


def install_options(args):
    port = args.port if args.port is not None else DEFAULT_PORT
    if not 1024 <= port <= 65535:
        raise HostError('--port must be between 1024 and 65535.')
    local_only = bool(getattr(args, 'local_only', False))
    if args.domain and (args.lan or local_only):
        raise HostError('--domain already decides how TOFI is reached; drop --lan/--local-only.')
    if args.lan and local_only:
        raise HostError('Use either --lan or --local-only, not both.')
    proxies = ''
    if getattr(args, 'behind_proxy', None):
        if args.domain or local_only:
            raise HostError('--behind-proxy excludes --domain (bundled Caddy) and --local-only.')
        entries, error = tofi_tui.parse_proxy_list(args.behind_proxy)
        if error:
            raise HostError('--behind-proxy: ' + error)
        proxies = ','.join(entries)
    if args.email and not args.domain:
        raise HostError('--email is only used with --domain.')
    computer = (getattr(args, 'computer', None) or '').lower()
    if computer and computer not in COMPUTER_BACKENDS:
        raise HostError('--computer must be none or kvm.')
    if computer and COMPUTER_BACKENDS[computer]:
        raise HostError('--computer %s is %s; use none or kvm (Firecracker).'
                        % (computer, COMPUTER_BACKENDS[computer]))
    if not computer:
        computer = default_computer()
    domain = (args.domain or '').lower().rstrip('.')
    if domain and not DOMAIN_RE.match(domain):
        raise HostError('%r is not a valid domain name.' % args.domain)
    # Default: HTTPS with a self-signed certificate on every interface.
    # --lan is the old name for that default and is kept as an alias.
    return {'domain': domain, 'behind_proxy': proxies, 'email': args.email or '', 'lan': args.lan, 'local_only': local_only,
            'port': port, 'port_given': args.port is not None,
            'bind': '127.0.0.1' if local_only else '0.0.0.0', 'yes': args.yes, 'computer': computer,
            'auto_update': auto_update_choice(args)}


def guard_options(args, interactive_default):
    """(wait minutes, force) for the active-run guard: --wait wins; otherwise
    10 minutes on a terminal and 0 for scripts and timers."""
    if args.wait is not None:
        wait = args.wait
    else:
        wait = DEFAULT_WAIT_MINUTES if interactive_default and sys.stdin.isatty() else 0
    if wait < 0:
        raise HostError('--wait takes a number of minutes (0 or more).')
    return wait, bool(args.force)


def ask_yes(question):
    try:
        answer = input(question + ' [Y/n] ').strip().lower()
    except EOFError:
        answer = 'n'
    return answer in ('', 'y', 'yes')


def confirm_update(installed, manifest):
    say('Update %s -> %s. TOFI stops briefly, a backup of data and settings is taken first, and it rolls '
        'back by itself if the new version does not start.' % (installed, manifest['version']))
    if manifest_notes_url(manifest):
        say('Release notes: ' + manifest_notes_url(manifest))
    if not ask_yes('Proceed?'):
        raise HostError('Update cancelled; nothing was changed.')


def confirm_restore(item, meta):
    say('Restore backup %s (TOFI %s, taken %s, %s).' % (item['id'], meta.get('version'), meta.get('created_at'),
                                                       meta.get('reason')))
    say('All current data and settings are replaced; the current state is backed up first (pre-restore).')
    if meta.get('disks'):
        say('It carries %s: they replace the current ones, which are kept beside them (workspace-before-restore-*.ext4); '
            'open pages of those computers are lost. --no-disks leaves the disks as they are.'
            % plural(len(meta['disks']), 'computer disk'))
    if not ask_yes('Restore it?'):
        raise HostError('Restore cancelled; nothing was changed.')


def main(argv=None):
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        if args.command == 'install':
            if not args.manifest and load_state() is None:
                raise HostError('A fresh install needs --manifest; use install.sh.')
            install(args.manifest, install_options(args))
            return 0
        if args.command == 'status':
            if os.geteuid() == 0:
                ensure_update_request_channel_quietly()
            result = status(details=True)
            if args.json:
                print(json.dumps(result, indent=2))
                return 0
            try:
                access = status_access(read_env())
            except (OSError, HostError):
                access = {}
            sys.stdout.write(render_status(result, color_mode(sys.stdout), args.verbose, access))
            return 0
        if args.command == 'computer':
            guard = guard_options(args, interactive_default=True)
            if args.computer_command == 'add':
                computer_add(args.backend, *guard)
            else:
                computer_remove(args.purge, args.yes, *guard, discard_unsaved_files=args.discard_unsaved_files)
            return 0
        if args.command == 'computers':
            require_root()
            if not has_computer(read_env()):
                if getattr(args, 'json', False):
                    print(json.dumps({'computer': 'none', 'computers': []}, indent=2))
                else:
                    say('This server has no computer (backend none). Add one with: sudo tofi computer add kvm')
                return 0
            if args.computers_command == 'upgrade':
                report = upgrade_computers(args.account, args.all, args.force)
                sys.stdout.write(json.dumps(report, indent=2) + '\n' if args.json else render_upgrade(report))
                return upgrade_exit_code(report)
            require_root()
            env = read_env()
            rows = computers_report(env.get('TOFI_GUEST_VERSION'))
            if args.json:
                print(json.dumps({'guest': env.get('TOFI_GUEST_VERSION'), 'computers': rows}, indent=2))
            else:
                sys.stdout.write('\n'.join(render_computers(rows, env.get('TOFI_GUEST_VERSION'))) + '\n')
            return 0
        if args.command == 'regenerate-cert':
            regenerate_certificate()
            return 0
        if args.command == 'start':
            start()
            ensure_update_request_channel_quietly()
            return 0
        if args.command == 'stop':
            stop(*guard_options(args, interactive_default=True))
            return 0
        if args.command == 'backup':
            if args.verify is not None:
                require_root()
                reports, ok = verify_backups(args.verify)
                sys.stdout.write(render_verify(reports))
                return 0 if ok else 1
            meta = backup_now(args.note, *guard_options(args, interactive_default=True), disks=not args.no_disks)
            say('Backup id: %s' % meta['id'])
            # Re-read what was just written: a backup nobody checked is a hope.
            reports, ok = verify_backups(meta['id'])
            sys.stdout.write(render_verify(reports))
            return 0 if ok else 1
        if args.command == 'backups':
            require_root()
            if args.remove:
                say('Removed backup %s.' % remove_backup(args.remove))
                return 0
            backups = list_backups()
            if args.json:
                print(json.dumps([dict(item['meta'], id=item['id'], size=item['size']) for item in backups], indent=2))
            else:
                sys.stdout.write(render_backups(backups))
            return 0
        if args.command == 'restore':
            restore_backup(args.backup_id, args.yes, *guard_options(args, interactive_default=not args.yes),
                           confirm=confirm_restore if sys.stdin.isatty() else None, disks=not args.no_disks)
            return 0
        if args.command == 'config':
            if args.config_command == 'trusted-proxies':
                configure_trusted_proxies(args.proxies, args.clear)
            elif args.config_command == 'telemetry':
                configure_telemetry(args.mode)
                return 0
            else:
                configure_auto_update(args.mode)
            ensure_update_request_channel_quietly()
            return 0
        if args.command == 'update':
            if args.from_request:
                if args.check or args.version or args.manifest or args.json or args.auto:
                    raise HostError('--from-request cannot be combined with other update options.')
                ensure_update_request_channel_quietly()
                return 1 if update_from_request()['result'] == 'failed' else 0
            if args.auto:
                if args.check or args.version or args.manifest or args.json:
                    raise HostError('--auto cannot be combined with --check, --version, --manifest or --json.')
                result = auto_update()
                ensure_update_request_channel_quietly()
                return 1 if result['result'] == 'failed' else 0
            if args.check:
                report = check_update(args.version, args.manifest)
                sys.stdout.write(json.dumps(report, indent=2) + '\n' if args.json else render_update_check(report))
                return UPDATE_AVAILABLE_EXIT if report['update_available'] else 0
            if args.json:
                raise HostError('--json is only used with --check.')
            wait, force = guard_options(args, interactive_default=not args.yes)
            upgrade(args.version, args.manifest, args.allow_schema_change, wait=wait, force=force,
                    confirm=None if args.yes or not sys.stdin.isatty() else confirm_update,
                    allow_runner_downgrade=args.allow_runner_downgrade)
            ensure_update_request_channel_quietly()
            return 0
        if args.command == 'uninstall':
            uninstall(args.purge, args.confirm_hostname)
            return 0
        if args.command == 'setup-secret':
            require_root()
            secret = read_setup_secret()
            if not secret:
                say('No setup key is pending: the Admin account already exists.')
                return 1
            say(secret)
            return 0
        if args.command == 'logs':
            command = compose_command(read_env(), ['tls']) + ['logs', '--tail', '200', '-f']
            if args.service:
                command.append(args.service)
            os.execvp('docker', command)
        if args.command == 'doctor':
            return 0 if doctor() else 1
        if args.command == 'security':
            report = security_audit(args.online)
            sys.stdout.write(json.dumps(report, indent=2) + '\n' if args.json else render_security_audit(report))
            return report['exit_code']
        if args.command == 'version':
            print(json.dumps(version(), indent=2))
            return 0
    except HostError as error:
        print('tofi: ' + str(error), file=sys.stderr)
        return 1
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or error.stdout or '').strip()[-500:]
        print('tofi: command failed: %s\n%s' % (' '.join(map(str, error.cmd[:4])), detail), file=sys.stderr)
        return 1
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print('tofi: ' + str(error), file=sys.stderr)
        return 1
    return 1


# --------------------------------------------------------------------------
# Security audit (`tofi security audit`)
#
# Read-only: it reads the journal, tofi.env, file modes, the certificate and
# the backup list, and changes nothing (no lock, no restore settling, no cache
# write, no network unless --online). Values of secrets are never read into a
# result: findings carry names, counts, modes and paths only.

AUDIT_PASS, AUDIT_WARN, AUDIT_FAIL = 'PASS', 'WARN', 'FAIL'
AUDIT_FAIL_EXIT = 2
BACKUP_STALE_DAYS = 7
# Environment names that look like credentials. tofi.env should hold none: the
# file layer carries platform settings, third-party keys live in the vault.
SECRET_NAME_RE = re.compile(r'KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASSPHRASE|CREDENTIAL')
# Model provider keys the App can read from its environment (cmd/tofi/main.go);
# the self-host compose.yaml passes none of them on.
MODEL_KEY_ENV_NAMES = ('TOFI_MODEL_API_KEY', 'TOFI_TRANSCRIPTION_API_KEY', 'ANTHROPIC_API_KEY', 'OPENAI_API_KEY')
# What a provider key looks like in the clear (sealed values are base64).
PLAIN_KEY_RE = re.compile(r'sk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}')
VAULT_MODEL_KIND = 'model_provider'


def audit_finding(level, check, title, detail, fix=None):
    return {'id': check, 'level': level, 'title': title, 'detail': detail, 'fix': fix if level != AUDIT_PASS else None}


def path_facts(path):
    """(uid, gid, permission bits, is_symlink) of `path` without following links, or None."""
    try:
        info = os.lstat(path)
    except OSError:
        return None
    return info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode), stat.S_ISLNK(info.st_mode)


def audit_paths(rules):
    """Compare (path, max mode, owner uid, allowed gids or None) rules with the disk.

    Returns (offences, checked): an offence is (path, problem sentence, fix
    command). A path that does not exist is skipped; a mode with any bit
    outside `max mode` is too permissive; a symbolic link is always refused.
    """
    offences, checked = [], 0
    for path, max_mode, uid, gids in rules:
        facts = path_facts(path)
        if facts is None:
            continue
        checked += 1
        actual_uid, actual_gid, mode, is_link = facts
        if is_link:
            offences.append((path, 'is a symbolic link', 'sudo rm %s  # then let tofi recreate it' % path))
            continue
        problems, fixes = [], []
        if mode & ~max_mode & 0o7777:
            problems.append('mode %04o, allowed %04o' % (mode, max_mode))
            fixes.append('sudo chmod %04o %s' % (max_mode, path))
        if actual_uid != uid:
            problems.append('owner uid %d, expected %d' % (actual_uid, uid))
            fixes.append('sudo chown %d %s' % (uid, path))
        if gids is not None and actual_gid not in gids:
            problems.append('group gid %d, expected %s' % (actual_gid, ' or '.join(map(str, gids))))
            fixes.append('sudo chgrp %d %s' % (gids[-1], path))
        if problems:
            offences.append((path, '; '.join(problems), ' && '.join(fixes)))
    return offences, checked


def audit_file_finding(check, title, rules, secret=True):
    offences, checked = audit_paths(rules)
    if not offences:
        return audit_finding(AUDIT_PASS, check, title, '%d path%s checked, modes and owners as expected'
                             % (checked, '' if checked == 1 else 's'))
    shown = ['%s: %s' % (path, problem) for path, problem, _ in offences[:5]]
    if len(offences) > 5:
        shown.append('and %d more' % (len(offences) - 5))
    fix = ' && '.join(dict.fromkeys(command for _, _, command in offences[:5]))
    return audit_finding(AUDIT_FAIL if secret else AUDIT_WARN, check, title, '; '.join(shown), fix)


def audit_certificate(env):
    """(issuer, subject, level) of the served certificate; level is PASS, WARN (renewal due) or FAIL (expired)."""
    shown = run(['openssl', 'x509', '-noout', '-issuer', '-subject', '-in', str(P.tls_cert)], check=False)
    fields = {}
    for line in shown.stdout.splitlines():
        name, _, value = line.partition('=')
        fields[name.strip()] = value.strip()
    expired = run(['openssl', 'x509', '-checkend', '0', '-noout', '-in', str(P.tls_cert)], check=False)
    renewal = run(['openssl', 'x509', '-checkend', str(CERT_RENEW_SECONDS), '-noout', '-in', str(P.tls_cert)],
                  check=False)
    level = AUDIT_FAIL if expired.returncode else AUDIT_WARN if renewal.returncode else AUDIT_PASS
    return fields.get('issuer'), fields.get('subject'), level


def public_addresses():
    import ipaddress
    """Global-scope addresses of this host that the Internet can route to (no NAT in between is known)."""
    found = []
    for _family, address in interface_addresses():
        try:
            if ipaddress.ip_address(address).is_global:
                found.append(address)
        except ValueError:
            continue
    return found


def audit_tls(env):
    domain, cert, bind = env.get('TOFI_DOMAIN'), env.get('TOFI_TLS_CERT_FILE'), env.get('TOFI_BIND') or '127.0.0.1'
    title = 'HTTPS and certificate'
    if domain:
        return audit_finding(AUDIT_PASS, 'exposure.tls', title,
                             'HTTPS for %s, terminated by the bundled Caddy (certificate issued and renewed by Caddy)' % domain)
    if not cert:
        loopback = bind in ('127.0.0.1', '::1')
        return audit_finding(AUDIT_WARN if loopback else AUDIT_FAIL, 'exposure.tls', title,
                             'the App serves plain HTTP on %s: sign-in passwords cross the network in the clear' % bind,
                             'sudo tofi install --yes  # re-run to restore the default HTTPS mode, or reinstall with --domain')
    if not P.tls_cert.is_file():
        return audit_finding(AUDIT_FAIL, 'exposure.tls', title, 'HTTPS is configured but %s is missing' % P.tls_cert,
                             'sudo tofi regenerate-cert')
    issuer, subject, level = audit_certificate(env)
    kind = 'self-signed' if issuer and issuer == subject else 'issued by %s' % (issuer or 'an unknown issuer')
    if level == AUDIT_FAIL:
        return audit_finding(AUDIT_FAIL, 'exposure.tls', title, 'the %s certificate has expired' % kind,
                             'sudo tofi regenerate-cert')
    if level == AUDIT_WARN:
        return audit_finding(AUDIT_WARN, 'exposure.tls', title,
                             'the %s certificate expires within %d days' % (kind, CERT_RENEW_SECONDS // 86400),
                             'sudo tofi regenerate-cert')
    return audit_finding(AUDIT_PASS, 'exposure.tls', title, 'HTTPS on, %s certificate (fingerprint %s)'
                         % (kind, short_fingerprint(certificate_fingerprint() or '?')))


def audit_listening(env):
    title = 'Listening interfaces'
    bind, port = env.get('TOFI_BIND') or '127.0.0.1', env.get('TOFI_HTTP_PORT') or str(DEFAULT_PORT)
    domain = env.get('TOFI_DOMAIN')
    if domain:
        if bind not in ('127.0.0.1', '::1'):
            return audit_finding(AUDIT_WARN, 'exposure.listen', title,
                                 'with a domain the App port %s is also published on %s, beside Caddy' % (port, bind),
                                 'set TOFI_BIND=127.0.0.1 in /etc/tofi/tofi.env, then sudo tofi stop && sudo tofi start')
        return audit_finding(AUDIT_PASS, 'exposure.listen', title,
                             'the App port is on 127.0.0.1; only Caddy (80/443) faces the network')
    if bind in ('127.0.0.1', '::1'):
        return audit_finding(AUDIT_PASS, 'exposure.listen', title, 'the App listens on %s:%s only (local-only)' % (bind, port))
    public = public_addresses()
    if public:
        return audit_finding(AUDIT_WARN, 'exposure.listen', title,
                             'the App listens on %s:%s and this host has public address%s %s, so the self-signed login page '
                             'may be reachable from the Internet (a router or firewall in front cannot be seen from here)'
                             % (bind, port, '' if len(public) == 1 else 'es', ', '.join(public[:3])),
                             'put it behind a domain (reinstall with --domain), a VPN, or a firewall that only admits your '
                             'LAN; or sudo tofi install --local-only')
    return audit_finding(AUDIT_PASS, 'exposure.listen', title,
                         'the App listens on %s:%s; no public address on any interface (LAN only)' % (bind, port))


def audit_lan_http(env):
    title = 'Plain-HTTP sign-in'
    if env.get('TOFI_DOMAIN'):
        return audit_finding(AUDIT_PASS, 'exposure.lan_http', title,
                             'plain HTTP is accepted only from Caddy over the Docker bridge (by design with --domain)')
    if (env.get('TOFI_OWNER_ALLOW_LAN_HTTP') or '1') == '1':  # compose: ${...:-1}, empty means 1
        return audit_finding(AUDIT_WARN, 'exposure.lan_http', title,
                             'TOFI_OWNER_ALLOW_LAN_HTTP=1 lets the Admin sign in over plain HTTP from private addresses',
                             'set TOFI_OWNER_ALLOW_LAN_HTTP=0 in /etc/tofi/tofi.env, then sudo tofi stop && sudo tofi start')
    return audit_finding(AUDIT_PASS, 'exposure.lan_http', title, 'sign-in over plain HTTP is refused')


def audit_admin():
    title = 'Admin password'
    if read_setup_secret():
        return audit_finding(AUDIT_WARN, 'auth.admin', title,
                             'the Admin account has not been created: a one-time setup key is still pending',
                             'open the web page and create the Admin password now (sudo tofi setup-secret prints the key)')
    if not P.data.is_dir():
        return audit_finding(AUDIT_WARN, 'auth.admin', title, 'the App has not created its data yet', 'sudo tofi start')
    return audit_finding(AUDIT_PASS, 'auth.admin', title, 'no pending setup key: the Admin account exists')


def audit_login_limits(env):
    title = 'Login rate limiting'
    try:
        compose = compose_file().read_text()
    except OSError:
        compose = ''
    if not re.search(r'^\s*TOFI_OWNER_AUTH:\s*"?1"?\s*$', compose, re.M):
        return audit_finding(AUDIT_FAIL, 'auth.rate_limit', title,
                             'compose.yaml does not switch owner sign-in on (TOFI_OWNER_AUTH=1), so no limiter runs',
                             'sudo tofi update --version <installed version>  # restores the shipped compose.yaml')
    proxies = env.get('TOFI_TRUSTED_PROXIES') or ''
    if proxies:
        entries, error = tofi_tui.parse_proxy_list(proxies)
        if error:
            return audit_finding(AUDIT_FAIL, 'auth.rate_limit', title,
                                 'TOFI_TRUSTED_PROXIES=%s is malformed or too broad: any visitor could forge '
                                 'X-Forwarded-For and dodge the per-address limit' % proxies[:80],
                                 'sudo tofi config trusted-proxies <your proxy IP>  # or --clear')
        return audit_finding(AUDIT_PASS, 'auth.rate_limit', title,
                             'limiter on (per address and per account); X-Forwarded-For trusted only from %s' % ', '.join(entries))
    if env.get('TOFI_DOMAIN'):
        return audit_finding(AUDIT_WARN, 'auth.rate_limit', title,
                             'limiter on, but with a domain no proxy is trusted, so every visitor shares Caddy\'s address '
                             'bucket and one attacker can lock everyone out',
                             'set TOFI_TRUSTED_PROXIES=%s in /etc/tofi/tofi.env, then sudo tofi stop && sudo tofi start'
                             % LOCAL_PROXY_CIDRS)
    return audit_finding(AUDIT_PASS, 'auth.rate_limit', title,
                         'limiter on (per address and per account); no proxy trusted, visitors are keyed by their own address')


def backup_member_rules(directory):
    """Rules for the members of a backup directory: files 0600, subdirectories (disks/) 0700 and walked."""
    rules = []
    for member in sorted(directory.iterdir()):
        if member.is_dir() and not member.is_symlink():
            rules.append((member, 0o700, 0, None))
            rules.extend(backup_member_rules(member))
        else:
            rules.append((member, 0o600, 0, None))
    return rules


def audit_files(env):
    findings = []
    host_rules = [(P.etc, 0o700, 0, None), (P.env_file, 0o600, 0, None), (P.worker_json, 0o600, 0, None),
                  (P.seccomp, 0o600, 0, None), (P.state_file, 0o600, 0, None), (P.worker, 0o700, 0, None),
                  (P.caddy, 0o700, 0, None)]
    findings.append(audit_file_finding('files.host', 'Host config and state files', host_rules))
    if env.get('TOFI_TLS_CERT_FILE'):
        findings.append(audit_file_finding('files.tls', 'HTTPS private key',
                                           [(P.tls, 0o750, 0, (0, APP_UID)), (P.tls_key, 0o640, 0, (0, APP_UID))]))
    secrets = P.data / 'secrets'
    data_rules = [(P.data, 0o700, APP_UID, None), (secrets, 0o700, APP_UID, None), (secrets / 'key', 0o600, APP_UID, None),
                  (secrets / 'vault.json', 0o600, APP_UID, None), (P.bootstrap_secret, 0o600, APP_UID, None)]
    findings.append(audit_file_finding('files.data', 'Data directory and secret vault files', data_rules))
    backup_rules = [(P.backups, 0o700, 0, None)]
    try:
        for entry in sorted(P.backups.iterdir()) if P.backups.is_dir() else []:
            backup_rules.append((entry, 0o700, 0, None))
            if entry.is_dir() and not entry.is_symlink():
                backup_rules.extend(backup_member_rules(entry))
    except OSError:
        pass
    findings.append(audit_file_finding('files.backups', 'Backups (they hold a copy of the vault key)', backup_rules))
    return findings


def audit_env_secrets(env):
    title = 'Secrets in tofi.env'
    managed = set(ENV_KEYS) | set(RUNNER_ENV_KEYS)
    # A *_FILE variable names where a secret lives (a path), not the secret itself.
    names = sorted(name for name, value in env.items() if name not in managed and name not in MODEL_KEY_ENV_NAMES
                   and not name.upper().endswith('_FILE') and value and SECRET_NAME_RE.search(name.upper()))
    if not names:
        return audit_finding(AUDIT_PASS, 'secrets.env', title, 'no credential-looking variable in /etc/tofi/tofi.env')
    shown = ', '.join(re.sub(r'[^A-Za-z0-9_.-]', '?', name)[:60] for name in names[:10])
    return audit_finding(AUDIT_WARN, 'secrets.env', title,
                         '%d credential-looking variable%s in /etc/tofi/tofi.env (names only): %s%s'
                         % (len(names), '' if len(names) == 1 else 's', shown, ' and more' if len(names) > 10 else ''),
                         'remove them from tofi.env; enter third-party keys in the web UI so they are sealed in the vault')


def audit_model_keys(env):
    title = 'Model provider keys'
    problems, notes = [], []
    named = sorted(name for name in MODEL_KEY_ENV_NAMES if env.get(name))
    shaped = sorted(name for name, value in env.items() if name not in named and PLAIN_KEY_RE.search(value or ''))
    if named or shaped:
        problems.append('%d provider key%s in plaintext in /etc/tofi/tofi.env (names only): %s'
                        % (len(named + shaped), '' if len(named + shaped) == 1 else 's',
                           ', '.join(re.sub(r'[^A-Za-z0-9_.-]', '?', n)[:60] for n in named + shaped)))
    vault = P.data / 'secrets' / 'vault.json'
    level = AUDIT_WARN if problems else AUDIT_PASS
    if vault.is_file():
        try:
            text = vault.read_text()
            records = json.loads(text)
            if not isinstance(records, dict):
                raise ValueError('not an object')
        except (OSError, ValueError):
            return audit_finding(AUDIT_WARN, 'secrets.model_keys', title, 'the vault file could not be read as JSON',
                                 'sudo tofi doctor; restore the vault from a backup if the App cannot open it')
        provider = [r for r in records.values() if isinstance(r, dict) and r.get('kind') == VAULT_MODEL_KIND]
        unsealed = [r for r in provider if not r.get('ciphertext')]
        if PLAIN_KEY_RE.search(text) or unsealed:
            level = AUDIT_FAIL
            problems.append('%d vault record%s hold%s a provider key without sealed ciphertext'
                            % (max(len(unsealed), 1), '' if max(len(unsealed), 1) == 1 else 's',
                               's' if max(len(unsealed), 1) == 1 else ''))
        notes.append('%d sealed provider key%s in the vault' % (len(provider), '' if len(provider) == 1 else 's'))
    if problems:
        return audit_finding(level, 'secrets.model_keys', title, '; '.join(problems + notes),
                             'delete the plaintext entry and enter the key again in the web UI (Settings), which seals it')
    return audit_finding(AUDIT_PASS, 'secrets.model_keys', title,
                         '; '.join(notes) if notes else 'no provider key on the host outside the sealed vault')


def cached_latest():
    """The last stored latest-release answer, read only (latest_release() would rewrite the cache)."""
    try:
        cached = json.loads(P.latest_cache.read_text())
    except (OSError, ValueError):
        return None
    return cached if isinstance(cached, dict) and cached.get('version') else None


def audit_versions(env, online=False):
    title = 'TOFI version'
    installed = env.get('TOFI_VERSION')
    source = 'cached release check'
    latest, checked = None, None
    if online:
        try:
            latest = fetch_manifest(timeout=5)['version']
            checked, source = iso_now(), 'GitHub, just now'
        except HostError as error:
            return audit_finding(AUDIT_WARN, 'versions.tofi', title,
                                 'installed %s; the latest release could not be fetched (%s)' % (installed, str(error)[:120]),
                                 'check the network, then sudo tofi update --check')
    else:
        cached = cached_latest()
        if cached:
            latest, checked = cached['version'], cached.get('checked_at')
    if latest is None:
        return audit_finding(AUDIT_WARN, 'versions.tofi', title,
                             'installed %s; no release check is stored yet, so freshness is unknown' % installed,
                             'sudo tofi security audit --online  # or sudo tofi update --check')
    if is_newer(latest, installed):
        return audit_finding(AUDIT_WARN, 'versions.tofi', title,
                             'installed %s, latest release %s (%s, %s)' % (installed, latest, source, checked or 'time unknown'),
                             'sudo tofi update --check, then sudo tofi update')
    return audit_finding(AUDIT_PASS, 'versions.tofi', title, 'installed %s is the latest release known (%s, %s)'
                         % (installed, source, checked or 'time unknown'))


def audit_computers(env):
    title = 'Computer images (Guest)'
    guest, server = env.get('TOFI_GUEST_VERSION'), env.get('TOFI_VERSION')
    if not has_computer(env):
        return audit_finding(AUDIT_PASS, 'versions.computers', title,
                             'no computer is installed (TOFI_COMPUTER_BACKEND=none): no Guest to keep current')
    if not guest or not (P.guest / guest / 'account-release.json').is_file():
        return audit_finding(AUDIT_FAIL, 'versions.computers', title,
                             'the pinned Guest release %s is not on disk under %s' % (guest, P.guest),
                             'sudo tofi update --version %s  # fetches the Guest again' % server)
    older = sorted(version for version in guest_references() if version != guest)
    if older:
        # Expected after an update: a hibernated computer stays hibernated until
        # it is used. A snapshot from another Guest is never restored (its
        # identity names the release); the next use cold boots on the
        # installed Guest. Upgrading now only frees the old snapshots' disk.
        return audit_finding(AUDIT_PASS, 'versions.computers', title,
                             'Guest %s installed with server %s; hibernated computers with snapshots from %s '
                             'start on Guest %s at their next use (an older snapshot is never restored). '
                             'To free their disk now: sudo tofi computers upgrade --all'
                             % (guest, server, ', '.join(older), guest))
    return audit_finding(AUDIT_PASS, 'versions.computers', title,
                         'Guest %s installed with server %s; no hibernated computer is on an older Guest '
                         '(running computers: sudo tofi computers)' % (guest, server))


def audit_runner_fence(env):
    """Read-only: is the Runner's egress fence in place? Changes nothing."""
    title = 'Runner network fence'
    if not env.get('TOFI_RUNNER_IMAGE'):
        return audit_finding(AUDIT_PASS, 'runner.fence', title, 'no Runner container is installed: nothing to fence')
    fix = 'sudo apt-get install iptables && sudo tofi restart  # applies the rules again'
    if shutil.which('iptables') is None:
        return audit_finding(AUDIT_FAIL, 'runner.fence', title,
                             'iptables is not installed, so the Runner container (it runs code a person installed) '
                             'can reach the host, the LAN and the cloud metadata address', fix)
    if _iptables('-S', 'DOCKER-USER').returncode != 0:
        return audit_finding(AUDIT_FAIL, 'runner.fence', title,
                             'Docker has no DOCKER-USER chain here (iptables disabled in Docker, or the nftables backend), '
                             'so the Runner container is not fenced', 'enable Docker iptables handling, then: sudo tofi restart')
    hooks = (('DOCKER-USER', ['-s', RUNNER_EGRESS_SUBNET, '-j', RUNNER_EGRESS_CHAIN]),
             ('INPUT', ['-s', RUNNER_EGRESS_SUBNET, '-j', 'DROP']))
    if _iptables('-S', RUNNER_EGRESS_CHAIN).returncode != 0 or any(_iptables('-C', chain, *rule).returncode != 0 for chain, rule in hooks):
        return audit_finding(AUDIT_WARN if env.get('TOFI_RUNNER_UNFENCED') == '1' else AUDIT_FAIL, 'runner.fence', title,
                             'the firewall rules of the Runner container are missing'
                             + ('; TOFI_RUNNER_UNFENCED=1 allows it' if env.get('TOFI_RUNNER_UNFENCED') == '1' else ''), fix)
    return audit_finding(AUDIT_PASS, 'runner.fence', title,
                         'the Runner container (%s) is fenced off from the host and the private networks' % RUNNER_EGRESS_SUBNET)


def audit_runner_sandbox(env):
    """Read-only: can the Runner confine memory and processes per account? Changes nothing."""
    title = 'Runner account limits'
    check = 'runner.cgroups'
    if not env.get('TOFI_RUNNER_IMAGE'):
        return audit_finding(AUDIT_PASS, check, title, 'no Runner container is installed: nothing to limit')
    fix = 'sudo tofi restart  # loads the tofi-runner AppArmor profile and starts the Runner with it'
    mode = env.get('TOFI_RUNNER_CGROUPS', 'auto')
    if mode != 'required':
        return audit_finding(AUDIT_WARN, check, title,
                             'TOFI_RUNNER_CGROUPS is %s: a Runner that cannot create account cgroups still starts, '
                             'without per-account memory and process limits' % mode, fix)
    if apparmor_active():
        if env.get('TOFI_RUNNER_APPARMOR') != RUNNER_APPARMOR or not P.runner_apparmor_profile.exists():
            return audit_finding(AUDIT_FAIL, check, title,
                                 'the Runner is not set up with its %s AppArmor profile; with Docker\'s default profile '
                                 'it cannot create account cgroups' % RUNNER_APPARMOR, fix)
        if not apparmor_profile_loaded(RUNNER_APPARMOR):
            return audit_finding(AUDIT_FAIL, check, title,
                                 'the %s AppArmor profile is not loaded in the kernel' % RUNNER_APPARMOR, fix)
    try:
        runner = next((c for c in project_containers() if service_of(c) == 'runner'), None)
    except (subprocess.SubprocessError, ValueError, OSError):
        runner = None
    if runner is not None and runner['State'].get('Running'):
        wanted = RUNNER_APPARMOR if apparmor_active() else None
        if (runner.get('HostConfig') or {}).get('CgroupnsMode') != 'private' \
                or (wanted and runner.get('AppArmorProfile') != wanted):
            return audit_finding(AUDIT_FAIL, check, title,
                                 'the running Runner container does not use a private cgroup namespace%s'
                                 % (' and the %s profile' % wanted if wanted else ''), fix)
    detail = ('the Runner uses the %s AppArmor profile and a private cgroup namespace; each account gets its own '
              'memory and process limits' % RUNNER_APPARMOR if apparmor_active() else
              'AppArmor is not enabled on this host; the Runner uses a private cgroup namespace and each account '
              'gets its own memory and process limits')
    return audit_finding(AUDIT_PASS, check, title, detail)


def audit_backup_age():
    title = 'Last backup'
    backups = list_backups()
    if not backups:
        return audit_finding(AUDIT_WARN, 'backups.recent', title, 'no backup exists on this host',
                             'sudo tofi backup --note first; copy backups off this machine')
    import calendar
    newest = backups[0]['meta'].get('created_at')
    try:
        taken = calendar.timegm(time.strptime(newest, '%Y-%m-%dT%H:%M:%SZ'))
    except (TypeError, ValueError):
        return audit_finding(AUDIT_WARN, 'backups.recent', title, 'the newest backup records no readable time',
                             'sudo tofi backup')
    days = (time.time() - taken) / 86400
    detail = 'newest backup %s (%s, %.1f days ago), %d kept on this host only' % (
        backups[0]['id'], backups[0]['meta'].get('reason'), max(days, 0), len(backups))
    if days > BACKUP_STALE_DAYS:
        return audit_finding(AUDIT_WARN, 'backups.recent', title, detail + '; older than %d days' % BACKUP_STALE_DAYS,
                             'sudo tofi backup')
    return audit_finding(AUDIT_PASS, 'backups.recent', title, detail)


def read_journal():
    """The install journal without load_state(), which may move restore leftovers back."""
    try:
        state = json.loads(P.state_file.read_text())
    except FileNotFoundError:
        return None
    except (OSError, ValueError) as error:
        raise HostError('The install journal %s is not readable (%s).' % (P.state_file, error)) from error
    if not isinstance(state, dict) or state.get('schema') != 1 or state.get('phase') not in PHASES:
        raise HostError('The install journal %s is not readable; inspect it before continuing.' % P.state_file)
    return state


def security_audit(online=False):
    """Run every check; return {'findings', 'summary', 'exit_code', ...}. Changes nothing."""
    require_root()
    state = read_journal()
    if state is None:
        raise HostError('TOFI is not installed; run install.sh.')
    env = read_env()
    findings = []
    if state['phase'] != 'installed':
        findings.append(audit_finding(AUDIT_WARN, 'install.journal', 'Install journal',
                                      'the install is in phase %s, not installed' % state['phase'],
                                      'sudo tofi install  # resumes or rolls back the interrupted operation'))

    def collect(check, function):
        try:
            result = function()
        except Exception as error:  # one broken check must not hide the others
            result = audit_finding(AUDIT_WARN, check, check, 'could not be checked (%s)' % str(error)[:160], 'sudo tofi doctor')
        findings.extend(result if isinstance(result, list) else [result])

    collect('exposure.tls', lambda: audit_tls(env))
    collect('exposure.listen', lambda: audit_listening(env))
    collect('exposure.lan_http', lambda: audit_lan_http(env))
    collect('auth.admin', audit_admin)
    collect('auth.rate_limit', lambda: audit_login_limits(env))
    collect('files', lambda: audit_files(env))
    collect('secrets.env', lambda: audit_env_secrets(env))
    collect('secrets.model_keys', lambda: audit_model_keys(env))
    collect('versions.tofi', lambda: audit_versions(env, online))
    collect('versions.computers', lambda: audit_computers(env))
    collect('runner.fence', lambda: audit_runner_fence(env))
    collect('runner.cgroups', lambda: audit_runner_sandbox(env))
    collect('backups.recent', audit_backup_age)
    summary = {level.lower(): sum(1 for f in findings if f['level'] == level)
               for level in (AUDIT_PASS, AUDIT_WARN, AUDIT_FAIL)}
    return {'schema': 1, 'version': env.get('TOFI_VERSION'), 'generated_at': iso_now(), 'summary': summary,
            'findings': findings, 'exit_code': AUDIT_FAIL_EXIT if summary['fail'] else 0}


def render_security_audit(report):
    lines = ['TOFI security audit (%s)' % (report.get('version') or 'unknown version'), '']
    for finding in report['findings']:
        lines.append('%-4s  %-20s %s' % (finding['level'], finding['id'], finding['title']))
        lines.append('      %s' % finding['detail'])
        if finding.get('fix'):
            lines.append('      fix: %s' % finding['fix'])
    summary = report['summary']
    lines += ['', '%d PASS, %d WARN, %d FAIL.' % (summary['pass'], summary['warn'], summary['fail']),
              'Read-only; for runtime checks (KVM, AppArmor, health) run `sudo tofi doctor`.']
    return '\n'.join(lines) + '\n'


if __name__ == '__main__':
    sys.exit(main())
