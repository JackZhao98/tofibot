"""install.sh behaviour with every host command stubbed on PATH.

No root, Docker, apt or network is used: `uname`, `id`, `nproc`, `df`,
`apt-get`, `systemctl`, `docker`, `curl` and `sha256sum` are stubs, host files
live under TOFI_TEST_ROOT, and the release is a synthetic local fixture.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]
INSTALL_SH = REPO / 'install.sh'
VERSION = 'v0.1.0'
RELEASES = 'https://github.com/JackZhao98/tofibot/releases'

STUBS = {
    'uname': 'echo "Linux x86_64"',
    'id': 'echo 0',
    'nproc': 'echo "${STUB_NPROC:-4}"',
    'df': ('echo "Filesystem 1024-blocks Used Available Capacity Mounted on"\n'
           'echo "/dev/vda1 104857600 1 ${STUB_DF_AVAIL_KIB:-52428800} 1% /"'),
    'apt-get': ('echo "(Reading database ... 5%"\necho "DEBIAN_FRONTEND=$DEBIAN_FRONTEND"\n'
                '[ -z "$STUB_APT_FAIL" ] || { echo "E: Unable to locate package zstd" >&2; exit 100; }'),
    'systemctl': 'exit 0',
    'docker': '''case "$*" in
  "compose version") echo "Docker Compose version v2.29.0" ;;
  *CgroupVersion*) echo 2 ;;
  *SecurityOptions*) echo '["name=apparmor","name=seccomp,profile=builtin","name=cgroupns"]' ;;
esac''',
    'curl': '''out=; url=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2 ;;
    --proto|--proto-redir|--retry) shift 2 ;;
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
cp "$STUB_RELEASES/$dir/$name" "$out"''',
    'sha256sum': 'python3 -c "import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],\'rb\').read()).hexdigest(), sys.argv[1])" "$1"',
}

HANDOFF_TOFI = '''#!/usr/bin/env bash
printf '%s\\n' "$@" > "$TOFI_TEST_ROOT/handoff.args"
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


class InstallScriptTests(unittest.TestCase):
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
        self.host_file('etc/os-release', 'ID=ubuntu\nVERSION_ID="24.04"\nVERSION_CODENAME=noble\n')
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

    def publish(self, manifest, version=VERSION):
        for directory in (self.releases / version, self.releases / 'latest'):
            directory.mkdir(parents=True, exist_ok=True)
            (directory / 'manifest.json').write_text(json.dumps(manifest))
            (directory / ('tofi-host-%s.tar.gz' % version)).write_bytes(self.bundle)

    def env(self, **extra):
        python_dir = str(Path(sys.executable).parent)
        env = {
            'PATH': os.pathsep.join([str(self.stubs), python_dir, '/usr/bin', '/bin']),
            'HOME': str(self.tmp), 'TMPDIR': str(self.tmp),
            'TOFI_TEST_ROOT': str(self.root),
            'TOFI_DEV_KVM': '/dev/null', 'TOFI_DEV_TUN': '/dev/null',
            'STUB_LOG': str(self.log), 'STUB_RELEASES': str(self.releases),
        }
        env.update(extra)
        return env

    def run_script(self, *args, script=None, **extra):
        if script is None:
            command = ['bash', str(INSTALL_SH), *args]
            return subprocess.run(command, env=self.env(**extra), capture_output=True, text=True, timeout=60)
        return subprocess.run(['bash', '-s', '--', *args], input=script, env=self.env(**extra),
                              capture_output=True, text=True, timeout=60)

    def stub_calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def host_tree(self):
        return sorted(str(p.relative_to(self.root)) for p in self.root.rglob('*'))

    def assert_no_side_effects(self, before):
        self.assertFalse(any(c.split()[0] in ('apt-get', 'docker', 'curl', 'systemctl') for c in self.stub_calls()),
                         self.stub_calls())
        self.assertEqual(self.host_tree(), before)

    def test_unsupported_distro_refused(self):
        self.host_file('etc/os-release', 'ID=fedora\nVERSION_ID=40\n')
        before = self.host_tree()
        result = self.run_script()
        self.assertEqual(result.returncode, 1)
        self.assertIn("Unsupported system 'fedora 40'", result.stderr)
        self.assertIn('[2/12]', result.stdout)
        self.assert_no_side_effects(before)

    def test_unsupported_distro_override(self):
        self.host_file('etc/os-release', 'ID=fedora\nVERSION_ID=40\n')
        result = self.run_script('--yes', TOFI_ALLOW_UNSUPPORTED='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Unsupported system fedora 40', result.stderr)

    def test_missing_kvm_refused(self):
        before = self.host_tree()
        result = self.run_script(TOFI_DEV_KVM=str(self.root / 'dev/kvm'))
        self.assertEqual(result.returncode, 1)
        self.assertIn('KVM is not available (/dev/kvm). Use bare metal or a VM with nested '
                      'virtualization enabled.', result.stderr)
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

    def test_port_in_use_refused(self):
        self.host_file('proc/net/tcp', '  sl  local_address rem_address   st\n'
                       '   0: 0100007F:2081 00000000:0000 0A 00000000:00000000\n')
        result = self.run_script()
        self.assertEqual(result.returncode, 1)
        self.assertIn('Port 8321 is already in use', result.stderr)

    def test_truncated_script_has_no_side_effects(self):
        text = INSTALL_SH.read_text()
        before = self.host_tree()
        cuts = [len(text) // 4, len(text) // 2, (len(text) * 9) // 10, text.rindex('main "$@"')]
        for cut in cuts:
            with self.subTest(cut=cut):
                self.run_script(script=text[:cut])
                self.assert_no_side_effects(before)
                self.assertFalse((self.root / 'handoff.args').exists())

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
        args = (self.root / 'handoff.args').read_text().splitlines()
        self.assertEqual(args, ['install', '--manifest', manifest_path, '--domain', 'tofi.example.com',
                                '--email', 'ops@example.com', '--port', '9000', '--yes'])
        self.assertEqual(os.readlink(self.root / 'opt/tofi/current'), 'releases/' + VERSION)
        self.assertEqual(os.readlink(self.root / 'usr/local/bin/tofi'), str(self.root / 'opt/tofi/current/bin/tofi'))
        self.assertEqual(json.loads(Path(manifest_path).read_text())['version'], VERSION)
        for number in range(1, 10):
            self.assertIn('[%d/12]' % number, result.stdout)
        self.assertTrue(any(c.startswith('apt-get -qq') and ' install -y ' in c for c in self.stub_calls()))
        self.assertEqual(list(self.tmp.iterdir()), [], 'temporary download directory must be removed')

    def test_hand_off_latest_and_lan(self):
        result = self.run_script('--lan', '--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        args = (self.root / 'handoff.args').read_text().splitlines()
        self.assertEqual(args[3:], ['--lan', '--yes'])
        self.assertTrue(any('latest/download/manifest.json' in c for c in self.stub_calls()))

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
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 1)
        self.assertIn("'latest' skips pre-releases", result.stderr)
        self.assertIn('--version', result.stderr)

    def test_existing_install_keeps_version(self):
        self.host_file('etc/tofi/install-state.json', '{"schema": 1, "phase": "installed"}')
        self.host_file('etc/tofi/tofi.env', 'TOFI_VERSION=%s\n' % VERSION)
        result = self.run_script('--version', 'v0.2.0')
        self.assertEqual(result.returncode, 1)
        self.assertIn('sudo tofi update --version v0.2.0', result.stderr)
        result = self.run_script('--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any('/download/%s/manifest.json' % VERSION in c for c in self.stub_calls()))

    def test_bad_arguments(self):
        for args, message in [(['--port', '80'], '--port must be'), (['--version', '1.0'], '--version must'),
                              (['--lan', '--domain', 'a.example.com'], 'either --domain or --lan'),
                              (['--email', 'a@example.com'], 'only used together with --domain'),
                              (['--bogus'], 'Unknown option')]:
            with self.subTest(args=args):
                result = self.run_script(*args)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)
        self.assertEqual(self.stub_calls(), [])


if __name__ == '__main__':
    unittest.main()
