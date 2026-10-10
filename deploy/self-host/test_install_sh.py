"""install.sh behaviour with every host command stubbed on PATH.

No root, Docker, apt or network is used: `uname`, `id`, `nproc`, `df`,
`apt-get`, `systemctl`, `docker`, `curl`, `ip`, `getent` and `sha256sum` are
stubs, host files live under TOFI_TEST_ROOT, and the release is a synthetic
local fixture.

Interactive runs go through a real pseudo-terminal: the child runs
`cat install.sh | bash -s -- ARGS` with the pty as its controlling terminal,
so stdin is a pipe (as with curl) while /dev/tty and stdout are the pty. Tests
read the screen and type keys exactly like a person; no test-only tty
override is involved.
"""
import errno
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import tarfile
import tempfile
import termios
import time
import unittest

REPO = Path(__file__).resolve().parents[2]
INSTALL_SH = REPO / 'install.sh'
TUI = REPO / 'deploy/self-host/tofi_tui.py'
VERSION = 'v0.1.0'
RELEASES = 'https://github.com/JackZhao98/tofibot/releases'
ANSI = re.compile(r'\x1b\[[0-9;?]*[A-Za-z]')

STUBS = {
    'uname': 'echo "${STUB_UNAME:-Linux x86_64}"',
    'id': 'echo "${STUB_UID:-0}"',
    'nproc': 'echo "${STUB_NPROC:-4}"',
    'df': ('echo "Filesystem 1024-blocks Used Available Capacity Mounted on"\n'
           'echo "/dev/vda1 104857600 1 ${STUB_DF_AVAIL_KIB:-52428800} 1% /"'),
    'apt-get': ('echo "(Reading database ... 5%"\necho "DEBIAN_FRONTEND=$DEBIAN_FRONTEND"\n'
                '[ -z "$STUB_APT_FAIL" ] || { echo "E: Unable to locate package zstd" >&2; exit 100; }'),
    'systemctl': 'exit 0',
    'docker': '''case "$*" in
  "--version") echo "Docker version 27.1.1, build 6312585" ;;
  "compose version") echo "Docker Compose version v2.29.0" ;;
  *CgroupVersion*) echo 2 ;;
  *SecurityOptions*) echo '["name=apparmor","name=seccomp,profile=builtin","name=cgroupns"]' ;;
esac''',
    'curl': '''out=; url=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2 ;;
    --proto|--proto-redir|--retry|--max-time) shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
name=$(basename "$url")
case "$url" in
  */latest/download/*) dir=latest ;;
  *) dir=$(basename "$(dirname "$url")") ;;
esac
[ -f "$STUB_RELEASES/$dir/$name" ] || exit 22
if [ -n "$out" ]; then cp "$STUB_RELEASES/$dir/$name" "$out"; else cat "$STUB_RELEASES/$dir/$name"; fi''',
    'ip': ('[ -n "$STUB_LAN" ] || exit 0\n'
           'echo "2: eth0    inet $STUB_LAN/24 brd 192.168.1.255 scope global eth0\\       valid_lft forever"'),
    'hostname': 'exit 1',
    'getent': ('[ -n "$STUB_DNS" ] || exit 2\n'
               'for a in $STUB_DNS; do echo "$a       STREAM $2"; done'),
    'sha256sum': 'python3 -c "import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],\'rb\').read()).hexdigest(), sys.argv[1])" "$1"',
}

HANDOFF_TOFI = '''#!/usr/bin/env bash
printf '%s\\n' "$@" > "$TOFI_TEST_ROOT/handoff.args"
'''
STATUS_TOFI = '''#!/usr/bin/env bash
echo "STATUS-STUB $*"
'''


def make_bundle():
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w:gz') as tar:
        for name, data, mode in [('bin/tofi', HANDOFF_TOFI.encode(), 0o755),
                                 ('lib/tofi_host.py', b'# synthetic\n', 0o644),
                                 ('compose.yaml', b'name: tofi\n', 0o644)]:
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = mode
            tar.addfile(info, io.BytesIO(data))
    return buffer.getvalue()


def make_manifest(bundle_sha, version=VERSION):
    base = '%s/download/%s/' % (RELEASES, version)
    return {
        'schema': 1, 'version': version,
        'images': {'app': 'ghcr.io/jackzhao98/tofi@sha256:' + '1' * 64,
                   'worker': 'ghcr.io/jackzhao98/tofi-worker@sha256:' + '2' * 64,
                   'caddy': 'docker.io/library/caddy@sha256:' + '3' * 64},
        'guest': {'version': version, 'url': base + 'tofi-guest-%s-x86_64.tar.zst' % version,
                  'sha256': '4' * 64, 'manifest_sha256': '5' * 64, 'guest_binary_sha256': '6' * 64},
        'bundle': {'url': base + 'tofi-host-%s.tar.gz' % version, 'sha256': bundle_sha},
        'min_host': {'cpus': 2, 'memory_gib': 4, 'disk_gib': 30},
        'data_schema': 'tofi-account-data-v1',
    }


