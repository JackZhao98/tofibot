#!/usr/bin/env python3
"""TOFI terminal UI, shared by install.sh and the `tofi` host tool.

- The palette and colour rules (truecolor -> 256 colours -> plain text) and the
  tofi art used by the end-of-install banner.
- Host detection and the "This machine" card: printed by install.sh before it
  asks anything or changes the host, and by `tofi doctor`.
- The interactive installer: questions are read from /dev/tty (stdin is the
  curl pipe); the answers go to a KEY=value file that install.sh reads.
- One-line-per-step progress with a spinner, used by `tofi install`.

install.sh runs before anything is downloaded, so it carries an exact copy of
this file between its TOFI_TUI_PY markers; test_install_sh.py fails when the
copy drifts. After editing this file run:

    python3 deploy/self-host/tofi_tui.py embed install.sh

Standard library only (Python 3.8+), no host changes: everything here reads.
"""
import argparse
import ipaddress
import json
import os
import re
import select
import shutil
import stat
import subprocess
import sys
import textwrap
import threading
import time

RELEASES_URL = 'https://github.com/JackZhao98/tofibot/releases'
RELEASES_API = 'https://api.github.com/repos/JackZhao98/tofibot/releases'
PUBLIC_IP_URL = 'https://cloudflare.com/cdn-cgi/trace'
INSTALL_COMMAND = 'curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh | sudo bash'
DEFAULT_PORT = 8321
TOTAL_STEPS = 12
MAX_WIDTH = 76      # the installer never draws wider than this
NARROW = 60         # below this: no art, no frames, plain rows
MAX_TRIES = 5

MIN_CPUS = 2
MIN_MEMORY_MIB = 3584      # "4 GiB" machines report slightly less in MemTotal
WARN_MEMORY_MIB = 7680
SWAP_ADVISED_BELOW_MIB = 15872
MIN_DISK_GIB = 30
WARN_DISK_GIB = 40
SUPPORTED_SYSTEMS = {('ubuntu', '22.04'), ('ubuntu', '24.04'), ('debian', '12'), ('debian', '13')}

VERSION_RE = re.compile(r'^v\d+\.\d+\.\d+(-rc\.\d+)?$')
DOMAIN_RE = re.compile(r'^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$')
EMAIL_RE = re.compile(r'^[^@\s]+@[^@\s]+\.[^@\s]+$')
VIRTUAL_INTERFACE_PREFIXES = ('lo', 'docker', 'br-', 'veth', 'virbr', 'tap', 'fc', 'tun', 'cni', 'flannel')

INSTALL_PHASES = {'prepared', 'installing', 'install-failed'}
UPGRADE_PHASES = {'upgrading', 'upgrade-failed', 'rolling-back', 'rollback-failed'}
UNINSTALL_PHASES = {'uninstalling', 'uninstall-failed'}

CANCELLED = 'Cancelled — nothing was changed.'
TOO_MANY = 'Too many invalid answers — cancelled, nothing was changed.'
NO_ANSWER = 'No answer: the terminal closed (end of input) — cancelled, nothing was changed.'
NOT_YET = 'not supported yet (coming in a later release)'

# --------------------------------------------------------------------------
# Palette, art and colour rules

BANNER_ART = [
    '   ▄▄              ▄▄▄  ▄▄        ▄▀▄   ▄▀▄',
    ' ▀▀██▀▀  ▄▄▄▄▄   ▄██▀   ▀▀       █  ▀▀▀▀▀  █',
    '   ██   ██▀  ▀██ ▀██▀▀  ██       █  ●   ●  █',
    '   ██▄▄ ██▄  ▄██  ██    ██        ▀▄▄▄▄▄▄▄▀',
    '    ▀▀▀  ▀▀▀▀▀    ▀▀    ▀▀   ',
]
# Column ranges of the letters t, o, f, i and the cat in BANNER_ART.
BANNER_REGIONS = [(0, 8, 'cream'), (8, 17, 'peach'), (17, 23, 'cream'), (23, 28, 'cream'), (28, 99, 'cat')]
BANNER_MIN_WIDTH = NARROW
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
    if not isatty(stream):
        return None
    if environ.get('COLORTERM', '').lower() in ('truecolor', '24bit'):
        return 'truecolor'
    return '256'


def isatty(stream):
    try:
        return stream.isatty()
    except (AttributeError, ValueError):
        return False


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

    def status(self, kind, text):
        """ok: teal, warn: peach, bad/info/off: dim grey."""
        if kind == 'ok':
            return self.color('teal', text)
        if kind == 'warn':
            return self.color('peach', text, '1')
        if kind in ('bad', 'info', 'off'):
            return self.dim(text)
        return text


def render_art(paint, version=None, note=None):
    """The 5-line tofi wordmark and cat; `note` (dim) ends the last line."""
    if note is None:
        note = 'v%s · %s' % (str(version or '').lstrip('v'), TAGLINE)
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
            line += paint.dim(note)
        lines.append(line.rstrip())
    return lines


UNICODE = {'ok': '✓', 'warn': '!', 'bad': '✗', 'info': '·', 'off': '✗', 'pointer': '❯', 'dot': '·',
           'box': '╭╮╰╯─│', 'spinner': '⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏', 'arrows': '↑/↓'}
ASCII = {'ok': '+', 'warn': '!', 'bad': 'x', 'info': '-', 'off': 'x', 'pointer': '>', 'dot': '-',
         'box': '++++-|', 'spinner': '|/-\\', 'arrows': 'up/down'}


