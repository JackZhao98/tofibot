"""Synthetic checks for the host lifecycle tool; no Docker, root or KVM needed.

Every host command goes through a mocked `run`; files are written under a
temporary root. These tests do not replace clean-host KVM acceptance.
"""
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import tofi_host  # noqa: E402

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
        options = {'domain': '', 'email': '', 'bind': '127.0.0.1', 'port': 8321}
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
        default = tofi_host.render_env(manifest(), {'bind': '127.0.0.1', 'port': 8321}, budgets)
        self.assertEqual((default['TOFI_BIND'], default['TOFI_PUBLIC_ORIGIN']), ('127.0.0.1', ''))
        lan = tofi_host.render_env(manifest(), {'bind': '0.0.0.0', 'port': 9000}, budgets)
        self.assertEqual((lan['TOFI_BIND'], lan['TOFI_HTTP_PORT']), ('0.0.0.0', '9000'))
        tls = tofi_host.render_env(manifest(), {'bind': '0.0.0.0', 'port': 8321, 'domain': 'tofi.example.com'}, budgets)
        self.assertEqual((tls['TOFI_BIND'], tls['TOFI_PUBLIC_ORIGIN']), ('127.0.0.1', 'https://tofi.example.com'))
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

    def test_manifest_validation(self):
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
        options = {'domain': '', 'email': '', 'bind': '127.0.0.1', 'port': 8321, 'lan': False}
        with mock.patch.object(tofi_host, 'preflight', return_value=[]), \
                mock.patch.object(tofi_host, 'ASSETS', self.P.releases / OLD), \
                mock.patch.object(tofi_host.os, 'cpu_count', return_value=4):
            self.P.data.mkdir(parents=True)
            self.write(self.P.bootstrap_secret, 'synthetic-setup-key\n')
            result = tofi_host.install(str(self.write_manifest(manifest(OLD, '1'))), options)
        self.assertEqual(self.phase(), 'installed')
        self.assertTrue(result['setup_key_pending'])
        self.assertIn('synthetic-setup-key', sys.stdout.getvalue())
        self.assertIn('ssh -L 8321:127.0.0.1:8321', sys.stdout.getvalue())
        env = tofi_host.read_env()
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
        self.assertIn('existing Admin account', sys.stdout.getvalue())

    def test_purge_requires_hostname(self):
        self.installed(phase='stopped-retained')
        with self.assertRaisesRegex(tofi_host.HostError, 'Purge cancelled'):
            tofi_host.uninstall(purge=True, confirm='wrong-host')
        self.assertTrue(self.P.data.exists())
        tofi_host.uninstall(purge=True, confirm=tofi_host.socket.gethostname())
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


if __name__ == '__main__':
    unittest.main()