class Terminal:
    """A child process on a real pseudo-terminal: read the screen, type keys."""

    def __init__(self, argv, env, cols=80, rows=40):
        self.raw = b''
        self.code = None
        self.mark = 0
        pid, fd = pty.fork()
        if pid == 0:  # child: the pty is the controlling terminal
            try:
                fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
                os.execve('/bin/bash', ['bash', '-c', 'cat "$0" | bash -s -- "$@"', str(INSTALL_SH)] + list(argv),
                          env)
            finally:
                os._exit(127)
        self.pid, self.fd = pid, fd

    def _read(self, timeout):
        ready, _, _ = select.select([self.fd], [], [], timeout)
        if not ready:
            return False
        try:
            data = os.read(self.fd, 65536)
        except OSError as error:
            if error.errno != errno.EIO:
                raise
            data = b''
        if not data:
            raise EOFError
        self.raw += data
        return True

    @property
    def text(self):
        """Everything shown so far, colours removed (\\r\\n kept as \\n)."""
        return ANSI.sub('', self.raw.decode('utf-8', 'replace')).replace('\r\n', '\n')

    @property
    def flat(self):
        """The screen with every run of whitespace as one space (wrapped lines join up)."""
        return ' '.join(self.text.split())

    def expect(self, needle, timeout=30):
        """Wait until `needle` (whitespace-insensitive) appears after what was already matched."""
        deadline = time.time() + timeout
        needle = ' '.join(needle.split())
        while needle not in self.flat[self.mark:]:
            if time.time() > deadline:
                raise AssertionError('timed out waiting for %r; screen:\n%s' % (needle, self.text[-3000:]))
            try:
                self._read(0.2)
            except EOFError:
                raise AssertionError('process ended before %r; screen:\n%s' % (needle, self.text[-3000:]))
        self.mark = self.flat.index(needle, self.mark) + len(needle)
        return self

    def send(self, keys):
        time.sleep(0.15)   # let the prompt settle, like a person would
        os.write(self.fd, keys.encode() if isinstance(keys, str) else keys)
        return self

    def line(self, text=''):
        return self.send(text + '\r')

    def finish(self, timeout=60):
        deadline = time.time() + timeout
        while True:
            try:
                self._read(0.2)
            except EOFError:
                break
            if time.time() > deadline:
                os.kill(self.pid, signal.SIGKILL)
                raise AssertionError('install.sh did not finish; screen:\n%s' % self.text[-3000:])
        _, status = os.waitpid(self.pid, 0)
        os.close(self.fd)
        self.code = os.waitstatus_to_exitcode(status) if hasattr(os, 'waitstatus_to_exitcode') else (
            os.WEXITSTATUS(status) if os.WIFEXITED(status) else -os.WTERMSIG(status))
        return self.code


