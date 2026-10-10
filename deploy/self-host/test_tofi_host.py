"""Synthetic checks for the host lifecycle tool; no Docker, root or KVM needed.

Every host command goes through a mocked `run`; files are written under a
temporary root. These tests do not replace clean-host KVM acceptance.
"""
import contextlib
import hashlib
import io
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import tofi_host  # noqa: E402
import tofi_tui  # noqa: E402

HERE = Path(__file__).resolve().parent
GUEST_SHA = 'a' * 64
OLD = 'v0.1.0'
NEW = 'v0.2.0'
DATA = b'synthetic account data written after install'


def image(name, digit):
    return '%s@sha256:%s' % (tofi_host.IMAGE_REPOS[name], digit * 64)


def manifest(version=NEW, digit='2'):
    base = '%s/download/%s/' % (tofi_host.RELEASE_BASE, version)
    return {
        'schema': 1,
        'version': version,
        'images': {'app': image('app', digit), 'worker': image('worker', digit), 'caddy': image('caddy', '9')},
        'guest': {'version': version, 'url': base + 'tofi-guest-%s-x86_64.tar.zst' % version,
                  'sha256': 'b' * 64, 'manifest_sha256': 'c' * 64, 'guest_binary_sha256': GUEST_SHA},
        'bundle': {'url': base + 'tofi-host-%s.tar.gz' % version, 'sha256': 'd' * 64},
        'min_host': {'cpus': 2, 'memory_gib': 4, 'disk_gib': 30},
        'data_schema': 'tofi-account-data-v1',
    }


def completed(stdout=''):
    return subprocess.CompletedProcess([], 0, stdout, '')


class HostCase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-host-synthetic-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.saved_paths = tofi_host.P
        tofi_host.P = tofi_host.Paths(self.root)
        self.addCleanup(setattr, tofi_host, 'P', self.saved_paths)
        self.P = tofi_host.P
        self.P.dev_kvm = Path('/dev/null')
        self.P.dev_tun = Path('/dev/null')
        self.commands = []
        patches = [
            mock.patch.object(tofi_host, 'require_root'),
            mock.patch.object(tofi_host, 'run', side_effect=self.fake_run),
            mock.patch.object(tofi_host.os, 'chown'),
            mock.patch.object(tofi_host.time, 'sleep'),
            mock.patch('sys.stdout', new_callable=io.StringIO),
        ]
        for patcher in patches:
            patcher.start()
            self.addCleanup(patcher.stop)
        self.write(self.P.meminfo, 'MemTotal:       8000000 kB\nMemAvailable:   7000000 kB\n')

    def fake_run(self, args, check=True, timeout=300, **kwargs):
        self.commands.append(list(args))
        if args[0] == 'openssl':
            # Certificates are made by the real openssl, into the temporary root.
            return subprocess.run(args, check=check, capture_output=True, text=True, timeout=timeout)
        return completed()

    def write(self, path, text):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def make_bundle(self, version):
        target = self.P.releases / version
        target.mkdir(parents=True, exist_ok=True)
        for name in ('compose.yaml', 'Caddyfile.tmpl', 'worker.apparmor.template',
                     'worker.seccomp.json', 'tofi.conf', 'tofi.service'):
            shutil.copy(HERE / name, target / name)
        return target

    def make_guest(self, version):
        directory = self.P.guest / version
        directory.mkdir(parents=True, exist_ok=True)
        (directory / 'account-release.json').write_text(json.dumps({'guest_binary_sha256': GUEST_SHA}))
        return directory

    def installed(self, phase='installed', version=OLD):
        """Lay out a synthetic installation at `version` in `phase`."""
        self.make_bundle(version)
        self.make_guest(version)
        self.P.opt.mkdir(parents=True, exist_ok=True)
        os.symlink('releases/' + version, self.P.current)
        options = {'domain': '', 'email': '', 'bind': '0.0.0.0', 'port': 8321}
        env = tofi_host.render_env(manifest(version, '1'), options, {'cpu': 3, 'memory_mib': 6144})
        tofi_host.prepare_directories()
        tofi_host.apply_host_config(self.P.releases / version, env)
        (self.P.data / 'tofi.db').write_bytes(DATA)
        state = tofi_host.new_state(version)
        tofi_host.complete(state, phase)
        self.commands.clear()
        return env

    def phase(self):
        return json.loads(self.P.state_file.read_text())['phase']


class PreflightTests(HostCase):
    def healthy_host(self):
        self.write(self.P.cgroup_controllers, 'cpu memory io\n')
        self.write(self.P.apparmor_enabled, 'Y\n')
        docker_info = json.dumps({'CgroupVersion': '2', 'SecurityOptions': ['name=apparmor', 'name=seccomp']})

        def docker(args, check=True, timeout=300, **kwargs):
            if args[:2] == ['docker', 'info']:
                return completed(docker_info)
            return completed()

        stack = contextlib.ExitStack()
        stack.enter_context(mock.patch.object(tofi_host.platform, 'system', return_value='Linux'))
        stack.enter_context(mock.patch.object(tofi_host.platform, 'machine', return_value='x86_64'))
        stack.enter_context(mock.patch.object(tofi_host.shutil, 'which', return_value='/usr/bin/tool'))
        stack.enter_context(mock.patch.object(tofi_host.os, 'cpu_count', return_value=4))
        stack.enter_context(mock.patch.object(tofi_host, 'free_disk_bytes', return_value=50 * tofi_host.GIB))
        stack.enter_context(mock.patch.object(tofi_host, 'port_in_use', return_value=False))
        stack.enter_context(mock.patch.object(tofi_host, 'domain_resolves_here', return_value=True))
        stack.enter_context(mock.patch.object(tofi_host, 'firecracker_running', return_value=False))
        tofi_host.run.side_effect = docker
        return stack

    def options(self, **extra):
        values = {'domain': '', 'email': '', 'bind': '127.0.0.1', 'port': 8321}
        values.update(extra)
        return values

    def refuses(self, pattern, options=None):
        with self.assertRaisesRegex(tofi_host.HostError, pattern):
            tofi_host.preflight(options or self.options())
        self.assertFalse(self.P.etc.exists(), 'preflight must not write host files')
        self.assertFalse(self.P.var.exists())

    def test_healthy_host_passes_without_warnings(self):
        with self.healthy_host():
            self.assertEqual(tofi_host.preflight(self.options()), [])

    def test_wrong_platform(self):
        with self.healthy_host(), mock.patch.object(tofi_host.platform, 'system', return_value='Darwin'):
            self.refuses('Linux x86_64')
        with self.healthy_host(), mock.patch.object(tofi_host.platform, 'machine', return_value='aarch64'):
            self.refuses('Linux x86_64')

    def test_not_root(self):
        with self.healthy_host(), mock.patch.object(tofi_host, 'require_root',
                                                    side_effect=tofi_host.HostError('Run this command as root')):
            self.refuses('as root')

    def test_missing_kvm(self):
        self.P.dev_kvm = self.root / 'dev/kvm'
        with self.healthy_host():
            self.refuses(r'KVM is not available \(/dev/kvm\)\. Use bare metal or a VM with nested virtualization enabled\.')

    def test_kvm_not_a_character_device(self):
        self.P.dev_kvm = self.P.meminfo
        with self.healthy_host():
            self.refuses('KVM is not available')

    def test_missing_tun(self):
        self.P.dev_tun = self.root / 'dev/net/tun'
        with self.healthy_host():
            self.refuses('/dev/net/tun')

    def test_cgroup_v1(self):
        with self.healthy_host():
            self.P.cgroup_controllers.unlink()
            self.refuses('cgroup v2')

    def test_apparmor_disabled(self):
        with self.healthy_host():
            self.P.apparmor_enabled.write_text('N\n')
            self.refuses('AppArmor must be enabled')

    def test_missing_tools(self):
        with self.healthy_host(), mock.patch.object(tofi_host.shutil, 'which',
                                                    side_effect=lambda t: None if t == 'debugfs' else '/bin/' + t):
            self.refuses('Missing host tools: debugfs')

    def test_docker_cgroup_and_apparmor(self):
        for info, pattern in [({'CgroupVersion': '1', 'SecurityOptions': ['name=apparmor']}, 'cgroup v2'),
                              ({'CgroupVersion': '2', 'SecurityOptions': ['name=seccomp']}, 'AppArmor enabled')]:
            with self.healthy_host():
                tofi_host.run.side_effect = lambda args, **kw: completed(json.dumps(info))
                self.refuses(pattern)

    def test_docker_not_running(self):
        with self.healthy_host():
            tofi_host.run.side_effect = subprocess.CalledProcessError(1, ['docker'])
            self.refuses('Docker is not running')

    def test_too_few_cpus(self):
        with self.healthy_host(), mock.patch.object(tofi_host.os, 'cpu_count', return_value=1):
            self.refuses('at least 2 vCPUs; this host has 1')

    def test_too_little_memory_and_warning(self):
        with self.healthy_host():
            self.P.meminfo.write_text('MemTotal: 2000000 kB\nMemAvailable: 1000000 kB\n')
            self.refuses('at least 4 GiB of RAM; this host has 1953 MiB')
        with self.healthy_host():
            self.P.meminfo.write_text('MemTotal: 4000000 kB\nMemAvailable: 3000000 kB\n')
            self.assertIn('8 GiB', tofi_host.preflight(self.options())[0])

    def test_too_little_disk_and_warning(self):
        with self.healthy_host(), mock.patch.object(tofi_host, 'free_disk_bytes', return_value=20 * tofi_host.GIB):
            self.refuses(r'30 GiB free under /var/lib; 20\.0 GiB is free')
        with self.healthy_host(), mock.patch.object(tofi_host, 'free_disk_bytes', return_value=35 * tofi_host.GIB):
            self.assertIn('40 GiB', tofi_host.preflight(self.options())[0])

    def test_port_in_use(self):
        default = tofi_host.install_options(tofi_host.parse_args(['install']))
        with self.healthy_host(), mock.patch.object(tofi_host, 'port_in_use', return_value=True) as probe:
            self.refuses('Port 8321 on 0.0.0.0 is already in use', default)
        probe.assert_called_with('0.0.0.0', 8321)
        with self.healthy_host(), mock.patch.object(tofi_host, 'port_in_use', return_value=True):
            self.refuses('Port 8321 on 127.0.0.1 is already in use')

    def test_domain_needs_80_and_443(self):
        with self.healthy_host(), mock.patch.object(tofi_host, 'port_in_use', side_effect=lambda h, p: p == 443):
            self.refuses('port 443 is in use', self.options(domain='tofi.example.com'))

    def test_unresolved_domain_only_warns(self):
        with self.healthy_host(), mock.patch.object(tofi_host, 'domain_resolves_here', return_value=False):
            warnings = tofi_host.preflight(self.options(domain='tofi.example.com'))
        self.assertIn('does not resolve', warnings[0])

    def test_foreign_containers_and_vms_only_warn(self):
        def docker(args, check=True, timeout=300, **kwargs):
            if args[:2] == ['docker', 'info']:
                return completed(json.dumps({'CgroupVersion': '2', 'SecurityOptions': ['name=apparmor']}))
            if 'label=com.docker.compose.project!=tofi' in args:
                return completed('abc123\n')
            return completed()
        with self.healthy_host(), mock.patch.object(tofi_host, 'firecracker_running', return_value=True):
            tofi_host.run.side_effect = docker
            warnings = tofi_host.preflight(self.options())
        self.assertEqual(len(warnings), 2)


class MemoryAdvisoryTests(HostCase):
    healthy_host = PreflightTests.healthy_host
    options = PreflightTests.options
    NO_SWAP_8G = 'MemTotal: 8000000 kB\nMemAvailable: 6000000 kB\nSwapTotal: 0 kB\n'

    def test_no_swap_under_16_gib_only_warns_with_capacity(self):
        with self.healthy_host():
            self.P.meminfo.write_text(self.NO_SWAP_8G)
            warnings = tofi_host.preflight(self.options())
        self.assertEqual(len(warnings), 1)
        self.assertIn('no swap', warnings[0])
        self.assertIn('5859 MiB available of 7812 MiB', warnings[0])
        self.assertIn('each computer needs about 1536 MiB (room for 3 now)', warnings[0])

    def test_swap_or_16_gib_needs_no_warning(self):
        for meminfo in ('MemTotal: 8000000 kB\nMemAvailable: 6000000 kB\nSwapTotal: 4194300 kB\n',
                        'MemTotal: 16300000 kB\nMemAvailable: 15000000 kB\nSwapTotal: 0 kB\n',
                        # Without a SwapTotal line the host is unknown, not swapless.
                        'MemTotal: 8000000 kB\nMemAvailable: 6000000 kB\n'):
            with self.healthy_host():
                self.P.meminfo.write_text(meminfo)
                self.assertEqual(tofi_host.preflight(self.options()), [], meminfo)

    def test_per_computer_need_follows_worker_config(self):
        self.P.meminfo.write_text(self.NO_SWAP_8G)
        self.assertEqual(tofi_host.computer_memory_need_mib({'memory_mib': 2048}), 2560)
        self.assertEqual(tofi_host.computer_memory_need_mib(None), 1536)
        warning, detail = tofi_host.memory_report(2560)
        self.assertIn('each computer needs about 2560 MiB (room for 2 now); swap 0 MiB', detail)
        self.assertIn(detail, warning)

    def test_doctor_reports_memory_as_warning_not_failure(self):
        self.installed()
        stubs = [mock.patch.object(tofi_host, name, return_value=True)
                 for name in ('health', 'inspect_image', 'validate_release', 'certificate_usable')]
        for stub in stubs:
            stub.start()
            self.addCleanup(stub.stop)
        results = {}
        for label, meminfo in (('no-swap', self.NO_SWAP_8G),
                               ('swap', 'MemTotal: 8000000 kB\nMemAvailable: 6000000 kB\nSwapTotal: 4194300 kB\n')):
            self.P.meminfo.write_text(meminfo)
            sys.stdout.truncate(0)
            sys.stdout.seek(0)
            results[label] = tofi_host.doctor()
            row = next(line for line in sys.stdout.getvalue().splitlines() if 'Memory' in line)
            if label == 'no-swap':
                self.assertTrue(row.startswith('WARN'), row)
                self.assertIn('no swap', row)
            else:
                self.assertTrue(row.startswith('ok'), row)
                self.assertIn('swap 4095 MiB', row)
            self.assertIn('each computer needs about 1536 MiB', row)
        # The advisory never changes the doctor's verdict.
        self.assertEqual(results['no-swap'], results['swap'])


