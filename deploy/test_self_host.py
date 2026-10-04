"""Synthetic installer failure-path checks; no host permission or Docker actions.

These unit checks do not establish plan integrity or clean-host installation.
"""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('self_host', Path(__file__).with_name('self_host.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallerFailurePaths(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-installer-synthetic-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        claims = tempfile.TemporaryDirectory(prefix='tofi-installer-claims-synthetic-')
        self.addCleanup(claims.cleanup)
        self.claim_parent = Path(claims.name).resolve()
        self.native_claim_root = installer.CLAIM_ROOT
        claim_root = patch.object(installer, 'CLAIM_ROOT', self.claim_parent / 'registry')
        claim_root.start()
        self.addCleanup(claim_root.stop)
        (self.root / 'data').mkdir()
        self.data = self.root / 'data/current-data'
        self.data.write_bytes(b'synthetic writes after initial installation')
        self.plan = {'root': str(self.root), 'project': 'synthetic', 'port': 18333}
        self.compose = {'name': 'synthetic', 'services': {
            'app': {'image': 'sha256:' + 'a' * 64},
            'worker': {'image': 'sha256:' + 'b' * 64}}}
        installer.write(self.root / 'compose.yaml', self.compose)

    def test_lifecycle_registry_is_disposable(self):
        self.assertEqual(installer.CLAIM_ROOT, self.claim_parent / 'registry')
        self.assertNotEqual(installer.CLAIM_ROOT, self.native_claim_root)
        self.assertNotEqual(self.claim_parent, self.root)
        self.assertFalse(installer.CLAIM_ROOT.exists())
        with patch.object(installer, 'owned_state', return_value=(self.root, self.plan, self.compose)), \
             installer.lifecycle(self.root):
            self.assertTrue(installer.CLAIM_ROOT.is_dir())
            self.assertEqual(installer.CLAIM_ROOT.stat().st_mode & 0o777, 0o700)
            self.assertEqual(list(installer.CLAIM_ROOT.iterdir()), [])
        self.assertTrue(self.data.exists())

    def test_mutable_tags_and_hostile_paths_rejected(self):
        for value in ['app:latest', 'sha256:short', 'sha256:' + 'g' * 64]:
            with self.assertRaises(ValueError): installer.image_id(value)
        for value in ['relative/path', '/tmp/name with space', '/tmp/name;command']:
            with self.assertRaises(ValueError): installer.absolute(value)
        link = self.root / 'link'
        link.symlink_to(self.root / 'data')
        with self.assertRaises((ValueError, OSError)): installer.absolute(link / 'new-root')

    def test_permission_acceptance_required_before_preflight(self):
        with patch.object(installer, 'preflight') as preflight:
            with self.assertRaises(ValueError): installer.apply(self.root, False)
            preflight.assert_not_called()

    def test_unsupported_host_preflight_is_read_only(self):
        with patch.object(installer, 'load_plan', return_value=self.plan), \
             patch.object(installer.platform, 'system', return_value='Darwin'), \
             patch.object(installer, 'run') as command:
            with self.assertRaises(ValueError): installer.preflight(self.root)
            command.assert_not_called()

    def test_unclean_stop_prevents_uninstall_and_keeps_data(self):
        with patch.object(installer, 'owned_state', return_value=(self.root, self.plan, self.compose)), \
             patch.object(installer, 'stopped', side_effect=ValueError('unclean stop')), \
             patch.object(installer, 'compose') as command:
            with self.assertRaises(ValueError): installer.uninstall(self.root)
            command.assert_not_called()
        self.assertTrue(self.data.exists())

    def test_successful_uninstall_keeps_current_data_and_only_removes_owned_services(self):
        with patch.object(installer, 'owned_state', return_value=(self.root, self.plan, self.compose)), \
             patch.object(installer, 'stopped'), patch.object(installer, 'compose') as command:
            result = installer.uninstall(self.root)
            command.assert_called_once_with(self.root, 'rm', '-f', 'app', 'worker')
        self.assertTrue(result['data_retained'])
        self.assertEqual(self.data.read_bytes(), b'synthetic writes after initial installation')
        self.assertEqual(json.loads((self.root / 'install-state.json').read_text())['phase'], 'stopped-retained')

    def test_unreviewed_upgrade_cannot_implicitly_restore_old_code(self):
        with patch.object(installer, 'owned_state', return_value=(self.root, self.plan, self.compose)), \
             patch.object(installer, 'inspect_image'), patch.object(installer, 'stopped') as stop, \
             patch.object(installer, 'compose'), \
             patch.object(installer, 'health', side_effect=[ValueError('new code unhealthy'), None]):
            with self.assertRaisesRegex(ValueError, 'reviewed D100 upgrade plan'):
                installer.upgrade(self.root, 'sha256:' + 'c' * 64, 'sha256:' + 'd' * 64)
            stop.assert_not_called()
        saved = json.loads((self.root / 'compose.yaml').read_text())
        self.assertEqual(saved['services']['app']['image'], 'sha256:' + 'a' * 64)
        self.assertEqual(self.data.read_bytes(), b'synthetic writes after initial installation')

    def test_nonzero_worker_exit_fences_restart(self):
        exited = [{'State': {'Running': False, 'Pid': 0, 'ExitCode': 1}}]
        responses = [subprocess.CompletedProcess([], 0, ''), subprocess.CompletedProcess([], 0, 'synthetic-id\n')]
        with patch.object(installer, 'compose', side_effect=responses), \
             patch.object(installer, 'run', return_value=subprocess.CompletedProcess([], 0, json.dumps(exited))):
            with self.assertRaisesRegex(ValueError, 'unclean stop'): installer.stopped(self.root)


if __name__ == '__main__': unittest.main()