class InstallBase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-install-sh-')
        self.addCleanup(self.temp.cleanup)
        base = Path(self.temp.name)
        self.root = base / 'root'
        self.stubs = base / 'stubs'
        self.releases = base / 'releases'
        self.tmp = base / 'tmp'
        self.log = base / 'stub.log'
        for directory in (self.root, self.stubs, self.tmp):
            directory.mkdir()
        for name, body in STUBS.items():
            path = self.stubs / name
            path.write_text('#!/bin/sh\necho "%s $*" >> "$STUB_LOG"\n%s\n' % (name, body))
            path.chmod(0o755)
        self.host_file('etc/os-release', 'NAME="Ubuntu"\nID=ubuntu\nVERSION_ID="24.04"\nVERSION_CODENAME=noble\n')
        self.host_file('sys/fs/cgroup/cgroup.controllers', 'cpu io memory pids\n')
        self.host_file('sys/module/apparmor/parameters/enabled', 'Y\n')
        self.host_file('proc/meminfo', 'MemTotal:       8152000 kB\n')
        self.host_file('proc/net/tcp', '  sl  local_address rem_address   st\n')
        self.host_file('var/lib/.keep', '')
        self.bundle = make_bundle()
        self.publish(make_manifest(hashlib.sha256(self.bundle).hexdigest()))

    def host_file(self, relative, text):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def publish(self, manifest, version=VERSION, latest=True):
        for directory in [self.releases / version] + ([self.releases / 'latest'] if latest else []):
            directory.mkdir(parents=True, exist_ok=True)
            (directory / 'manifest.json').write_text(json.dumps(manifest))
            (directory / ('tofi-host-%s.tar.gz' % version)).write_bytes(self.bundle)

    def only_prereleases(self, tag='v0.1.0-rc.2'):
        """GitHub as before the first stable release: 'latest' 404s, the API lists prereleases."""
        for path in (self.releases / 'latest').iterdir():
            path.unlink()
        self.publish(make_manifest(hashlib.sha256(self.bundle).hexdigest(), tag), tag, latest=False)
        api = self.releases / 'tofibot'
        api.mkdir(exist_ok=True)
        (api / 'releases').write_text(json.dumps([
            {'tag_name': tag, 'prerelease': True, 'draft': False},
            {'tag_name': 'v0.1.0-rc.1', 'prerelease': True, 'draft': False}]))

    def listen(self, port, process=None, pid=4242, inode=None):
        """A TCP socket in LISTEN on `port`, owned by `process` when given."""
        inode = inode or str(7000 + port)
        table = self.root / 'proc/net/tcp'
        table.write_text(table.read_text() + '   1: 00000000:%04X 00000000:0000 0A 00000000:00000000 '
                         '00:00000000 00000000     0        0 %s 1\n' % (port, inode))
        if process:
            fd_dir = self.root / 'proc' / str(pid) / 'fd'
            fd_dir.mkdir(parents=True, exist_ok=True)
            os.symlink('socket:[%s]' % inode, fd_dir / str(len(list(fd_dir.iterdir())) + 3))
            (self.root / 'proc' / str(pid) / 'comm').write_text(process + '\n')

    def installed(self, phase='installed', version=VERSION, status_tool=False):
        self.host_file('etc/tofi/install-state.json', json.dumps({'schema': 1, 'phase': phase}))
        self.host_file('etc/tofi/tofi.env', 'TOFI_VERSION=%s\n' % version)
        if status_tool:
            self.host_file('usr/local/bin/tofi', STATUS_TOFI)
            (self.root / 'usr/local/bin/tofi').chmod(0o755)

    def env(self, **extra):
        python_dir = str(Path(sys.executable).parent)
        env = {
            'PATH': os.pathsep.join([str(self.stubs), python_dir, '/usr/bin', '/bin']),
            'HOME': str(self.tmp), 'TMPDIR': str(self.tmp), 'LANG': 'C.UTF-8',
            'TOFI_TEST_ROOT': str(self.root),
            'TOFI_DEV_KVM': '/dev/null', 'TOFI_DEV_TUN': '/dev/null',
            'TOFI_PUBLIC_IP': '203.0.113.5', 'STUB_LAN': '192.168.1.20',
            'STUB_LOG': str(self.log), 'STUB_RELEASES': str(self.releases),
        }
        env.update(extra)
        return {key: value for key, value in env.items() if value is not None}

    def run_script(self, *args, script=None, **extra):
        if script is None:
            command = ['bash', str(INSTALL_SH), *args]
            return subprocess.run(command, env=self.env(**extra), capture_output=True, text=True, timeout=60,
                                  start_new_session=True)
        return subprocess.run(['bash', '-s', '--', *args], input=script, env=self.env(**extra),
                              capture_output=True, text=True, timeout=60, start_new_session=True)

    def terminal(self, *args, cols=80, **extra):
        extra.setdefault('TERM', 'xterm-256color')
        return Terminal(args, self.env(**extra), cols=cols)

    def stub_calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def host_tree(self):
        return sorted(str(p.relative_to(self.root)) for p in self.root.rglob('*'))

    def changing_calls(self):
        """Stub calls that change the host: packages, services, Docker beyond --version, downloads to disk."""
        changing = []
        for call in self.stub_calls():
            name = call.split()[0]
            if name in ('apt-get', 'systemctl') or (name == 'docker' and call != 'docker --version') \
                    or (name == 'curl' and ' -o ' in call):
                changing.append(call)
        return changing

    def assert_no_side_effects(self, before):
        self.assertEqual(self.changing_calls(), [])
        self.assertEqual(self.host_tree(), before)
        self.assertFalse((self.root / 'handoff.args').exists())
        self.assertEqual(list(self.tmp.iterdir()), [], 'the answers file must be removed')

    def assertShown(self, term, needle):
        self.assertIn(' '.join(needle.split()), term.flat, term.text[-3000:])

    def assertNotShown(self, term, needle):
        self.assertNotIn(' '.join(needle.split()), term.flat)

    def handoff(self):
        return (self.root / 'handoff.args').read_text().splitlines()