class RenderTests(HostCase):
    def test_worker_config_passes_validate_config(self):
        config = tofi_host.render_worker_config(OLD, 3, 6144, GUEST_SHA, 'c' * 64)
        tofi_host.validate_config(config)
        self.assertEqual(config['release_dir'], '/var/lib/tofi/guest/' + OLD)
        self.assertEqual(config['broker_socket'], '/run/tofi/broker.sock')
        self.assertEqual(config['per_account_internal_reserved_bytes'], 8 * tofi_host.GIB)
        self.assertEqual(config['host_memory_headroom_mib'], 1024)
        self.assertEqual(config['cgroup_root'], '/run/tofi-worker/cgroup/tofi-vms')

    def test_worker_memory_budget_lower_bound_is_1536(self):
        tofi_host.render_worker_config(OLD, 1, 1536, GUEST_SHA, 'c' * 64)
        with self.assertRaises(tofi_host.HostError):
            tofi_host.render_worker_config(OLD, 1, 1535, GUEST_SHA, 'c' * 64)

    def test_budgets_follow_host_size(self):
        with mock.patch.object(tofi_host.os, 'cpu_count', return_value=2):
            self.P.meminfo.write_text('MemTotal: 4000000 kB\n')
            self.assertEqual(tofi_host.runtime_budgets(), {'cpu': 1, 'memory_mib': 1858})
            self.P.meminfo.write_text('MemTotal: 3000000 kB\n')
            self.assertEqual(tofi_host.runtime_budgets()['memory_mib'], 1536)

    def test_env_exposure_modes(self):
        budgets = {'cpu': 1, 'memory_mib': 2048}

        def env_for(*argv):
            return tofi_host.render_env(manifest(), tofi_host.install_options(
                tofi_host.parse_args(['install'] + list(argv))), budgets)

        tls_files = ('/etc/tofi-tls/cert.pem', '/etc/tofi-tls/key.pem')
        default = env_for()
        self.assertEqual((default['TOFI_BIND'], default['TOFI_PUBLIC_ORIGIN']), ('0.0.0.0', ''))
        self.assertEqual((default['TOFI_TLS_CERT_FILE'], default['TOFI_TLS_KEY_FILE']), tls_files)
        self.assertEqual(default['TOFI_OWNER_ALLOW_LAN_HTTP'], '0')
        self.assertEqual(env_for('--lan'), default, '--lan is an alias of the HTTPS default')
        local = env_for('--local-only', '--port', '9000')
        self.assertEqual((local['TOFI_BIND'], local['TOFI_HTTP_PORT']), ('127.0.0.1', '9000'))
        self.assertEqual((local['TOFI_TLS_CERT_FILE'], local['TOFI_TLS_KEY_FILE']), tls_files)
        tls = env_for('--domain', 'tofi.example.com')
        self.assertEqual((tls['TOFI_BIND'], tls['TOFI_PUBLIC_ORIGIN']), ('127.0.0.1', 'https://tofi.example.com'))
        self.assertEqual((tls['TOFI_TLS_CERT_FILE'], tls['TOFI_TLS_KEY_FILE']), ('', ''))
        self.assertEqual(tls['TOFI_OWNER_ALLOW_LAN_HTTP'], '1', 'Caddy reaches the App over the bridge')
        # Only the local Caddy container is trusted for X-Forwarded-For, and only with --domain.
        self.assertEqual(tls['TOFI_TRUSTED_PROXIES'], '172.16.0.0/12,192.168.0.0/16')
        self.assertEqual((default['TOFI_TRUSTED_PROXIES'], local['TOFI_TRUSTED_PROXIES']), ('', ''))
        self.assertIn('TOFI_TRUSTED_PROXIES=172.16.0.0/12,192.168.0.0/16\n', tofi_host.format_env(tls))
        self.assertIn('TOFI_TRUSTED_PROXIES: ${TOFI_TRUSTED_PROXIES:-}', (HERE / 'compose.yaml').read_text())
        for argv in (['--lan', '--local-only'], ['--domain', 'tofi.example.com', '--local-only'],
                     ['--domain', 'tofi.example.com', '--lan']):
            with self.subTest(argv=argv), self.assertRaises(tofi_host.HostError):
                env_for(*argv)
        self.assertEqual(tls['TOFI_WORKER_MEMORY_LIMIT'], '2304m')
        self.assertEqual(tofi_host.read_env(self.write_env(tls)), tls)

    def write_env(self, values):
        tofi_host.write_env(values)
        return self.P.env_file

    def test_caddyfile(self):
        text = tofi_host.render_caddyfile(HERE, 'tofi.example.com', 'ops@example.com')
        self.assertIn('email ops@example.com', text)
        self.assertIn('tofi.example.com {', text)
        self.assertIn('reverse_proxy app:8321', text)
        self.assertNotIn('email', tofi_host.render_caddyfile(HERE, 'tofi.example.com', ''))
        with self.assertRaises(tofi_host.HostError):
            tofi_host.render_caddyfile(HERE, 'bad domain; {', '')

    def test_apparmor_profile_name_and_paths(self):
        profile = tofi_host.render_apparmor(HERE)
        self.assertIn('profile tofi-worker flags', profile)
        self.assertIn('/var/lib/tofi/worker/** rwkl', profile)
        self.assertNotIn('tofi-account-worker', profile)

    def test_apparmor_profile_lets_firecracker_write_its_snapshot(self):
        # rc.5 acceptance: hibernation failed with EACCES (apparmor DENIED
        # mknod /snapshot/vmstate) because the confined, jailed Firecracker
        # could not create the files manager.py asks it to write.
        import re
        source = (HERE.parent / 'microvm' / 'manager.py').read_text()
        written = set(re.findall(r'"(?:snapshot_path|mem_file_path)": "(/snapshot/[a-z]+)"', source))
        self.assertEqual(written, {'/snapshot/vmstate', '/snapshot/memory'})
        files = re.search(r'^SNAPSHOT_FILES = \(([^)]*)\)', source, re.M).group(1)
        self.assertEqual({'/snapshot/' + name for name in re.findall(r'"([a-z]+)"', files)}, written)
        profile = tofi_host.render_apparmor(HERE)
        rule = re.search(r'^\s*/snapshot/\{([a-z,]+)\} (\w+),$', profile, re.M)
        self.assertIsNotNone(rule, 'profile must allow the jailed snapshot files')
        self.assertEqual({'/snapshot/' + name for name in rule.group(1).split(',')}, written)
        self.assertIn('w', rule.group(2))

    def test_apparmor_profile_lets_the_worker_bind_release_images_into_jails(self):
        # rc.5 acceptance: link(2) from the read-only release volume into the
        # jail fails with EXDEV, and the dense 5 GiB copy cost ~7.6 s on every
        # start and resume. manager.py now bind-mounts the release file
        # read-only over a jail placeholder and unmounts it before removal.
        import re
        source = (HERE.parent / 'microvm' / 'manager.py').read_text()
        names = re.search(r'for name in \(("vmlinux", "rootfs.ext4")\):\n\s+target = self.jail / name', source)
        self.assertIsNotNone(names, 'manager attaches exactly these release images')
        self.assertIn('run("mount", "--bind", str(source), str(target)', source)
        self.assertIn('run("umount", point', source)
        profile = tofi_host.render_apparmor(HERE)
        bind = re.search(r'^\s*mount options=\(bind\) (/var/lib/tofi/guest/\S+) -> (\S+),$', profile, re.M)
        self.assertIsNotNone(bind, 'profile must allow the release image bind mount')
        self.assertEqual(bind.group(1), '/var/lib/tofi/guest/*/{vmlinux,rootfs.ext4}')
        target = bind.group(2)
        self.assertTrue(target.startswith('/var/lib/tofi/worker/state/'), target)
        self.assertTrue(target.endswith('/root/{vmlinux,rootfs.ext4}'), target)
        self.assertIn('/jails/firecracker/ac-', target)
        self.assertRegex(profile, r'(?m)^\s*umount ' + re.escape(target) + ',$')
        # The jail path pattern is the same one the jailer's rbind is allowed on.
        jail = target[:-len('{vmlinux,rootfs.ext4}')]
        self.assertIn('mount options=(rbind) %s -> %s,' % (jail, jail), profile)
        self.assertNotIn('/var/lib/tofi-guest', profile)

        tofi_host.validate_manifest(manifest())
        bad = manifest()
        bad['images']['app'] = 'ghcr.io/jackzhao98/tofi:latest'
        with self.assertRaisesRegex(tofi_host.HostError, 'pinned'):
            tofi_host.validate_manifest(bad)
        bad = manifest()
        bad['bundle']['url'] = 'http://example.com/tofi-host.tar.gz'
        with self.assertRaisesRegex(tofi_host.HostError, 'HTTPS'):
            tofi_host.validate_manifest(bad)
        bad = manifest()
        bad['version'] = '0.2'
        with self.assertRaises(tofi_host.HostError):
            tofi_host.validate_manifest(bad)

    def test_apply_host_config_files_and_commands(self):
        env = self.installed()
        self.assertEqual(json.loads(self.P.worker_json.read_text())['release_dir'], '/var/lib/tofi/guest/' + OLD)
        self.assertEqual(self.P.tmpfiles.read_text().splitlines()[-1], 'd /run/tofi 0750 0 10001 -')
        self.assertEqual(oct(self.P.env_file.stat().st_mode & 0o777), '0o600')
        tofi_host.apply_host_config(self.P.current, env)
        parsed = [c[:2] for c in self.commands]
        self.assertIn(['apparmor_parser', '-Q'], parsed)
        self.assertIn(['apparmor_parser', '-r'], parsed)
        self.assertLess(parsed.index(['apparmor_parser', '-Q']), parsed.index(['apparmor_parser', '-r']))