class UI:
    """Where and how to draw: colours, glyphs, frames and width for one stream."""

    def __init__(self, out=None, environ=None, width=None):
        self.out = out or sys.stdout
        environ = os.environ if environ is None else environ
        self.mode = color_mode(self.out, environ)
        self.paint = Paint(self.mode)
        # Frames, spinners and cursor movement only on a real terminal.
        self.fancy = isatty(self.out) and environ.get('TERM') != 'dumb'
        encoding = (getattr(self.out, 'encoding', None) or '').lower().replace('-', '')
        self.g = UNICODE if (encoding.startswith('utf') or not encoding) else ASCII
        self.width = max(20, min(MAX_WIDTH, width or terminal_width()))
        self.lock = threading.RLock()

    def write(self, text=''):
        with self.lock:
            clear_spinner_line()
            self.out.write(text + '\n')
            self.out.flush()

    def wrap(self, text, width, indent=0):
        """Wrap plain `text` to `width`; continuation lines get `indent` spaces."""
        width = max(12, width)
        first = textwrap.wrap(text, width, break_long_words=True, break_on_hyphens=False) or ['']
        if len(first) == 1 or not indent:
            return first
        rest = textwrap.wrap(' '.join(first[1:]), max(12, width - indent), break_long_words=True,
                             break_on_hyphens=False)
        return [first[0]] + [' ' * indent + line for line in rest]

    def para(self, text, kind=None, margin=2):
        for line in self.wrap(text, self.width - margin):
            self.write(' ' * margin + (self.paint.status(kind, line) if kind else line))

    def mark(self, kind, text, margin=2):
        """A glyph line: '  ✓ text', wrapped under the text."""
        glyph = self.g[kind]
        lines = self.wrap(text, self.width - margin - 2)
        self.write(' ' * margin + self.paint.status(kind, glyph) + ' ' + (
            self.paint.dim(lines[0]) if kind in ('off', 'info') else lines[0]))
        for line in lines[1:]:
            self.write(' ' * (margin + 2) + (self.paint.dim(line) if kind in ('off', 'info') else line))

    def header(self, note):
        if self.fancy and self.width >= NARROW:
            for line in render_art(self.paint, note=note):
                self.write(line)
        elif self.fancy:
            self.write(self.paint.color('peach', 'tofi', '1') + ' ' + self.paint.dim('· ' + note))
        else:
            self.write('TOFI ' + note)
        self.write()

    def title(self, text):
        self.write(self.paint.color('cream', text, '1') if self.mode else text)

    def card(self, title, rows):
        """A framed card of (status, label, text) rows; plain rows when narrow or not a tty."""
        label_width = max([len(label) for _, label, _ in rows] + [4]) + 2
        if not (self.fancy and self.width >= NARROW):
            self.title(title)
            for kind, label, text in rows:
                lines = self.wrap(text, self.width - 4 - label_width)
                self.write('  %s %s%s' % (self.paint.status(kind, self.g[kind]),
                                          self.paint.dim(label.ljust(label_width)), lines[0]))
                for line in lines[1:]:
                    self.write(' ' * (4 + label_width) + line)
            self.write()
            return
        box = self.g['box']
        width = self.width
        inner = width - 4
        top = box[0] + box[4] + ' ' + title + ' ' + box[4] * max(1, width - len(title) - 5) + box[1]
        p = self.paint
        self.write(p.dim(top[:3]) + p.color('cream', title, '1') + p.dim(top[3 + len(title):]))
        for kind, label, text in rows:
            lines = self.wrap(text, inner - 2 - label_width)
            for number, line in enumerate(lines):
                if number == 0:
                    head = p.status(kind, self.g[kind]) + ' ' + p.dim(label.ljust(label_width))
                else:
                    head = ' ' * (2 + label_width)
                pad = inner - 2 - label_width - len(line)
                self.write(p.dim(box[5]) + ' ' + head + line + ' ' * max(0, pad) + ' ' + p.dim(box[5]))
        self.write(p.dim(box[2] + box[4] * (width - 2) + box[3]))
        self.write()


# --------------------------------------------------------------------------
# Spinner (one at a time; any write clears its line first)

_SPINNER = {'active': None}


def clear_spinner_line():
    spinner = _SPINNER['active']
    if spinner is not None:
        spinner.clear()


class Progress:
    """`[n/12] text` lines, or on a terminal one spinner line that ends in ✓/✗."""

    def __init__(self, ui, total=TOTAL_STEPS):
        self.ui = ui
        self.total = total
        self.text = ''
        self._stop = threading.Event()
        self._thread = None
        self._drawn = False

    def begin(self, number, text):
        self.end()
        self.text = text
        if not self.ui.fancy:
            self.ui.write('[%d/%d] %s' % (number, self.total, text))
            return
        self._stop = threading.Event()
        _SPINNER['active'] = self
        self._thread = threading.Thread(target=self._spin, daemon=True)
        self._thread.start()

    def _spin(self):
        frames = self.ui.g['spinner']
        index = 0
        while not self._stop.is_set():
            with self.ui.lock:
                self.ui.out.write('\r\033[K  %s %s' % (self.ui.paint.color('peach', frames[index % len(frames)]),
                                                       self.text[:self.ui.width - 4]))
                self.ui.out.flush()
                self._drawn = True
            index += 1
            self._stop.wait(0.1)

    def clear(self):
        if self._drawn:
            self.ui.out.write('\r\033[K')
            self._drawn = False

    def end(self, ok=True, text=None):
        if self._thread is None:
            return
        self._stop.set()
        self._thread.join()
        self._thread = None
        _SPINNER['active'] = None
        with self.ui.lock:
            self.clear()
        self.ui.mark('ok' if ok else 'bad', text or self.text)


# --------------------------------------------------------------------------
# Host detection (reads only)


def command(args, timeout=10):
    """(returncode, stdout) or (None, '') when the command is missing or hangs."""
    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, stdin=subprocess.DEVNULL)
    except (OSError, subprocess.SubprocessError):
        return None, ''
    return result.returncode, result.stdout


class Host:
    """Where detection looks. TOFI_TEST_ROOT and TOFI_OS_RELEASE / TOFI_DEV_* exist for tests."""

    def __init__(self, environ=None):
        environ = os.environ if environ is None else environ
        root = environ.get('TOFI_TEST_ROOT', '')
        self.root = root
        self.os_release = environ.get('TOFI_OS_RELEASE') or root + '/etc/os-release'
        self.dev_kvm = environ.get('TOFI_DEV_KVM') or '/dev/kvm'
        self.dev_tun = environ.get('TOFI_DEV_TUN') or '/dev/net/tun'
        self.cgroup = root + '/sys/fs/cgroup/cgroup.controllers'
        self.apparmor = root + '/sys/module/apparmor/parameters/enabled'
        self.meminfo = root + '/proc/meminfo'
        self.cpuinfo = root + '/proc/cpuinfo'
        self.proc = root + '/proc'
        self.var_lib = root + '/var/lib'
        self.state_file = root + '/etc/tofi/install-state.json'
        self.env_file = root + '/etc/tofi/tofi.env'
        self.bin_tofi = root + '/usr/local/bin/tofi'


def read_text(path):
    try:
        with open(path) as stream:
            return stream.read()
    except (OSError, UnicodeDecodeError):
        return None


def os_release(path):
    text = read_text(path)
    if text is None:
        return None
    values = {}
    for line in text.splitlines():
        key, sep, value = line.partition('=')
        if sep:
            values[key.strip()] = value.strip().strip('"')
    return values


def is_char_device(path):
    try:
        return stat.S_ISCHR(os.stat(path).st_mode)
    except OSError:
        return False


def kvm_reason(host):
    """Why /dev/kvm is missing, as specific as /proc/cpuinfo allows."""
    cpuinfo = read_text(host.cpuinfo)
    if cpuinfo is not None:
        flags = set()
        for line in cpuinfo.splitlines():
            if line.startswith('flags'):
                flags.update(line.partition(':')[2].split())
        if flags and not flags & {'vmx', 'svm'}:
            return ('no /dev/kvm — the CPU does not expose virtualization; enable nested virtualization '
                    'on cloud VMs (or VT-x/AMD-V in the firmware)')
        if flags:
            return 'no /dev/kvm — the CPU supports it; load the module: modprobe kvm_intel (or kvm_amd)'
    return 'no /dev/kvm — enable nested virtualization on cloud VMs, or use bare metal'


def meminfo(host):
    values = {}
    for line in (read_text(host.meminfo) or '').splitlines():
        key, _, rest = line.partition(':')
        if key in ('MemTotal', 'SwapTotal') and rest.split():
            values[key] = int(rest.split()[0]) // 1024
    return values


def free_disk_gib(host):
    probe = host.var_lib
    while probe and not os.path.exists(probe):
        probe = os.path.dirname(probe)
    code, out = command(['df', '-Pk', probe or '/'])
    lines = out.splitlines()
    if code != 0 or len(lines) < 2:
        return None
    try:
        return int(lines[1].split()[3]) // 1024 // 1024
    except (IndexError, ValueError):
        return None