class InstallScriptTests(InstallBase):
    def test_unsupported_distro_refused(self):
        self.host_file('etc/os-release', 'ID=fedora\nVERSION_ID=40\n')
        before = self.host_tree()
        result = self.run_script()
        self.assertEqual(result.returncode, 1)
        self.assertIn("Unsupported system 'fedora 40'", result.stderr)
        self.assertIn('[2/12]', result.stdout)
        # The whole summary is shown before the refusal.
        self.assertIn('This machine', result.stdout)
        self.assertIn('Docker', result.stdout)
        self.assert_no_side_effects(before)

    def test_unsupported_distro_override(self):
        self.host_file('etc/os-release', 'ID=fedora\nVERSION_ID=40\n')
        result = self.run_script('--yes', TOFI_ALLOW_UNSUPPORTED='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Unsupported system fedora 40', result.stderr)

    def test_missing_kvm_shows_summary_and_options_then_refuses(self):
        before = self.host_tree()
        self.host_file('proc/cpuinfo', 'processor : 0\nflags : fpu sse2 hypervisor\n')
        result = self.run_script(TOFI_DEV_KVM=str(self.root / 'dev/kvm'))
        self.assertEqual(result.returncode, 1)
        self.assertIn('KVM is not available (/dev/kvm). Use bare metal or a VM with nested '
                      'virtualization enabled.', result.stderr)
        out = result.stdout
        self.assertLess(out.index('This machine'), out.index("How should each account's computer run?"))
        self.assertIn('1) KVM (Firecracker) — not supported: no /dev/kvm — the CPU does not expose '
                      'virtualization', ' '.join(out.split()))
        self.assertIn('2) gVisor container — not supported yet (coming in a later release)', out)
        self.assertIn('3) Plain container — not supported yet (coming in a later release)', out)
        self.assertIn('every option above is unsupported', ' '.join(out.split()))
        self.host_file('proc/cpuinfo', 'flags : fpu vmx sse2\n')
        before = self.host_tree()
        result = self.run_script(TOFI_DEV_KVM=str(self.root / 'dev/kvm'))
        self.assertIn('modprobe kvm_intel', ' '.join(result.stdout.split()))
        self.assert_no_side_effects(before)

    def test_small_disk_refused_without_files(self):
        before = self.host_tree()
        result = self.run_script(STUB_DF_AVAIL_KIB=str(20 * 1024 * 1024))
        self.assertEqual(result.returncode, 1)
        self.assertIn('TOFI needs at least 30 GiB free under /var/lib; 20 GiB is free.', result.stderr)
        self.assert_no_side_effects(before)

    def test_small_memory_and_cpu_refused(self):
        self.host_file('proc/meminfo', 'MemTotal: 2048000 kB\n')
        result = self.run_script()
        self.assertIn('at least 4 GiB of RAM; this host has 2000 MiB', result.stderr)
        self.host_file('proc/meminfo', 'MemTotal: 8152000 kB\n')
        result = self.run_script(STUB_NPROC='1')
        self.assertIn('at least 2 vCPUs; this host has 1', result.stderr)

    def test_every_failure_is_listed_after_the_summary(self):
        result = self.run_script(STUB_UNAME='Darwin arm64', STUB_UID='501', STUB_NPROC='1')
        self.assertEqual(result.returncode, 1)
        self.assertIn("this machine is 'Darwin arm64'", result.stderr)
        self.assertIn('current uid 501', result.stderr)
        self.assertIn('at least 2 vCPUs', result.stderr)
        self.assertIn('This machine', result.stdout)

    def test_no_swap_under_16_gib_only_warns(self):
        self.host_file('proc/meminfo', 'MemTotal: 8152000 kB\nSwapTotal: 0 kB\n')
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('7960 MiB of RAM and no swap', result.stderr)
        for meminfo in ('MemTotal: 8152000 kB\nSwapTotal: 4194300 kB\n', 'MemTotal: 16300000 kB\nSwapTotal: 0 kB\n'):
            self.host_file('proc/meminfo', meminfo)
            result = self.run_script('--yes')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn('no swap', result.stderr)

    def test_port_in_use_refused_with_owner_and_next_free_port(self):
        self.listen(8321, 'nginx')
        self.listen(8322)
        result = self.run_script()
        self.assertEqual(result.returncode, 1)
        self.assertIn('Port 8321 is already in use by nginx (pid 4242); choose another with --port '
                      '(8323 is free).', result.stderr)
        self.assertIn('8321 in use by nginx (pid 4242)', ' '.join(result.stdout.split()))

    def test_domain_flag_needs_80_and_443(self):
        self.listen(80, 'caddy', pid=77)
        result = self.run_script('--domain', 'tofi.example.com')
        self.assertEqual(result.returncode, 1)
        self.assertIn('--domain needs ports 80 and 443 free for HTTPS certificates; port 80 is in use by '
                      'caddy (pid 77).', result.stderr)

    def test_truncated_script_has_no_side_effects(self):
        text = INSTALL_SH.read_text()
        before = self.host_tree()
        cuts = [len(text) // 4, len(text) // 2, (len(text) * 9) // 10, text.rindex('main "$@"')]
        for cut in cuts:
            with self.subTest(cut=cut):
                self.run_script(script=text[:cut])
                self.assert_no_side_effects(before)

    def test_bundle_sha_mismatch_aborts(self):
        self.publish(make_manifest('0' * 64))
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 1)
        self.assertIn('Checksum mismatch', result.stderr)
        self.assertIn('Nothing was installed', result.stderr)
        self.assertFalse((self.root / 'opt/tofi/releases' / VERSION).exists())
        self.assertFalse((self.root / 'opt/tofi/current').exists())
        self.assertFalse((self.root / 'handoff.args').exists())

    def test_invalid_manifest_aborts(self):
        manifest = make_manifest(hashlib.sha256(self.bundle).hexdigest())
        manifest['images']['app'] = 'ghcr.io/jackzhao98/tofi:latest'
        self.publish(manifest)
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 1)
        self.assertIn('app image is not pinned by digest', result.stderr)
        self.assertFalse((self.root / 'opt/tofi').exists())

    def test_hand_off_command_line(self):
        result = self.run_script('--version', VERSION, '--domain', 'tofi.example.com',
                                 '--email', 'ops@example.com', '--port', '9000', '--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest_path = str(self.root / 'opt/tofi/releases' / VERSION / 'manifest.json')
        self.assertEqual(self.handoff(), ['install', '--manifest', manifest_path, '--domain', 'tofi.example.com',
                                          '--email', 'ops@example.com', '--port', '9000', '--yes'])
        self.assertEqual(os.readlink(self.root / 'opt/tofi/current'), 'releases/' + VERSION)
        self.assertEqual(os.readlink(self.root / 'usr/local/bin/tofi'), str(self.root / 'opt/tofi/current/bin/tofi'))
        self.assertEqual(json.loads(Path(manifest_path).read_text())['version'], VERSION)
        for number in range(1, 10):
            self.assertIn('[%d/12]' % number, result.stdout)
        self.assertTrue(any(c.startswith('apt-get -qq') and ' install -y ' in c for c in self.stub_calls()))
        self.assertEqual(list(self.tmp.iterdir()), [], 'temporary download directory must be removed')
        # The DNS check warns, it does not stop the install.
        self.assertIn('tofi.example.com does not resolve yet', result.stderr)

    def test_environment_variables_answer_like_flags(self):
        result = self.run_script(TOFI_DOMAIN='tofi.example.com', TOFI_EMAIL='ops@example.com', TOFI_PORT='9000',
                                 TOFI_VERSION=VERSION, TOFI_YES='1', STUB_DNS='203.0.113.5')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.handoff()[3:], ['--domain', 'tofi.example.com', '--email', 'ops@example.com',
                                              '--port', '9000', '--yes'])
        self.assertNotIn('does not resolve', result.stderr)
        # A flag wins over the variable for the same question.
        result = self.run_script('--local-only', TOFI_DOMAIN='tofi.example.com', TOFI_LOCAL_ONLY='0')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.handoff()[3:], ['--local-only'])

    def test_auto_update_flags_are_handed_to_tofi_install(self):
        for env in ({}, {'TOFI_INSTALLER_UI': 'plain'}):
            for flags, expected in ((['--auto-update'], ['--auto-update']),
                                    (['--no-auto-update'], ['--no-auto-update']),
                                    ([], [])):
                with self.subTest(env=env, flags=flags):
                    result = self.run_script('--local-only', '--yes', *flags, **env)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    # Without a flag tofi decides: --yes means automatic fix releases.
                    self.assertEqual(self.handoff()[3:], ['--local-only'] + expected + ['--yes'])

    def test_hand_off_latest_and_local_only(self):
        result = self.run_script('--local-only', '--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.handoff()[3:], ['--local-only', '--yes'])
        self.assertTrue(any('latest/download/manifest.json' in c for c in self.stub_calls()))
        self.assertIn('v0.1.0 (latest stable)', result.stdout)

    def test_default_is_https_everywhere_and_lan_is_an_alias(self):
        # No exposure flag: tofi_host's default (self-signed HTTPS on 0.0.0.0).
        # --lan no longer means plain HTTP; it is accepted and changes nothing.
        result = self.run_script('--lan')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('Proceed?', result.stdout + result.stderr)
        self.assertEqual(self.handoff()[3:], [])
        self.assertIn('https://192.168.1.20:8321 (self-signed)', result.stdout)
        install = [c for c in self.stub_calls() if c.startswith('apt-get -qq') and ' install -y ' in c]
        self.assertTrue(install and ' openssl' in install[0], 'openssl creates the certificate')

    def test_package_output_goes_to_log(self):
        result = self.run_script('--version', VERSION, '--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('Reading database', result.stdout + result.stderr)
        log = (self.root / 'var/log/tofi-install.log').read_text()
        self.assertIn('Reading database', log)
        self.assertIn('DEBIAN_FRONTEND=noninteractive', log)
        self.assertIn('+ env DEBIAN_FRONTEND=noninteractive apt-get -qq', log)

    def test_package_failure_points_to_log(self):
        result = self.run_script('--version', VERSION, '--yes', STUB_APT_FAIL='1')
        self.assertEqual(result.returncode, 1)
        self.assertIn('see /var/log/tofi-install.log', result.stderr)
        self.assertNotIn('Unable to locate package', result.stderr)
        self.assertIn('Unable to locate package', (self.root / 'var/log/tofi-install.log').read_text())

    def test_latest_missing_suggests_version(self):
        (self.releases / 'latest' / 'manifest.json').unlink()
        before = self.host_tree()
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 1)
        self.assertIn("'latest' skips pre-releases", result.stderr)
        self.assertIn('--version', result.stderr)
        self.assert_no_side_effects(before)

    def test_prerelease_only_is_never_installed_silently(self):
        self.only_prereleases()
        before = self.host_tree()
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 1)
        self.assertIn('No stable release is published yet', result.stderr)
        self.assertIn('--version v0.1.0-rc.2', result.stderr)
        self.assert_no_side_effects(before)
        result = self.run_script('--version', 'v0.1.0-rc.2', '--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('v0.1.0-rc.2 (set by --version)', result.stdout)

    def test_unreachable_github_stops_before_changes(self):
        for path in (self.releases / 'latest').iterdir():
            path.unlink()
        curl = self.stubs / 'curl'
        curl.write_text('#!/bin/sh\necho "curl $*" >> "$STUB_LOG"\nexit 6\n')
        before = self.host_tree()
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 1)
        self.assertIn('Could not reach GitHub to find the latest release (curl exit 6)', result.stderr)
        self.assert_no_side_effects(before)

    def test_public_address_lookup(self):
        (self.releases / 'cdn-cgi').mkdir()
        (self.releases / 'cdn-cgi' / 'trace').write_text('fl=1\nh=cloudflare.com\nip=198.51.100.7\nts=1\n')
        result = self.run_script('--yes', TOFI_PUBLIC_IP=None)
        self.assertIn('LAN 192.168.1.20 · public 198.51.100.7', result.stdout)
        result = self.run_script('--yes', TOFI_PUBLIC_IP='none')
        self.assertNotIn('public', result.stdout.split('Address')[1].splitlines()[0])

    def test_existing_install_same_version_reports_and_exits(self):
        self.installed(status_tool=True)
        before = self.host_tree()
        result = self.run_script('--version', 'v0.2.0')
        self.assertEqual(result.returncode, 1)
        self.assertIn('sudo tofi update --version v0.2.0', result.stderr)
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('TOFI v0.1.0 is already installed', result.stdout)
        self.assertIn('STATUS-STUB status', result.stdout)
        self.assert_no_side_effects(before)
        self.assertFalse(any('manifest.json' in c for c in self.stub_calls()))

    def test_stopped_retained_and_interrupted_installs_resume(self):
        for phase, message in (('stopped-retained', 'Starting it again with its data'),
                               ('install-failed', 'An interrupted install was found (phase install-failed'),
                               ('upgrade-failed', 'restores the previous version')):
            with self.subTest(phase=phase):
                self.installed(phase)
                result = self.run_script('--yes')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(message, ' '.join(result.stdout.split()))
                self.assertTrue(any('/download/%s/manifest.json' % VERSION in c for c in self.stub_calls()))
                self.assertEqual(self.handoff()[:2], ['install', '--manifest'])

    def test_bad_arguments(self):
        for args, message in [(['--port', '80'], '--port must be'), (['--version', '1.0'], '--version must'),
                              (['--local-only', '--domain', 'a.example.com'], 'either --domain or --local-only'),
                              (['--email', 'a@example.com'], 'only used together with --domain'),
                              (['--domain', 'a.example.com', '--email', 'nope'], '--email must be an address'),
                              (['--computer', 'gvisor'], '--computer gvisor is not supported yet (coming in a '
                                                         'later release)'),
                              (['--computer', 'container'], 'not supported yet'),
                              (['--computer', 'xen'], '--computer must be kvm, gvisor or container'),
                              (['--bogus'], 'Unknown option')]:
            with self.subTest(args=args):
                result = self.run_script(*args)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)
        self.assertEqual(self.stub_calls(), [])

    def test_embedded_ui_matches_tofi_tui(self):
        sys.path.insert(0, str(TUI.parent))
        import tofi_tui
        self.assertEqual(tofi_tui.embedded_copy(INSTALL_SH.read_text()), TUI.read_text(),
                         'install.sh carries a stale copy; run: python3 deploy/self-host/tofi_tui.py embed install.sh')

    def test_plain_fallback_without_the_python_ui(self):
        result = self.run_script('--yes', TOFI_INSTALLER_UI='plain')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('This machine', result.stdout)
        for number in range(1, 10):
            self.assertIn('[%d/12]' % number, result.stdout)
        result = self.run_script(TOFI_INSTALLER_UI='plain', TOFI_DEV_KVM=str(self.root / 'dev/kvm'))
        self.assertEqual(result.returncode, 1)
        self.assertIn('KVM is not available (/dev/kvm)', result.stderr)