class DockerTests(HostCase):
    def inspect(self, reference, labels, entrypoint, arch='amd64', digests=None):
        data = [{'Architecture': arch, 'Os': 'linux',
                 'RepoDigests': digests if digests is not None else [reference],
                 'Config': {'Labels': labels, 'Entrypoint': entrypoint}}]
        tofi_host.run.side_effect = lambda args, **kw: completed(json.dumps(data))

    def test_inspect_image_labels(self):
        app = image('app', '1')
        labels = {'io.tofi.account-runtime': '1', 'io.tofi.data-schema': 'tofi-account-data-v1',
                  'io.tofi.account-guest-protocol': 'tofi-account-guest-v1'}
        self.inspect(app, labels, ['/app/tofi'])
        tofi_host.inspect_image(app, 'app', 'tofi-account-data-v1')
        with self.assertRaisesRegex(tofi_host.HostError, 'data schema'):
            tofi_host.inspect_image(app, 'app', 'tofi-account-data-v2')
        self.inspect(app, dict(labels, **{'io.tofi.account-guest-protocol': 'other'}), ['/app/tofi'])
        with self.assertRaisesRegex(tofi_host.HostError, 'Guest protocol'):
            tofi_host.inspect_image(app, 'app')
        self.inspect(app, labels, ['/app/tofi'], arch='arm64')
        with self.assertRaisesRegex(tofi_host.HostError, 'linux/amd64'):
            tofi_host.inspect_image(app, 'app')
        self.inspect(app, labels, ['/app/tofi'], digests=['ghcr.io/jackzhao98/tofi@sha256:' + '0' * 64])
        with self.assertRaisesRegex(tofi_host.HostError, 'pinned digest'):
            tofi_host.inspect_image(app, 'app')
        worker = image('worker', '1')
        self.inspect(worker, {'io.tofi.account-worker': '1',
                              'io.tofi.account-guest-protocol': 'tofi-account-guest-v1'},
                     tofi_host.WORKER_ENTRYPOINT)
        tofi_host.inspect_image(worker, 'worker')
        caddy = image('caddy', '9')
        self.inspect(caddy, {}, ['caddy'], digests=['caddy@sha256:' + '9' * 64])
        tofi_host.inspect_image(caddy, 'caddy')

    def test_pull_failure_names_docker_reason(self):
        def failing(args, **kw):
            raise subprocess.CalledProcessError(1, args, '', 'Error response from daemon: manifest unknown\n')
        tofi_host.run.side_effect = failing
        env = {'TOFI_APP_IMAGE': image('app', '0'), 'TOFI_WORKER_IMAGE': image('worker', '0')}
        with self.assertRaisesRegex(tofi_host.HostError, 'Cannot pull .*: Error response from daemon: manifest unknown'):
            tofi_host.pull_images(env, None)

    def test_stopped_fences_unclean_worker_exit(self):
        containers = [{'Id': 'w' * 64, 'Config': {'Labels': {'com.docker.compose.service': 'worker'}},
                       'State': {'Running': False, 'ExitCode': 1}}]

        def docker(args, **kw):
            if args[:2] == ['docker', 'ps']:
                return completed('w' * 12 + '\n')
            if args[:2] == ['docker', 'inspect']:
                return completed(json.dumps(containers))
            return completed()
        tofi_host.run.side_effect = docker
        with self.assertRaisesRegex(tofi_host.HostError, 'stopped uncleanly'):
            tofi_host.stopped()
        containers[0]['State']['ExitCode'] = 0
        tofi_host.stopped()

    def test_worker_ready_requires_first_account_capacity(self):
        worker = [{'Id': 'w', 'Config': {'Labels': {'com.docker.compose.service': 'worker'}},
                   'State': {'Running': True, 'Pid': 4242}}]
        replies = {'value': {'remaining_bytes': 10 * tofi_host.GIB, 'empty': True}}

        def docker(args, **kw):
            if args[:2] == ['docker', 'ps']:
                return completed('w\n')
            if args[:2] == ['docker', 'inspect']:
                return completed(json.dumps(worker))
            self.assertEqual((kw['user'], kw['group']), (10001, 10001))
            self.assertEqual(args[-1], '4242')
            return completed(json.dumps(replies['value']))
        tofi_host.run.side_effect = docker
        with self.assertRaisesRegex(tofi_host.HostError, 'first account needs 16 GiB'):
            tofi_host.worker_ready(first_install=True)
        tofi_host.worker_ready(first_install=False)
        replies['value'] = {'remaining_bytes': 20 * tofi_host.GIB, 'empty': True}
        tofi_host.worker_ready(first_install=True)

    def test_worker_ready_retries_while_broker_starts(self):
        worker = [{'Id': 'w', 'Config': {'Labels': {'com.docker.compose.service': 'worker'}},
                   'State': {'Running': True, 'Pid': 7}}]
        attempts = []

        def docker(args, **kw):
            if args[:2] == ['docker', 'ps']:
                return completed('w\n')
            if args[:2] == ['docker', 'inspect']:
                return completed(json.dumps(worker))
            attempts.append(1)
            if len(attempts) < 3:
                raise subprocess.CalledProcessError(75, args, '', 'broker starting')
            return completed(json.dumps({'remaining_bytes': 0, 'empty': False}))
        tofi_host.run.side_effect = docker
        tofi_host.worker_ready()
        self.assertEqual(len(attempts), 3)
        tofi_host.run.side_effect = lambda args, **kw: (
            completed('w\n') if args[:2] == ['docker', 'ps'] else
            completed(json.dumps(worker)) if args[:2] == ['docker', 'inspect'] else
            (_ for _ in ()).throw(subprocess.CalledProcessError(78, args, '', 'peer mismatch')))
        with self.assertRaisesRegex(tofi_host.HostError, 'peer mismatch'):
            tofi_host.worker_ready()


class LockTests(HostCase):
    def test_lock_contention_refused(self):
        with tofi_host.lifecycle_lock():
            with self.assertRaisesRegex(tofi_host.HostError, 'Another tofi operation is running'):
                with tofi_host.lifecycle_lock():
                    pass
        with tofi_host.lifecycle_lock():
            pass

    def test_mutating_commands_refuse_while_locked(self):
        self.installed()
        with tofi_host.lifecycle_lock():
            for call in (tofi_host.stop, tofi_host.start, lambda: tofi_host.uninstall(),
                         lambda: tofi_host.upgrade(manifest_path=None, version=NEW)):
                with self.assertRaisesRegex(tofi_host.HostError, 'Another tofi operation'):
                    call()


class GuestTests(HostCase):
    def serve(self, payload):
        @contextlib.contextmanager
        def opener(url, timeout=60):
            yield io.BytesIO(payload)
        return mock.patch.object(tofi_host, 'https_open', side_effect=opener)

    def leftovers(self):
        return sorted(p.name for p in self.P.guest.iterdir()) if self.P.guest.exists() else []

    def test_guest_sha_mismatch_leaves_no_directory(self):
        guest = manifest()['guest']
        with self.serve(b'not the published guest archive'):
            with self.assertRaisesRegex(tofi_host.HostError, 'Checksum mismatch'):
                tofi_host.fetch_guest(guest, HERE / 'tofi_host.py')
        self.assertEqual(self.leftovers(), [])
        self.assertFalse(any(c[0] == 'tar' for c in self.commands))

    def test_invalid_release_leaves_no_directory(self):
        payload = b'synthetic archive'
        guest = manifest()['guest']
        guest['sha256'] = hashlib.sha256(payload).hexdigest()

        def extract(args, check=True, timeout=300, **kwargs):
            target = Path(args[args.index('-C') + 1])
            (target / 'bin').mkdir()
            (target / 'rootfs.ext4').write_bytes(b'x')
            return completed()
        tofi_host.run.side_effect = extract
        with self.serve(payload), mock.patch.object(tofi_host, 'validate_release',
                                                    side_effect=ValueError('release manifest mismatch')):
            with self.assertRaisesRegex(ValueError, 'mismatch'):
                tofi_host.fetch_guest(guest, HERE / 'tofi_host.py')
        self.assertEqual(self.leftovers(), [])

    def test_valid_release_is_sealed_in_place(self):
        payload = b'synthetic archive'
        guest = manifest()['guest']
        guest['sha256'] = hashlib.sha256(payload).hexdigest()
        tofi_host.run.side_effect = lambda args, **kw: completed()
        with self.serve(payload), mock.patch.object(tofi_host, 'validate_release') as validate:
            final = tofi_host.fetch_guest(guest, HERE / 'tofi_host.py')
            self.assertEqual(final, self.P.guest / NEW)
            self.assertEqual(oct(final.stat().st_mode & 0o777), '0o555')
            tofi_host.fetch_guest(guest, HERE / 'tofi_host.py')  # idempotent: re-validates only
            self.assertEqual(validate.call_count, 2)
        self.assertEqual(self.leftovers(), [NEW])
        os.chmod(final, 0o755)


class LifecycleBase(HostCase):
    def setUp(self):
        super().setUp()
        self.stopped = mock.patch.object(tofi_host, 'stopped').start()
        self.start_services = mock.patch.object(tofi_host, 'start_services').start()
        self.pull = mock.patch.object(tofi_host, 'pull_images').start()
        self.label = mock.patch.object(tofi_host, 'image_label', return_value='tofi-account-data-v1').start()
        self.fetch_guest = mock.patch.object(tofi_host, 'fetch_guest', side_effect=lambda g, m: self.make_guest(g['version'])).start()
        self.install_bundle = mock.patch.object(tofi_host, 'install_bundle', side_effect=lambda m: self.make_bundle(m['version'])).start()
        self.active_runs = mock.patch.object(tofi_host, 'active_runs', return_value=0).start()
        self.addCleanup(mock.patch.stopall)

    def write_manifest(self, value):
        path = self.root / 'manifest.json'
        path.write_text(json.dumps(value))
        return path


class LifecycleTests(LifecycleBase):
    def test_fresh_install_writes_config_and_journal(self):
        self.make_bundle(OLD)
        self.P.opt.mkdir(parents=True, exist_ok=True)
        os.symlink('releases/' + OLD, self.P.current)
        options = tofi_host.install_options(tofi_host.parse_args(['install', '--manifest', 'm.json']))
        addresses = [('inet', '10.0.10.39'), ('inet', '192.168.7.2'), ('inet6', '2001:db8::39')]
        with mock.patch.object(tofi_host, 'preflight', return_value=[]), \
                mock.patch.object(tofi_host, 'ASSETS', self.P.releases / OLD), \
                mock.patch.object(tofi_host, 'interface_addresses', return_value=addresses), \
                mock.patch.object(tofi_host.os, 'cpu_count', return_value=4):
            self.P.data.mkdir(parents=True)
            self.write(self.P.bootstrap_secret, 'synthetic-setup-key\n')
            result = tofi_host.install(str(self.write_manifest(manifest(OLD, '1'))), options)
        self.assertEqual(self.phase(), 'installed')
        self.assertTrue(result['setup_key_pending'])
        out = sys.stdout.getvalue()
        self.assertIn('  Setup key   synthetic-setup-key   (one time)', out)
        self.assertIn('  Open        https://10.0.10.39:8321\n              https://192.168.7.2:8321\n', out)
        self.assertNotIn('http://', out)
        self.assertNotIn('ssh -L', out)
        self.assertIn(tofi_host.certificate_fingerprint(), out)
        self.assertIn('Cloud server? allow TCP 8321', out)
        self.assertEqual(result['urls'], ['https://10.0.10.39:8321', 'https://192.168.7.2:8321'])
        env = tofi_host.read_env()
        self.assertEqual(env['TOFI_BIND'], '0.0.0.0')
        self.assertEqual(env['TOFI_TLS_CERT_FILE'], '/etc/tofi-tls/cert.pem')
        self.assertEqual(env['TOFI_TLS_KEY_FILE'], '/etc/tofi-tls/key.pem')
        self.assertEqual(env['TOFI_OWNER_ALLOW_LAN_HTTP'], '0')
        self.assertEqual(env['TOFI_PUBLIC_ORIGIN'], '')
        self.assertEqual(env['TOFI_APP_IMAGE'], image('app', '1'))
        self.assertEqual(env['TOFI_CPU_BUDGET'], '3')
        tofi_host.validate_config(json.loads(self.P.worker_json.read_text()))
        self.start_services.assert_called_once()
        self.assertTrue(self.start_services.call_args.kwargs['first_install'])
        self.assertIn(['systemctl', 'enable', 'tofi.service'], self.commands)

    def test_install_again_when_installed_only_reports(self):
        self.installed()
        with mock.patch.object(tofi_host, 'status', return_value={'phase': 'installed'}) as status:
            tofi_host.install(None, {'port': 8321})
        status.assert_called_once()
        self.start_services.assert_not_called()
        self.stopped.assert_not_called()

    def test_failed_install_is_journalled_and_keeps_data(self):
        env = self.installed(phase='installed')
        state = tofi_host.new_state(OLD)
        self.start_services.side_effect = tofi_host.HostError('worker not ready')
        with self.assertRaises(tofi_host.HostError):
            tofi_host.finish_install(state, env)
        journal = json.loads(self.P.state_file.read_text())
        self.assertEqual(journal['phase'], 'install-failed')
        self.assertEqual(journal['last_error']['message'], 'worker not ready')
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)

    def test_upgrade_success(self):
        self.installed()
        result = tofi_host.upgrade(manifest_path=str(self.write_manifest(manifest())))
        self.assertTrue(result['upgraded'])
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], NEW)
        self.assertEqual(tofi_host.current_release(), NEW)
        self.assertEqual(self.phase(), 'installed')
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)

    def test_upgrade_failure_rolls_back_env_and_journal(self):
        before = self.installed()
        env_bytes = self.P.env_file.read_bytes()
        worker_bytes = json.loads(self.P.worker_json.read_text())
        self.start_services.side_effect = [tofi_host.HostError('candidate unhealthy'), None]
        with self.assertRaisesRegex(tofi_host.HostError, 'update rejected; previous version restored'):
            tofi_host.upgrade(manifest_path=str(self.write_manifest(manifest())))
        self.assertEqual(self.stopped.call_count, 2)
        self.assertEqual(self.P.env_file.read_bytes(), env_bytes)
        self.assertEqual(tofi_host.read_env()['TOFI_APP_IMAGE'], before['TOFI_APP_IMAGE'])
        self.assertEqual(json.loads(self.P.worker_json.read_text()), worker_bytes)
        self.assertEqual(tofi_host.current_release(), OLD)
        journal = json.loads(self.P.state_file.read_text())
        self.assertEqual((journal['phase'], journal['version'], journal['transaction']), ('installed', OLD, None))
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)
        # The rejection stays visible and the rejected candidate is removed.
        self.assertEqual(journal['last_update_failure']['version'], NEW)
        self.assertEqual(journal['last_update_failure']['message'], 'candidate unhealthy')
        self.assertFalse((self.P.releases / NEW).exists())
        self.assertFalse((self.P.guest / NEW).exists())
        self.assertTrue((self.P.releases / OLD).is_dir())
        self.assertTrue((self.P.guest / OLD).is_dir())
        with mock.patch.object(tofi_host, 'project_containers', return_value=[]), \
                mock.patch.object(tofi_host, 'health', return_value=True):
            self.assertEqual(tofi_host.status()['last_update_failure']['version'], NEW)
        # A later successful update clears the record.
        self.start_services.side_effect = None
        tofi_host.upgrade(manifest_path=str(self.write_manifest(manifest())))
        self.assertNotIn('last_update_failure', json.loads(self.P.state_file.read_text()))

    def test_schema_label_mismatch_refuses_before_stop(self):
        self.installed()
        env_bytes = self.P.env_file.read_bytes()
        state_bytes = self.P.state_file.read_bytes()
        self.label.side_effect = lambda ref, label: ('tofi-account-data-v1' if ref.endswith('1' * 64)
                                                     else 'tofi-account-data-v2')
        changed = manifest()
        changed['data_schema'] = 'tofi-account-data-v2'
        with self.assertRaisesRegex(tofi_host.HostError, 'data schema changes'):
            tofi_host.upgrade(manifest_path=str(self.write_manifest(changed)))
        self.stopped.assert_not_called()
        self.start_services.assert_not_called()
        self.install_bundle.assert_not_called()
        self.assertEqual(self.P.env_file.read_bytes(), env_bytes)
        self.assertEqual(self.P.state_file.read_bytes(), state_bytes)
        # The explicit override proceeds.
        result = tofi_host.upgrade(manifest_path=str(self.write_manifest(changed)), allow_schema_change=True)
        self.assertTrue(result['upgraded'])

    def test_same_version_update_is_noop(self):
        self.installed()
        result = tofi_host.upgrade(manifest_path=str(self.write_manifest(manifest(OLD, '1'))))
        self.assertFalse(result['upgraded'])
        self.stopped.assert_not_called()

    def test_uninstall_retains_data(self):
        self.installed()
        result = tofi_host.uninstall()
        self.assertTrue(result['data_retained'])
        self.assertEqual(self.phase(), 'stopped-retained')
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)
        self.assertTrue(self.P.env_file.exists())
        self.assertTrue(self.P.apparmor_profile.exists())
        self.assertTrue((self.P.guest / OLD).exists())
        compose_calls = [c for c in self.commands if c[:2] == ['docker', 'compose']]
        self.assertEqual(compose_calls[-1][-3:], ['rm', '-f', '-s'])
        self.assertFalse(any('down' in c or '-v' in c or '--volumes' in c for c in compose_calls))
        self.assertFalse(any(c[:3] == ['docker', 'image', 'rm'] for c in self.commands))

    def test_unclean_stop_blocks_uninstall_and_keeps_data(self):
        self.installed()
        self.stopped.side_effect = tofi_host.HostError('worker stopped uncleanly')
        with self.assertRaises(tofi_host.HostError):
            tofi_host.uninstall()
        self.assertEqual(self.phase(), 'uninstall-failed')
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)

    def test_reinstall_over_stopped_retained_keeps_data(self):
        self.installed(phase='stopped-retained')
        tofi_host.install(None, {'port': 8321})
        self.assertEqual(self.phase(), 'installed')
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)
        self.assertFalse(self.start_services.call_args is None)
        self.assertIn('Sign in     with your existing admin account', sys.stdout.getvalue())
        self.assertNotIn('Setup key', sys.stdout.getvalue())

    def test_purge_requires_hostname(self):
        self.installed(phase='stopped-retained')
        with self.assertRaisesRegex(tofi_host.HostError, 'Purge cancelled'):
            tofi_host.uninstall(purge=True, confirm='wrong-host')
        self.assertTrue(self.P.data.exists())
        tofi_host.write_json(self.P.latest_cache, {'version': NEW})
        tofi_host.uninstall(purge=True, confirm=tofi_host.socket.gethostname())
        self.assertFalse(self.P.latest_cache.parent.exists())
        self.assertFalse(self.P.var.exists())
        self.assertFalse(self.P.etc.exists())
        self.assertFalse(self.P.apparmor_profile.exists())
        self.assertIn(['apparmor_parser', '-R', str(self.P.apparmor_profile)], self.commands)

    def test_start_refuses_pending_transaction(self):
        self.installed(phase='upgrade-failed')
        with self.assertRaisesRegex(tofi_host.HostError, 'resume'):
            tofi_host.start()


