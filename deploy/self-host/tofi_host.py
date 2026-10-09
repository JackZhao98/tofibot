#!/usr/bin/env python3
"""TOFI host lifecycle: install, start/stop, update/rollback, uninstall.

Runs on a dedicated Linux x86_64 host with KVM as root, using only the Python
standard library. install.sh downloads and verifies a release bundle, then hands
off to `tofi install --manifest ...`; everything after that lives here.

Every mutating command holds one host lock (/run/tofi-installer.lock) and
records its progress in /etc/tofi/install-state.json so an interrupted install,
update or uninstall can be resumed by running `tofi install` again. Account
data under /var/lib/tofi is never deleted except by `tofi uninstall --purge`.
"""
import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import socket
import ssl
import stat
import subprocess
import sys
import tempfile
import time
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
COMPUTER_OVERHEAD_MIB = 512
MIN_DISK_BYTES = 30 * GIB
WARN_DISK_BYTES = 40 * GIB
# The first account needs an 8 GiB workspace disk plus 8 GiB internal reserve.
FIRST_ACCOUNT_BYTES = 16 * GIB
DEFAULT_PORT = 8321
# Self-signed certificate for direct HTTPS. Browsers reject leaf certificates
# valid for more than 825 days; renew when fewer than 30 days remain.
CERT_DAYS = 825
CERT_RENEW_SECONDS = 30 * 86400
# Interfaces whose addresses are never how a person reaches this server.
VIRTUAL_INTERFACE_PREFIXES = ('lo', 'docker', 'br-', 'veth', 'virbr', 'tap', 'fc', 'tun', 'cni', 'flannel')

REQUIRED_TOOLS = ['docker', 'apparmor_parser', 'systemd-tmpfiles', 'systemctl',
                  'debugfs', 'e2fsck', 'tar', 'zstd', 'openssl']

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
            'TOFI_TLS_KEY_FILE', 'TOFI_OWNER_ALLOW_LAN_HTTP', 'TOFI_COMPUTER_BACKEND']
# How account computers run. Only KVM (Firecracker) exists today; the value is
# recorded so later releases can add backends without guessing.
COMPUTER_BACKENDS = {'kvm': None, 'gvisor': 'not supported yet (coming in a later release)',
                     'container': 'not supported yet (coming in a later release)'}


class HostError(Exception):
    """A refusal or failure with one actionable sentence for the operator."""


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
        self.apparmor_profile = root / 'etc/apparmor.d/tofi-worker'
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
            raise HostError('Another tofi operation is running (lock %s); wait for it to finish.' % lockfile)
        current = os.stat(str(lockfile), follow_symlinks=False)
        if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino):
            raise HostError('The installer lock changed while it was being taken; retry.')
        yield
    finally:
        # Never unlink the lock inode: another process may be waiting on it.
        os.close(fd)


def load_state():
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
    path = path or P.env_file
    values = {}
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        key, separator, value = line.partition('=')
        if not separator:
            raise HostError('Malformed line in %s: %r' % (path, line))
        values[key.strip()] = value.strip()
    return values


def format_env(values):
    lines = ['# Managed by tofi. Edit with care; run `sudo tofi stop && sudo tofi start` after changes.']
    for key in ENV_KEYS:
        value = str(values.get(key, ''))
        if '\n' in value or ' ' in value:
            raise HostError('Invalid value for %s' % key)
        lines.append('%s=%s' % (key, value))
    return '\n'.join(lines) + '\n'


def write_env(values):
    write_file(P.env_file, format_env(values), 0o600)


# --------------------------------------------------------------------------
# Release manifest

SHA_RE = re.compile(r'^[0-9a-f]{64}$')
VERSION_RE = re.compile(r'^v\d+\.\d+\.\d+(-rc\.\d+)?$')
DOMAIN_RE = re.compile(r'^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$')
EMAIL_RE = re.compile(r'^[^@\s]+@[^@\s]+\.[^@\s]+$')