def listening_ports(host):
    """{port: socket inode or ''} for TCP sockets in LISTEN, IPv4 and IPv6."""
    ports = {}
    for table in ('tcp', 'tcp6'):
        text = read_text(os.path.join(host.proc, 'net', table))
        for line in (text or '').splitlines()[1:]:
            fields = line.split()
            if len(fields) < 4 or fields[3] != '0A':
                continue
            try:
                port = int(fields[1].rsplit(':', 1)[1], 16)
            except (IndexError, ValueError):
                continue
            inode = fields[9] if len(fields) > 9 else ''
            if port not in ports or not ports[port]:
                ports[port] = inode
    return ports


def socket_owners(host, inodes):
    """{inode: 'name (pid N)'} by scanning /proc/<pid>/fd (needs root for other users' processes)."""
    wanted = {'socket:[%s]' % inode: inode for inode in inodes if inode and inode != '0'}
    found = {}
    if not wanted:
        return found
    try:
        pids = [name for name in os.listdir(host.proc) if name.isdigit()]
    except OSError:
        return found
    for pid in pids:
        fd_dir = os.path.join(host.proc, pid, 'fd')
        try:
            fds = os.listdir(fd_dir)
        except OSError:
            continue
        for fd in fds:
            try:
                link = os.readlink(os.path.join(fd_dir, fd))
            except OSError:
                continue
            if link in wanted and wanted[link] not in found:
                name = (read_text(os.path.join(host.proc, pid, 'comm')) or '?').strip()
                found[wanted[link]] = '%s (pid %s)' % (name, pid)
        if len(found) == len(wanted):
            break
    return found


class Ports:
    """Which TCP ports are taken, and by what when root can see it."""

    def __init__(self, host):
        self.host = host
        self.table = listening_ports(host)
        self._owners = None

    def busy(self, port):
        return int(port) in self.table

    def owner(self, port):
        port = int(port)
        if port not in self.table:
            return None
        if self._owners is None:
            self._owners = socket_owners(self.host, set(self.table.values()))
        return self._owners.get(self.table[port], '')

    def describe(self, port):
        """'in use by nginx (pid 812)' / 'in use' / 'free'."""
        if not self.busy(port):
            return 'free'
        owner = self.owner(port)
        return 'in use by %s' % owner if owner else 'in use'

    def next_free(self, port):
        candidate = int(port) + 1
        while candidate <= 65535 and self.busy(candidate):
            candidate += 1
        return candidate if candidate <= 65535 else None


def interface_addresses():
    """[(family, address)] of global addresses on physical-looking interfaces."""
    found = []
    code, output = command(['ip', '-o', 'addr', 'show'])
    for line in output.splitlines() if code == 0 else []:
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
        code, output = command(['hostname', '-I'])
        for address in output.split() if code == 0 else []:
            if address.startswith(('127.', '169.254.', '172.17.', 'fe80')) or address == '::1':
                continue
            found.append(('inet6' if ':' in address else 'inet', address))
    return found


def is_public(address):
    try:
        return ipaddress.ip_address(address).is_global
    except ValueError:
        return False


def public_ip_lookup(environ):
    """This host's public IPv4 as the internet sees it (one HTTPS request, 3 s), or None.

    TOFI_PUBLIC_IP=<address> sets it; TOFI_PUBLIC_IP=none skips the lookup.
    """
    configured = environ.get('TOFI_PUBLIC_IP', '').strip()
    if configured:
        return None if configured.lower() == 'none' else configured
    code, output = command(['curl', '-4', '-fsS', '--max-time', '3', PUBLIC_IP_URL], timeout=6)
    if code != 0:
        return None
    for line in output.splitlines():
        if line.startswith('ip='):
            value = line[3:].strip()
            try:
                ipaddress.ip_address(value)
                return value
            except ValueError:
                return None
    return None


def installed_state(host):
    """(phase or None, installed version or None, error or None)."""
    text = read_text(host.state_file)
    if text is None:
        return None, None, None
    try:
        phase = json.loads(text).get('phase')
    except (ValueError, AttributeError):
        return None, None, 'The install journal %s is not readable; run `sudo tofi status` to inspect.' % _real(
            host, host.state_file)
    version = None
    for line in (read_text(host.env_file) or '').splitlines():
        if line.startswith('TOFI_VERSION='):
            version = line.split('=', 1)[1].strip() or None
            break
    if not version:
        return phase, None, 'Found %s but no TOFI_VERSION in %s; run \'sudo tofi status\' to inspect.' % (
            _real(host, host.state_file), _real(host, host.env_file))
    return phase, version, None


def _real(host, path):
    return path[len(host.root):] if host.root and path.startswith(host.root) else path


def describe_phase(phase, version):
    if phase is None:
        return 'info', 'not installed'
    if phase == 'installed':
        return 'ok', 'installed %s' % version
    if phase == 'stopped-retained':
        return 'warn', 'stopped, data retained (%s)' % version
    kind = 'install' if phase in INSTALL_PHASES else 'update' if phase in UPGRADE_PHASES else 'uninstall'
    return 'warn', 'interrupted %s (%s), %s' % (kind, phase, version)


def detect(host=None, environ=None, port=DEFAULT_PORT, lookup_public=True):
    """Everything the installer and `tofi doctor` show about this machine."""
    environ = os.environ if environ is None else environ
    host = host or Host(environ)
    facts = {'host': host}
    code, out = command(['uname', '-sm'])
    facts['platform'] = out.strip() if code == 0 else 'unknown'
    code, out = command(['id', '-u'])
    facts['uid'] = out.strip() if code == 0 else '?'
    facts['os'] = os_release(host.os_release)
    code, out = command(['nproc'])
    try:
        facts['cpus'] = int(out.strip()) if code == 0 else (os.cpu_count() or 0)
    except ValueError:
        facts['cpus'] = os.cpu_count() or 0
    memory = meminfo(host)
    facts['memory_mib'] = memory.get('MemTotal')
    facts['swap_mib'] = memory.get('SwapTotal')
    facts['disk_gib'] = free_disk_gib(host)
    docker = None
    if shutil.which('docker'):
        code, out = command(['docker', '--version'])
        match = re.search(r'version ([0-9][^ ,]*)', out) if code == 0 else None
        docker = match.group(1) if match else 'present'
    facts['docker'] = docker
    facts['kvm'] = is_char_device(host.dev_kvm)
    facts['kvm_reason'] = None if facts['kvm'] else kvm_reason(host)
    facts['tun'] = is_char_device(host.dev_tun)
    facts['cgroup2'] = os.path.isfile(host.cgroup)
    apparmor = read_text(host.apparmor)
    facts['apparmor'] = apparmor.strip() if apparmor is not None else 'missing'
    facts['ports'] = Ports(host)
    facts['port'] = int(port)
    facts['phase'], facts['installed'], facts['journal_error'] = installed_state(host)
    addresses = [address for family, address in interface_addresses() if family == 'inet']
    facts['lan'] = [address for address in addresses if not is_public(address)]
    public = [address for address in addresses if is_public(address)]
    looked_up = public_ip_lookup(environ) if lookup_public else None
    if looked_up and looked_up not in public:
        public.append(looked_up)
    facts['public'] = public
    return facts


def system_name(facts):
    info = facts['os']
    if info is None:
        return 'unknown system'
    return '%s %s' % (info.get('NAME', info.get('ID', '?')).split(' GNU/')[0], info.get('VERSION_ID', ''))