class ResumeTests(LifecycleBase):
    def pending(self, phase, kind):
        self.installed()
        state = json.loads(self.P.state_file.read_text())
        state['phase'] = phase
        if kind == 'upgrade':
            previous = tofi_host.snapshot()
            # The interrupted candidate already switched config and current.
            self.make_bundle(NEW)
            self.make_guest(NEW)
            candidate = dict(previous['env'], TOFI_VERSION=NEW, TOFI_GUEST_VERSION=NEW,
                             TOFI_APP_IMAGE=image('app', '2'), TOFI_WORKER_IMAGE=image('worker', '2'))
            tofi_host.apply_host_config(self.P.releases / NEW, candidate)
            tofi_host.switch_current(NEW)
            state['transaction'] = {'kind': 'upgrade', 'step': 'start-candidate', 'previous': previous,
                                    'candidate_version': NEW}
        else:
            state['transaction'] = {'kind': kind, 'step': 'stop-services' if kind == 'uninstall' else 'start-worker'}
        self.P.state_file.write_text(json.dumps(state))
        self.commands.clear()

    def test_resume_install_phases(self):
        for phase in ('prepared', 'installing', 'install-failed'):
            with self.subTest(phase=phase):
                self.tearDown_install()
                self.pending(phase, 'install')
                tofi_host.install(None, {'port': 8321})
                self.assertEqual(self.phase(), 'installed')
                self.assertTrue(self.start_services.call_args.kwargs['first_install'])
                self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)

    def test_resume_upgrade_phases_roll_back(self):
        for phase in ('upgrading', 'upgrade-failed', 'rolling-back', 'rollback-failed'):
            with self.subTest(phase=phase):
                self.tearDown_install()
                self.pending(phase, 'upgrade')
                tofi_host.install(None, {'port': 8321})
                self.assertEqual(self.phase(), 'installed')
                self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
                self.assertEqual(tofi_host.current_release(), OLD)
                self.assertEqual(json.loads(self.P.worker_json.read_text())['release_dir'],
                                 '/var/lib/tofi/guest/' + OLD)

    def test_resume_uninstall_phases(self):
        for phase in ('uninstalling', 'uninstall-failed'):
            with self.subTest(phase=phase):
                self.tearDown_install()
                self.pending(phase, 'uninstall')
                tofi_host.install(None, {'port': 8321})
                self.assertEqual(self.phase(), 'stopped-retained')
                self.assertEqual((self.P.data / 'tofi.db').read_bytes(), DATA)

    def tearDown_install(self):
        """Reset the synthetic root between subtests."""
        for child in list(self.root.iterdir()):
            if child.name == 'proc':
                continue
            tofi_host.remove_tree(child)
        self.start_services.reset_mock(side_effect=True)


ACCOUNTS = {
    'running': '3f2a9c1e-0000-4000-8000-000000000001',
    'hibernated': '9b77d0aa-0000-4000-8000-000000000002',
    'stopped': 'c0ffee00-0000-4000-8000-000000000003',
    'unresponsive': 'd00dfeed-0000-4000-8000-000000000004',
    'busy': 'e1e1e1e1-0000-4000-8000-000000000005',
}
OLDER = 'v0.0.9'


class FakeWorker:
    """The Worker broker and computer control sockets behind CONTROL_PROBE."""

    def __init__(self, release=OLD):
        self.release = release
        self.rows = []
        self.managers = {}
        self.upgrades = []

    def computer(self, key, running=True, snapshot=None, state='ready', release=None, health='ok',
                 busy=(), ledger='ready', rss=612, wake=None):
        account = ACCOUNTS[key]
        self.rows.append({'account_id': account, 'slot': len(self.rows) + 1, 'state': ledger,
                          'quota_bytes': 8 * tofi_host.GIB, 'running': running,
                          'config_release': release or self.release,
                          'snapshot': {'release': snapshot, 'created_at': '2026-10-08T01:02:03Z'} if snapshot else None})
        if running:
            info = {'state': state, 'release': release or self.release, 'error': ''}
            if wake:
                info['last_wake'] = wake
            self.managers[account] = {
                '/v1/info': info,
                '/v1/resources': {'memory': {'host_rss_mib': rss if state == 'ready' else None}},
                '/v1/health': {'state': state, 'guest': health},
                '/v1/busy': {'state': state, 'release': release or self.release, 'busy': list(busy)},
            }
        return account

    def __call__(self, calls):
        replies = []
        for call in calls:
            if call['socket'] == '/run/tofi/broker.sock':
                assert call['peer_pid'] == 4242
                body = call['body']
                if body['op'] == 'computers':
                    replies.append({'status': 200, 'body': {'release': self.release, 'computers': self.rows}})
                elif body['op'] == 'upgrade':
                    self.upgrades.append(body['account_id'])
                    row = next(r for r in self.rows if r['account_id'] == body['account_id'])
                    replies.append({'status': 200, 'body': {
                        'account_id': row['account_id'], 'release': self.release,
                        'stopped': bool(row['running']), 'discarded_snapshot': bool(row['snapshot'])}})
                else:
                    replies.append({'status': 400, 'body': {'error': 'request rejected'}})
                continue
            account = call['socket'].split('/')[4]
            answer = self.managers.get(account, {}).get(call['path'])
            replies.append({'status': 200, 'body': answer} if answer else {'error': 'not running'})
        return replies


class ComputerCase(LifecycleBase):
    def setUp(self):
        super().setUp()
        self.worker = FakeWorker()
        mock.patch.object(tofi_host, 'worker_pid', return_value=4242).start()
        self.calls = mock.patch.object(tofi_host, 'control_calls', side_effect=self.worker).start()
        self.fetch = mock.patch.object(tofi_host, 'fetch_manifest', return_value=tofi_host.validate_manifest(manifest())).start()

    def snapshot_file(self, account, release):
        """A hibernated computer's snapshot meta as the manager writes it."""
        directory = self.P.worker_state / account / 'snapshot'
        directory.mkdir(parents=True, exist_ok=True)
        (directory / 'meta.json').write_text(json.dumps({'format': 1, 'release': release,
                                                         'created_at': '2026-10-08T01:02:03Z'}))

    def fleet(self):
        """One computer in each state; the hibernated one holds an older snapshot."""
        w = self.worker
        w.computer('running', wake={'kind': 'restore', 'seconds': 2.1})
        w.computer('hibernated', running=False, snapshot=OLDER)
        w.computer('stopped', running=False)
        w.computer('unresponsive', health='unresponsive')
        return w

    def main(self, *argv):
        sys.stdout.seek(0)
        sys.stdout.truncate()
        with mock.patch('sys.stderr', new_callable=io.StringIO) as err:
            code = tofi_host.main(list(argv))
        self.stderr = err.getvalue()
        return code, sys.stdout.getvalue()