def validate_manifest(manifest):
    """Check the shape of a release manifest.json and return it."""
    if not isinstance(manifest, dict) or manifest.get('schema') != 1:
        raise HostError('The release manifest has an unsupported schema (expected 1).')
    version = manifest.get('version')
    if not isinstance(version, str) or not VERSION_RE.match(version):
        raise HostError('The release manifest version %r is not vMAJOR.MINOR.PATCH[-rc.N].' % version)
    images = manifest.get('images')
    if not isinstance(images, dict) or set(images) != set(IMAGE_REPOS):
        raise HostError('The release manifest must pin exactly the app, worker and caddy images.')
    for role, repo in IMAGE_REPOS.items():
        reference = images[role]
        name, _, digest = str(reference).partition('@sha256:')
        if name != repo or not SHA_RE.match(digest):
            raise HostError('The %s image must be pinned as %s@sha256:<digest>; got %r.' % (role, repo, reference))
    download = '%s/download/%s/' % (RELEASE_BASE, version)
    guest = manifest.get('guest')
    guest_keys = {'version', 'url', 'sha256', 'manifest_sha256', 'guest_binary_sha256'}
    if not isinstance(guest, dict) or set(guest) != guest_keys:
        raise HostError('The release manifest guest entry is incomplete.')
    if not VERSION_RE.match(str(guest['version'])):
        raise HostError('The guest version %r is invalid.' % guest['version'])
    for key in ('sha256', 'manifest_sha256', 'guest_binary_sha256'):
        if not SHA_RE.match(str(guest[key])):
            raise HostError('The guest %s is not a SHA-256 digest.' % key)
    bundle = manifest.get('bundle')
    if not isinstance(bundle, dict) or set(bundle) != {'url', 'sha256'} or not SHA_RE.match(str(bundle['sha256'])):
        raise HostError('The release manifest bundle entry is incomplete.')
    guest_download = '%s/download/%s/' % (RELEASE_BASE, guest['version'])
    if not str(guest['url']).startswith(guest_download) or not str(bundle['url']).startswith(download):
        raise HostError('Release artifacts must be downloaded from %s over HTTPS.' % RELEASE_BASE)
    min_host = manifest.get('min_host')
    if not isinstance(min_host, dict):
        raise HostError('The release manifest has no min_host requirements.')
    if not isinstance(manifest.get('data_schema'), str) or not manifest['data_schema']:
        raise HostError('The release manifest has no data_schema.')
    return manifest


def load_manifest_file(path):
    try:
        manifest = json.loads(Path(path).read_text())
    except (OSError, ValueError) as error:
        raise HostError('Cannot read release manifest %s (%s).' % (path, error)) from error
    return validate_manifest(manifest)


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


def preflight(options):
    """Refuse an unsupported host before anything is written; return warnings."""
    warnings = []
    if platform.system() != 'Linux' or platform.machine() not in ('x86_64', 'AMD64'):
        raise HostError('TOFI needs Linux x86_64; this host is %s %s.' % (platform.system(), platform.machine()))
    require_root()
    if not P.dev_kvm.exists() or not stat.S_ISCHR(P.dev_kvm.stat().st_mode):
        raise HostError('KVM is not available (/dev/kvm). Use bare metal or a VM with nested virtualization enabled.')
    if not P.dev_tun.exists() or not stat.S_ISCHR(P.dev_tun.stat().st_mode):
        raise HostError('The TUN device /dev/net/tun is missing; load the tun kernel module (modprobe tun).')
    if not P.cgroup_controllers.exists():
        raise HostError('cgroup v2 is required; boot with systemd.unified_cgroup_hierarchy=1.')
    try:
        apparmor = P.apparmor_enabled.read_text().strip()
    except OSError:
        apparmor = 'missing'
    if apparmor != 'Y':
        raise HostError('AppArmor must be enabled in the kernel (found %s); enable it and reboot.' % apparmor)
    missing = [tool for tool in REQUIRED_TOOLS if not shutil.which(tool)]
    if missing:
        raise HostError('Missing host tools: %s. Re-run install.sh, which installs them.' % ', '.join(missing))
    try:
        info = json.loads(run(['docker', 'info', '--format', '{{json .}}']).stdout)
    except (subprocess.SubprocessError, ValueError) as error:
        raise HostError('Docker is not running; start it with `systemctl start docker`.') from error
    if str(info.get('CgroupVersion')) != '2':
        raise HostError('Docker must use cgroup v2 (found %s).' % info.get('CgroupVersion'))
    if not any('apparmor' in item for item in info.get('SecurityOptions') or []):
        raise HostError('Docker must run with AppArmor enabled (security options: %s).' % info.get('SecurityOptions'))
    try:
        run(['docker', 'compose', 'version'])
    except subprocess.SubprocessError as error:
        raise HostError('Docker Compose v2 is missing; install docker-compose-plugin.') from error
    cpus = os.cpu_count() or 0
    if cpus < MIN_CPUS:
        raise HostError('TOFI needs at least %d vCPUs; this host has %d.' % (MIN_CPUS, cpus))
    memory = meminfo_mib()['MemTotal']
    if memory < MIN_MEMORY_MIB:
        raise HostError('TOFI needs at least 4 GiB of RAM; this host has %d MiB.' % memory)
    if memory < WARN_MEMORY_MIB:
        warnings.append('This host has %d MiB of RAM; 8 GiB or more is recommended.' % memory)
    swap_warning, _ = memory_report()
    if swap_warning:
        warnings.append(swap_warning)
    free = free_disk_bytes()
    if free < MIN_DISK_BYTES:
        raise HostError('TOFI needs at least 30 GiB free under /var/lib; %.1f GiB is free.' % (free / GIB))
    if free < WARN_DISK_BYTES:
        warnings.append('Only %.1f GiB is free under /var/lib; 40 GiB or more is recommended.' % (free / GIB))
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
    if firecracker_running():
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