def os_supported(facts):
    info = facts['os'] or {}
    return (info.get('ID'), info.get('VERSION_ID')) in SUPPORTED_SYSTEMS


def check_host(facts, environ=None, fresh=True):
    """(failures, warnings): the installer's hard stops and advisories, in step order."""
    environ = os.environ if environ is None else environ
    host = facts['host']
    failures, warnings = [], []
    if facts['uid'] != '0':
        failures.append('Run as root: %s (current uid %s).' % (INSTALL_COMMAND, facts['uid']))
    if facts['platform'] != 'Linux x86_64':
        failures.append("TOFI servers need Linux x86_64; this machine is '%s'." % facts['platform'])
    info = facts['os']
    if info is None:
        failures.append('Cannot read %s; supported systems are Ubuntu 22.04/24.04 and Debian 12/13.'
                        % _real(host, host.os_release))
    elif not os_supported(facts):
        label = '%s %s' % (info.get('ID', '?'), info.get('VERSION_ID', '?'))
        if environ.get('TOFI_ALLOW_UNSUPPORTED') == '1':
            warnings.append('Unsupported system %s; continuing because TOFI_ALLOW_UNSUPPORTED=1.' % label)
        else:
            failures.append("Unsupported system '%s'; TOFI supports Ubuntu 22.04/24.04 and Debian 12/13 "
                            '(set TOFI_ALLOW_UNSUPPORTED=1 to try anyway).' % label)
    if not facts['kvm']:
        failures.append('KVM is not available (/dev/kvm). Use bare metal or a VM with nested virtualization '
                        'enabled. (%s)' % facts['kvm_reason'])
    if not facts['tun']:
        failures.append("The TUN device (/dev/net/tun) is missing; load it with 'modprobe tun' and retry.")
    if not facts['cgroup2']:
        failures.append('cgroup v2 is required (no %s); boot with systemd.unified_cgroup_hierarchy=1.'
                        % _real(host, host.cgroup))
    if facts['apparmor'] != 'Y':
        failures.append("AppArmor must be enabled in the kernel (found '%s'); enable it and reboot."
                        % facts['apparmor'])
    if facts['cpus'] < MIN_CPUS:
        failures.append('TOFI needs at least %d vCPUs; this host has %d.' % (MIN_CPUS, facts['cpus']))
    memory = facts['memory_mib']
    if memory is not None:
        if memory < MIN_MEMORY_MIB:
            failures.append('TOFI needs at least 4 GiB of RAM; this host has %d MiB.' % memory)
        elif memory < WARN_MEMORY_MIB:
            warnings.append('This host has %d MiB of RAM; 8 GiB or more is recommended.' % memory)
        if facts['swap_mib'] == 0 and memory < SWAP_ADVISED_BELOW_MIB:
            warnings.append('This host has %d MiB of RAM and no swap; a computer that runs out of memory can '
                            'freeze the whole host. Add swap (for example a 4 GiB swap file) or use 16 GiB of '
                            'RAM or more.' % memory)
    disk = facts['disk_gib']
    if disk is not None:
        if fresh and disk < MIN_DISK_GIB:
            failures.append('TOFI needs at least %d GiB free under /var/lib; %d GiB is free.' % (MIN_DISK_GIB, disk))
        elif disk < WARN_DISK_GIB:
            warnings.append('Only %d GiB is free under /var/lib; %d GiB or more is recommended.'
                            % (disk, WARN_DISK_GIB))
    if facts['journal_error']:
        failures.append(facts['journal_error'])
    return failures, warnings


def machine_rows(facts, fresh=True, ports=None):
    """Rows for the "This machine" card: (status, label, text)."""
    rows = []
    platform = facts['platform']
    system = '%s · %s' % (system_name(facts), platform)
    supported = os_supported(facts) and platform == 'Linux x86_64'
    rows.append(('ok' if supported else 'bad', 'System', system if supported else system + ' — not supported'))
    cpus = facts['cpus']
    rows.append(('ok' if cpus >= MIN_CPUS else 'bad', 'CPU', '%d vCPU%s' % (cpus, '' if cpus == 1 else 's')
                 + ('' if cpus >= MIN_CPUS else ' — needs %d' % MIN_CPUS)))
    memory, swap = facts['memory_mib'], facts['swap_mib']
    if memory is None:
        rows.append(('warn', 'RAM', 'unknown'))
    else:
        text = '%.1f GiB' % (memory / 1024.0)
        kind = 'ok'
        if swap == 0:
            text += ' · no swap'
            kind = 'warn' if memory < SWAP_ADVISED_BELOW_MIB else kind
        elif swap:
            text += ' · swap %.1f GiB' % (swap / 1024.0)
        if memory < MIN_MEMORY_MIB:
            kind, text = 'bad', text + ' — needs 4 GiB'
        elif memory < WARN_MEMORY_MIB:
            kind, text = 'warn', text + ' (8 GiB recommended)'
        rows.append((kind, 'RAM', text))
    disk = facts['disk_gib']
    if disk is None:
        rows.append(('warn', 'Disk', 'free space unknown'))
    else:
        kind = 'ok' if disk >= WARN_DISK_GIB else 'warn' if (disk >= MIN_DISK_GIB or not fresh) else 'bad'
        rows.append((kind, 'Disk', '%d GiB free under /var/lib%s' % (
            disk, ' — needs %d' % MIN_DISK_GIB if kind == 'bad' else '')))
    if facts['docker'] in (None,):
        rows.append(('info', 'Docker', 'not installed — will be installed'))
    else:
        rows.append(('ok', 'Docker', facts['docker']))
    rows.append(('ok', 'KVM', '/dev/kvm ready') if facts['kvm'] else ('bad', 'KVM', facts['kvm_reason']))
    kernel = [('cgroup v2', facts['cgroup2']), ('AppArmor', facts['apparmor'] == 'Y'), ('TUN', facts['tun'])]
    if all(ok for _, ok in kernel):
        rows.append(('ok', 'Kernel', 'cgroup v2 · AppArmor · TUN'))
    else:
        rows.append(('bad', 'Kernel', ' · '.join(name + ('' if ok else ' missing') for name, ok in kernel)))
    table = facts['ports']
    wanted = ports or [facts['port'], 80, 443]
    busy = [port for port in wanted if table.busy(port)]
    if not busy:
        rows.append(('ok', 'Ports', ' · '.join(str(port) for port in wanted) + ' free'))
    else:
        groups = []
        for port in wanted:
            description = table.describe(port)
            for group in groups:
                if group[1] == description:
                    group[0].append(str(port))
                    break
            else:
                groups.append(([str(port)], description))
        rows.append(('warn', 'Ports', ' · '.join('%s %s' % (', '.join(ports), description)
                                                  for ports, description in groups)))
    kind, text = describe_phase(facts['phase'], facts['installed'])
    rows.append((kind, 'TOFI', text))
    parts = []
    if facts['lan']:
        parts.append('LAN ' + ', '.join(facts['lan']))
    if facts['public']:
        parts.append('public ' + ', '.join(facts['public']))
    rows.append(('info', 'Address', ' · '.join(parts) if parts else 'no IPv4 address found'))
    return rows


# --------------------------------------------------------------------------
# Questions