class StatusTests(ComputerCase):
    def test_status_json_reports_versions_latest_and_computers(self):
        self.installed()
        self.fleet()
        with mock.patch.object(tofi_host, 'project_containers', return_value=[]), \
                mock.patch.object(tofi_host, 'health', return_value=True):
            code, out = self.main('status', '--json')
        self.assertEqual(code, 0)
        result = json.loads(out)
        versions = result['versions']
        self.assertEqual((versions['installed'], versions['latest'], versions['update_available']), (OLD, NEW, True))
        self.assertEqual((versions['app_image'], versions['guest']), (image('app', '1'), OLD))
        rows = {row['short']: row for row in result['computers']}
        self.assertEqual(list(rows), ['3f2a9c1e', '9b77d0aa', 'c0ffee00', 'd00dfeed'])
        running = rows['3f2a9c1e']
        self.assertEqual((running['state'], running['release'], running['upgrade_pending'], running['memory_rss_mib']),
                         ('running', OLD, False, 612))
        self.assertEqual(running['last_wake'], {'kind': 'restore', 'seconds': 2.1})
        hibernated = rows['9b77d0aa']
        self.assertEqual((hibernated['state'], hibernated['release'], hibernated['upgrade_pending'],
                          hibernated['memory_rss_mib'], hibernated['hibernated_at']),
                         ('hibernated', OLDER, True, None, '2026-10-08T01:02:03Z'))
        self.assertEqual((rows['c0ffee00']['state'], rows['c0ffee00']['release'], rows['c0ffee00']['upgrade_pending']),
                         ('stopped', None, False))
        self.assertEqual(rows['d00dfeed']['state'], 'unresponsive')
        # Status never asks a guest whether it is busy, and never asks stopped computers anything.
        asked = [c['path'] for batch in self.calls.call_args_list for c in batch.args[0]]
        self.assertNotIn('/v1/busy', asked)
        sockets = {c['socket'] for batch in self.calls.call_args_list for c in batch.args[0]}
        self.assertNotIn(tofi_host.control_socket(ACCOUNTS['stopped']), sockets)

    def test_status_human_output(self):
        self.installed()
        self.fleet()
        with mock.patch.object(tofi_host, 'project_containers', return_value=[]), \
                mock.patch.object(tofi_host, 'health', return_value=True):
            code, out = self.main('status')
        self.assertEqual(code, 0)
        self.assertIn('tofi %s · healthy\n' % OLD, out)
        self.assertIn('  Version      %s · %s available · sudo tofi update\n' % (OLD, NEW), out)
        self.assertNotIn('ghcr.io', out)
        self.assertIn('  Computers    4 · 1 running · 1 hibernated · 1 stopped · 1 unresponsive\n', out)
        self.assertIn('  ACCOUNT   STATE         GUEST   UPGRADE  MEMORY   LAST WAKE\n', out)
        self.assertIn('  3f2a9c1e  running       v0.1.0  -        612 MiB  restore 2.1s\n', out)
        self.assertIn('  9b77d0aa  hibernated    v0.0.9  pending  -        -\n', out)
        self.assertIn('  c0ffee00  stopped       -       -        -        -\n', out)
        self.assertIn('  d00dfeed  unresponsive  v0.1.0  -        612 MiB  -\n', out)
        self.assertIn('1 computer is on an older Guest than v0.1.0', out)
        self.assertIn('Switch now: sudo tofi computers upgrade --all', out)
        self.assertIn('  Open         ', out)
        self.assertNotIn('\033', out)

    def test_status_survives_a_stopped_worker_and_offline_release_check(self):
        self.installed()
        tofi_host.worker_pid.side_effect = tofi_host.HostError('The Worker is not running; start TOFI with `sudo tofi start`.')
        self.fetch.side_effect = tofi_host.HostError('Cannot download the release manifest (offline).')
        with mock.patch.object(tofi_host, 'project_containers', return_value=[]), \
                mock.patch.object(tofi_host, 'health', return_value=False):
            code, out = self.main('status')
        self.assertEqual(code, 0)
        self.assertIn('NOT healthy', out)
        self.assertIn("  Version      %s · couldn't check for updates\n" % OLD, out)
        self.assertNotIn('Cannot download', out)
        self.assertIn('Computers    unknown (The Worker is not running', out)

    def test_latest_release_is_cached_and_kept_offline(self):
        first = tofi_host.latest_release()
        self.assertEqual((first['version'], first['error']), (NEW, None))
        self.assertEqual(self.fetch.call_count, 1)
        self.assertEqual(self.fetch.call_args.kwargs, {'timeout': 5})
        tofi_host.latest_release()
        self.assertEqual(self.fetch.call_count, 1)  # within the cache lifetime
        self.fetch.side_effect = tofi_host.HostError('offline')
        with mock.patch.object(tofi_host.time, 'time', return_value=time.time() + tofi_host.LATEST_TTL_SECONDS + 1):
            stale = tofi_host.latest_release()
        self.assertEqual((stale['version'], stale['error'], stale['checked_at']),
                         (NEW, 'offline', first['checked_at']))
        tofi_host.latest_release()
        self.assertEqual(self.fetch.call_count, 2)  # a failed check is retried only after a while
        self.P.latest_cache.unlink()
        self.assertEqual((tofi_host.latest_release()['version'], tofi_host.latest_release()['error']),
                         (None, 'offline'))

    def test_version_ordering(self):
        self.assertTrue(tofi_host.is_newer('v0.1.0', 'v0.1.0-rc.6'))
        self.assertTrue(tofi_host.is_newer('v0.1.0-rc.10', 'v0.1.0-rc.9'))
        self.assertFalse(tofi_host.is_newer('v0.1.0-rc.6', 'v0.1.0'))
        self.assertFalse(tofi_host.is_newer(None, 'v0.1.0'))

    def test_computer_states(self):
        row = {'account_id': ACCOUNTS['running'], 'state': 'ready', 'config_release': OLD, 'snapshot': None}
        for manager_state, shown, running in (('ready', 'running', True), ('resuming', 'starting', True),
                                              ('starting', 'starting', True), ('restarting', 'restarting', True),
                                              ('purging', 'restarting', True), ('hibernating', 'hibernating', True),
                                              ('hibernated', 'hibernated', False), ('stopped', 'stopped', False),
                                              ('error', 'error', False)):
            with self.subTest(state=manager_state):
                result = tofi_host.computer_row(row, {'/v1/info': {'state': manager_state, 'release': OLD}}, NEW)
                self.assertEqual((result['state'], result['running']), (shown, running))
        disabled = tofi_host.computer_row(dict(row, state='disabled'), {}, NEW)
        self.assertEqual((disabled['state'], disabled['upgrade_pending']), ('disabled', False))
        older = tofi_host.computer_row(row, {'/v1/info': {'state': 'ready'}}, NEW)
        self.assertEqual((older['release'], older['upgrade_pending']), (OLD, True))  # an older manager: config

    def test_control_probe_only_reaches_sockets_under_run_tofi(self):
        calls = [{'socket': '/tmp/elsewhere.sock', 'method': 'GET', 'path': '/v1/info'},
                 {'socket': '/run/tofi/../etc/x.sock', 'method': 'GET', 'path': '/v1/info'},
                 {'socket': '/run/tofi/accounts/%s/control.sock' % ACCOUNTS['stopped'], 'method': 'GET',
                  'path': '/v1/info', 'timeout': 1}]
        result = subprocess.run([sys.executable, '-I', '-c', tofi_host.CONTROL_PROBE, json.dumps(calls)],
                                capture_output=True, text=True, timeout=30, check=True)
        replies = json.loads(result.stdout)
        self.assertIn('outside /run/tofi', replies[0]['error'])
        self.assertIn('outside /run/tofi', replies[1]['error'])
        self.assertEqual(replies[2], {'error': 'not running'})

    def test_control_calls_runs_the_probe_as_the_app_uid(self):
        mock.patch.stopall()
        mock.patch.object(tofi_host, 'run', side_effect=lambda args, **kw: (
            self.commands.append((args, kw)) or completed(json.dumps([{'status': 200, 'body': {'ok': True}}])))).start()
        self.commands.clear()
        self.assertEqual(tofi_host.control_calls([{'socket': '/run/tofi/broker.sock', 'method': 'GET',
                                                    'path': '/v1/info'}]), [{'status': 200, 'body': {'ok': True}}])
        args, kw = self.commands[0]
        self.assertEqual(args[:3], [sys.executable, '-I', '-c'])
        self.assertEqual((kw['user'], kw['group'], kw['extra_groups'], kw['timeout']), (10001, 10001, [], 25))
        tofi_host.run.side_effect = lambda args, **kw: completed('[]')
        with self.assertRaisesRegex(tofi_host.HostError, 'unexpected reply'):
            tofi_host.control_calls([{'socket': '/run/tofi/broker.sock', 'method': 'GET', 'path': '/'}])

    def test_broker_errors_are_named(self):
        self.worker.rows = None
        with self.assertRaisesRegex(tofi_host.HostError, 'no computer list'):
            tofi_host.computers_report(OLD)
        tofi_host.control_calls.side_effect = lambda calls: [{'status': 409, 'body': {'error': 'computer disabled'}}]
        with self.assertRaisesRegex(tofi_host.HostError, 'refused upgrade: computer disabled'):
            tofi_host.broker_request({'op': 'upgrade', 'account_id': ACCOUNTS['running']})
        tofi_host.control_calls.side_effect = lambda calls: [{'error': 'not running'}]
        with self.assertRaisesRegex(tofi_host.HostError, 'broker is unreachable'):
            tofi_host.broker_request({'op': 'computers'})


class UpdateCheckTests(ComputerCase):
    def test_up_to_date_exits_0_and_touches_nothing(self):
        self.installed()
        self.fetch.return_value = tofi_host.validate_manifest(manifest(OLD, '1'))
        env_bytes, state_bytes = self.P.env_file.read_bytes(), self.P.state_file.read_bytes()
        code, out = self.main('update', '--check')
        self.assertEqual(code, 0)
        self.assertIn('Available    %s  (up to date)' % OLD, out)
        self.assertNotIn('Apply with', out)
        self.assertEqual((self.P.env_file.read_bytes(), self.P.state_file.read_bytes()), (env_bytes, state_bytes))
        for call in (self.pull, self.stopped, self.start_services, self.install_bundle, self.fetch_guest):
            call.assert_not_called()

    def test_update_available_exits_10_with_components_schema_and_computers(self):
        self.installed()
        w = self.fleet()
        w.computer('busy', busy=['run_lease'])
        code, out = self.main('update', '--check')
        self.assertEqual(code, tofi_host.UPDATE_AVAILABLE_EXIT)
        self.assertIn('Available    %s  (update available)' % NEW, out)
        rows = [line.split() for line in out.splitlines()[3:9]]
        self.assertEqual(rows, [
            ['COMPONENT', 'INSTALLED', 'AVAILABLE'],
            ['Host', 'bundle', OLD, NEW, 'changes'],
            ['App', 'image', 'sha256:111111111111', 'sha256:222222222222', 'changes'],
            ['Worker', 'image', 'sha256:111111111111', 'sha256:222222222222', 'changes'],
            ['Guest', 'release', OLD, NEW, 'changes'],
            ['Data', 'schema', 'tofi-account-data-v1', 'tofi-account-data-v1', 'compatible']])
        self.assertIn('Computers    5 total: 3 running, 1 hibernated', out)
        self.assertIn('e1e1e1e1 is busy (run_lease): its current task is interrupted.', out)
        self.assertIn('The Guest changes to %s' % NEW, out)
        self.assertIn('Apply with: sudo tofi update\n', out)
        self.stopped.assert_not_called()
        self.pull.assert_not_called()
        self.assertEqual(json.loads(self.P.latest_cache.read_text())['version'], NEW)
        code, out = self.main('update', '--check', '--json')
        report = json.loads(out)
        self.assertEqual((code, report['update_available'], report['data_schema']['compatible']),
                         (10, True, True))
        self.assertEqual([c['name'] for c in report['components'] if c['changes']],
                         ['version', 'app', 'worker', 'guest'])

    def test_schema_change_and_explicit_version(self):
        self.installed()
        changed = manifest()
        changed['data_schema'] = 'tofi-account-data-v2'
        self.fetch.return_value = tofi_host.validate_manifest(changed)
        code, out = self.main('update', '--check', '--version', NEW)
        self.assertEqual(code, 10)
        self.fetch.assert_called_with(NEW)
        self.assertIn('CHANGES: back up /var/lib/tofi; needs --allow-schema-change', out)
        self.assertIn('Apply with: sudo tofi update --version %s --allow-schema-change' % NEW, out)
        self.assertFalse(self.P.latest_cache.exists())  # only the latest release is cached

    def test_errors_exit_1(self):
        self.installed()
        self.fetch.side_effect = tofi_host.HostError('Cannot download the release manifest (offline).')
        self.assertEqual(self.main('update', '--check')[0], 1)
        self.assertEqual(self.stderr, 'tofi: Cannot download the release manifest (offline).\n')
        self.fetch.side_effect = None
        state = json.loads(self.P.state_file.read_text())
        state['phase'] = 'upgrade-failed'
        self.P.state_file.write_text(json.dumps(state))
        self.assertEqual(self.main('update', '--check')[0], 1)
        self.assertEqual(self.main('update', '--json')[0], 1)

    def test_unreachable_worker_is_reported_not_fatal(self):
        self.installed()
        tofi_host.worker_pid.side_effect = tofi_host.HostError('The Worker is not running.')
        code, out = self.main('update', '--check')
        self.assertEqual(code, 10)
        self.assertIn('Computers    unknown (The Worker is not running.)', out)


class ComputersUpgradeTests(ComputerCase):
    def test_list(self):
        self.installed()
        self.fleet()
        code, out = self.main('computers')
        self.assertEqual(code, 0)
        self.assertTrue(out.startswith('  ACCOUNT   STATE'))
        self.assertIn('9b77d0aa  hibernated    v0.0.9  pending', out)
        code, out = self.main('computers', '--json')
        self.assertEqual([row['state'] for row in json.loads(out)['computers']],
                         ['running', 'hibernated', 'stopped', 'unresponsive'])
        self.worker.rows = []
        self.assertIn('No account computers yet.', self.main('computers')[1])

    def test_idle_and_hibernated_computers_switch_and_busy_ones_are_deferred(self):
        self.installed()
        w = self.worker
        w.computer('running', release=OLDER)
        w.computer('hibernated', running=False, snapshot=OLDER)
        w.computer('busy', release=OLDER, busy=['run_lease', 'viewer'])
        w.computer('stopped', running=False)
        code, out = self.main('computers', 'upgrade', '--all')
        self.assertEqual(code, 2)
        self.assertEqual(w.upgrades, [ACCOUNTS['running'], ACCOUNTS['hibernated']])
        self.assertIn('  3f2a9c1e  running     v0.0.9  v0.1.0  upgraded  stopped; next start boots v0.1.0\n', out)
        self.assertIn('  9b77d0aa  hibernated  v0.0.9  v0.1.0  upgraded  snapshot discarded; next start boots v0.1.0\n', out)
        self.assertIn('  e1e1e1e1  running     v0.0.9  v0.1.0  deferred  busy (run_lease, viewer); retry when idle or use --force\n', out)
        self.assertIn('  c0ffee00  stopped     -       v0.1.0  current   next start boots v0.1.0\n', out)
        self.assertIn('  1 current, 1 deferred, 2 upgraded\n', out)
        journal = json.loads(self.P.state_file.read_text())
        self.assertEqual(journal['phase'], 'installed')
        self.assertEqual([r['result'] for r in journal['last_computers_upgrade']['results']],
                         ['upgraded', 'upgraded', 'deferred', 'current'])

    def test_force_asks_then_restarts_busy_computers(self):
        self.installed()
        self.worker.computer('busy', release=OLDER, busy=['terminal_job'])
        with self.assertRaisesRegex(tofi_host.HostError, 'cancelled; nothing was changed'):
            tofi_host.upgrade_computers(all_computers=True, force=True, confirm='no')
        self.assertEqual(self.worker.upgrades, [])
        report = tofi_host.upgrade_computers(all_computers=True, force=True, confirm='yes')
        self.assertEqual([r['result'] for r in report['results']], ['upgraded'])
        self.assertEqual(tofi_host.upgrade_exit_code(report), 0)
        # A busy probe that failed counts as busy.
        self.worker.managers[ACCOUNTS['busy']]['/v1/busy'] = None
        report = tofi_host.upgrade_computers(all_computers=True)
        self.assertEqual(report['results'][0]['note'], 'busy (state unknown); retry when idle or use --force')

    def test_one_account_by_prefix(self):
        self.installed()
        self.worker.computer('running', release=OLDER)
        self.worker.computer('hibernated', running=False, snapshot=OLDER)
        code, out = self.main('computers', 'upgrade', '--account', '9b77d0aa')
        self.assertEqual((code, self.worker.upgrades), (0, [ACCOUNTS['hibernated']]))
        for account, message in (('9b77', 'No computer matches'), ('nope-0000', 'No computer matches')):
            with self.assertRaisesRegex(tofi_host.HostError, message):
                tofi_host.upgrade_computers(account=account)
        self.worker.rows.append(dict(self.worker.rows[0], account_id='3f2a9c1e-ffff-4000-8000-00000000000f'))
        with self.assertRaisesRegex(tofi_host.HostError, 'several computers'):
            tofi_host.upgrade_computers(account='3f2a9c1e')

    def test_needs_a_selection_an_installed_phase_and_the_lock(self):
        with self.assertRaises(SystemExit):
            tofi_host.parse_args(['computers', 'upgrade'])
        with self.assertRaisesRegex(tofi_host.HostError, '--all, or --account'):
            tofi_host.upgrade_computers()
        self.installed(phase='upgrade-failed')
        with self.assertRaisesRegex(tofi_host.HostError, 'phase installed'):
            tofi_host.upgrade_computers(all_computers=True)
        with tofi_host.lifecycle_lock():
            with self.assertRaisesRegex(tofi_host.HostError, 'Another tofi operation'):
                tofi_host.upgrade_computers(all_computers=True)

    def test_unreferenced_guest_releases_are_removed_afterwards(self):
        self.installed()
        for version in (OLDER, 'v0.0.8', 'v0.0.7'):
            self.make_guest(version)
        (self.P.guest / '.v0.0.6.partial').mkdir()
        self.snapshot_file(ACCOUNTS['hibernated'], 'v0.0.7')  # stays hibernated (deferred elsewhere)
        self.worker.computer('running', release=OLDER)
        self.worker.computer('busy', release='v0.0.8', busy=['viewer'])
        report = tofi_host.upgrade_computers(all_computers=True)
        self.assertEqual(report['removed_releases'], [OLDER])  # v0.0.8: still used by the deferred computer
        self.assertEqual(sorted(p.name for p in self.P.guest.iterdir()), ['.v0.0.6.partial', 'v0.0.7', 'v0.0.8', OLD])
        self.assertIn('Removed unused Guest releases: v0.0.9', tofi_host.render_upgrade(report))
        self.worker.managers[ACCOUNTS['busy']]['/v1/busy']['busy'] = []
        self.assertEqual(tofi_host.upgrade_computers(all_computers=True)['removed_releases'], ['v0.0.8'])