def render_worker_config(guest_version, cpu_budget, memory_budget_mib, guest_binary_sha256,
                         release_manifest_sha256):
    """Render worker.json for the given Guest release and runtime budgets."""
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
        'vcpus': 1,
        'memory_mib': 1024,
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
    else:
        # Direct mode always serves HTTPS with the host's self-signed certificate.
        bind = options['bind']
        origin = ''
        cert, key = TLS_REAL + '/cert.pem', TLS_REAL + '/key.pem'
        allow_lan_http = '0'
    return {
        'TOFI_VERSION': manifest['version'],
        'TOFI_DOMAIN': domain,
        'TOFI_EMAIL': options.get('email') or '',
        'TOFI_HTTP_PORT': str(options['port']),
        'TOFI_BIND': bind,
        'TOFI_PUBLIC_ORIGIN': origin,
        'TOFI_APP_IMAGE': manifest['images']['app'],
        'TOFI_WORKER_IMAGE': manifest['images']['worker'],
        'TOFI_CADDY_IMAGE': manifest['images']['caddy'],
        'TOFI_GUEST_VERSION': manifest['guest']['version'],
        'TOFI_CPU_BUDGET': str(budgets['cpu']),
        'TOFI_MEMORY_BUDGET_MIB': str(budgets['memory_mib']),
        'TOFI_WORKER_MEMORY_LIMIT': '%dm' % (budgets['memory_mib'] + 256),
        'TOFI_TLS_CERT_FILE': cert,
        'TOFI_TLS_KEY_FILE': key,
        'TOFI_OWNER_ALLOW_LAN_HTTP': allow_lan_http,
        'TOFI_COMPUTER_BACKEND': options.get('computer') or 'kvm',
    }


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


def apply_host_config(bundle, env):
    """Write worker.json, seccomp, AppArmor, tmpfiles, Caddyfile and unit from `bundle`."""
    bundle = Path(bundle)
    guest_dir = P.guest / env['TOFI_GUEST_VERSION']
    release_manifest = guest_dir / 'account-release.json'
    guest_manifest = json.loads(release_manifest.read_text())
    config = render_worker_config(env['TOFI_GUEST_VERSION'], env['TOFI_CPU_BUDGET'],
                                  env['TOFI_MEMORY_BUDGET_MIB'], guest_manifest['guest_binary_sha256'],
                                  sha256_file(release_manifest))
    ensure_tls(env)
    write_env(env)
    write_json(P.worker_json, config, 0o600)
    write_file(P.seccomp, (bundle / 'worker.seccomp.json').read_bytes(), 0o600)
    if env.get('TOFI_DOMAIN'):
        write_file(P.caddyfile, render_caddyfile(bundle, env['TOFI_DOMAIN'], env.get('TOFI_EMAIL')), 0o644)
    profile = render_apparmor(bundle)
    candidate = P.etc / '.worker.apparmor.candidate'
    write_file(candidate, profile, 0o644)
    try:
        run(['apparmor_parser', '-Q', '-T', str(candidate)])
    finally:
        candidate.unlink(missing_ok=True)
    write_file(P.apparmor_profile, profile, 0o644)
    run(['apparmor_parser', '-r', '-W', str(P.apparmor_profile)])
    write_file(P.tmpfiles, (bundle / 'tofi.conf').read_text(), 0o644)
    run(['systemd-tmpfiles', '--create', str(P.tmpfiles)])
    write_file(P.unit, (bundle / 'tofi.service').read_text(), 0o644)
    run(['systemctl', 'daemon-reload'], check=False)
    return config


def prepare_directories():
    for path, mode in [(P.etc, 0o700), (P.var, 0o755), (P.worker, 0o700), (P.guest, 0o755),
                       (P.caddy, 0o700)]:
        path.mkdir(parents=True, exist_ok=True)
        os.chmod(path, mode)
    for name in ('state', 'config', 'ledger', 'unused-units'):
        child = P.worker / name
        child.mkdir(exist_ok=True)
        os.chmod(child, 0o700)
    for name in ('data', 'config'):
        (P.caddy / name).mkdir(exist_ok=True)
    P.data.mkdir(exist_ok=True)
    os.chown(P.data, APP_UID, APP_UID)
    os.chmod(P.data, 0o700)


# --------------------------------------------------------------------------
# Images and Guest release


