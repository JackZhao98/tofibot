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

from account_release_check import PROTOCOL, validate_release  # noqa: E402
from worker_entrypoint import validate_config  # noqa: E402

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
            'TOFI_TLS_KEY_FILE', 'TOFI_OWNER_ALLOW_LAN_HTTP']


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
    print(message, flush=True)


def warn(message):
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


def fetch_manifest(version=None):
    url = manifest_url(version)
    try:
        with https_open(url) as response:
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
    say('[10/12] Preparing host configuration, images and the computer release')
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
    say('[11/12] Starting the Worker, then the App')
    finish_install(state, env)
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

BANNER_ART = [
    '   ▄▄              ▄▄▄  ▄▄        ▄▀▄   ▄▀▄',
    ' ▀▀██▀▀  ▄▄▄▄▄   ▄██▀   ▀▀       █  ▀▀▀▀▀  █',
    '   ██   ██▀  ▀██ ▀██▀▀  ██       █  ●   ●  █',
    '   ██▄▄ ██▄  ▄██  ██    ██        ▀▄▄▄▄▄▄▄▀',
    '    ▀▀▀  ▀▀▀▀▀    ▀▀    ▀▀   ',
]
# Column ranges of the letters t, o, f, i and the cat in BANNER_ART.
BANNER_REGIONS = [(0, 8, 'cream'), (8, 17, 'peach'), (17, 23, 'cream'), (23, 28, 'cream'), (28, 99, 'cat')]
BANNER_MIN_WIDTH = 60
TAGLINE = 'your crew is awake'
COLORS = {  # (truecolor RGB, 256-color index)
    'cream': ((0xF3, 0xEA, 0xDB), 230),
    'peach': ((0xE8, 0x95, 0x6D), 209),
    'teal': ((0x7F, 0xD1, 0xC1), 115),
}


def color_mode(stream, environ=None):
    """'truecolor', '256' or None (plain text: NO_COLOR, TERM=dumb, not a tty)."""
    environ = os.environ if environ is None else environ
    if 'NO_COLOR' in environ or environ.get('TERM') == 'dumb':
        return None
    try:
        if not stream.isatty():
            return None
    except (AttributeError, ValueError):
        return None
    if environ.get('COLORTERM', '').lower() in ('truecolor', '24bit'):
        return 'truecolor'
    return '256'


def terminal_width():
    return shutil.get_terminal_size((80, 24)).columns


class Paint:
    def __init__(self, mode):
        self.mode = mode

    def _wrap(self, codes, text):
        if not self.mode or not text:
            return text
        return '\033[%sm%s\033[0m' % (';'.join(codes), text)

    def fg(self, name):
        rgb, index = COLORS[name]
        if self.mode == 'truecolor':
            return '38;2;%d;%d;%d' % rgb
        return '38;5;%d' % index

    def color(self, name, text, *extra):
        return self._wrap(list(extra) + [self.fg(name)], text)

    def dim(self, text):
        return self._wrap(['2'], text)

    def bold(self, text):
        return self._wrap(['1'], text)

    def link(self, text):
        return self._wrap(['1', '4', self.fg('teal')], text)


def render_art(paint, version):
    lines = []
    for number, row in enumerate(BANNER_ART):
        out = []
        for start, end, role in BANNER_REGIONS:
            part = row[start:end]
            if role == 'cat':
                # Outline peach, eyes teal; spaces stay uncoloured.
                out.append(re.sub(r'●|[^ ●]+', lambda m: paint.color(
                    'teal' if m.group() == '●' else 'peach', m.group()), part))
            else:
                out.append(re.sub(r'\S+', lambda m: paint.color(role, m.group()), part))
        line = ''.join(out)
        if number == len(BANNER_ART) - 1:
            line += paint.dim('v%s · %s' % (version.lstrip('v'), TAGLINE))
        lines.append(line.rstrip())
    return lines


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
        return {'upgraded': True, 'version': manifest['version'], 'data_retained': True}


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
    for entry in P.releases.iterdir() if P.releases.is_dir() else []:
        if entry.name.startswith('.') or entry.name in keep:
            continue
        remove_tree(entry)
    for entry in P.guest.iterdir() if P.guest.is_dir() else []:
        if entry.name.startswith('.') or entry.name in keep_guests:
            continue
        remove_tree(entry)


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
    for path in (P.var, P.etc, P.run_dir, P.opt):
        remove_tree(path)
    say('TOFI and all of its data were removed.')
    return {'purged': True}


def status():
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
        result['urls'] = access_urls(env)
        if app_scheme(env) == 'https':
            result['certificate_sha256'] = certificate_fingerprint()
    except (OSError, HostError):
        pass
    return result


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

    check('KVM device', kvm)
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
    install_parser.add_argument('--yes', action='store_true')
    sub.add_parser('status')
    sub.add_parser('start')
    sub.add_parser('stop')
    update_parser = sub.add_parser('update')
    update_parser.add_argument('--version')
    update_parser.add_argument('--manifest', help='use a local manifest instead of GitHub Releases')
    update_parser.add_argument('--allow-schema-change', action='store_true')
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
    domain = (args.domain or '').lower().rstrip('.')
    if domain and not DOMAIN_RE.match(domain):
        raise HostError('%r is not a valid domain name.' % args.domain)
    # Default: HTTPS with a self-signed certificate on every interface.
    # --lan is the old name for that default and is kept as an alias.
    return {'domain': domain, 'email': args.email or '', 'lan': args.lan, 'local_only': local_only,
            'port': port, 'port_given': args.port is not None,
            'bind': '127.0.0.1' if local_only else '0.0.0.0', 'yes': args.yes}


def main(argv=None):
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        if args.command == 'install':
            if not args.manifest and load_state() is None:
                raise HostError('A fresh install needs --manifest; use install.sh.')
            install(args.manifest, install_options(args))
            return 0
        if args.command == 'status':
            result = status()
            if result.get('phase') == 'installed' and color_mode(sys.stdout, {}) is not None:
                # Interactive terminal: banner first; pipes get the JSON only.
                sys.stdout.write(status_banner() + '\n')
            print(json.dumps(result, indent=2))
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