class GuestRetentionTests(ComputerCase):
    def test_update_keeps_guest_releases_that_snapshots_were_taken_with(self):
        self.installed()
        for version in (OLDER, 'v0.0.8'):
            self.make_guest(version)
        self.snapshot_file(ACCOUNTS['hibernated'], OLDER)
        (self.P.worker_state / ACCOUNTS['stopped']).mkdir(parents=True)
        self.assertEqual(tofi_host.guest_references(), {OLDER})
        self.worker.computer('running', busy=['run_lease'])
        tofi_host.upgrade(manifest_path=str(self.write_manifest(manifest())))
        self.assertEqual(sorted(p.name for p in self.P.guest.iterdir()), [OLDER, OLD, NEW])
        out = sys.stdout.getvalue()
        self.assertIn('3f2a9c1e is busy (run_lease): its current task is interrupted.', out)
        self.assertIn('Hibernated computers still hold snapshots from %s: each cold-boots on %s' % (OLDER, NEW), out)

    def test_update_proceeds_when_the_worker_cannot_be_asked(self):
        self.installed()
        tofi_host.worker_pid.side_effect = tofi_host.HostError('The Worker is not running.')
        self.assertTrue(tofi_host.upgrade(manifest_path=str(self.write_manifest(manifest())))['upgraded'])



IP_ADDR_OUTPUT = r"""1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
1: lo    inet6 ::1/128 scope host noprefixroute \       valid_lft forever preferred_lft forever
2: ens18    inet 10.0.10.39/24 brd 10.0.10.255 scope global dynamic ens18\       valid_lft 86000sec
2: ens18    inet6 2001:db8::39/64 scope global dynamic mngtmpaddr \       valid_lft 86000sec
2: ens18    inet6 fe80::2:acff:fe15:75cc/64 scope link \       valid_lft forever preferred_lft forever
3: docker0    inet 172.17.0.1/16 brd 172.17.255.255 scope global docker0\       valid_lft forever
4: br-1a2b3c    inet 172.18.0.1/16 brd 172.18.255.255 scope global br-1a2b3c\       valid_lft forever
5: veth12@if4    inet6 fe80::1/64 scope link \       valid_lft forever
6: wlan0    inet 192.168.7.2/24 brd 192.168.7.255 scope global wlan0\       valid_lft forever
"""


class TLSTests(HostCase):
    ADDRESSES = [('inet', '10.0.10.39'), ('inet6', '2001:db8::39'), ('inet', '192.168.7.2')]

    def setUp(self):
        super().setUp()
        patcher = mock.patch.object(tofi_host, 'interface_addresses', return_value=list(self.ADDRESSES))
        patcher.start()
        self.addCleanup(patcher.stop)

    def certificate_text(self):
        return subprocess.run(['openssl', 'x509', '-noout', '-text', '-in', str(self.P.tls_cert)],
                              check=True, capture_output=True, text=True).stdout

    def test_certificate_created_once_and_reused(self):
        env = self.installed()
        fingerprint = tofi_host.certificate_fingerprint()
        self.assertRegex(fingerprint, r'^([0-9A-F]{2}:){31}[0-9A-F]{2}$')
        self.assertEqual(oct(self.P.tls_key.stat().st_mode & 0o777), '0o640')
        self.assertEqual(oct(self.P.tls_cert.stat().st_mode & 0o777), '0o644')
        self.assertEqual(oct(self.P.tls.stat().st_mode & 0o777), '0o750')
        owners = [(c.args[0].name, c.args[1:]) for c in tofi_host.os.chown.call_args_list]
        self.assertIn(('key.pem', (0, tofi_host.APP_UID)), owners)
        tofi_host.os.chown.assert_any_call(self.P.tls, 0, tofi_host.APP_UID)
        key_before = self.P.tls_key.read_bytes()
        # Update, resume and reinstall all re-apply host config: same pair.
        tofi_host.apply_host_config(self.P.current, env)
        tofi_host.apply_host_config(self.P.current, env)
        self.assertEqual(tofi_host.certificate_fingerprint(), fingerprint)
        self.assertEqual(self.P.tls_key.read_bytes(), key_before)
        self.assertFalse([p for p in self.P.tls.iterdir() if p.name.startswith('.')], 'no leftovers')
        # A missing key (or an expiring certificate) means a new pair.
        self.P.tls_key.unlink()
        tofi_host.apply_host_config(self.P.current, env)
        self.assertNotEqual(tofi_host.certificate_fingerprint(), fingerprint)

    def test_expiring_certificate_is_replaced(self):
        env = self.installed()
        fingerprint = tofi_host.certificate_fingerprint()
        with mock.patch.object(tofi_host, 'CERT_RENEW_SECONDS', tofi_host.CERT_DAYS * 86400 + 86400):
            tofi_host.apply_host_config(self.P.current, env)
        self.assertNotEqual(tofi_host.certificate_fingerprint(), fingerprint)

    def test_certificate_names_and_lifetime(self):
        self.installed()
        text = self.certificate_text()
        for name in ('IP Address:10.0.10.39', 'IP Address:192.168.7.2', 'IP Address:2001:DB8:0:0:0:0:0:39',
                     'IP Address:127.0.0.1', 'DNS:localhost', 'DNS:' + tofi_host.certificate_hostname()):
            self.assertIn(name, text)
        self.assertIn('CN = ' + tofi_host.certificate_hostname(), text.replace('CN=', 'CN = '))
        self.assertIn('prime256v1', text)
        self.assertIn('TLS Web Server Authentication', text)
        days = subprocess.run(['openssl', 'x509', '-noout', '-checkend', str(825 * 86400 + 3600),
                               '-in', str(self.P.tls_cert)], capture_output=True)
        self.assertNotEqual(days.returncode, 0, 'validity must not exceed 825 days')

    def test_domain_mode_has_no_certificate_but_keeps_mount_point(self):
        budgets = {'cpu': 3, 'memory_mib': 6144}
        env = tofi_host.render_env(manifest(OLD, '1'), {'domain': 'tofi.example.com', 'bind': '0.0.0.0',
                                                        'port': 8321}, budgets)
        self.make_bundle(OLD)
        self.make_guest(OLD)
        tofi_host.prepare_directories()
        tofi_host.apply_host_config(self.P.releases / OLD, env)
        self.assertTrue(self.P.tls.is_dir())
        self.assertFalse(self.P.tls_cert.exists())
        self.assertFalse(any(c[0] == 'openssl' and 'req' in c for c in self.commands))

    def test_regenerate_cert(self):
        self.installed()
        fingerprint = tofi_host.certificate_fingerprint()
        with mock.patch.object(tofi_host, 'project_containers', return_value=[{}]), \
                mock.patch.object(tofi_host, 'health', return_value=True) as health:
            result = tofi_host.regenerate_certificate()
        self.assertNotEqual(result['fingerprint'], fingerprint)
        self.assertEqual(tofi_host.certificate_fingerprint(), result['fingerprint'])
        self.assertTrue(any(c[-2:] == ['restart', 'app'] for c in self.commands))
        health.assert_called_once()

    def test_access_urls(self):
        env = {'TOFI_BIND': '0.0.0.0', 'TOFI_HTTP_PORT': '8321', 'TOFI_TLS_CERT_FILE': '/etc/tofi-tls/cert.pem'}
        self.assertEqual(tofi_host.access_urls(env), ['https://10.0.10.39:8321', 'https://192.168.7.2:8321'])
        self.assertEqual(tofi_host.access_urls(dict(env, TOFI_BIND='127.0.0.1')), ['https://127.0.0.1:8321'])
        self.assertEqual(tofi_host.access_urls(dict(env, TOFI_DOMAIN='tofi.example.com')),
                         ['https://tofi.example.com'])

    def test_health_probes_https_without_verification(self):
        seen = {}

        class Response(io.BytesIO):
            def __enter__(self):
                return self

            def __exit__(self, *exc):
                return False

        class Opener:
            def open(self, url, timeout):
                seen['url'] = url
                return Response(b'{"ok": true}')

        def build(*handlers):
            seen['handlers'] = handlers
            return Opener()

        with mock.patch.object(tofi_host.urllib.request, 'build_opener', side_effect=build):
            tofi_host.health({'TOFI_HTTP_PORT': '8321', 'TOFI_TLS_CERT_FILE': '/etc/tofi-tls/cert.pem'}, attempts=1)
        self.assertEqual(seen['url'], 'https://127.0.0.1:8321/health')
        https = [h for h in seen['handlers'] if isinstance(h, tofi_host.urllib.request.HTTPSHandler)]
        self.assertEqual(https[0]._context.verify_mode, tofi_host.ssl.CERT_NONE)
        with mock.patch.object(tofi_host.urllib.request, 'build_opener', side_effect=build):
            tofi_host.health({'TOFI_HTTP_PORT': '8321', 'TOFI_TLS_CERT_FILE': ''}, attempts=1)
        self.assertEqual(seen['url'], 'http://127.0.0.1:8321/health')

    def test_status_lists_urls_and_fingerprint(self):
        self.installed()
        with mock.patch.object(tofi_host, 'project_containers', return_value=[]), \
                mock.patch.object(tofi_host, 'health', return_value=True):
            result = tofi_host.status()
        self.assertEqual(result['urls'], ['https://10.0.10.39:8321', 'https://192.168.7.2:8321'])
        self.assertEqual(result['certificate_sha256'], tofi_host.certificate_fingerprint())


STATUS_FP = ':'.join('%02X' % n for n in range(1, 33))
STATUS_KEY = 'synthetic-setup-key-never-print-me'


def status_result(**extra):
    result = {
        'phase': 'installed', 'version': 'v0.1.0-rc.12', 'healthy': True,
        'services': {'app': 'healthy', 'worker': 'running'},
        'urls': ['https://10.0.10.56:8321'], 'certificate_sha256': STATUS_FP,
        'setup_key_pending': True, 'setup_key': STATUS_KEY,
        'versions': {'installed': 'v0.1.0-rc.12', 'latest': None, 'update_available': False,
                     'latest_error': 'Cannot download the release manifest https://github.com/x/releases/latest/'
                                     'download/manifest.json (HTTP Error 404: Not Found).',
                     'latest_checked_at': None, 'app_image': image('app', 'a'), 'worker_image': image('worker', 'b'),
                     'guest': 'v0.1.0-rc.12'},
        'computers': [],
    }
    result.update(extra)
    return result


def status_versions(**values):
    result = status_result()
    result['versions'].update(values)
    return result