class InteractiveTests(InstallBase):
    """Real pty sessions: the child's /dev/tty and stdout are the terminal, stdin is a pipe."""

    def test_all_defaults_with_enter(self):
        before = self.host_tree()
        term = self.terminal(TOFI_PROMPT='plain')
        term.expect("How should each account's computer run?").expect('Choose 1-3 or a name [1]:').line()
        term.expect('How will you open TOFI?').expect('[1]:').line()
        term.expect('Port for TOFI').expect('[8321]:').line()
        term.expect('Install fix releases automatically? (recommended)')
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Ready to install')
        self.assertEqual(self.host_tree(), before, 'nothing changes before Proceed')
        self.assertEqual(self.changing_calls(), [])
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertEqual(self.handoff()[3:], ['--auto-update'])
        for shown in ('1) KVM (Firecracker) (recommended)',
                      '1) By IP over HTTPS (self-signed) https://192.168.1.20:8321 (recommended)',
                      '2) gVisor container — not supported yet (coming in a later release)',
                      '✓ Docker ready', '✓ Host tools v0.1.0 verified'):
            self.assertShown(term, shown)
        self.assertNotShown(term, '[5/12]')

    def test_arrow_key_menu(self):
        term = self.terminal()
        term.expect('Enter select')
        term.send('\x1b[B').send('\r')   # down to gVisor, Enter: refused
        term.expect('gVisor container is not supported yet (coming in a later release).')
        term.send('\x1b[A').send('\r')   # back up to KVM
        term.expect('How will you open TOFI?').expect('Enter select')
        term.send('\x1b[B').send('\x1b[B').send('\r')   # this machine only
        term.expect('Port for TOFI').line('9100')
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Proceed? [Y/n]').line('YES')
        self.assertEqual(term.finish(), 0, term.text)
        self.assertEqual(self.handoff()[3:], ['--local-only', '--port', '9100', '--auto-update'])
        self.assertShown(term, '✓ This machine only (SSH tunnel)')

    def test_menu_accepts_numbers_and_typed_names(self):
        term = self.terminal()
        term.expect('Enter select').send('1')
        term.expect('How will you open TOFI?').expect('Enter select').send('LOCAL\r')
        term.expect('Port for TOFI').line()
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertEqual(self.handoff()[3:], ['--local-only', '--auto-update'])

    def test_choose_domain_with_validation_and_dns_warning(self):
        term = self.terminal(TOFI_PROMPT='plain', STUB_DNS='198.51.100.99')
        term.expect('[1]:').line(' kvm ')
        term.expect('How will you open TOFI?').expect('[1]:').line('Domain')
        term.expect('Domain name').line('not a domain')
        term.expect("'not a domain' is not a valid host name")
        term.line('TOFI.Example.com.')
        term.expect('tofi.example.com points at 198.51.100.99, not at this server (203.0.113.5)')
        term.expect("Email for Let's Encrypt").line('nope')
        term.expect("'nope' is not an email address").line('ops@example.com')
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Ready to install').expect('Proceed? [Y/n]').line('y')
        self.assertEqual(term.finish(), 0, term.text)
        self.assertEqual(self.handoff()[3:], ['--domain', 'tofi.example.com', '--email', 'ops@example.com',
                                              '--auto-update'])
        self.assertShown(term, 'https://tofi.example.com (Let\'s Encrypt)')
        self.assertNotShown(term, 'Port for TOFI')

    def test_domain_option_is_dim_when_80_or_443_is_taken(self):
        self.listen(443, 'nginx', pid=812)
        term = self.terminal(TOFI_PROMPT='plain')
        term.expect('[1]:').line()
        term.expect('2) With a domain (automatic HTTPS) — not supported: port 443 in use by nginx (pid 812)')
        term.expect('[1]:').line('2')
        term.expect('With a domain (automatic HTTPS) is not supported: port 443 in use by nginx (pid 812).')
        term.line('ip')
        term.expect('Port for TOFI').line()
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        # The colour version renders the unsupported row dim (SGR 2).
        self.assertRegex(term.raw.decode(), r'\x1b\[2m2\) With a domain')

    def test_port_in_use_offers_the_next_free_port(self):
        self.listen(8321, 'nginx')
        term = self.terminal(TOFI_PROMPT='plain')
        term.expect('[1]:').line()
        term.expect('[1]:').line()
        term.expect('Port 8321 is in use by nginx (pid 4242); 8322 is the next free one.').expect('[8322]:')
        term.line('8321')
        term.expect('Port 8321 is in use by nginx (pid 4242); 8322 is free.')
        term.line('80')
        term.expect("'80' is not a port between 1024 and 65535.")
        term.line()
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertEqual(self.handoff()[3:], ['--port', '8322', '--auto-update'])

    def test_five_invalid_answers_abort(self):
        before = self.host_tree()
        term = self.terminal(TOFI_PROMPT='plain')
        term.expect('[1]:')
        for answer in ('9', 'banana', '2', 'gvisor', ' 3 '):
            term.line(answer)
        term.expect('Too many invalid answers — cancelled, nothing was changed.')
        self.assertEqual(term.finish(), 1)
        self.assertShown(term, "'banana' is not one of the choices; type 1-3 or a name.")
        self.assertShown(term, 'Plain container is not supported yet')
        self.assert_no_side_effects(before)

    def test_ctrl_c_before_proceed_cancels_without_changes(self):
        before = self.host_tree()
        answered = [('Choose 1-3', '\r'), ('Choose 1-3', '\r'), ('[8321]:', '\r'),
                    ('Turn on automatic fix releases?', '\r')]
        for prompt, count in (('Choose 1-3', 0), ('Port for TOFI', 2), ('Turn on automatic', 3), ('Proceed?', 4)):
            with self.subTest(prompt=prompt):
                term = self.terminal(TOFI_PROMPT='plain')
                for earlier, key in answered[:count]:
                    term.expect(earlier).send(key)
                term.expect(prompt)
                term.send('\x03')
                term.expect('Cancelled — nothing was changed.')
                self.assertEqual(term.finish(), 130, term.text)
                self.assert_no_side_effects(before)
        term = self.terminal()   # in the arrow-key menu too
        term.expect('Enter select').send('\x03')
        term.expect('Cancelled — nothing was changed.')
        self.assertEqual(term.finish(), 130)
        self.assert_no_side_effects(before)

    def test_no_at_proceed_cancels(self):
        before = self.host_tree()
        term = self.terminal(TOFI_PROMPT='plain')
        term.expect('[1]:').line()
        term.expect('[1]:').line()
        term.expect('[8321]:').line()
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Proceed? [Y/n]').line('maybe')
        term.expect('Answer y or n (Enter means yes).').line(' N ')
        term.expect('Cancelled — nothing was changed.')
        self.assertEqual(term.finish(), 1)
        self.assert_no_side_effects(before)

    def test_end_of_input_aborts_safely(self):
        before = self.host_tree()
        for prompt_env in ('plain', None):
            term = self.terminal(TOFI_PROMPT=prompt_env)
            term.expect('Choose 1-3' if prompt_env else 'Enter select').send('\x04')
            term.expect('No answer: the terminal closed (end of input) — cancelled, nothing was changed.')
            self.assertEqual(term.finish(), 1)
            self.assert_no_side_effects(before)

    def test_flags_answer_questions(self):
        term = self.terminal('--domain', 'tofi.example.com', '--email', 'ops@example.com', '--port', '9000',
                             TOFI_PROMPT='plain', STUB_DNS='203.0.113.5')
        term.expect("How should each account's computer run?").expect('[1]:').line()
        term.expect('With a domain (automatic HTTPS)  (set by --domain)')
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertNotShown(term, 'Domain name')
        self.assertNotShown(term, 'Email for')
        self.assertNotShown(term, 'Port for TOFI')
        self.assertEqual(self.handoff()[3:], ['--domain', 'tofi.example.com', '--email', 'ops@example.com',
                                              '--port', '9000', '--auto-update'])
        term = self.terminal('--computer', 'kvm', '--local-only', TOFI_PROMPT='plain')
        term.expect('KVM (Firecracker)  (set by --computer)')
        term.expect('This machine only (SSH tunnel)  (set by --local-only)')
        term.expect('Port for TOFI').line()
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        # A flag answers the auto-update question too.
        term = self.terminal('--no-auto-update', '--local-only', TOFI_PROMPT='plain')
        term.expect('[1]:').line()
        term.expect('Port for TOFI').line()
        term.expect('Install fix releases automatically? (recommended)')
        term.expect('No  (set by --no-auto-update)')
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertNotShown(term, 'Turn on automatic fix releases?')
        self.assertEqual(self.handoff()[3:], ['--local-only', '--no-auto-update'])
        # Declining the question is remembered.
        term = self.terminal('--local-only', TOFI_PROMPT='plain')
        term.expect('[1]:').line()
        term.expect('Port for TOFI').line()
        term.expect('Turn on automatic fix releases? [Y/n]').line('n')
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertEqual(self.handoff()[3:], ['--local-only', '--no-auto-update'])

    def test_yes_ci_and_dumb_pipes_never_prompt(self):
        for args, extra in ((['--yes'], {}), (['--non-interactive'], {}), ([], {'CI': 'true'}),
                            ([], {'TOFI_YES': '1'})):
            with self.subTest(args=args, extra=extra):
                term = self.terminal(*args, **extra)
                self.assertEqual(term.finish(timeout=60), 0, term.text)
                self.assertNotShown(term, 'Proceed?')
                self.assertNotShown(term, 'Choose')
                self.assertNotShown(term, 'Enter select')

    def test_no_kvm_on_a_terminal_shows_summary_then_exits(self):
        term = self.terminal(TOFI_DEV_KVM=str(self.root / 'dev/kvm'))
        self.assertEqual(term.finish(), 1)
        self.assertShown(term, 'This machine')
        self.assertShown(term, '— not supported: no /dev/kvm')
        self.assertShown(term, 'every option above is unsupported')
        self.assertNotShown(term, 'Enter select')

    def test_prerelease_needs_explicit_confirmation(self):
        self.only_prereleases()
        before = self.host_tree()
        term = self.terminal(TOFI_PROMPT='plain')
        for _ in range(2):
            term.expect('[1]:').line()
        term.expect('[8321]:').line()
        term.expect('No stable release is published yet. The newest is v0.1.0-rc.2')
        term.expect('Install prerelease v0.1.0-rc.2? [y/N]').line()
        term.expect('Cancelled — nothing was changed.')
        self.assertEqual(term.finish(), 1)
        self.assert_no_side_effects(before)
        term = self.terminal(TOFI_PROMPT='plain')
        for _ in range(2):
            term.expect('[1]:').line()
        term.expect('[8321]:').line()
        term.expect('[y/N]').line('y')
        term.expect('Turn on automatic fix releases? [Y/n]').line()
        term.expect('v0.1.0-rc.2 (prerelease, confirmed)')
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertIn('/releases/v0.1.0-rc.2/manifest.json', self.handoff()[2])

    def test_re_runs(self):
        self.installed(status_tool=True)
        term = self.terminal()
        self.assertEqual(term.finish(), 0)
        self.assertShown(term, 'TOFI v0.1.0 is already installed')
        self.assertNotIn('?', term.text.split('This machine')[1].replace('Newer releases', ''))
        term = self.terminal('--version', 'v0.2.0')
        self.assertEqual(term.finish(), 1)
        self.assertShown(term, 'sudo tofi update --version v0.2.0')
        self.installed('stopped-retained')
        before = self.host_tree()
        term = self.terminal(TOFI_PROMPT='plain')
        term.expect('Start the existing installation with its data? [Y/n]').line('n')
        self.assertEqual(term.finish(), 1)
        self.assert_no_side_effects(before)
        term = self.terminal(TOFI_PROMPT='plain')
        term.expect('Start the existing installation with its data? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)
        self.assertEqual(self.handoff()[:2], ['install', '--manifest'])
        self.installed('installing')
        term = self.terminal(TOFI_PROMPT='plain')
        self.assertEqual(term.finish(), 0, term.text)
        self.assertIn('An interrupted install was found (phase installing', ' '.join(term.text.split()))
        self.assertNotShown(term, '[Y/n]')

    def test_plain_colours_and_narrow_terminal(self):
        term = self.terminal('--yes', NO_COLOR='1', cols=50)
        self.assertEqual(term.finish(), 0, term.text)
        self.assertNotRegex(term.raw.decode(), r'\x1b\[(2|38;)')   # glyphs, no colours
        self.assertNotShown(term, '╭')                            # no frame under 60 columns
        too_wide = [line for line in (row.split('\r')[-1] for row in term.text.splitlines()) if len(line) > 50]
        self.assertEqual(too_wide, [], term.text)
        term = self.terminal('--yes', TERM='dumb')
        self.assertEqual(term.finish(), 0, term.text)
        self.assertShown(term, '[5/12] Installing system packages')
        self.assertShown(term, '[1/12] Checking platform')

    def test_python_less_fallback_still_confirms(self):
        before = self.host_tree()
        term = self.terminal(TOFI_INSTALLER_UI='plain')
        term.expect('Proceed? [Y/n]').line('n')
        term.expect('Cancelled — nothing was changed.')
        self.assertEqual(term.finish(), 1)
        self.assertEqual(self.changing_calls(), [])
        term = self.terminal(TOFI_INSTALLER_UI='plain')
        term.expect('Proceed? [Y/n]').line()
        self.assertEqual(term.finish(), 0, term.text)


if __name__ == '__main__':
    unittest.main()