def inspect_image(reference, role, data_schema=None):
    """Check a pulled image's platform, entrypoint and TOFI labels."""
    try:
        data = json.loads(run(['docker', 'image', 'inspect', reference]).stdout)[0]
    except (subprocess.SubprocessError, ValueError, IndexError) as error:
        raise HostError('Image %s is not available locally.' % reference) from error
    if data.get('Architecture') != 'amd64' or data.get('Os') != 'linux':
        raise HostError('Image %s is %s/%s; linux/amd64 is required.' % (reference, data.get('Os'), data.get('Architecture')))
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
    roles = [('app', env['TOFI_APP_IMAGE']), ('worker', env['TOFI_WORKER_IMAGE'])]
    if env.get('TOFI_DOMAIN'):
        roles.append(('caddy', env['TOFI_CADDY_IMAGE']))
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


def compose(*args, env=None, timeout=600):
    env = env if env is not None else read_env()
    command = ['docker', 'compose', '--project-name', PROJECT, '--env-file', str(P.env_file),
               '-f', str(compose_file())]
    if env.get('TOFI_DOMAIN'):
        command += ['--profile', 'tls']
    return run(command + list(args), timeout=timeout)


def project_containers():
    ids = run(['docker', 'ps', '-a', '-q', '--filter',
               'label=com.docker.compose.project=' + PROJECT]).stdout.split()
    if not ids:
        return []
    return json.loads(run(['docker', 'inspect'] + ids).stdout)


def service_of(container):
    return ((container.get('Config') or {}).get('Labels') or {}).get('com.docker.compose.service')


def stopped():
    """Stop every project container and prove the Worker and App exited cleanly."""
    containers = project_containers()
    if containers:
        run(['docker', 'stop', '--time', '180'] + [c['Id'] for c in containers], timeout=600)
    for container in project_containers():
        state = container['State']
        service = service_of(container)
        if state.get('Running') or state.get('Restarting'):
            raise HostError('Container %s is still running after stop; inspect it with `docker ps`.' % service)
        if service in ('app', 'worker') and state.get('ExitCode') not in (0, None):
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
    for _ in range(attempts):
        try:
            with opener.open(url, timeout=2) as response:
                if json.loads(response.read(65536)).get('ok') is True:
                    return True
        except (OSError, ValueError):
            pass
        time.sleep(1)
    raise HostError('The App did not report healthy within %d s; see `tofi logs app`.' % attempts)


def start_services(env, first_install=False, state=None):
    compose('up', '-d', '--no-build', '--pull', 'never', '--remove-orphans', 'worker', env=env)
    worker_ready(first_install=first_install)
    if state is not None and state.get('transaction') and state['transaction']['kind'] == 'install':
        transition(state, 'installing', 'start-app')
    services = ['app', 'caddy'] if env.get('TOFI_DOMAIN') else ['app']
    compose('up', '-d', '--no-build', '--pull', 'never', *services, env=env)
    health(env)


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
                if (options.get('domain') or options.get('lan') or options.get('local_only')
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


def fresh_install(manifest, options):
    progress = tofi_tui.Progress(tofi_tui.UI(sys.stdout))
    try:
        progress.begin(10, 'Preparing host configuration, images and the computer release')
        for message in preflight(options):
            warn(message)
        bundle = ASSETS
        budgets = runtime_budgets()
        env = render_env(manifest, options, budgets)
        prepare_directories()
        pull_images(env, manifest['data_schema'])
        fetch_guest(manifest['guest'], bundle_manager(bundle))
        apply_host_config(bundle, env)
        state = new_state(manifest['version'])
        save_state(state)
        progress.begin(11, 'Starting the Worker, then the App')
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
            'local_only': not env.get('TOFI_DOMAIN') and env.get('TOFI_BIND') != '0.0.0.0'}
    sys.stdout.write(render_banner(info, color_mode(sys.stdout), terminal_width()))
    sys.stdout.flush()
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


def stop():
    with lifecycle_lock():
        if load_state() is None:
            raise HostError('TOFI is not installed.')
        stopped()
        return {'stopped': True, 'data_retained': True}


def snapshot():
    """What rollback needs to restore the running version exactly."""
    return {
        'env': read_env(),
        'worker_json': json.loads(P.worker_json.read_text()),
        'bundle': current_release(),
    }