class RenderStatusTests(unittest.TestCase):
    ACCESS = {'domain': '', 'port': '8321', 'local_only': False}

    def render(self, result=None, **kwargs):
        kwargs.setdefault('access', self.ACCESS)
        return tofi_host.render_status(result or status_result(), **kwargs)

    def test_default_view_is_compact(self):
        text = self.render()
        self.assertTrue(text.startswith('tofi v0.1.0-rc.12 · healthy\n\n'), text)
        self.assertIn('  Open         https://10.0.10.56:8321\n', text)
        self.assertIn('  Certificate  SHA256 01:02:03:04 … 1D:1E:1F:20\n', text)
        self.assertIn('  Admin        not created yet · sudo tofi setup-secret\n', text)
        self.assertIn('  Version      v0.1.0-rc.12 · no stable release yet\n', text)
        self.assertIn('  Services     app healthy · worker running\n', text)
        self.assertIn('  Computers    none yet\n', text)
        self.assertTrue(text.endswith('\n  tofi update · tofi logs · tofi computers · tofi doctor\n'))
        for noise in ('App ', 'Worker', 'Guest', 'ghcr.io', 'github.com', 'HTTP Error', STATUS_FP):
            self.assertNotIn(noise, text)
        self.assertTrue(all(len(line) <= 80 for line in text.splitlines()), text)

    def test_setup_key_value_is_never_printed(self):
        for verbose in (False, True):
            self.assertNotIn(STATUS_KEY, self.render(verbose=verbose))

    def test_no_admin_row_once_the_admin_exists(self):
        text = self.render(status_result(setup_key_pending=False))
        self.assertNotIn('Admin', text)

    def test_update_available(self):
        text = self.render(status_versions(latest='v0.1.0', update_available=True, latest_error=None))
        self.assertIn('  Version      v0.1.0-rc.12 · v0.1.0 available · sudo tofi update\n', text)

    def test_up_to_date(self):
        text = self.render(status_versions(latest='v0.1.0-rc.12', latest_error=None))
        self.assertIn('  Version      v0.1.0-rc.12 · up to date\n', text)

    def test_other_check_error(self):
        text = self.render(status_versions(latest_error='Cannot download the release manifest (timed out).'))
        self.assertIn("  Version      v0.1.0-rc.12 · couldn't check for updates\n", text)
        self.assertNotIn('timed out', text)

    def test_stale_latest_with_error_is_not_a_failure_in_default_view(self):
        text = self.render(status_versions(latest='v0.1.0-rc.12', latest_error='offline',
                                           latest_checked_at='2026-10-08T01:00:00Z'))
        self.assertIn('· up to date\n', text)
        self.assertNotIn('offline', text)
        self.assertIn('checked 2026-10-08T01:00:00Z, offline now', self.render(
            status_versions(latest='v0.1.0-rc.12', latest_error='offline',
                            latest_checked_at='2026-10-08T01:00:00Z'), verbose=True))

    def test_domain_install_has_no_certificate_row(self):
        text = self.render(status_result(urls=['https://tofi.example.com']),
                           access={'domain': 'tofi.example.com', 'port': '443', 'local_only': False})
        self.assertIn('  Open         https://tofi.example.com\n', text)
        self.assertNotIn('Certificate', text)

    def test_local_only_hint(self):
        text = self.render(status_result(urls=['https://127.0.0.1:8321']),
                           access={'domain': '', 'port': '8321', 'local_only': True})
        self.assertIn('  Open         https://127.0.0.1:8321\n'
                      '               this server only · ssh -L 8321:127.0.0.1:8321 <user>@<server>\n', text)

    def test_several_urls(self):
        text = self.render(status_result(urls=['https://10.0.10.56:8321', 'https://192.168.7.2:8321']))
        self.assertIn('  Open         https://10.0.10.56:8321\n               https://192.168.7.2:8321\n', text)

    def test_verbose_adds_images_full_fingerprint_and_raw_error(self):
        text = self.render(verbose=True)
        self.assertIn('  Certificate  SHA256 %s\n' % STATUS_FP, text)
        self.assertIn('  App          ghcr.io/jackzhao98/tofi@sha256:' + 'a' * 12, text)
        self.assertIn('  Worker       ', text)
        self.assertIn('  Guest        v0.1.0-rc.12\n', text)
        self.assertIn('HTTP Error 404', text)

    def test_computers_summary_and_table(self):
        rows = [{'short': 'aaaaaaaa', 'state': 'running', 'release': OLD, 'upgrade_pending': False,
                 'memory_rss_mib': 600, 'last_wake': None},
                {'short': 'bbbbbbbb', 'state': 'hibernated', 'release': OLD, 'upgrade_pending': False,
                 'memory_rss_mib': None, 'last_wake': None}]
        with mock.patch.object(tofi_host, 'describe_wake', return_value='-'):
            text = self.render(status_result(computers=rows))
        self.assertIn('  Computers    2 · 1 running · 1 hibernated\n', text)
        self.assertIn('  ACCOUNT   STATE', text)
        self.assertIn('  aaaaaaaa  running', text)

    def test_computers_error(self):
        text = self.render(status_result(computers_error='The Worker is not running'))
        self.assertIn('  Computers    unknown (The Worker is not running)\n', text)

    def test_not_healthy_and_phase(self):
        text = self.render(status_result(phase='failed', healthy=False,
                                         last_error={'message': 'boom'},
                                         transaction={'kind': 'update', 'step': 'pull'}))
        self.assertTrue(text.startswith('tofi v0.1.0-rc.12 · NOT healthy · failed\n'))
        self.assertIn('  Last error   boom\n', text)
        self.assertIn('  Pending      update at step pull · sudo tofi install resumes it\n', text)

    def test_not_installed(self):
        self.assertEqual(tofi_host.render_status({'installed': False}),
                         'TOFI is not installed. Install it with install.sh.\n')

    def test_plain_without_color_mode_and_colored_with_one(self):
        self.assertNotIn('\033', self.render(mode=None))
        colored = self.render(mode='256')
        self.assertIn('\033[', colored)
        self.assertEqual(re.sub(r'\033\[[0-9;]*m', '', colored), self.render(mode=None))


class AddressTests(unittest.TestCase):
    def test_interface_addresses_skip_loopback_link_local_and_bridges(self):
        with mock.patch.object(tofi_host, 'run', return_value=completed(IP_ADDR_OUTPUT)):
            self.assertEqual(tofi_host.interface_addresses(),
                             [('inet', '10.0.10.39'), ('inet6', '2001:db8::39'), ('inet', '192.168.7.2')])

    def test_hostname_fallback(self):
        def fake(args, check=True, **kwargs):
            if args[0] == 'ip':
                raise FileNotFoundError('ip')
            return completed('10.0.10.39 172.17.0.1 2001:db8::39 fe80::1\n')
        with mock.patch.object(tofi_host, 'run', side_effect=fake):
            self.assertEqual(tofi_host.interface_addresses(), [('inet', '10.0.10.39'), ('inet6', '2001:db8::39')])


SPEC_ART = """\
   ▄▄              ▄▄▄  ▄▄        ▄▀▄   ▄▀▄
 ▀▀██▀▀  ▄▄▄▄▄   ▄██▀   ▀▀       █  ▀▀▀▀▀  █
   ██   ██▀  ▀██ ▀██▀▀  ██       █  ●   ●  █
   ██▄▄ ██▄  ▄██  ██    ██        ▀▄▄▄▄▄▄▄▀
    ▀▀▀  ▀▀▀▀▀    ▀▀    ▀▀   v0.1.0 · your crew is awake
"""
FINGERPRINT = ':'.join(['AB'] * 32)
KEY = 'synthetic-setup-key-0123456789abcdefghijklmnopqrstuvwxyz'


class BannerTests(unittest.TestCase):
    def info(self, **extra):
        values = {'version': 'v0.1.0', 'urls': ['https://10.0.10.39:8321', 'https://192.168.7.2:8321'],
                  'setup_key': KEY, 'domain': '', 'port': '8321', 'fingerprint': FINGERPRINT,
                  'local_only': False}
        values.update(extra)
        return values

    def test_plain_mode_layout(self):
        text = tofi_host.render_banner(self.info(), None, 120)
        self.assertNotIn('\033', text)
        self.assertTrue(text.startswith(SPEC_ART), text)
        self.assertIn('\n  Open        https://10.0.10.39:8321\n              https://192.168.7.2:8321\n', text)
        self.assertIn("              or your server's public IP\n", text)
        self.assertIn('\n  Setup key   %s   (one time)\n' % KEY, text)
        self.assertIn('\n  Next        enter the key, create your admin, connect a model\n', text)
        self.assertIn("\n  Browser warns about the certificate? That's expected — continue.\n", text)
        self.assertIn('\n  Fingerprint SHA256 %s\n  Cloud server? allow TCP 8321\n' % FINGERPRINT, text)
        self.assertTrue(text.endswith('\n\n  tofi status · tofi update · tofi logs · tofi uninstall\n'))

    def test_art_display_width(self):
        widths = [len(line) for line in tofi_host.BANNER_ART]
        self.assertEqual(widths[:4], [43, 44, 44, 43])
        # Every glyph is a single-column block element or a space.
        self.assertTrue(set(''.join(tofi_host.BANNER_ART)) <= set(' ▄▀█●'))

    def test_wide_terminal_keeps_fingerprint_and_firewall_on_one_line(self):
        text = tofi_host.render_banner(self.info(), None, 200)
        self.assertIn('\n  Fingerprint SHA256 %s   ·   Cloud server? allow TCP 8321\n' % FINGERPRINT, text)

    def strip(self, text):
        import re
        return re.sub('\033\\[[0-9;]*m', '', text)

    def test_truecolor_mode(self):
        text = tofi_host.render_banner(self.info(), 'truecolor', 120)
        self.assertIn(' \033[38;2;243;234;219m▀▀██▀▀\033[0m', text)  # t in cream
        self.assertIn('\033[38;2;232;149;109m▄▄▄▄▄', text)    # o in peach
        self.assertIn('\033[38;2;127;209;193m●', text)        # eyes in teal
        self.assertIn('\033[1;4;38;2;127;209;193mhttps://10.0.10.39:8321\033[0m', text)
        self.assertIn('\033[1m%s\033[0m' % KEY, text)
        self.assertEqual(self.strip(text), tofi_host.render_banner(self.info(), None, 120))

    def test_256_color_mode(self):
        text = tofi_host.render_banner(self.info(), '256', 120)
        self.assertIn('\033[38;5;230m', text)
        self.assertIn('\033[38;5;209m', text)
        self.assertIn('\033[38;5;115m●', text)
        self.assertNotIn('38;2;', text)
        self.assertEqual(self.strip(text), tofi_host.render_banner(self.info(), None, 120))

    def test_color_mode_detection(self):
        class Tty(io.StringIO):
            def isatty(self):
                return True
        tty = Tty()
        self.assertEqual(tofi_host.color_mode(tty, {'COLORTERM': 'truecolor'}), 'truecolor')
        self.assertEqual(tofi_host.color_mode(tty, {'COLORTERM': '24bit'}), 'truecolor')
        self.assertEqual(tofi_host.color_mode(tty, {'TERM': 'xterm-256color'}), '256')
        self.assertIsNone(tofi_host.color_mode(tty, {'NO_COLOR': '', 'COLORTERM': 'truecolor'}))
        self.assertIsNone(tofi_host.color_mode(tty, {'TERM': 'dumb'}))
        self.assertIsNone(tofi_host.color_mode(io.StringIO(), {'COLORTERM': 'truecolor'}))

    def test_narrow_terminal_skips_art(self):
        text = tofi_host.render_banner(self.info(), None, 59)
        self.assertTrue(text.startswith('tofi v0.1.0 · your crew is awake\n\n  Open        https://10.0.10.39:8321\n'))
        self.assertNotIn('▄', text)
        self.assertIn('\n  Fingerprint SHA256\n  %s\n  Cloud server? allow TCP 8321\n' % FINGERPRINT, text)
        self.assertIn(KEY, text)

    def test_retained_data_variant(self):
        text = tofi_host.render_banner(self.info(setup_key=None), None, 120)
        self.assertIn('\n  Sign in     with your existing admin account\n', text)
        self.assertNotIn('Setup key', text)
        self.assertNotIn('enter the key', text)

    def test_domain_variant(self):
        text = tofi_host.render_banner(self.info(domain='tofi.example.com', urls=['https://tofi.example.com'],
                                                 fingerprint=None), None, 120)
        self.assertIn('\n  Open        https://tofi.example.com\n', text)
        self.assertIn('\n  Allow TCP 80 and 443.\n', text)
        for absent in ('Fingerprint', 'certificate', 'Cloud server', "public IP"):
            self.assertNotIn(absent, text)

    def test_local_only_variant(self):
        text = tofi_host.render_banner(self.info(urls=['https://127.0.0.1:8321'], local_only=True), None, 120)
        self.assertIn('\n  Open        https://127.0.0.1:8321\n', text)
        self.assertIn('ssh -L 8321:127.0.0.1:8321', text)
        self.assertNotIn('Cloud server', text)
        self.assertNotIn('public IP', text)


class Tty(io.StringIO):
    encoding = 'utf-8'

    def isatty(self):
        return True


def plain(text):
    return re.sub('\033\\[[0-9;]*m', '', text)


