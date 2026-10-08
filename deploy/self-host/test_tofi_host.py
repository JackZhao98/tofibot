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


if __name__ == '__main__':
    unittest.main()