class Cancel(Exception):
    def __init__(self, message, code=1):
        Exception.__init__(self, message)
        self.code = code


class Option:
    def __init__(self, key, label, detail='', aliases=(), unsupported=None):
        self.key = key
        self.label = label
        self.detail = detail
        self.aliases = {key, label.lower()} | {alias.lower() for alias in aliases}
        self.unsupported = unsupported   # the "— not supported..." reason, or None


class Prompter:
    """Reads answers from the terminal; raw arrow-key menus when possible, typed answers otherwise."""

    def __init__(self, ui, fd, environ):
        self.ui = ui
        self.fd = fd
        self.raw = False
        if environ.get('TOFI_PROMPT') != 'plain' and ui.fancy:
            try:
                import termios  # noqa: F401
                termios.tcgetattr(fd)
                self.raw = True
            except (ImportError, OSError, Exception):
                self.raw = False

    # Typed input ----------------------------------------------------------

    def readline(self, prompt):
        self.ui.out.write(prompt)
        self.ui.out.flush()
        data = b''
        while not data.endswith(b'\n'):
            chunk = os.read(self.fd, 1024)
            if not chunk:
                self.ui.write()
                raise Cancel(NO_ANSWER, 1)
            data += chunk
        return data.decode('utf-8', 'replace').strip()

    def collapse(self):
        """Replace the answered prompt line with its check line (terminals only)."""
        if self.ui.fancy:
            self.ui.out.write('\033[1A\r\033[K')

    def reject(self, message):
        self.ui.mark('bad', message, margin=4)

    def ask_text(self, question, validate, default='', hint=''):
        """validate(answer) -> (value, error); empty answer means `default`."""
        self.ui.title(question)
        if hint:
            self.ui.para(hint, 'info')
        suffix = ' [%s]' % default if default else ''
        for _ in range(MAX_TRIES):
            answer = self.readline('  %s%s: ' % (self.ui.paint.color('teal', self.ui.g['pointer']), suffix))
            if not answer and default:
                answer = default
            value, error = validate(answer)
            if error is None:
                self.collapse()
                self.ui.mark('ok', value or '(none)', margin=4)
                return value
            self.reject(error)
        raise Cancel(TOO_MANY, 1)

    def confirm(self, question, default=True):
        choices = '[Y/n]' if default else '[y/N]'
        for _ in range(MAX_TRIES):
            answer = self.readline('%s %s ' % (self.ui.paint.bold(question), choices)).lower()
            if not answer:
                return default
            if answer in ('y', 'yes'):
                return True
            if answer in ('n', 'no'):
                return False
            self.reject("Answer y or n (Enter means %s)." % ('yes' if default else 'no'))
        raise Cancel(TOO_MANY, 1)

    # Choices --------------------------------------------------------------

    def option_lines(self, options, cursor, recommended, pointer_on_cursor):
        ui, p, g = self.ui, self.ui.paint, self.ui.g
        lines = []
        for index, option in enumerate(options):
            text = '%d) %s' % (index + 1, option.label)
            if option.detail:
                text += '  ' + option.detail
            if option.unsupported:
                text += ' — ' + option.unsupported
            elif index == recommended:
                text += '  (recommended)'
            wrapped = ui.wrap(text, ui.width - 4, indent=3)
            selected = index == cursor
            for number, line in enumerate(wrapped):
                lead = '    '
                if number == 0:
                    lead = '  ' + (p.color('teal', g['pointer']) if selected else ' ') + ' '
                if option.unsupported:
                    body = p.dim(line)
                elif number == 0 and selected:
                    body = p.color('cream', line, '1') if p.mode else line
                else:
                    body = line
                if p.mode and not option.unsupported and '(recommended)' in body:
                    body = body.replace('(recommended)', p.color('teal', '(recommended)'))
                lines.append(lead + body)
        return lines

    def resolve(self, answer, options):
        answer = answer.strip().lower()
        if answer.isdigit() and 1 <= int(answer) <= len(options):
            return int(answer) - 1
        for index, option in enumerate(options):
            if answer in option.aliases:
                return index
        return None

    def unsupported_message(self, option):
        return '%s is %s.' % (option.label, option.unsupported)

    def choose(self, question, options, default):
        self.ui.title(question)
        if self.raw:
            try:
                index = self._menu(options, default)
            except Cancel:
                raise
            self.ui.mark('ok', options[index].label, margin=4)
            return options[index]
        for line in self.option_lines(options, default, default, True):
            self.ui.write(line)
        names = '1-%d or a name' % len(options)
        for _ in range(MAX_TRIES):
            answer = self.readline('  %s Choose %s [%d]: ' % (
                self.ui.paint.color('teal', self.ui.g['pointer']), names, default + 1))
            index = default if not answer else self.resolve(answer, options)
            if index is None:
                self.reject("'%s' is not one of the choices; type %s." % (answer, names))
                continue
            if options[index].unsupported:
                self.reject(self.unsupported_message(options[index]))
                continue
            self.collapse()
            self.ui.mark('ok', options[index].label, margin=4)
            return options[index]
        raise Cancel(TOO_MANY, 1)

    def _key(self):
        data = os.read(self.fd, 1)
        if not data:
            return 'eof', ''
        if data == b'\x1b':
            sequence = b''
            while select.select([self.fd], [], [], 0.05)[0]:
                sequence += os.read(self.fd, 1)
                if len(sequence) >= 2 and sequence[-1:].isalpha() or len(sequence) > 6:
                    break
            if sequence in (b'[A', b'OA'):
                return 'up', ''
            if sequence in (b'[B', b'OB'):
                return 'down', ''
            return 'other', ''
        if data in (b'\r', b'\n'):
            return 'enter', ''
        if data in (b'\x7f', b'\x08'):
            return 'backspace', ''
        if data == b'\x04':
            return 'eof', ''
        if data == b'\x03':
            raise KeyboardInterrupt
        char = data.decode('latin-1')
        return ('char', char) if char.isprintable() else ('other', '')

    def _menu(self, options, default):
        import termios
        import tty
        ui, p, g = self.ui, self.ui.paint, self.ui.g
        cursor, typed, message, tries, drawn = default, '', '', 0, 0
        names = '1-%d' % len(options)
        saved = termios.tcgetattr(self.fd)

        def draw():
            lines = self.option_lines(options, cursor, default, True)
            if typed:
                lines.append('    ' + p.color('teal', '› ') + typed)
            else:
                lines.append('    ' + p.dim('%s move · Enter select · %s or a name' % (g['arrows'], names)))
            if message:
                for number, line in enumerate(ui.wrap(message, ui.width - 6)):
                    lines.append('    ' + (p.status('bad', g['bad']) + ' ' if number == 0 else '  ') + line)
            return lines

        def paint_block(lines, previous):
            with ui.lock:
                if previous:
                    ui.out.write('\r' + ('\033[%dA' % (previous - 1) if previous > 1 else '') + '\033[J')
                ui.out.write('\n'.join(lines))
                ui.out.flush()

        try:
            tty.setcbreak(self.fd)
            ui.out.write('\033[?25l')
            while True:
                lines = draw()
                paint_block(lines, drawn)
                drawn = len(lines)
                key, char = self._key()
                choice = None
                if key == 'eof':
                    paint_block([], drawn)
                    drawn = 0
                    ui.write()
                    raise Cancel(NO_ANSWER, 1)
                if key == 'up':
                    cursor, message = (cursor - 1) % len(options), ''
                elif key == 'down':
                    cursor, message = (cursor + 1) % len(options), ''
                elif key == 'backspace':
                    typed = typed[:-1]
                elif key == 'char' and char.isdigit() and not typed:
                    choice = self.resolve(char, options)
                    if choice is None:
                        message = "'%s' is not one of the choices; type %s or a name." % (char, names)
                        tries += 1
                elif key == 'char':
                    typed += char
                elif key == 'enter':
                    if typed:
                        choice = self.resolve(typed, options)
                        if choice is None:
                            message = "'%s' is not one of the choices; type %s or a name." % (typed, names)
                            tries += 1
                        typed = ''
                    else:
                        choice = cursor
                if choice is not None:
                    if options[choice].unsupported:
                        cursor = choice
                        message = self.unsupported_message(options[choice])
                        tries += 1
                    else:
                        paint_block([], drawn)
                        drawn = 0
                        ui.out.write('\r\033[J')
                        return choice
                if tries >= MAX_TRIES:
                    paint_block(draw(), drawn)
                    drawn = 0
                    ui.write()
                    raise Cancel(TOO_MANY, 1)
        except KeyboardInterrupt:
            if drawn:
                ui.out.write('\n')
            raise
        finally:
            termios.tcsetattr(self.fd, termios.TCSADRAIN, saved)
            ui.out.write('\033[?25h')
            ui.out.flush()