class ComputerBackendTests(HostCase):
    def args(self, *extra):
        return tofi_host.parse_args(['install', '--manifest', 'm.json', *extra])

    def test_backend_is_recorded_in_tofi_env(self):
        options = tofi_host.install_options(self.args())
        self.assertEqual(options['computer'], 'kvm')
        env = tofi_host.render_env(manifest(), options, {'cpu': 3, 'memory_mib': 6144})
        self.assertEqual(env['TOFI_COMPUTER_BACKEND'], 'kvm')
        self.assertIn('TOFI_COMPUTER_BACKEND=kvm\n', tofi_host.format_env(env))

    def test_trusted_proxies_survive_a_rewrite_of_tofi_env(self):
        # An operator behind an external reverse proxy adds the value by hand;
        # `tofi update` rebuilds the file from the installed env and must keep it.
        env = tofi_host.render_env(manifest(), tofi_host.install_options(self.args()), {'cpu': 3, 'memory_mib': 6144})
        env['TOFI_TRUSTED_PROXIES'] = '10.0.10.0/24'
        path = self.P.env_file
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(tofi_host.format_env(env))
        candidate = dict(tofi_host.read_env(path), TOFI_VERSION='v9.9.9')
        tofi_host.write_env(candidate)
        self.assertEqual(tofi_host.read_env(path)['TOFI_TRUSTED_PROXIES'], '10.0.10.0/24')

    def test_other_backends_are_refused_as_not_supported_yet(self):
        for backend in ('gvisor', 'container'):
            with self.assertRaisesRegex(tofi_host.HostError, r'not supported yet \(coming in a later release\)'):
                tofi_host.install_options(self.args('--computer', backend))
        with self.assertRaisesRegex(tofi_host.HostError, 'must be kvm, gvisor or container'):
            tofi_host.install_options(self.args('--computer', 'xen'))

    def test_doctor_shows_the_machine_card_and_backend(self):
        self.installed()
        for name in ('health', 'inspect_image', 'validate_release', 'certificate_usable'):
            patcher = mock.patch.object(tofi_host, name, return_value=True)
            patcher.start()
            self.addCleanup(patcher.stop)
        tofi_host.doctor()
        out = sys.stdout.getvalue()
        self.assertLess(out.index('This machine'), out.index('KVM device'))
        self.assertIn('TOFI     installed v0.1.0', out)
        self.assertRegex(out, r'ok +Computer backend +kvm \(Firecracker\)')
        env = tofi_host.read_env()
        env['TOFI_COMPUTER_BACKEND'] = 'gvisor'
        tofi_host.write_env(env)
        sys.stdout.truncate(0)
        sys.stdout.seek(0)
        self.assertFalse(tofi_host.doctor())
        self.assertRegex(sys.stdout.getvalue(), r'FAIL +Computer backend +gvisor is not supported yet')


class ProgressTests(unittest.TestCase):
    def test_plain_progress_keeps_step_numbers(self):
        out = io.StringIO()
        progress = tofi_tui.Progress(tofi_tui.UI(out, {}))
        progress.begin(10, 'Preparing')
        progress.begin(11, 'Starting')
        progress.end()
        self.assertEqual(out.getvalue(), '[10/12] Preparing\n[11/12] Starting\n')

    def test_terminal_progress_spins_then_checks(self):
        out = Tty()
        ui = tofi_tui.UI(out, {'TERM': 'xterm', 'NO_COLOR': '1'}, width=80)
        progress = tofi_tui.Progress(ui)
        progress.begin(10, 'Preparing')
        time.sleep(0.25)
        ui.write('a note in between')
        progress.begin(11, 'Starting')
        progress.end(ok=False)
        text = out.getvalue()
        self.assertIn('\r\x1b[K  ⠋ Preparing', text)
        self.assertIn('a note in between\n', text)
        self.assertIn('  ✓ Preparing\n', text)
        self.assertTrue(text.endswith('  ✗ Starting\n'), text)
        self.assertNotIn('[10/12]', text)


class TuiTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-tui-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.host = tofi_tui.Host({'TOFI_TEST_ROOT': str(self.root)})

    def write(self, relative, text):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def test_ports_name_the_listening_process(self):
        self.write('proc/net/tcp', '  sl local rem st\n'
                   '   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 555 1\n'
                   '   1: 0100007F:2081 00000000:0000 01 00000000:00000000 00:00000000 00000000 0 0 556 1\n')
        self.write('proc/net/tcp6', '  sl local rem st\n'
                   '   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A '
                   '00000000:00000000 00:00000000 00000000 0 0 557 1\n')
        (self.root / 'proc/812/fd').mkdir(parents=True)
        os.symlink('socket:[555]', str(self.root / 'proc/812/fd/6'))
        self.write('proc/812/comm', 'nginx\n')
        ports = tofi_tui.Ports(self.host)
        self.assertEqual(ports.describe(80), 'in use by nginx (pid 812)')
        self.assertEqual(ports.describe(443), 'in use')          # owner not visible
        self.assertEqual(ports.describe(8321), 'free')           # 0x2081 is connected, not listening
        self.assertEqual(ports.next_free(79), 81)

    def test_kvm_reasons(self):
        self.assertIn('enable nested virtualization on cloud VMs, or use bare metal', tofi_tui.kvm_reason(self.host))
        self.write('proc/cpuinfo', 'flags\t\t: fpu sse2 hypervisor\n')
        self.assertIn('does not expose virtualization', tofi_tui.kvm_reason(self.host))
        self.write('proc/cpuinfo', 'flags\t\t: fpu svm\n')
        self.assertIn('modprobe kvm_intel (or kvm_amd)', tofi_tui.kvm_reason(self.host))

    def test_installed_state(self):
        self.assertEqual(tofi_tui.installed_state(self.host), (None, None, None))
        self.write('etc/tofi/install-state.json', '{"phase": "stopped-retained"}')
        self.assertIn('no TOFI_VERSION', tofi_tui.installed_state(self.host)[2])
        self.write('etc/tofi/tofi.env', '# managed\nTOFI_VERSION=v0.1.0\n')
        self.assertEqual(tofi_tui.installed_state(self.host), ('stopped-retained', 'v0.1.0', None))
        self.assertEqual(tofi_tui.describe_phase('upgrade-failed', 'v0.1.0'),
                         ('warn', 'interrupted update (upgrade-failed), v0.1.0'))

    def facts(self, **extra):
        facts = {'host': self.host, 'platform': 'Linux x86_64', 'uid': '0',
                 'os': {'NAME': 'Debian GNU/Linux', 'ID': 'debian', 'VERSION_ID': '12'}, 'cpus': 4,
                 'memory_mib': 7960, 'swap_mib': 0, 'disk_gib': 35, 'docker': None, 'kvm': True,
                 'kvm_reason': None, 'tun': True, 'cgroup2': True, 'apparmor': 'Y',
                 'ports': tofi_tui.Ports(self.host), 'port': 8321, 'phase': None, 'installed': None,
                 'journal_error': None, 'lan': ['192.168.1.20'], 'public': []}
        facts.update(extra)
        return facts

    def test_machine_rows_and_checks(self):
        rows = {label: (kind, text) for kind, label, text in tofi_tui.machine_rows(self.facts())}
        self.assertEqual(rows['System'], ('ok', 'Debian 12 · Linux x86_64'))
        self.assertEqual(rows['RAM'], ('warn', '7.8 GiB · no swap'))
        self.assertEqual(rows['Disk'], ('warn', '35 GiB free under /var/lib'))
        self.assertEqual(rows['Docker'], ('info', 'not installed — will be installed'))
        failures, warnings = tofi_tui.check_host(self.facts(), {})
        self.assertEqual(failures, [])
        self.assertEqual(len(warnings), 2)   # no swap, disk under 40 GiB
        failures, _ = tofi_tui.check_host(self.facts(kvm=False, kvm_reason='no /dev/kvm', apparmor='N', cpus=1), {})
        self.assertEqual([f.split(' ')[0] for f in failures], ['KVM', 'AppArmor', 'TOFI'])

    def test_card_framed_plain_and_narrow(self):
        rows = [('ok', 'System', 'Ubuntu 24.04'),
                ('bad', 'KVM', 'no /dev/kvm — enable nested virtualization on cloud VMs, or use bare metal')]
        out = Tty()
        ui = tofi_tui.UI(out, {'TERM': 'xterm', 'COLORTERM': 'truecolor'}, width=200)
        ui.card('This machine', rows)
        lines = [plain(line) for line in out.getvalue().splitlines() if line]
        self.assertEqual({len(line) for line in lines}, {76})       # never wider than 76 columns
        self.assertTrue(lines[0].startswith('╭─ This machine ─'))
        self.assertIn('\033[38;2;127;209;193m✓', out.getvalue())   # teal check
        self.assertIn('\033[2m✗', out.getvalue())                   # dim cross
        out = Tty()
        tofi_tui.UI(out, {'TERM': 'xterm'}, width=40).card('This machine', rows)
        self.assertNotIn('╭', out.getvalue())
        self.assertTrue(all(len(plain(line)) <= 40 for line in out.getvalue().splitlines()))
        out = io.StringIO()
        tofi_tui.UI(out, {}).card('This machine', rows)
        self.assertEqual(out.getvalue().splitlines()[:2], ['This machine', '  ✓ System  Ubuntu 24.04'])
        self.assertNotIn('\033', out.getvalue())

    def test_ascii_glyphs_when_the_terminal_is_not_utf8(self):
        class Ascii(Tty):
            encoding = 'ANSI_X3.4-1968'
        ui = tofi_tui.UI(Ascii(), {'TERM': 'xterm', 'NO_COLOR': '1'}, width=70)
        ui.card('X', [('ok', 'A', 'b')])
        self.assertIn('+ A', ui.out.getvalue())
        self.assertNotIn('✓', ui.out.getvalue())

    def test_unsupported_options_stay_readable_without_colour(self):
        ui = tofi_tui.UI(io.StringIO(), {})
        prompter = tofi_tui.Prompter(ui, -1, {'TOFI_PROMPT': 'plain'})
        options = [tofi_tui.Option('kvm', 'KVM (Firecracker)'),
                   tofi_tui.Option('gvisor', 'gVisor container', unsupported=tofi_tui.NOT_YET)]
        self.assertEqual(prompter.option_lines(options, 0, 0, True), [
            '  ❯ 1) KVM (Firecracker)  (recommended)',
            '    2) gVisor container — not supported yet (coming in a later release)'])
        colour = tofi_tui.Prompter(tofi_tui.UI(Tty(), {'TERM': 'xterm'}), -1, {'TOFI_PROMPT': 'plain'})
        lines = colour.option_lines(options, 0, 0, True)
        self.assertTrue(lines[1].startswith('    \033[2m2) gVisor'), lines[1])
        self.assertEqual(prompter.resolve(' GVISOR ', options), 1)
        self.assertEqual(prompter.resolve('2', options), 1)
        self.assertIsNone(prompter.resolve('3', options))

    def test_validators(self):
        self.assertEqual(tofi_tui.validate_domain(' TOFI.Example.com. '), ('tofi.example.com', None))
        self.assertIsNotNone(tofi_tui.validate_domain('localhost')[1])
        self.assertEqual(tofi_tui.validate_email(''), ('', None))
        self.assertIsNotNone(tofi_tui.validate_email('a@b')[1])
        ports = mock.Mock(busy=lambda port: port == 9000, describe=lambda port: 'in use by x',
                          next_free=lambda port: port + 1)
        validate = tofi_tui.port_validator(ports)
        self.assertEqual(validate('9001'), ('9001', None))
        self.assertEqual(validate('9000')[1], 'Port 9000 is in use by x; 9001 is free.')
        self.assertIsNotNone(validate('80')[1])
        self.assertIsNotNone(validate('70000')[1])

    def test_release_resolution(self):
        answers = {}

        def curl(url, timeout=20):
            return answers.get(url, (22, ''))
        with mock.patch.object(tofi_tui, 'curl_text', side_effect=curl):
            answers[tofi_tui.RELEASES_URL + '/latest/download/manifest.json'] = (0, '{"version": "v0.2.0"}')
            self.assertEqual(tofi_tui.resolve_release(), ('stable', 'v0.2.0'))
            answers.clear()
            answers[tofi_tui.RELEASES_API] = (0, json.dumps([{'tag_name': 'nightly', 'draft': False},
                                                              {'tag_name': 'v0.1.0-rc.3', 'draft': True},
                                                              {'tag_name': 'v0.1.0-rc.2', 'draft': False}]))
            self.assertEqual(tofi_tui.resolve_release(), ('prerelease', 'v0.1.0-rc.2'))
            answers.clear()
            self.assertEqual(tofi_tui.resolve_release(), ('none', None))
            answers[tofi_tui.RELEASES_URL + '/latest/download/manifest.json'] = (6, '')
            self.assertEqual(tofi_tui.resolve_release(), ('offline', 'curl exit 6'))

    def test_dns_warning(self):
        facts = self.facts(public=['203.0.113.5'])
        with mock.patch.object(tofi_tui, 'resolve_dns', return_value=['203.0.113.5']):
            self.assertIsNone(tofi_tui.dns_warning('tofi.example.com', facts))
        with mock.patch.object(tofi_tui, 'resolve_dns', return_value=['198.51.100.1']):
            self.assertIn('points at 198.51.100.1, not at this server (203.0.113.5)',
                          tofi_tui.dns_warning('tofi.example.com', facts))
        with mock.patch.object(tofi_tui, 'resolve_dns', return_value=None):
            self.assertIn('does not resolve yet', tofi_tui.dns_warning('tofi.example.com', facts))

    def test_banner_palette_is_shared(self):
        self.assertIs(tofi_host.Paint, tofi_tui.Paint)
        self.assertIs(tofi_host.color_mode, tofi_tui.color_mode)
        header = tofi_tui.render_art(tofi_tui.Paint(None), note='installer')
        self.assertEqual(header[:4], [line.rstrip() for line in tofi_host.BANNER_ART[:4]])
        self.assertTrue(header[4].endswith('installer'))


if __name__ == '__main__':
    unittest.main()