def upgrade(version=None, manifest_path=None, allow_schema_change=False):
    with lifecycle_lock():
        state = load_state()
        if state is None or state['phase'] != 'installed':
            raise HostError('Update needs an installed TOFI in phase installed; run `sudo tofi install` first.')
        manifest = load_manifest_file(manifest_path) if manifest_path else fetch_manifest(version)
        previous = snapshot()
        if manifest['version'] == previous['env']['TOFI_VERSION']:
            say('TOFI %s is already installed.' % manifest['version'])
            return {'upgraded': False, 'version': manifest['version']}
        candidate = dict(previous['env'])
        candidate.update({
            'TOFI_VERSION': manifest['version'],
            'TOFI_APP_IMAGE': manifest['images']['app'],
            'TOFI_WORKER_IMAGE': manifest['images']['worker'],
            'TOFI_CADDY_IMAGE': manifest['images']['caddy'],
            'TOFI_GUEST_VERSION': manifest['guest']['version'],
        })
        # Everything that can be checked happens before anything is stopped.
        pull_images(candidate, None)
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
        fetch_guest(manifest['guest'], bundle_manager(bundle))
        announce_computer_impact(previous['env'], manifest)
        state['transaction'] = {'kind': 'upgrade', 'step': 'stop-old', 'previous': previous,
                                'candidate_version': manifest['version']}
        state['phase'] = 'upgrading'
        save_state(state)
        try:
            stopped()
            transition(state, 'upgrading', 'switch-config')
            apply_host_config(bundle, candidate)
            switch_current(manifest['version'])
            transition(state, 'upgrading', 'start-candidate')
            start_services(candidate)
            complete(state, version=manifest['version'])
        except BaseException as error:
            failed(state, 'upgrade-failed', error)
            try:
                rollback(state)
            except BaseException as rollback_error:
                raise HostError('Update failed (%s) and rollback failed (%s); data is retained. '
                                'Run `sudo tofi install` to retry.' % (error, rollback_error)) from error
            # complete() cleared last_error; keep a record `tofi status` shows,
            # and drop the rejected candidate's bundle and Guest release.
            state['last_update_failure'] = {'version': manifest['version'], 'message': str(error)[:500],
                                            'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
            save_state(state)
            discard_candidate(manifest['version'], candidate['TOFI_GUEST_VERSION'], previous)
            raise HostError('update rejected; previous version restored (%s)' % error) from error
        state.pop('last_update_failure', None)
        save_state(state)
        prune_releases(keep=[manifest['version'], previous['bundle']],
                       keep_guests=[candidate['TOFI_GUEST_VERSION'], previous['env']['TOFI_GUEST_VERSION']])
        say('Updated to %s.' % manifest['version'])
        older = sorted(guest_references() - {candidate['TOFI_GUEST_VERSION']})
        if older:
            say('Hibernated computers still hold snapshots from %s: each cold-boots on %s at its next use. '
                'See `sudo tofi computers`; switch them now with `sudo tofi computers upgrade --all`.'
                % (', '.join(older), candidate['TOFI_GUEST_VERSION']))
        return {'upgraded': True, 'version': manifest['version'], 'data_retained': True}


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
    if guest_version != previous['env'].get('TOFI_GUEST_VERSION'):
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
    """Restore the previous images, Guest pin and host config with CURRENT data."""
    previous = state['transaction']['previous']
    env = previous['env']
    try:
        transition(state, 'rolling-back', 'stop-candidate')
        stopped()
        transition(state, 'rolling-back', 'restore-config')
        bundle = P.releases / previous['bundle'] if previous.get('bundle') else ASSETS
        apply_host_config(bundle, env)
        write_json(P.worker_json, previous['worker_json'])
        if previous.get('bundle'):
            switch_current(previous['bundle'])
        transition(state, 'rolling-back', 'start-previous')
        start_services(env)
        complete(state, version=env['TOFI_VERSION'])
    except BaseException as error:
        failed(state, 'rollback-failed', error)
        raise
    return {'recovered': True, 'version': env['TOFI_VERSION'], 'data_retained': True}


def prune_releases(keep, keep_guests):
    """Drop host bundles outside `keep`, and Guest releases outside
    `keep_guests` that no hibernated computer's snapshot was taken with."""
    for entry in P.releases.iterdir() if P.releases.is_dir() else []:
        if entry.name.startswith('.') or entry.name in keep:
            continue
        remove_tree(entry)
    prune_guests(keep_guests)


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
        stopped()
        transition(state, 'uninstalling', 'remove-services')
        if compose_file().exists():
            compose('rm', '-f', '-s')
        complete(state, 'stopped-retained')
    except BaseException as error:
        failed(state, 'uninstall-failed', error)
        raise
    say('TOFI is stopped and its containers are removed. Kept: /etc/tofi, /var/lib/tofi, images, AppArmor profile.')
    say('Re-run install.sh to start again with the same data, or `sudo tofi uninstall --purge` to delete everything.')
    return {'uninstalled': True, 'data_retained': True}


def purge_everything(state, confirm):
    hostname = socket.gethostname()
    if confirm is None:
        try:
            with open('/dev/tty') as tty:
                print('This permanently deletes all TOFI accounts, conversations and computer disks.')
                print('Type the hostname (%s) to confirm: ' % hostname, end='', flush=True)
                confirm = tty.readline().strip()
        except OSError:
            confirm = ''
    if confirm != hostname:
        raise HostError('Purge cancelled: the typed hostname did not match %s.' % hostname)
    if state is not None:
        stopped()
    if compose_file().exists() and P.env_file.exists():
        compose('down', '--remove-orphans')
    images = []
    if P.env_file.exists():
        env = read_env()
        images = [env.get(k) for k in ('TOFI_APP_IMAGE', 'TOFI_WORKER_IMAGE', 'TOFI_CADDY_IMAGE') if env.get(k)]
    for reference in images:
        run(['docker', 'image', 'rm', reference], check=False)
    run(['systemctl', 'disable', 'tofi.service'], check=False)
    if P.apparmor_profile.exists():
        run(['apparmor_parser', '-R', str(P.apparmor_profile)], check=False)
    for path in (P.unit, P.tmpfiles, P.apparmor_profile, P.bin_link):
        if path.is_symlink() or path.exists():
            path.unlink()
    run(['systemctl', 'daemon-reload'], check=False)
    for path in (P.var, P.etc, P.run_dir, P.opt, P.latest_cache.parent):
        remove_tree(path)
    say('TOFI and all of its data were removed.')
    return {'purged': True}


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
        try:
            result['urls'] = access_urls(env)
            if app_scheme(env) == 'https':
                result['certificate_sha256'] = certificate_fingerprint()
        except (OSError, HostError):
            pass
    if details and env is not None:
        result['versions'] = versions_report(env)
        if state.get('last_computers_upgrade'):
            result['last_computers_upgrade'] = state['last_computers_upgrade']
        try:
            result['computers'] = computers_report(env.get('TOFI_GUEST_VERSION'))
        except (HostError, OSError, ValueError, subprocess.SubprocessError) as error:
            result['computers_error'] = str(error)
    return result


def render_status(result, with_access=True):
    """`tofi status` for people (`--json` prints `result` itself)."""
    if not result.get('phase'):
        return 'TOFI is not installed. Install it with install.sh.\n'
    versions = result.get('versions') or {}
    lines = ['TOFI %s · %s · %s' % (result.get('version') or '?', result['phase'],
                                    'healthy' if result.get('healthy') else 'NOT healthy')]

    def row(label, value):
        lines.append('  %-12s %s' % (label, value))

    if versions:
        latest = versions.get('latest')
        if latest is None:
            text = 'unknown (%s)' % (versions.get('latest_error') or 'not checked')
        elif versions.get('update_available'):
            text = '%s available · sudo tofi update --check' % latest
        else:
            text = '%s (up to date)' % latest
        if latest and versions.get('latest_error'):
            text += ' · checked %s, offline now' % versions.get('latest_checked_at')
        row('Latest', text)
        row('App', short_image(versions.get('app_image')))
        row('Worker', short_image(versions.get('worker_image')))
        row('Guest', versions.get('guest') or '-')
    services = result.get('services')
    if isinstance(services, dict):
        row('Services', ' · '.join('%s %s' % item for item in sorted(services.items())) or 'none running')
    elif services:
        row('Services', services)
    if with_access:
        for index, url in enumerate(result.get('urls') or []):
            row('Open' if index == 0 else '', url)
        if result.get('certificate_sha256'):
            row('Certificate', 'SHA256 ' + result['certificate_sha256'])
        if result.get('setup_key_pending'):
            row('Setup key', 'pending · sudo tofi setup-secret')
    if result.get('transaction'):
        row('Pending', '%(kind)s at step %(step)s · sudo tofi install resumes it' % result['transaction'])
    if result.get('last_error'):
        row('Last error', result['last_error'].get('message'))
    if result.get('last_update_failure'):
        failure = result['last_update_failure']
        row('Update', 'to %s rejected at %s: %s' % (failure.get('version'), failure.get('at'), failure.get('message')))
    if 'computers' in result or 'computers_error' in result:
        lines.append('')
        if 'computers' in result:
            lines.append('Computers (%d)' % len(result['computers']))
            lines += render_computers(result['computers'], versions.get('guest'))
        else:
            lines.append('Computers    unknown (%s)' % result['computers_error'])
    return '\n'.join(lines) + '\n'


def status_banner():
    """The install banner as a `tofi status` header (terminals only)."""
    try:
        env = read_env()
    except (OSError, HostError):
        return ''
    info = {'version': env.get('TOFI_VERSION', ''), 'urls': access_urls(env), 'setup_key': read_setup_secret(),
            'domain': env.get('TOFI_DOMAIN', ''), 'port': env.get('TOFI_HTTP_PORT') or str(DEFAULT_PORT),
            'fingerprint': certificate_fingerprint() if app_scheme(env) == 'https' else None,
            'local_only': not env.get('TOFI_DOMAIN') and env.get('TOFI_BIND') != '0.0.0.0'}
    return render_banner(info, color_mode(sys.stdout), terminal_width())


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
    match = re.match(r'^v(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$', str(version or ''))
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
                  ('app', env.get('TOFI_APP_IMAGE'), manifest['images']['app']),
                  ('worker', env.get('TOFI_WORKER_IMAGE'), manifest['images']['worker']),
                  ('guest', env.get('TOFI_GUEST_VERSION'), manifest['guest']['version'])]
    if env.get('TOFI_DOMAIN'):
        components.append(('caddy', env.get('TOFI_CADDY_IMAGE'), manifest['images']['caddy']))
    try:
        installed_schema = image_label(env['TOFI_APP_IMAGE'], DATA_SCHEMA_LABEL)
    except (OSError, ValueError, IndexError, KeyError, subprocess.SubprocessError):
        installed_schema = None
    schema = {'installed': installed_schema, 'available': manifest['data_schema'],
              'compatible': None if installed_schema is None else installed_schema == manifest['data_schema']}
    report = {'installed': env.get('TOFI_VERSION'), 'available': manifest['version'],
              'update_available': manifest['version'] != env.get('TOFI_VERSION'),
              'downgrade': is_newer(env.get('TOFI_VERSION'), manifest['version']),
              'components': [{'name': name, 'installed': old, 'available': new, 'changes': old != new}
                             for name, old, new in components],
              'data_schema': schema,
              'apply': 'sudo tofi update' + (' --version ' + manifest['version'] if version else
                                             ' --manifest ' + str(manifest_path) if manifest_path else '')}
    if not schema['compatible'] and report['update_available']:
        report['apply'] += ' --allow-schema-change'
    try:
        report['computers'] = computers_report(env.get('TOFI_GUEST_VERSION'), busy=True)
    except HostError as error:
        report['computers'] = None
        report['computers_error'] = str(error)
    return report