# --------------------------------------------------------------------------
# The installer conversation


def curl_text(url, timeout=20):
    """(returncode, body) via curl, HTTPS only; 22 means an HTTP error such as 404."""
    code, out = command(['curl', '-fsSL', '--proto', '=https', '--proto-redir', '=https', '--tlsv1.2',
                         '--retry', '2', url], timeout=timeout)
    return code, out


def resolve_release():
    """('stable', version) | ('prerelease', version) | ('none', None) | ('offline', detail)."""
    code, body = curl_text(RELEASES_URL + '/latest/download/manifest.json')
    if code == 0:
        try:
            version = json.loads(body).get('version')
        except (ValueError, AttributeError):
            version = None
        if version and VERSION_RE.match(version):
            return 'stable', version
        return 'offline', 'the latest release manifest is not valid JSON'
    if code != 22:
        return 'offline', 'curl exit %s' % code
    code, body = curl_text(RELEASES_API)
    if code != 0:
        return 'none', None
    try:
        releases = json.loads(body)
    except ValueError:
        return 'none', None
    for release in releases if isinstance(releases, list) else []:
        tag = str(release.get('tag_name', ''))
        if not release.get('draft') and VERSION_RE.match(tag):
            return 'prerelease', tag
    return 'none', None


def resolve_dns(name):
    """Addresses `name` resolves to (A and AAAA), or None when it does not resolve."""
    code, out = command(['getent', 'ahosts', name])
    if code is None:
        import socket
        try:
            return sorted({info[4][0] for info in socket.getaddrinfo(name, 443)})
        except OSError:
            return None
    if code != 0:
        return None
    return sorted({line.split()[0] for line in out.splitlines() if line.split()})


def dns_warning(domain, facts):
    addresses = resolve_dns(domain)
    mine = set(facts['public']) | set(facts['lan'])
    here = ', '.join(facts['public'] or facts['lan']) or 'this server'
    if not addresses:
        return ('%s does not resolve yet; certificates are issued once its A/AAAA record points at %s.'
                % (domain, here))
    if not set(addresses) & mine:
        return ('%s points at %s, not at this server (%s); HTTPS certificates are issued only once DNS '
                'points here.' % (domain, ', '.join(addresses), here))
    return None


def validate_domain(answer):
    domain = answer.strip().lower().rstrip('.')
    if not domain:
        return None, 'Type a host name such as tofi.example.com.'
    if not DOMAIN_RE.match(domain):
        return None, "'%s' is not a valid host name; use something like tofi.example.com." % answer.strip()
    return domain, None


def validate_email(answer):
    answer = answer.strip()
    if not answer:
        return '', None
    if not EMAIL_RE.match(answer):
        return None, "'%s' is not an email address; type one like you@example.com, or press Enter to skip." % answer
    return answer, None


def port_validator(ports):
    def validate(answer):
        answer = answer.strip()
        if not answer.isdigit() or not 1024 <= int(answer) <= 65535:
            return None, "'%s' is not a port between 1024 and 65535." % answer
        port = int(answer)
        if ports.busy(port):
            suggestion = ports.next_free(port)
            return None, 'Port %d is %s; %d is free.' % (port, ports.describe(port), suggestion)
        return str(port), None
    return validate


def parse_installer_args(argv, environ):
    parser = argparse.ArgumentParser(prog='install.sh', add_help=False)
    parser.add_argument('--result', required=True)
    parser.add_argument('--version')
    parser.add_argument('--domain')
    parser.add_argument('--email')
    parser.add_argument('--port')
    parser.add_argument('--computer')
    parser.add_argument('--local-only', action='store_true')
    parser.add_argument('--lan', action='store_true')
    parser.add_argument('--yes', '-y', '--non-interactive', dest='yes', action='store_true')
    args = parser.parse_args(argv)
    # Flags win; environment variables stand in for missing flags; each remembers where it came from.
    chosen = {}
    # An exposure flag (--domain, --local-only, --lan) also overrides exposure variables.
    exposure_flag = bool(args.domain or args.local_only or args.lan)
    for name, env_name in (('version', 'TOFI_VERSION'), ('domain', 'TOFI_DOMAIN'), ('email', 'TOFI_EMAIL'),
                           ('port', 'TOFI_PORT'), ('computer', 'TOFI_COMPUTER')):
        value = getattr(args, name)
        if value:
            chosen[name] = (value, '--' + name)
        elif environ.get(env_name) and not (exposure_flag and name in ('domain', 'email')):
            chosen[name] = (environ[env_name], env_name)
    if args.local_only:
        chosen['local_only'] = (True, '--local-only')
    elif environ.get('TOFI_LOCAL_ONLY') == '1' and not exposure_flag and 'domain' not in chosen:
        chosen['local_only'] = (True, 'TOFI_LOCAL_ONLY')
    if args.lan:
        chosen['lan'] = (True, '--lan')
    yes = args.yes or environ.get('TOFI_YES') == '1' or environ.get('TOFI_NON_INTERACTIVE') == '1'
    return args.result, chosen, yes


def open_terminal(yes, environ, out):
    """The /dev/tty file descriptor when the installer may ask questions, else None."""
    if yes or environ.get('CI') or not isatty(out):
        return None
    try:
        return os.open(environ.get('TOFI_TTY') or '/dev/tty', os.O_RDWR | os.O_NOCTTY)
    except OSError:
        return None


def access_url(mode, facts, port, domain=''):
    if mode == 'domain':
        return 'https://' + (domain or '<your domain>')
    if mode == 'local':
        return 'https://127.0.0.1:%s' % port
    address = (facts['lan'] or facts['public'] or ['<server-ip>'])[0]
    return 'https://%s:%s' % (address, port)


def write_result(path, values):
    with open(path, 'w') as stream:
        for key, value in values.items():
            stream.write('%s=%s\n' % (key, value))


