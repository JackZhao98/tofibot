"""upgrade_acceptance.py's logic against an in-memory host; the real run needs a VM."""
import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
import unittest.mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import upgrade_acceptance as ua  # noqa: E402

FROM, TO, BROKEN = 'v0.1.0-rc.4', 'v0.1.0-rc.5', 'v0.1.0-rc.9'


class FakeApp:
    """Just enough App API for the fixture: a Bot, a skill with access, a preference, a key."""

    def __init__(self, host):
        self.host = host
        self.authenticated = False

    @property
    def data(self):
        return self.host.data

    def request(self, route, body=None, method=None, expected=200):
        data = self.data
        if route == '/api/auth/session':
            return {'authenticated': self.authenticated, 'setup_required': not data['admin']}
        if route == '/api/auth/setup':
            data['admin'] = body['username']
            self.authenticated = True
            return {}
        if route == '/api/auth/login':
            self.authenticated = True
            return {}
        if route == '/api/bots' and body is not None:
            bot = {'id': 'bot%d' % len(data['bots']), 'name': body['name']}
            data['bots'].append(bot)
            return bot
        if route == '/api/bots':
            return list(data['bots'])
        if route == '/api/extensions/skills' and body is not None:
            data['skills'][body['name']] = {'name': body['name'], 'access': {'mode': 'all'}}
            return {}
        if route == '/api/extensions/skills':
            return {'skills': list(data['skills'].values())}
        if route.startswith('/api/extensions/skills/'):
            data['skills'][route.split('/')[4]]['access'] = {'mode': body['mode'], 'bot_ids': body['bot_ids']}
            return {}
        if route == '/api/preferences' and body is not None:
            data['timezone'] = body['timezone']
            return {}
        if route == '/api/preferences':
            return {'timezone': data['timezone']}
        if route == '/api/computer/credentials' and body is not None:
            data['keys'].append({'name': body['name']})
            return {}
        if route == '/api/computer/credentials':
            return list(data['keys'])
        raise AssertionError('unexpected route ' + route)


class FakeHost:
    """The tofi CLI and tofi.env of a test VM, with switchable misbehaviour."""

    def __init__(self, args, **faults):
        self.args = args
        self.faults = faults
        self.client = FakeApp(self)
        self.state = {}
        self.installed = None
        self.version = None
        self.env = ''
        self.data = {'admin': None, 'bots': [], 'skills': {}, 'timezone': None, 'keys': []}
        self.backups = []
        self.calls = []
        self.healthy = True

    def api(self):
        return self.client

    def setup_key(self):
        return 'synthetic-key'

    def read_env(self):
        return self.env

    def append_env(self, line):
        self.env += line + '\n'

    def install(self, version):
        self.calls.append(('install', version))
        self.version = version
        self.installed = True
        self.env = 'TOFI_VERSION=%s\n' % version
        return 0, '', ''

    def backup(self, reason):
        item = {'id': 'b%d-%s' % (len(self.backups), self.version), 'version': self.version, 'reason': reason,
                'created_at': '2026-10-09T00:00:%02dZ' % len(self.backups),
                'data': copy.deepcopy(self.data), 'env': self.env}
        self.backups.append(item)
        return item

    def tofi(self, *argv, timeout=0):
        self.calls.append(argv)
        command = argv[0]
        if command == 'status':
            return 0, json.dumps({'phase': 'installed', 'version': self.version, 'healthy': self.healthy,
                                  'services': {'app': 'healthy', 'worker': 'running'}}), ''
        if command == 'backups' and '--json' in argv:
            return 0, json.dumps([{k: v for k, v in b.items() if k not in ('data', 'env')} for b in self.backups]), ''
        if command == 'backups':
            return 0, '\n'.join(b['id'] for b in self.backups), ''
        if command == 'update':
            target = argv[argv.index('--version') + 1] if '--version' in argv else BROKEN
            self.backup('pre-update')
            if '--manifest' in argv:
                if self.faults.get('broken_succeeds'):
                    self.version = BROKEN
                    return 0, '', ''
                if self.faults.get('rollback_loses_data'):
                    self.data['bots'].pop()
                if self.faults.get('rollback_loses_env'):
                    self.env = self.env.replace(ua.CUSTOM_KEY, 'GONE')
                if self.faults.get('no_rollback_message'):
                    return 1, '', 'boom'
                return 1, '', 'tofi: update rejected; previous version restored (candidate unhealthy)'
            self.version = target
            if self.faults.get('update_loses_env'):
                self.env = 'TOFI_VERSION=%s\n' % target
            else:
                self.env = self.env.replace('TOFI_VERSION=%s' % FROM, 'TOFI_VERSION=%s' % target)
            if self.faults.get('update_loses_data'):
                self.data['keys'] = []
            return 0, '', ''
        if command == 'restore':
            item = next(b for b in self.backups if b['id'] == argv[1])
            if self.faults.get('restore_keeps_new_data'):
                pass
            else:
                pre = self.backup('pre-restore')
                self.data = copy.deepcopy(item['data'])
                self.env = item['env']
                self.version = item['version']
                self.client.authenticated = True
                return 0, '', ''
            self.backup('pre-restore')
            self.version = item['version']
            return 0, '', ''
        raise AssertionError('unexpected command %r' % (argv,))


def make_args(**overrides):
    values = dict(from_version=FROM, to_version=TO, broken_manifest=Path('/nonexistent/broken.json'),
                  skip_failure_scenario=False, skip_install=False, install_script=Path('install.sh'),
                  url='https://127.0.0.1:8321', state_dir=Path('/tmp'), evidence=None)
    values.update(overrides)
    return type('Args', (), values)()


class AcceptanceFlowTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.manifest = Path(self.temp.name) / 'broken.json'
        self.manifest.write_text(json.dumps({'version': BROKEN}))
        self.log = []

    def run_flow(self, **faults):
        args = make_args(broken_manifest=self.manifest)
        host = FakeHost(args, **faults)
        # The fixture check treats a missing install-state as "fresh".
        with unittest.mock.patch.object(ua.Path, 'exists', lambda self_: False):
            evidence = ua.Acceptance(host, args, log=self.log.append).run()
        return host, evidence

    def fails(self, pattern, **faults):
        with self.assertRaisesRegex(ua.Failure, pattern):
            self.run_flow(**faults)

    def test_a_correct_installer_passes_every_step_in_order(self):
        host, evidence = self.run_flow()
        self.assertTrue(evidence['passed'])
        commands = [call[0] if isinstance(call, tuple) else call for call in host.calls]
        updates = [call for call in host.calls if call[0] == 'update']
        self.assertEqual(updates[0][:4], ('update', '--version', TO, '--yes'))
        self.assertEqual(updates[1][:3], ('update', '--manifest', str(self.manifest)))
        self.assertEqual([c for c in commands if c in ('install', 'update', 'restore')],
                         ['install', 'update', 'update', 'restore', 'restore'])
        self.assertEqual(host.version, TO)
        self.assertIn(ua.CUSTOM_KEY, host.env)
        self.assertEqual([s.split('.')[0] for s in evidence['steps']], ['1', '2', '3', '4', '5', '6', '7'])
        self.assertIn('Written after the backup', [b['name'] for b in host.data['bots']])

    def test_data_lost_in_the_update_fails(self):
        self.fails('data changed across the update', update_loses_data=True)

    def test_custom_env_line_lost_in_the_update_fails(self):
        self.fails('custom line .* lost', update_loses_env=True)

    def test_rollback_that_loses_data_fails(self):
        self.fails('written before the failed update is not intact', rollback_loses_data=True)

    def test_rollback_that_loses_the_env_line_fails(self):
        self.fails('custom line .* lost', rollback_loses_env=True)

    def test_a_broken_manifest_that_installs_fails(self):
        self.fails('unexpectedly succeeded', broken_succeeds=True)

    def test_missing_rollback_message_fails(self):
        self.fails('rollback message missing', no_rollback_message=True)

    def test_restore_that_keeps_post_backup_data_fails(self):
        self.fails('', restore_keeps_new_data=True)

    def test_skipping_the_failure_scenario_is_loud(self):
        args = make_args(skip_failure_scenario=True, broken_manifest=None)
        host = FakeHost(args)
        with unittest.mock.patch.object(ua.Path, 'exists', lambda self_: False):
            evidence = ua.Acceptance(host, args, log=self.log.append).run()
        self.assertEqual(evidence['failure_scenario'], 'skipped')
        self.assertTrue(any('NOT a release-gate pass' in line for line in self.log))

    def test_refuses_to_overwrite_an_existing_installation(self):
        args = make_args(broken_manifest=self.manifest)
        with unittest.mock.patch.object(ua.Path, 'exists', lambda self_: True):
            with self.assertRaisesRegex(ua.Failure, 'already installed'):
                ua.Acceptance(FakeHost(args), args, log=self.log.append).run()


class HelperTests(unittest.TestCase):
    def test_env_lines(self):
        self.assertEqual(ua.env_lines('# c\nA=1\n\nB = x y \nnot a line\n'), {'A': '1', 'B': 'x y'})

    def test_snapshot_diff(self):
        self.assertEqual(ua.snapshot_diff({'a': 1}, {'a': 1}), [])
        self.assertEqual(len(ua.snapshot_diff({'a': 1, 'b': 2}, {'a': 1, 'c': 3})), 2)

    def test_pick_backup_takes_the_newest_match(self):
        rows = [{'id': 'old', 'version': 'v1', 'reason': 'pre-update', 'created_at': '1'},
                {'id': 'new', 'version': 'v1', 'reason': 'pre-update', 'created_at': '2'},
                {'id': 'other', 'version': 'v1', 'reason': 'manual', 'created_at': '3'}]
        self.assertEqual(ua.pick_backup(rows, 'v1', 'pre-update')['id'], 'new')
        self.assertIsNone(ua.pick_backup(rows, 'v2', 'pre-update'))

    def test_argument_validation(self):
        args = ua.parse_args(['--from', FROM, '--to', TO, '--broken-manifest', 'b.json'])
        self.assertEqual((args.from_version, args.to_version, args.broken_manifest), (FROM, TO, Path('b.json')))
        for argv in (['--from', FROM, '--to', TO],                       # no broken manifest
                     ['--from', 'x', '--to', TO, '--broken-manifest', 'b'],
                     ['--from', FROM, '--to', FROM, '--broken-manifest', 'b']):
            with self.subTest(argv=argv), self.assertRaises(ua.Failure):
                ua.parse_args(argv)
        self.assertTrue(ua.parse_args(['--from', FROM, '--to', TO, '--skip-failure-scenario']).skip_failure_scenario)

    def test_refuses_to_run_without_root(self):
        with unittest.mock.patch.object(ua.os, 'geteuid', return_value=1000):
            self.assertEqual(ua.main(['--from', FROM, '--to', TO, '--broken-manifest', 'b.json']), 2)


if __name__ == '__main__':
    unittest.main()