def render_update_check(report):
    state = 'up to date' if not report['update_available'] else (
        'older release' if report['downgrade'] else 'update available')
    lines = ['Installed    %s' % report['installed'], 'Available    %s  (%s)' % (report['available'], state), '']
    labels = {'version': 'Host bundle', 'app': 'App image', 'worker': 'Worker image',
              'guest': 'Guest release', 'caddy': 'Caddy image'}
    table = []
    for item in report['components']:
        show = short_digest if item['name'] in ('app', 'worker', 'caddy') else str
        table.append([labels[item['name']], show(item['installed']), show(item['available']),
                      'changes' if item['changes'] else 'same'])
    schema = report['data_schema']
    verdict = {True: 'compatible', False: 'CHANGES: back up /var/lib/tofi; needs --allow-schema-change',
               None: 'installed schema unknown'}[schema['compatible']]
    table.append(['Data schema', schema['installed'] or '?', schema['available'], verdict])
    lines += format_table(['COMPONENT', 'INSTALLED', 'AVAILABLE', ''], table)
    if report['update_available']:
        lines.append('')
        if report.get('computers') is None:
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
    return [computer_row(row, answers.get(index, {}), installed_guest, busy) for index, row in enumerate(rows)]


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
        backend = read_env().get('TOFI_COMPUTER_BACKEND') or 'kvm'
        if backend not in COMPUTER_BACKENDS:
            raise HostError('unknown TOFI_COMPUTER_BACKEND=%s in /etc/tofi/tofi.env' % backend)
        if COMPUTER_BACKENDS[backend]:
            raise HostError('%s is %s; set TOFI_COMPUTER_BACKEND=kvm' % (backend, COMPUTER_BACKENDS[backend]))
        return 'kvm (Firecracker)'

    host_card()
    check('KVM device', kvm)
    check('Computer backend', computer_backend)
    check('cgroup v2', lambda: P.cgroup_controllers.read_text() and None)
    check('AppArmor profile', apparmor_loaded)
    check('/run/tofi ownership', run_dir)
    check('Worker config', lambda: validate_config(json.loads(P.worker_json.read_text())) and None)
    check('Guest release', guest)
    check('Images', images)
    check('Disk', disk)
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
    ui.card('This machine', tofi_tui.machine_rows(facts, fresh=facts['phase'] is None))
    return facts