class Installer:
    def __init__(self, argv, environ=None, out=None):
        self.environ = os.environ if environ is None else environ
        self.result_path, self.chosen, self.yes = parse_installer_args(argv, self.environ)
        self.ui = UI(out, self.environ)
        self.fd = open_terminal(self.yes, self.environ, self.ui.out)
        self.ask = Prompter(self.ui, self.fd, self.environ) if self.fd is not None else None

    def source(self, name):
        return self.chosen[name][1] if name in self.chosen else None

    def value(self, name, default=None):
        return self.chosen[name][0] if name in self.chosen else default

    def set_by(self, question, text, source):
        self.ui.title(question)
        self.ui.mark('ok', '%s  %s' % (text, self.ui.paint.dim('(set by %s)' % source)), margin=4)

    def fail(self, messages, code=1):
        for message in messages:
            if self.ui.fancy:
                lines = textwrap.wrap(message, self.ui.width - 7, break_on_hyphens=False) or ['']
                message = '\n       '.join(lines)
            sys.stderr.write('ERROR: %s\n' % message)
        sys.stderr.flush()
        return code

    def warnings(self, messages):
        for message in messages:
            if self.ui.fancy:
                self.ui.mark('warn', message)
            else:
                sys.stderr.write('WARNING: %s\n' % message)
        if messages and self.ui.fancy:
            self.ui.write()

    def computer_options(self, facts):
        kvm = Option('kvm', 'KVM (Firecracker)', aliases=('firecracker', 'kvm (firecracker)'),
                     unsupported=None if facts['kvm'] else 'not supported: ' + facts['kvm_reason'])
        return [kvm,
                Option('gvisor', 'gVisor container', aliases=('gvisor',), unsupported=NOT_YET),
                Option('container', 'Plain container', aliases=('container', 'plain', 'docker'),
                       unsupported=NOT_YET)]

    def run(self):
        ui = self.ui
        port = int(self.value('port', DEFAULT_PORT))
        ui.header('installer')
        if not ui.fancy:
            for number, text in enumerate(['Checking platform', 'Checking operating system',
                                           'Checking KVM, TUN, cgroup v2 and AppArmor',
                                           'Checking CPU, memory, disk and ports'], 1):
                ui.write('[%d/%d] %s' % (number, TOTAL_STEPS, text))
        facts = detect(environ=self.environ, port=port)
        fresh = facts['phase'] is None
        ui.card('This machine', machine_rows(facts, fresh))
        failures, warnings = check_host(facts, self.environ, fresh)
        self.warnings(warnings)
        if failures:
            if not facts['kvm']:
                ui.title("How should each account's computer run?")
                for line in Prompter(ui, -1, {'TOFI_PROMPT': 'plain'}).option_lines(
                        self.computer_options(facts), -1, 0, False):
                    ui.write(line)
                ui.write()
                ui.para('TOFI cannot run account computers here: every option above is unsupported. Use bare '
                        'metal, or a cloud VM with nested virtualization (KVM) enabled, then run the '
                        'installer again.')
                ui.write()
            return self.fail(failures)
        if not fresh:
            return self.existing(facts)
        return self.fresh(facts)

    # Re-runs ----------------------------------------------------------------

    def existing(self, facts):
        ui, phase, installed = self.ui, facts['phase'], facts['installed']
        wanted = self.value('version')
        if phase == 'installed':
            if wanted and wanted != installed:
                ui.para('TOFI %s is installed. To move to %s run: sudo tofi update --version %s'
                        % (installed, wanted, wanted))
                return self.fail(['TOFI %s is already installed; to change versions run: sudo tofi update '
                                  '--version %s' % (installed, wanted)])
            ui.para('TOFI %s is already installed and nothing needs doing. Newer releases: '
                    'sudo tofi update --check' % installed, 'ok')
            ui.write()
            if os.access(facts['host'].bin_tofi, os.X_OK):
                subprocess.call([facts['host'].bin_tofi, 'status'])
            write_result(self.result_path, {'ACTION': 'done', 'VERSION': installed})
            return 0
        if wanted and wanted != installed:
            return self.fail(['TOFI %s is already installed; to change versions run: sudo tofi update --version %s'
                              % (installed, wanted)])
        if phase == 'stopped-retained':
            ui.para('TOFI %s was uninstalled with its data kept (accounts, files and settings in /var/lib/tofi).'
                    % installed)
            if self.ask is not None:
                if not self.ask.confirm('Start the existing installation with its data?', True):
                    ui.write(CANCELLED)
                    return 1
            else:
                ui.para('Starting it again with its data.')
        else:
            kind = ('install' if phase in INSTALL_PHASES else 'update' if phase in UPGRADE_PHASES
                    else 'uninstall')
            effect = {'install': 'finishes the install', 'update': 'restores the previous version',
                      'uninstall': 'finishes the uninstall, keeping data'}[kind]
            ui.para('An interrupted %s was found (phase %s, %s). Re-running %s with the existing data; '
                    'nothing to choose.' % (kind, phase, installed, effect))
        ui.write()
        write_result(self.result_path, {'ACTION': 'install', 'VERSION': installed, 'EXISTING': '1'})
        return 0

    # Fresh install ------------------------------------------------------------

    def fresh(self, facts):
        ui, ports = self.ui, facts['ports']
        interactive = self.ask is not None
        sources = {}

        # a. Computer backend.
        computer = self.value('computer', 'kvm').lower()
        question = "How should each account's computer run?"
        options = self.computer_options(facts)
        if computer != 'kvm':
            return self.fail(['--computer %s is %s; only kvm (Firecracker) is available.' % (computer, NOT_YET)])
        if self.source('computer'):
            sources['computer'] = self.source('computer')
            if interactive:
                self.set_by(question, options[0].label, self.source('computer'))
        elif interactive:
            self.ask.choose(question, options, 0)
        if interactive:
            ui.write()

        # b. Access.
        tls_busy = [p for p in (80, 443) if ports.busy(p)]
        if self.source('domain'):
            mode, sources['access'] = 'domain', self.source('domain')
        elif self.source('local_only'):
            mode, sources['access'] = 'local', self.source('local_only')
        elif self.source('lan'):
            mode, sources['access'] = 'ip', self.source('lan')
        else:
            mode = None
        if mode == 'domain' and tls_busy:
            busy = tls_busy[0]
            return self.fail(['--domain needs ports 80 and 443 free for HTTPS certificates; port %d is %s.'
                              % (busy, ports.describe(busy))])
        port = int(self.value('port', DEFAULT_PORT))
        question = 'How will you open TOFI?'
        labels = {'ip': 'By IP over HTTPS (self-signed)', 'domain': 'With a domain (automatic HTTPS)',
                  'local': 'This machine only (SSH tunnel)'}
        if mode:
            if interactive:
                self.set_by(question, labels[mode], sources['access'])
        elif interactive:
            owners = sorted({ports.owner(p) or '' for p in tls_busy} - {''})
            reason = None
            if tls_busy:
                reason = 'not supported: port%s %s in use%s' % (
                    's' if len(tls_busy) > 1 else '', '/'.join(str(p) for p in tls_busy),
                    ' by ' + ', '.join(owners) if owners else '')
            choice = self.ask.choose(question, [
                Option('ip', labels['ip'], detail=access_url('ip', facts, port),
                       aliases=('ip', 'https', 'self-signed', 'by ip')),
                Option('domain', labels['domain'], aliases=('domain', 'letsencrypt', "let's encrypt"),
                       unsupported=reason),
                Option('local', labels['local'], aliases=('local', 'local-only', 'ssh', 'tunnel', 'this machine')),
            ], 0)
            mode = choice.key
            ui.write()
        else:
            mode = 'ip'

        # c. Follow-ups.
        domain, email = '', ''
        if mode == 'domain':
            if self.source('domain'):
                domain = self.value('domain').lower().rstrip('.')
            else:
                domain = self.ask.ask_text('Domain name', validate_domain,
                                           hint='Its DNS A/AAAA record should point at this server (%s).'
                                           % (', '.join(facts['public'] or facts['lan']) or 'its public IP'))
            warning = dns_warning(domain, facts)
            if warning:
                self.warnings([warning])
            if self.source('email'):
                email = self.value('email')
            elif interactive:
                email = self.ask.ask_text("Email for Let's Encrypt (optional)", validate_email,
                                          hint='Certificate expiry notices go here; press Enter to skip.')
                ui.write()
        if self.source('port'):
            sources['port'] = self.source('port')
            if ports.busy(port):
                return self.fail(['Port %d is already %s; choose another with --port%s.' % (
                    port, ports.describe(port), ' (%d is free)' % ports.next_free(port)
                    if ports.next_free(port) else '')])
        elif ports.busy(port):
            free = ports.next_free(port)
            if not interactive:
                return self.fail(['Port %d is already %s; choose another with --port (%d is free).'
                                  % (port, ports.describe(port), free)])
            if mode == 'domain':
                ui.mark('warn', 'Port %d is %s; TOFI listens on %d behind Caddy instead.'
                        % (port, ports.describe(port), free))
                port = free
            else:
                port = int(self.ask.ask_text('Port for TOFI', port_validator(ports), default=str(free),
                                             hint='Port %d is %s; %d is the next free one.'
                                             % (port, ports.describe(port), free)))
                ui.write()
        elif interactive and mode != 'domain':
            port = int(self.ask.ask_text('Port for TOFI', port_validator(ports), default=str(port)))
            ui.write()

        # d. Version.
        version = self.value('version')
        version_note = '(set by %s)' % self.source('version') if version else ''
        if not version:
            kind, found = resolve_release()
            if kind == 'stable':
                version, version_note = found, '(latest stable)'
            elif kind == 'offline':
                return self.fail(['Could not reach GitHub to find the latest release (%s); check network access '
                                  'to github.com, or pass --version.' % found])
            elif kind == 'prerelease':
                if not interactive:
                    return self.fail(["No stable release is published yet (GitHub's 'latest' skips pre-releases); "
                                      'the newest is %s. Install it explicitly with --version %s (see %s).'
                                      % (found, found, RELEASES_URL)])
                ui.title('Version')
                ui.para('No stable release is published yet. The newest is %s, a prerelease '
                        '(release candidate).' % found)
                if not self.ask.confirm('Install prerelease %s?' % found, False):
                    ui.write(CANCELLED + ' Re-run with --version when you choose a release.')
                    return 1
                version, version_note = found, '(prerelease, confirmed)'
                ui.write()
            else:
                return self.fail(["No release found: GitHub's 'latest' skips pre-releases and none is listed; "
                                  'pass one with --version (see %s).' % RELEASES_URL])

        # e. Summary and confirmation.
        source_note = lambda key: ('  (set by %s)' % sources[key]) if key in sources else ''  # noqa: E731
        rows = [('ok', 'Release', '%s %s' % (version, version_note)),
                ('ok', 'Computers', 'KVM (Firecracker), one per account' + source_note('computer'))]
        if mode == 'domain':
            rows.append(('ok', 'Open', 'https://%s (Let\'s Encrypt)%s' % (domain, source_note('access'))))
            rows.append(('ok', 'Email', email or 'none'))
            rows.append(('info', 'Firewall', 'allow TCP 80 and 443; the App listens on 127.0.0.1:%d' % port))
        elif mode == 'local':
            rows.append(('ok', 'Open', 'https://127.0.0.1:%d via ssh -L %d:127.0.0.1:%d%s'
                         % (port, port, port, source_note('access'))))
        else:
            rows.append(('ok', 'Open', '%s (self-signed)%s' % (access_url('ip', facts, port), source_note('access'))))
            rows.append(('info', 'Firewall', 'on a cloud server allow TCP %d' % port))
        rows.append(('info', 'Changes', 'apt packages, Docker if missing, /opt/tofi, /etc/tofi; data in '
                     '/var/lib/tofi'))
        ui.card('Ready to install', rows)
        if interactive:
            if not self.ask.confirm('Nothing has changed yet. Proceed?', True):
                ui.write(CANCELLED)
                return 1
            ui.write()
        if ui.fancy:
            ui.title('Installing TOFI %s' % version)
        result = {'ACTION': 'install', 'VERSION': version, 'DOMAIN': domain, 'EMAIL': email,
                  'LOCAL_ONLY': '1' if mode == 'local' else '0', 'COMPUTER': 'kvm', 'EXISTING': '0',
                  'PORT': str(port) if (self.source('port') or port != DEFAULT_PORT) else ''}
        write_result(self.result_path, result)
        return 0