def version():
    info = {'host_bundle': (ASSETS / 'VERSION').read_text().strip() if (ASSETS / 'VERSION').exists() else 'source'}
    try:
        env = read_env()
        info.update(installed=env.get('TOFI_VERSION'), app_image=env.get('TOFI_APP_IMAGE'),
                    worker_image=env.get('TOFI_WORKER_IMAGE'), guest=env.get('TOFI_GUEST_VERSION'))
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
    install_parser.add_argument('--email')
    install_parser.add_argument('--local-only', action='store_true',
                                help='listen on 127.0.0.1 only (still HTTPS); reach it with an SSH tunnel')
    install_parser.add_argument('--lan', action='store_true', help=argparse.SUPPRESS)
    install_parser.add_argument('--port', type=int)
    install_parser.add_argument('--computer', default='kvm',
                                help='how account computers run: kvm (Firecracker); gvisor and container '
                                     'are not supported yet')
    install_parser.add_argument('--yes', action='store_true')
    status_parser = sub.add_parser('status', help='versions, services, health and account computers')
    status_parser.add_argument('--json', action='store_true', help='machine-readable output')
    sub.add_parser('start')
    sub.add_parser('stop')
    update_parser = sub.add_parser('update')
    update_parser.add_argument('--version')
    update_parser.add_argument('--manifest', help='use a local manifest instead of GitHub Releases')
    update_parser.add_argument('--allow-schema-change', action='store_true')
    update_parser.add_argument('--check', action='store_true',
                               help='report what would change and exit 0 (up to date), 10 (update available) '
                                    'or 1 (error); changes nothing')
    update_parser.add_argument('--json', action='store_true', help='with --check: machine-readable output')
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
    uninstall_parser = sub.add_parser('uninstall')
    uninstall_parser.add_argument('--purge', action='store_true')
    uninstall_parser.add_argument('--confirm-hostname', help='non-interactive purge confirmation')
    sub.add_parser('setup-secret')
    sub.add_parser('regenerate-cert', help='replace the self-signed HTTPS certificate and restart the App')
    logs_parser = sub.add_parser('logs')
    logs_parser.add_argument('service', nargs='?', choices=['app', 'worker', 'caddy'])
    sub.add_parser('doctor')
    sub.add_parser('version')
    return parser.parse_args(argv)


def install_options(args):
    port = args.port if args.port is not None else DEFAULT_PORT
    if not 1024 <= port <= 65535:
        raise HostError('--port must be between 1024 and 65535.')
    local_only = bool(getattr(args, 'local_only', False))
    if args.domain and (args.lan or local_only):
        raise HostError('--domain already decides how TOFI is reached; drop --lan/--local-only.')
    if args.lan and local_only:
        raise HostError('Use either --lan or --local-only, not both.')
    if args.email and not args.domain:
        raise HostError('--email is only used with --domain.')
    computer = (getattr(args, 'computer', None) or 'kvm').lower()
    if computer not in COMPUTER_BACKENDS:
        raise HostError('--computer must be kvm, gvisor or container.')
    if COMPUTER_BACKENDS[computer]:
        raise HostError('--computer %s is %s; only kvm (Firecracker) is available.'
                        % (computer, COMPUTER_BACKENDS[computer]))
    domain = (args.domain or '').lower().rstrip('.')
    if domain and not DOMAIN_RE.match(domain):
        raise HostError('%r is not a valid domain name.' % args.domain)
    # Default: HTTPS with a self-signed certificate on every interface.
    # --lan is the old name for that default and is kept as an alias.
    return {'domain': domain, 'email': args.email or '', 'lan': args.lan, 'local_only': local_only,
            'port': port, 'port_given': args.port is not None,
            'bind': '127.0.0.1' if local_only else '0.0.0.0', 'yes': args.yes, 'computer': computer}


def main(argv=None):
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        if args.command == 'install':
            if not args.manifest and load_state() is None:
                raise HostError('A fresh install needs --manifest; use install.sh.')
            install(args.manifest, install_options(args))
            return 0
        if args.command == 'status':
            result = status(details=True)
            if args.json:
                print(json.dumps(result, indent=2))
                return 0
            banner = result.get('phase') == 'installed' and color_mode(sys.stdout, {}) is not None
            if banner:
                # Interactive terminal: the install banner (URLs, setup key) first.
                sys.stdout.write(status_banner() + '\n')
            sys.stdout.write(render_status(result, with_access=not banner))
            return 0
        if args.command == 'computers':
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
            return 0
        if args.command == 'stop':
            stop()
            return 0
        if args.command == 'update':
            if args.check:
                report = check_update(args.version, args.manifest)
                sys.stdout.write(json.dumps(report, indent=2) + '\n' if args.json else render_update_check(report))
                return UPDATE_AVAILABLE_EXIT if report['update_available'] else 0
            if args.json:
                raise HostError('--json is only used with --check.')
            upgrade(args.version, args.manifest, args.allow_schema_change)
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
            command = ['docker', 'compose', '--project-name', PROJECT, '--env-file', str(P.env_file),
                       '-f', str(compose_file()), '--profile', 'tls', 'logs', '--tail', '200', '-f']
            if args.service:
                command.append(args.service)
            os.execvp('docker', command)
        if args.command == 'doctor':
            return 0 if doctor() else 1
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


if __name__ == '__main__':
    sys.exit(main())