def installer_main(argv, environ=None):
    try:
        sys.stdout.reconfigure(errors='replace')
        sys.stderr.reconfigure(errors='replace')
    except (AttributeError, ValueError):
        pass
    installer = None
    try:
        installer = Installer(argv, environ)
        return installer.run()
    except Cancel as cancel:
        sys.stdout.write(str(cancel) + '\n')
        sys.stdout.flush()
        return cancel.code
    except KeyboardInterrupt:
        sys.stdout.write('\n' + CANCELLED + '\n')
        sys.stdout.flush()
        return 130


# --------------------------------------------------------------------------
# install.sh embedding

EMBED_BEGIN = "  cat <<'TOFI_TUI_PY'\n"
EMBED_END = 'TOFI_TUI_PY\n'


def _embed_span(text):
    """(start, end) of the embedded copy inside install.sh's text."""
    start = text.index(EMBED_BEGIN) + len(EMBED_BEGIN)
    end = start if text.startswith(EMBED_END, start) else text.index('\n' + EMBED_END, start) + 1
    return start, end


def embedded_copy(install_sh_text):
    start, end = _embed_span(install_sh_text)
    return install_sh_text[start:end]


def embed(install_sh):
    with open(install_sh) as stream:
        text = stream.read()
    with open(os.path.abspath(__file__)) as stream:
        source = stream.read()
    start, end = _embed_span(text)
    updated = text[:start] + source + text[end:]
    if updated != text:
        with open(install_sh, 'w') as stream:
            stream.write(updated)
    return updated != text


def main(argv):
    if argv[:1] == ['installer']:
        return installer_main(argv[1:])
    if argv[:1] == ['embed'] and len(argv) == 2:
        print('updated' if embed(argv[1]) else 'already current')
        return 0
    if argv[:1] == ['detect']:
        ui = UI()
        facts = detect()
        ui.card('This machine', machine_rows(facts, facts['phase'] is None))
        return 0
    sys.stderr.write('usage: tofi_tui.py installer --result FILE [install.sh options] | embed install.sh | detect\n')
    return 2


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
