"""Synthetic checks for the safe-update machinery (docs/agent-plan/safe-updates.md, A1-A6).

Fake root, fake data dir, sqlite fixtures; no Docker, root or KVM. These do not
replace upgrade_acceptance.py on a real VM.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import sqlite3
import sys
import tarfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_tofi_host as base  # noqa: E402
import tofi_host  # noqa: E402

HostError = tofi_host.HostError
OLD, NEW = base.OLD, base.NEW
CUSTOM_LINES = 'CUSTOM_X=1\nTOFI_EXPERIMENT=a b c\n'


def add_custom_env(case):
    """Operator-added lines, as the 2026-10-09 production incident had."""
    with open(case.P.env_file, 'a') as stream:
        stream.write(CUSTOM_LINES)
    values = tofi_host.read_env()
    values['TOFI_TRUSTED_PROXIES'] = '10.0.0.0/8'
    tofi_host.write_env(values)


def make_runs_db(path, statuses):
    connection = sqlite3.connect(str(path))
    connection.execute('CREATE TABLE runs(id TEXT PRIMARY KEY, status TEXT NOT NULL)')
    connection.executemany('INSERT INTO runs VALUES (?, ?)', [('run%d' % i, s) for i, s in enumerate(statuses)])
    connection.commit()
    connection.close()


# ---------------------------------------------------------------- A1


class EnvPreservationTests(base.LifecycleBase):
    def test_format_env_writes_known_keys_first_then_unknown_keys_verbatim_in_order(self):
        text = tofi_host.format_env({'ZZ_LAST': '1', 'TOFI_VERSION': 'v1.2.3', 'A_FIRST': 'x y', 'TOFI_DOMAIN': ''})
        lines = text.splitlines()
        self.assertTrue(lines[0].startswith('# Managed by tofi'))
        known = lines[1:1 + len(tofi_host.ENV_KEYS)]
        self.assertEqual([line.split('=')[0] for line in known], tofi_host.ENV_KEYS)
        self.assertEqual(lines[1 + len(tofi_host.ENV_KEYS):], ['ZZ_LAST=1', 'A_FIRST=x y'])
        # Round trip.
        self.assertEqual(tofi_host.format_env(self.parse(text)), text)

    def parse(self, text):
        path = self.root / 'roundtrip.env'
        path.write_text(text)
        return tofi_host.read_env(path)

    def test_known_keys_still_refuse_spaces_and_newlines(self):
        with self.assertRaises(HostError):
            tofi_host.format_env({'TOFI_DOMAIN': 'a b'})
        with self.assertRaises(HostError):
            tofi_host.format_env({'EXTRA': 'a\nb'})

    def test_custom_lines_survive_update_rollback_and_install_resume(self):
        self.installed()
        add_custom_env(self)
        self.assertIn('CUSTOM_X=1', self.P.env_file.read_text())
        # Successful update.
        tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        env = tofi_host.read_env()
        self.assertEqual((env['CUSTOM_X'], env['TOFI_EXPERIMENT'], env['TOFI_TRUSTED_PROXIES']),
                         ('1', 'a b c', '10.0.0.0/8'))
        self.assertEqual(env['TOFI_VERSION'], NEW)
        # Rejected update: rollback restores the previous env with its extras.
        self.start_services.side_effect = [HostError('candidate unhealthy'), None]
        with self.assertRaisesRegex(HostError, 'update rejected'):
            tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest('v0.3.0', '3'))))
        env = tofi_host.read_env()
        self.assertEqual((env['CUSTOM_X'], env['TOFI_EXPERIMENT'], env['TOFI_TRUSTED_PROXIES']),
                         ('1', 'a b c', '10.0.0.0/8'))
        self.assertEqual(env['TOFI_VERSION'], NEW)
        # Install-resume rewrites the env through apply_host_config.
        self.start_services.side_effect = None
        state = json.loads(self.P.state_file.read_text())
        state['phase'], state['transaction'] = 'install-failed', {'kind': 'install', 'step': 'start-worker'}
        self.P.state_file.write_text(json.dumps(state))
        tofi_host.install(None, {'port': 8321})
        env = tofi_host.read_env()
        self.assertEqual((env['CUSTOM_X'], env['TOFI_EXPERIMENT']), ('1', 'a b c'))

    def test_custom_lines_survive_restore(self):
        self.installed()
        add_custom_env(self)
        meta = self.take_backup()
        values = tofi_host.read_env()
        values['CUSTOM_X'] = 'changed-after-backup'
        tofi_host.write_env(values)
        self.write_manifest(base.manifest(OLD, '1'))
        with mock.patch.object(tofi_host, 'restore_manifest', return_value=base.manifest(OLD, '1')):
            tofi_host.restore_backup(meta['id'], yes=True)
        self.assertEqual(tofi_host.read_env()['CUSTOM_X'], '1')

    def take_backup(self, reason='manual'):
        return tofi_host.backup_data(reason, tofi_host.read_env())


# ---------------------------------------------------------------- A2


class BackupCase(base.LifecycleBase):
    def setUp(self):
        super().setUp()
        self.stamp = iter(range(1000))
        mock.patch.object(tofi_host, 'backup_timestamp',
                          side_effect=lambda: '20261009T%06dZ' % next(self.stamp)).start()

    def seed(self):
        """Synthetic account data (several files, a subdir and a link) plus a computer disk."""
        self.installed()
        (self.P.data / 'sub').mkdir()
        (self.P.data / 'sub' / 'notes.txt').write_text('synthetic notes')
        os.symlink('tofi.db', self.P.data / 'db-link')
        disk = self.P.worker_state / 'acct-1' / 'workspace.img'
        disk.parent.mkdir(parents=True)
        disk.write_bytes(b'D' * 4096)
        return disk

    def backup(self, reason='pre-update'):
        return tofi_host.backup_data(reason, tofi_host.read_env())

    def names(self, archive):
        with tarfile.open(archive) as tar:
            return {name[2:] if name.startswith('./') else name for name in tar.getnames()}

    def data_snapshot(self):
        return {str(path.relative_to(self.P.data)): (path.read_bytes() if path.is_file() and not path.is_symlink()
                                                     else os.readlink(path) if path.is_symlink() else None)
                for path in sorted(self.P.data.rglob('*'))}


class BackupTests(BackupCase):
    def check_backup(self, compression):
        disk = self.seed()
        expected_data = self.data_snapshot()
        with mock.patch.object(tofi_host, 'zstd_available', return_value=compression == 'zst'):
            meta = self.backup('pre-update')
        directory = self.P.backups / meta['id']
        self.assertEqual(directory.name, '20261009T000000Z-%s' % OLD)
        self.assertEqual(oct(directory.stat().st_mode & 0o777), '0o700')
        self.assertEqual(oct(self.P.backups.stat().st_mode & 0o777), '0o700')
        data_name = 'data.tar.' + compression
        self.assertEqual(meta['archives'], {'data': data_name, 'etc': 'etc.tar.gz'})
        self.assertEqual(sorted(p.name for p in directory.iterdir()), sorted([data_name, 'etc.tar.gz', 'meta.json']))
        for path in directory.iterdir():
            self.assertEqual(oct(path.stat().st_mode & 0o777), '0o600', path.name)
        for name in (data_name, 'etc.tar.gz'):
            self.assertEqual(meta['sha256'][name], hashlib.sha256((directory / name).read_bytes()).hexdigest())
        self.assertEqual(tofi_host.read_backup_meta(directory)['sha256'], meta['sha256'])
        self.assertEqual((meta['version'], meta['reason'], meta['guest']), (OLD, 'pre-update', OLD))
        self.assertEqual((meta['app_image'], meta['worker_image']),
                         (base.image('app', '1'), base.image('worker', '1')))
        self.assertTrue(meta['created_at'].endswith('Z'))
        for key in ('data_bytes', 'etc_bytes', 'data_archive', 'etc_archive'):
            self.assertGreater(meta['sizes'][key], 0, key)
        self.assertEqual(tofi_host.verify_backup(directory)['id'], meta['id'])
        # Extract the real archive and compare with the source tree; the computer
        # disk and the journal are not in any archive.
        target = self.root / 'extracted'
        target.mkdir()
        tofi_host.extract_archive(directory / data_name, target)
        self.assertEqual((target / 'tofi.db').read_bytes(), base.DATA)
        self.assertEqual((target / 'sub' / 'notes.txt').read_text(), 'synthetic notes')
        self.assertEqual(os.readlink(target / 'db-link'), 'tofi.db')
        etc = self.root / 'etc-extracted'
        etc.mkdir()
        tofi_host.extract_archive(directory / 'etc.tar.gz', etc)
        self.assertTrue((etc / 'tofi.env').is_file())
        self.assertTrue((etc / 'worker.json').is_file())
        self.assertTrue((etc / 'tls').is_dir())
        self.assertFalse((etc / 'install-state.json').exists())
        everything = ' '.join(self.names_any(directory / data_name) | self.names(directory / 'etc.tar.gz'))
        self.assertNotIn('workspace.img', everything)
        self.assertNotIn('acct-1', everything)
        self.assertEqual(self.data_snapshot(), expected_data, 'backup must not modify the data')
        self.assertTrue(disk.exists())

    def names_any(self, archive):
        with tofi_host.archive_reader(archive) as tar:
            return {name[2:] if name.startswith('./') else name for name in tar.getnames()}

    def test_backup_gzip(self):
        self.check_backup('gz')

    @unittest.skipUnless(shutil.which('zstd'), 'zstd not installed')
    def test_backup_zstd(self):
        self.check_backup('zst')

    def test_backup_directory_is_hidden_until_complete(self):
        self.seed()
        with mock.patch.object(tofi_host, 'sha256_file', side_effect=OSError('disk error')):
            with self.assertRaisesRegex(HostError, 'Cannot write the backup'):
                self.backup()
        self.assertEqual([p.name for p in self.P.backups.iterdir()], [])
        self.assertEqual(tofi_host.list_backups(), [])

    def test_unreadable_zstd_fails_cleanly(self):
        self.seed()
        with mock.patch.object(tofi_host, 'zstd_available', return_value=True), \
                mock.patch.object(tofi_host.subprocess, 'Popen', side_effect=FileNotFoundError('zstd')):
            with self.assertRaises(HostError):
                self.backup()
        self.assertEqual(list(self.P.backups.iterdir()), [])

    def test_two_backups_in_the_same_second_get_distinct_ids(self):
        self.seed()
        with mock.patch.object(tofi_host, 'backup_timestamp', return_value='20261009T010101Z'):
            first, second = self.backup(), self.backup()
        self.assertNotEqual(first['id'], second['id'])

    def test_prune_keeps_the_newest_three(self):
        self.seed()
        ids = [self.backup()['id'] for _ in range(5)]
        (self.P.backups / '.leftover.partial').mkdir()
        removed = tofi_host.prune_backups()
        self.assertEqual(sorted(removed), sorted(ids[:2]))
        self.assertEqual([item['id'] for item in tofi_host.list_backups()], ids[:1:-1])
        self.assertFalse((self.P.backups / '.leftover.partial').exists())

    def test_listing_and_prefix_lookup(self):
        self.seed()
        first = self.backup('manual')
        self.assertEqual(tofi_host.find_backup(first['id'])['id'], first['id'])
        self.assertEqual(tofi_host.find_backup(first['id'][:12])['id'], first['id'])
        with self.assertRaisesRegex(HostError, 'No backup matches'):
            tofi_host.find_backup('nope')
        with self.assertRaisesRegex(HostError, 'not a backup id'):
            tofi_host.find_backup('../x')
        text = tofi_host.render_backups(tofi_host.list_backups())
        self.assertIn(first['id'], text)
        self.assertIn('manual', text)
        self.assertIn('No backups yet', tofi_host.render_backups([]))

    def test_disk_space_refusal_names_the_numbers_and_changes_nothing(self):
        self.seed()
        estimate = tofi_host.tree_bytes(self.P.data) + tofi_host.tree_bytes(self.P.etc)
        need = 2 * estimate + tofi_host.GIB
        env_bytes, state_bytes = self.P.env_file.read_bytes(), self.P.state_file.read_bytes()
        with mock.patch.object(tofi_host, 'free_bytes', return_value=need - 1):
            with self.assertRaises(HostError) as caught:
                tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        message = str(caught.exception)
        self.assertIn('Not enough free disk space', message)
        self.assertIn(tofi_host.human_bytes(need), message)
        self.assertIn(tofi_host.human_bytes(need - 1), message)
        self.assertIn('Nothing was changed', message)
        self.stopped.assert_not_called()
        self.assertEqual((self.P.env_file.read_bytes(), self.P.state_file.read_bytes()), (env_bytes, state_bytes))
        self.assertFalse(self.P.backups.exists() and list(self.P.backups.iterdir()))
        # Exactly enough passes.
        with mock.patch.object(tofi_host, 'free_bytes', return_value=need):
            self.assertTrue(tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))['upgraded'])

    def test_estimate_excludes_computer_disks(self):
        disk = self.seed()
        disk.write_bytes(b'D' * (3 * 1024 * 1024))
        self.assertLess(tofi_host.tree_bytes(self.P.data) + tofi_host.tree_bytes(self.P.etc), 1024 * 1024)


class RestoreFilesTests(BackupCase):
    def test_restore_replaces_data_and_etc_and_keeps_the_live_journal(self):
        self.seed()
        meta = self.backup()
        before = self.data_snapshot()
        (self.P.data / 'tofi.db').write_bytes(b'CHANGED')
        (self.P.data / 'extra').write_text('written after the backup')
        values = tofi_host.read_env()
        values['CUSTOM_X'] = 'later'
        tofi_host.write_env(values)
        journal = self.P.state_file.read_bytes()
        tofi_host.restore_files(meta['id'])
        self.assertEqual(self.data_snapshot(), before)
        self.assertNotIn('CUSTOM_X', tofi_host.read_env())
        self.assertEqual(self.P.state_file.read_bytes(), journal)
        self.assertEqual(sorted(p.name for p in self.P.var.iterdir() if '.restore-' in p.name), [])
        self.assertEqual(sorted(p.name for p in self.P.etc.parent.iterdir() if '.restore-' in p.name), [])
        # Applied with the application identity on the data directory.
        tofi_host.os.chown.assert_any_call(mock.ANY, tofi_host.APP_UID, tofi_host.APP_UID)
        self.assertEqual(oct(self.P.data.stat().st_mode & 0o777), '0o700')

    def test_failure_mid_extract_leaves_the_original_data(self):
        self.seed()
        meta = self.backup()
        (self.P.data / 'tofi.db').write_bytes(b'NEWER')
        data_before, env_before = self.data_snapshot(), self.P.env_file.read_bytes()

        def explode(tar):
            members = iter(tar)
            yield next(members)
            yield next(members)
            raise OSError('No space left on device')

        with mock.patch.object(tofi_host, 'safe_members', side_effect=explode):
            with self.assertRaisesRegex(HostError, 'Cannot extract'):
                tofi_host.restore_files(meta['id'])
        self.assertEqual(self.data_snapshot(), data_before)
        self.assertEqual(self.P.env_file.read_bytes(), env_before)
        for parent in (self.P.var, self.P.etc.parent):
            self.assertEqual([p.name for p in parent.iterdir() if '.restore-' in p.name], [])

    def test_failure_staging_etc_leaves_both_directories(self):
        self.seed()
        meta = self.backup()
        (self.P.data / 'tofi.db').write_bytes(b'NEWER')
        real = tofi_host.stage_restore

        def stage(target, archive, finish=None):
            if Path(target) == self.P.etc:
                raise HostError('etc archive unreadable')
            return real(target, archive, finish)

        with mock.patch.object(tofi_host, 'stage_restore', side_effect=stage):
            with self.assertRaisesRegex(HostError, 'etc archive unreadable'):
                tofi_host.restore_files(meta['id'])
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), b'NEWER')
        self.assertEqual([p.name for p in self.P.var.iterdir() if '.restore-' in p.name], [])

    def test_old_data_is_never_deleted_before_the_new_copy_is_in_place(self):
        self.seed()
        meta = self.backup()
        (self.P.data / 'tofi.db').write_bytes(b'NEWER')
        real_rename = os.rename
        data_tmp = str(self.P.data) + '.restore-tmp'

        def rename(source, destination):
            if str(source) == data_tmp:
                raise OSError('rename failed')
            return real_rename(source, destination)

        with mock.patch.object(tofi_host.os, 'rename', side_effect=rename):
            with self.assertRaises(OSError):
                tofi_host.restore_files(meta['id'])
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), b'NEWER')
        self.assertFalse(Path(str(self.P.data) + '.restore-old').exists())

    def test_interrupted_swap_is_recovered(self):
        self.seed()
        old = Path(str(self.P.data) + '.restore-old')
        os.rename(self.P.data, old)  # the crash window: data aside, new copy not yet renamed in
        tofi_host.restore_leftovers(self.P.data)
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), base.DATA)
        self.assertFalse(old.exists())
        # A completed swap whose cleanup was cut short just loses the stale copy.
        old.mkdir()
        (old / 'stale').write_text('x')
        tofi_host.restore_leftovers(self.P.data)
        self.assertFalse(old.exists())
        self.assertTrue((self.P.data / 'tofi.db').exists())

    def test_tampered_archive_is_refused_before_anything_changes(self):
        self.seed()
        meta = self.backup()
        archive = self.P.backups / meta['id'] / meta['archives']['data']
        raw = bytearray(archive.read_bytes())
        raw[len(raw) // 2] ^= 0xFF
        archive.write_bytes(bytes(raw))
        before = self.data_snapshot()
        with self.assertRaisesRegex(HostError, 'damaged'):
            tofi_host.restore_files(meta['id'])
        self.assertEqual(self.data_snapshot(), before)
        # Missing archive and unreadable meta are refused too.
        archive.unlink()
        with self.assertRaisesRegex(HostError, 'missing'):
            tofi_host.verify_backup(self.P.backups / meta['id'])
        (self.P.backups / meta['id'] / 'meta.json').write_text('{not json')
        with self.assertRaisesRegex(HostError, 'meta.json'):
            tofi_host.verify_backup(self.P.backups / meta['id'])

    def craft_backup(self, members):
        """A backup directory whose data archive holds `members` (name, kind, extra)."""
        directory = self.P.backups / 'crafted'
        directory.mkdir(parents=True)
        path = directory / 'data.tar.gz'
        with tarfile.open(path, 'w:gz') as tar:
            for name, kind, extra in members:
                info = tarfile.TarInfo(name)
                if kind == 'file':
                    info.size = 1
                    tar.addfile(info, io.BytesIO(b'x'))
                elif kind == 'dir':
                    info.type = tarfile.DIRTYPE
                    info.mode = 0o755
                    tar.addfile(info)
                elif kind == 'sym':
                    info.type = tarfile.SYMTYPE
                    info.linkname = extra
                    tar.addfile(info)
                elif kind == 'fifo':
                    info.type = tarfile.FIFOTYPE
                    tar.addfile(info)
        etc = directory / 'etc.tar.gz'
        with tarfile.open(etc, 'w:gz') as tar:
            info = tarfile.TarInfo('tofi.env')
            tar.addfile(info, io.BytesIO(b''))
        meta = {'schema': 1, 'id': 'crafted', 'version': OLD, 'created_at': '2026-01-01T00:00:00Z',
                'archives': {'data': 'data.tar.gz', 'etc': 'etc.tar.gz'}, 'sizes': {},
                'sha256': {'data.tar.gz': tofi_host.sha256_file(path), 'etc.tar.gz': tofi_host.sha256_file(etc)}}
        (directory / 'meta.json').write_text(json.dumps(meta))

    def test_unsafe_archives_are_refused_and_write_nothing_outside(self):
        self.installed()
        outside = self.root / 'var/lib/evil'
        cases = {
            'parent escape': [('.', 'dir', None), ('../evil', 'file', None)],
            'absolute path': [('.', 'dir', None), ('/tmp/tofi-evil-absolute', 'file', None)],
            'device-like entry': [('.', 'dir', None), ('pipe', 'fifo', None)],
            'write through a link': [('.', 'dir', None), ('d', 'sym', '/etc'), ('d/passwd', 'file', None)],
        }
        for label, members in cases.items():
            with self.subTest(label):
                if (self.P.backups / 'crafted').exists():
                    shutil.rmtree(self.P.backups / 'crafted')
                self.craft_backup(members)
                with self.assertRaises(HostError):
                    tofi_host.restore_files('crafted')
                self.assertFalse(outside.exists())
                self.assertEqual((self.P.data / 'tofi.db').read_bytes(), base.DATA)
        self.assertFalse(Path('/tmp/tofi-evil-absolute').exists())


# ---------------------------------------------------------------- A2: update, rollback, restore commands


class UpdateBackupTests(BackupCase):
    def test_update_backs_up_after_stop_and_before_anything_changes(self):
        self.seed()
        order = []
        real_backup, real_apply = tofi_host.backup_data, tofi_host.apply_host_config
        self.stopped.side_effect = lambda: order.append('stopped')
        with mock.patch.object(tofi_host, 'backup_data', side_effect=lambda *a, **k: (
                order.append('backup'), real_backup(*a, **k))[1]), \
                mock.patch.object(tofi_host, 'apply_host_config', side_effect=lambda *a, **k: (
                    order.append('apply'), real_apply(*a, **k))[1]):
            tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        self.assertEqual(order[:3], ['stopped', 'backup', 'apply'])
        backups = tofi_host.list_backups()
        self.assertEqual(len(backups), 1)
        self.assertEqual((backups[0]['meta']['reason'], backups[0]['meta']['version']), ('pre-update', OLD))
        self.assertEqual(self.phase(), 'installed')

    def test_update_prunes_to_three_backups(self):
        self.seed()
        old_ids = [self.backup()['id'] for _ in range(3)]
        tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        remaining = [item['id'] for item in tofi_host.list_backups()]
        self.assertEqual(len(remaining), 3)
        self.assertNotIn(old_ids[0], remaining)
        self.assertEqual(tofi_host.list_backups()[0]['meta']['version'], OLD)

    def test_failed_backup_aborts_and_restarts_the_previous_version_untouched(self):
        self.seed()
        restore = mock.Mock(wraps=tofi_host.restore_files)
        with mock.patch.object(tofi_host, 'backup_data', side_effect=HostError('disk full')), \
                mock.patch.object(tofi_host, 'restore_files', restore):
            with self.assertRaisesRegex(HostError, 'update rejected'):
                tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        restore.assert_not_called()
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
        self.assertEqual(tofi_host.current_release(), OLD)
        self.assertEqual(self.phase(), 'installed')
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), base.DATA)

    def test_started_candidate_failure_restores_data_and_settings_from_the_backup(self):
        self.seed()
        add_custom_env(self)
        before, env_before = self.data_snapshot(), self.P.env_file.read_bytes()
        calls = []

        def start(env, **kwargs):
            calls.append(env['TOFI_VERSION'])
            if len(calls) == 1:
                # The candidate "migrates" the data, then fails its health check.
                (self.P.data / 'tofi.db').write_bytes(b'MIGRATED BY CANDIDATE')
                (self.P.data / 'candidate-table').write_text('x')
                raise HostError('candidate unhealthy')

        self.start_services.side_effect = start
        with self.assertRaisesRegex(HostError, 'update rejected; previous version restored'):
            tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        self.assertEqual(calls, [NEW, OLD])
        self.assertEqual(self.data_snapshot(), before)
        self.assertEqual(self.P.env_file.read_bytes(), env_before)
        self.assertEqual(tofi_host.current_release(), OLD)
        self.assertEqual(self.phase(), 'installed')
        self.assertEqual(self.stopped.call_count, 2)
        self.assertIn('Restored data and settings from backup', sys.stdout.getvalue())
        self.assertEqual(len(tofi_host.list_backups()), 1)

    def test_failure_before_the_candidate_starts_leaves_the_data_alone(self):
        self.seed()
        restore = mock.Mock(wraps=tofi_host.restore_files)
        real_apply = tofi_host.apply_host_config
        calls = []

        def apply(bundle, env):
            calls.append(env['TOFI_VERSION'])
            if len(calls) == 1:
                (self.P.data / 'touched-while-stopped').write_text('1')
                raise HostError('apparmor refused the profile')
            return real_apply(bundle, env)

        with mock.patch.object(tofi_host, 'apply_host_config', side_effect=apply), \
                mock.patch.object(tofi_host, 'restore_files', restore):
            with self.assertRaisesRegex(HostError, 'update rejected'):
                tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        restore.assert_not_called()
        self.assertTrue((self.P.data / 'touched-while-stopped').exists())
        self.assertEqual(self.phase(), 'installed')
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)

    def test_resumed_rollback_does_not_restore_twice(self):
        self.seed()
        calls = []

        def start(env, **kwargs):
            calls.append(env['TOFI_VERSION'])
            if len(calls) <= 2:
                raise HostError('start failed %d' % len(calls))

        self.start_services.side_effect = start
        with self.assertRaisesRegex(HostError, 'rollback failed'):
            tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        self.assertEqual(self.phase(), 'rollback-failed')
        transaction = json.loads(self.P.state_file.read_text())['transaction']
        self.assertTrue(transaction['data_restored'])
        self.assertTrue(transaction['data_changed'])
        (self.P.data / 'written-after-restore').write_text('keep me')
        restore = mock.Mock(wraps=tofi_host.restore_files)
        with mock.patch.object(tofi_host, 'restore_files', restore):
            tofi_host.install(None, {'port': 8321})
        restore.assert_not_called()
        self.assertTrue((self.P.data / 'written-after-restore').exists())
        self.assertEqual(self.phase(), 'installed')

    def test_rollback_refuses_when_the_backup_is_damaged_and_keeps_current_data(self):
        self.seed()

        def start(env, **kwargs):
            if env['TOFI_VERSION'] == NEW:
                (self.P.data / 'tofi.db').write_bytes(b'MIGRATED')
                for item in tofi_host.list_backups():
                    (item['path'] / item['meta']['archives']['etc']).write_bytes(b'garbage')
                raise HostError('candidate unhealthy')

        self.start_services.side_effect = start
        with self.assertRaisesRegex(HostError, 'rollback failed'):
            tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), b'MIGRATED')
        self.assertEqual(self.phase(), 'rollback-failed')

    def test_rollback_of_an_update_begun_by_an_older_tofi_warns_and_proceeds(self):
        self.seed()
        previous = tofi_host.snapshot()
        self.make_bundle(NEW)
        self.make_guest(NEW)
        state = json.loads(self.P.state_file.read_text())
        state.update(phase='upgrade-failed', transaction={
            'kind': 'upgrade', 'step': 'start-candidate', 'previous': previous, 'candidate_version': NEW,
            'data_changed': True, 'backup': None})
        self.P.state_file.write_text(json.dumps(state))
        with mock.patch('sys.stderr', new_callable=io.StringIO) as err:
            tofi_host.install(None, {'port': 8321})
        self.assertIn('no backup was taken', err.getvalue())
        self.assertEqual(self.phase(), 'installed')


class BackupCommandTests(BackupCase):
    def test_backup_stops_backs_up_and_starts_again(self):
        self.seed()
        running = [{'State': {'Running': True}, 'Config': {'Labels': {}}}]
        with mock.patch.object(tofi_host, 'project_containers', return_value=running):
            meta = tofi_host.backup_now('before the holiday')
        self.stopped.assert_called_once()
        self.start_services.assert_called_once()
        self.assertEqual((meta['reason'], meta['note']), ('manual', 'before the holiday'))
        self.assertEqual(self.phase(), 'installed')

    def test_backup_leaves_a_stopped_installation_stopped(self):
        self.seed()
        tofi_host.backup_now()
        self.start_services.assert_not_called()

    def test_failed_backup_still_restarts_the_services(self):
        self.seed()
        running = [{'State': {'Running': True}, 'Config': {'Labels': {}}}]
        with mock.patch.object(tofi_host, 'project_containers', return_value=running), \
                mock.patch.object(tofi_host, 'backup_data', side_effect=HostError('boom')):
            with self.assertRaisesRegex(HostError, 'boom'):
                tofi_host.backup_now()
        self.start_services.assert_called_once()

    def test_backup_needs_room(self):
        self.seed()
        with mock.patch.object(tofi_host, 'free_bytes', return_value=10):
            with self.assertRaisesRegex(HostError, 'Not enough free disk space'):
                tofi_host.backup_now()
        self.stopped.assert_not_called()


class RestoreCommandTests(BackupCase):
    def local_manifest(self, version=OLD, digit='1'):
        return mock.patch.object(tofi_host, 'restore_manifest', return_value=base.manifest(version, digit))

    def test_round_trip_restores_data_and_switches_to_the_backups_version(self):
        self.seed()
        add_custom_env(self)
        meta = self.backup('manual')
        original = self.data_snapshot()
        tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        (self.P.data / 'tofi.db').write_bytes(b'data written on the new version')
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], NEW)
        with self.local_manifest():
            result = tofi_host.restore_backup(meta['id'], yes=True)
        self.assertEqual(result['version'], OLD)
        self.assertEqual(self.data_snapshot(), original)
        env = tofi_host.read_env()
        self.assertEqual((env['TOFI_VERSION'], env['CUSTOM_X']), (OLD, '1'))
        self.assertEqual(tofi_host.current_release(), OLD)
        self.assertEqual(self.phase(), 'installed')
        # The current state was backed up first (reason pre-restore).
        reasons = [item['meta']['reason'] for item in tofi_host.list_backups()]
        self.assertIn('pre-restore', reasons)
        pre = next(item for item in tofi_host.list_backups() if item['meta']['reason'] == 'pre-restore')
        self.assertEqual(pre['meta']['version'], NEW)
        self.assertEqual(result['pre_restore_backup'], pre['id'])
        # ...and restoring that returns to the newer state.
        with self.local_manifest(NEW, '2'):
            tofi_host.restore_backup(pre['id'], yes=True)
        self.assertEqual((self.P.data / 'tofi.db').read_bytes(), b'data written on the new version')
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], NEW)

    def test_restore_refetches_the_bundle_when_it_was_pruned(self):
        self.seed()
        meta = self.backup()
        fetched = []
        with mock.patch.object(tofi_host, 'fetch_manifest', side_effect=lambda v: (
                fetched.append(v), base.manifest(v, '1'))[1]):
            shutil.rmtree(self.P.releases / OLD)
            self.install_bundle.reset_mock()
            tofi_host.restore_backup(meta['id'], yes=True)
        self.assertEqual(fetched, [OLD])
        self.install_bundle.assert_called_once()

    def test_restore_needs_confirmation(self):
        self.seed()
        meta = self.backup()
        before = self.data_snapshot()
        with self.assertRaisesRegex(HostError, 'add --yes'):
            tofi_host.restore_backup(meta['id'])
        declined = mock.Mock(side_effect=HostError('Restore cancelled; nothing was changed.'))
        with self.assertRaisesRegex(HostError, 'cancelled'):
            tofi_host.restore_backup(meta['id'], confirm=declined)
        self.stopped.assert_not_called()
        self.assertEqual(self.data_snapshot(), before)

    def test_failed_restore_rolls_back_to_the_state_before_it(self):
        self.seed()
        meta = self.backup()
        tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        (self.P.data / 'tofi.db').write_bytes(b'CURRENT')
        current = self.data_snapshot()
        calls = []

        def start(env, **kwargs):
            calls.append(env['TOFI_VERSION'])
            if len(calls) == 1:
                raise HostError('restored version will not start')

        self.start_services.side_effect = start
        with self.local_manifest():
            with self.assertRaisesRegex(HostError, 'restore failed; previous state recovered'):
                tofi_host.restore_backup(meta['id'], yes=True)
        self.assertEqual(calls, [OLD, NEW])
        self.assertEqual(self.data_snapshot(), current)
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], NEW)
        self.assertEqual(self.phase(), 'installed')

    def test_restore_checks_the_archive_before_touching_anything(self):
        self.seed()
        meta = self.backup()
        (self.P.backups / meta['id'] / meta['archives']['etc']).write_bytes(b'garbage')
        with self.assertRaisesRegex(HostError, 'damaged'):
            tofi_host.restore_backup(meta['id'], yes=True)
        self.stopped.assert_not_called()

    def test_restore_needs_room_for_both_copies(self):
        self.seed()
        meta = self.backup()
        with self.local_manifest(), mock.patch.object(tofi_host, 'free_bytes', return_value=1000):
            with self.assertRaisesRegex(HostError, 'Not enough free disk space for the restore'):
                tofi_host.restore_backup(meta['id'], yes=True)
        self.stopped.assert_not_called()


# ---------------------------------------------------------------- A3


class ActiveRunsTests(base.HostCase):
    def setUp(self):
        super().setUp()
        self.P.data.mkdir(parents=True)
        self.db = self.P.data / 'tofi.db'

    def test_counts_queued_running_and_waiting_only(self):
        make_runs_db(self.db, ['queued', 'running', 'waiting', 'completed', 'failed', 'cancelled', 'running'])
        self.assertEqual(tofi_host.active_runs(), 4)

    def test_database_is_opened_read_only_and_never_modified(self):
        make_runs_db(self.db, ['running'])
        before = self.db.read_bytes()
        real = sqlite3.connect
        seen = []
        with mock.patch.object(tofi_host.sqlite3, 'connect', side_effect=lambda *a, **k: (
                seen.append((a, k)), real(*a, **k))[1]):
            self.assertEqual(tofi_host.active_runs(), 1)
        (uri,), kwargs = seen[0]
        self.assertTrue(uri.startswith('file:') and uri.endswith('?mode=ro'), uri)
        self.assertTrue(kwargs.get('uri'))
        self.assertEqual(self.db.read_bytes(), before)
        self.assertEqual(sorted(p.name for p in self.P.data.iterdir()), ['tofi.db'])
        # Even a write attempt through the same URI fails.
        connection = real('file:%s?mode=ro' % self.db, uri=True)
        with self.assertRaises(sqlite3.OperationalError):
            connection.execute("UPDATE runs SET status='x'")
        connection.close()

    def test_paths_with_special_characters_are_quoted(self):
        self.P.data = self.root / 'var/lib/tofi/da ta?x'
        self.P.data.mkdir(parents=True)
        make_runs_db(self.P.data / 'tofi.db', ['waiting'])
        self.assertEqual(tofi_host.active_runs(), 1)

    def test_missing_table_unreadable_file_and_no_file_count_zero(self):
        connection = sqlite3.connect(str(self.db))
        connection.execute('CREATE TABLE other(x)')
        connection.commit()
        connection.close()
        with mock.patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(tofi_host.active_runs(), 0)
        self.assertIn('Could not count active Bot runs', err.getvalue())
        self.db.write_bytes(b'not a database at all' * 100)
        with mock.patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(tofi_host.active_runs(), 0)
        self.assertIn('WARNING', err.getvalue())
        self.db.unlink()
        with mock.patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(tofi_host.active_runs(), 0)
        self.assertEqual(err.getvalue(), '')

    def test_wait_polls_every_15_seconds_until_idle(self):
        with mock.patch.object(tofi_host, 'active_runs', side_effect=[2, 2, 1, 0]), \
                mock.patch.object(tofi_host.time, 'sleep') as sleep:
            self.assertEqual(tofi_host.wait_for_idle('the update', wait_minutes=10), 0)
        self.assertEqual([c.args for c in sleep.call_args_list], [(15,)] * 3)
        self.assertIn('2 Bot runs are active; waiting up to 10 minutes', sys.stdout.getvalue())

    def test_refuses_with_the_count_after_the_wait(self):
        with mock.patch.object(tofi_host, 'active_runs', return_value=3), \
                mock.patch.object(tofi_host.time, 'sleep') as sleep:
            with self.assertRaises(tofi_host.ActiveRunsError) as caught:
                tofi_host.wait_for_idle('the update', wait_minutes=1)
        self.assertEqual(sleep.call_count, 4)
        self.assertEqual(caught.exception.count, 3)
        message = str(caught.exception)
        self.assertIn('3 Bot runs still active', message)
        self.assertIn('--wait MINUTES', message)
        self.assertIn('--force', message)
        with mock.patch.object(tofi_host, 'active_runs', return_value=1):
            with self.assertRaisesRegex(HostError, '1 Bot run still active'):
                tofi_host.wait_for_idle('the update')

    def test_force_proceeds_and_reports_the_count(self):
        with mock.patch.object(tofi_host, 'active_runs', return_value=2), \
                mock.patch('sys.stderr', new_callable=io.StringIO) as err:
            self.assertEqual(tofi_host.wait_for_idle('the update', force=True), 2)
        self.assertIn('--force interrupts them', err.getvalue())

    def test_idle_host_is_not_delayed(self):
        with mock.patch.object(tofi_host, 'active_runs', return_value=0), \
                mock.patch.object(tofi_host.time, 'sleep') as sleep:
            self.assertEqual(tofi_host.wait_for_idle('the update', wait_minutes=10), 0)
        sleep.assert_not_called()


class GuardedCommandTests(base.ComputerCase):
    def busy(self, count=2):
        self.active_runs.return_value = count

    def test_update_refuses_while_runs_are_active_and_changes_nothing(self):
        self.installed()
        self.busy()
        env_bytes, state_bytes = self.P.env_file.read_bytes(), self.P.state_file.read_bytes()
        code, out = self.main('update', '--yes', '--manifest', str(self.write_manifest(base.manifest())))
        self.assertEqual(code, 1)
        self.assertIn('2 Bot runs still active', self.stderr)
        self.pull.assert_not_called()
        self.stopped.assert_not_called()
        self.assertEqual((self.P.env_file.read_bytes(), self.P.state_file.read_bytes()), (env_bytes, state_bytes))
        history = [json.loads(line) for line in self.P.update_history.read_text().splitlines()]
        self.assertEqual((history[0]['result'], history[0]['reason'], history[0]['trigger']),
                         ('refused', 'active-runs', 'manual'))

    def test_update_force_goes_ahead(self):
        self.installed()
        self.busy()
        code, out = self.main('update', '--yes', '--force', '--manifest', str(self.write_manifest(base.manifest())))
        self.assertEqual(code, 0, self.stderr)
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], NEW)

    def test_update_waits_when_asked(self):
        self.installed()
        self.active_runs.side_effect = [2, 1, 0, 0, 0]
        code, out = self.main('update', '--yes', '--wait', '1', '--manifest',
                              str(self.write_manifest(base.manifest())))
        self.assertEqual(code, 0, self.stderr)
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], NEW)

    def test_a_run_that_starts_during_the_downloads_stops_the_update_before_the_stop(self):
        self.installed()
        self.active_runs.side_effect = [0, 3]
        code, _ = self.main('update', '--yes', '--manifest', str(self.write_manifest(base.manifest())))
        self.assertEqual(code, 1)
        self.stopped.assert_not_called()
        self.assertEqual(self.phase(), 'installed')

    def test_backup_restore_and_stop_honor_the_guard(self):
        self.installed()
        meta = tofi_host.backup_data('manual', tofi_host.read_env())
        self.busy(1)
        for argv in (['backup'], ['restore', meta['id'], '--yes'], ['stop']):
            with self.subTest(argv=argv):
                code, _ = self.main(*argv)
                self.assertEqual(code, 1)
                self.assertIn('1 Bot run still active', self.stderr)
        self.stopped.assert_not_called()
        code, _ = self.main('stop', '--force')
        self.assertEqual(code, 0)
        self.stopped.assert_called_once()

    def test_systemd_unit_stops_with_force_so_shutdown_is_never_refused(self):
        unit = (base.HERE / 'tofi.service').read_text()
        self.assertIn('ExecStop=/usr/local/bin/tofi stop --force', unit)

    def test_wait_defaults(self):
        args = tofi_host.parse_args(['update', '--yes'])
        with mock.patch.object(tofi_host.sys.stdin, 'isatty', return_value=True):
            self.assertEqual(tofi_host.guard_options(args, True), (10, False))
            self.assertEqual(tofi_host.guard_options(tofi_host.parse_args(['update', '--wait', '3', '--force']), True),
                             (3, True))
        with mock.patch.object(tofi_host.sys.stdin, 'isatty', return_value=False):
            self.assertEqual(tofi_host.guard_options(args, True), (0, False))
        with self.assertRaises(HostError):
            tofi_host.guard_options(tofi_host.parse_args(['update', '--wait', '-1']), True)


# ---------------------------------------------------------------- A4


class CliPolishTests(base.ComputerCase):
    def test_update_yes_flag_parses_and_skips_the_prompt(self):
        self.assertTrue(tofi_host.parse_args(['update', '--yes']).yes)
        self.assertTrue(tofi_host.parse_args(['update', '-y']).yes)
        self.installed()
        with mock.patch.object(tofi_host.sys.stdin, 'isatty', return_value=True), \
                mock.patch('builtins.input', side_effect=AssertionError('must not ask')):
            code, _ = self.main('update', '--yes', '--manifest', str(self.write_manifest(base.manifest())))
        self.assertEqual(code, 0, self.stderr)

    def test_interactive_update_asks_first_and_can_be_declined(self):
        self.installed()
        with mock.patch.object(tofi_host.sys.stdin, 'isatty', return_value=True), \
                mock.patch('builtins.input', return_value='n'):
            code, out = self.main('update', '--manifest', str(self.write_manifest(base.manifest())))
        self.assertEqual(code, 1)
        self.assertIn('Update cancelled; nothing was changed.', self.stderr)
        self.assertIn('a backup of data and settings is taken first', out)
        self.pull.assert_not_called()
        with mock.patch.object(tofi_host.sys.stdin, 'isatty', return_value=True), \
                mock.patch('builtins.input', return_value=''):
            code, out = self.main('update', '--manifest', str(self.write_manifest(base.manifest())))
        self.assertEqual(code, 0, self.stderr)

    def test_update_check_prints_the_release_notes_url(self):
        self.installed()
        notes = 'https://github.com/JackZhao98/tofibot/releases/tag/' + NEW
        self.fetch.return_value = tofi_host.validate_manifest(dict(base.manifest(), notes_url=notes))
        code, out = self.main('update', '--check')
        self.assertEqual(code, tofi_host.UPDATE_AVAILABLE_EXIT)
        self.assertIn('Notes        ' + notes, out)
        code, out = self.main('update', '--check', '--json')
        self.assertEqual(json.loads(out)['notes_url'], notes)
        # Older manifests have none.
        self.fetch.return_value = tofi_host.validate_manifest(base.manifest())
        code, out = self.main('update', '--check')
        self.assertNotIn('Notes', out)
        self.assertIsNone(json.loads(self.main('update', '--check', '--json')[1])['notes_url'])

    def render(self, **extra):
        return tofi_host.render_status(base.status_result(**extra), mode=None, access={})

    def test_header_says_starting_until_the_app_is_up(self):
        starting = self.render(healthy=False, services={'app': 'starting', 'worker': 'running'})
        self.assertTrue(starting.startswith('tofi v0.1.0-rc.12 · starting\n'), starting)
        also_up = self.render(healthy=True, services={'app': 'starting', 'worker': 'running'})
        self.assertIn('· starting', also_up.splitlines()[0])
        healthy = self.render(healthy=True)
        self.assertIn('· healthy', healthy.splitlines()[0])
        down = self.render(healthy=False, services={'app': 'unhealthy', 'worker': 'running'})
        self.assertIn('· NOT healthy', down.splitlines()[0])
        self.assertIn('· NOT healthy', self.render(healthy=False, services='docker unavailable').splitlines()[0])

    def test_status_reports_the_update_mode(self):
        self.installed()
        with mock.patch.object(tofi_host, 'project_containers', return_value=[]), \
                mock.patch.object(tofi_host, 'health', return_value=True):
            self.assertFalse(tofi_host.status()['auto_update'])
            self.assertIn('  Updates      manual\n', self.render(auto_update=False))
            values = tofi_host.read_env()
            values['TOFI_AUTO_UPDATE'] = 'patch'
            tofi_host.write_env(values)
            with mock.patch.object(tofi_host, 'auto_update_next_check', return_value='04:12'):
                result = tofi_host.status(details=True)
        self.assertEqual((result['auto_update'], result['next_check']), (True, '04:12'))
        text = self.render(auto_update=True, next_check='04:12')
        self.assertIn('  Updates      automatic (fix releases) · next check 04:12\n', text)
        self.assertIn('  Updates      automatic (fix releases)\n', self.render(auto_update=True, next_check=None))

    def test_next_check_is_read_from_the_timer(self):
        def run(args, **kwargs):
            return base.subprocess.CompletedProcess(args, 0, 'Sat 2026-10-10 04:12:33 PDT\n', '')

        with mock.patch.object(tofi_host, 'run', side_effect=run):
            self.assertEqual(tofi_host.auto_update_next_check(), '04:12')
        with mock.patch.object(tofi_host, 'run', side_effect=lambda *a, **k: base.completed('')):
            self.assertIsNone(tofi_host.auto_update_next_check())

    def test_backup_commands(self):
        self.installed()
        code, out = self.main('backups')
        self.assertEqual(code, 0)
        self.assertIn('No backups yet', out)
        code, out = self.main('backup', '--note', 'before the holiday')
        self.assertEqual(code, 0, self.stderr)
        self.assertIn('Backup id: ', out)
        code, out = self.main('backups')
        self.assertIn('ID', out.splitlines()[0])
        self.assertIn('manual', out)
        code, out = self.main('backups', '--json')
        rows = json.loads(out)
        self.assertEqual((len(rows), rows[0]['note'], rows[0]['reason']), (1, 'before the holiday', 'manual'))
        self.assertEqual(self.main('restore', 'nonesuch', '--yes')[0], 1)
        self.assertIn('No backup matches', self.stderr)


# ---------------------------------------------------------------- A5


class AutoUpdateGatingTests(unittest.TestCase):
    def test_version_gating_table(self):
        allowed = tofi_host.auto_update_allowed
        table = [
            ('v0.1.0', 'v0.1.1', True),
            ('v0.1.0', 'v0.1.7', True),
            ('v0.1.0-rc.3', 'v0.1.1', True),
            ('v0.1.4', 'v0.1.4', False),
            ('v0.1.4', 'v0.1.3', False),
            ('v0.1.0', 'v0.1.1-rc.1', False),
            ('v0.1.0', 'v0.2.0', False),
            ('v0.1.9', 'v0.2.0', False),
            ('v0.1.0', 'v1.1.1', False),
            ('v1.4.2', 'v1.4.3', True),
            ('v1.4.2', 'v1.5.0', False),
            ('v1.4.2', 'v2.4.3', False),
            ('v0.1.0-rc.1', 'v0.1.0', False),
            ('v0.1.0-rc.1', 'v0.1.0-rc.2', False),
            ('v0.1.0', 'garbage', False),
            ('garbage', 'v0.1.1', False),
            (None, 'v0.1.1', False),
        ]
        for installed, candidate, expected in table:
            with self.subTest(installed=installed, candidate=candidate):
                self.assertIs(allowed(installed, candidate), expected)


class AutoUpdateTests(base.ComputerCase):
    def setUp(self):
        super().setUp()
        self.installed()
        self.set_mode('patch')
        self.next = base.manifest('v0.1.1', '2')
        self.fetch.return_value = tofi_host.validate_manifest(self.next)

    def set_mode(self, mode):
        values = tofi_host.read_env()
        values['TOFI_AUTO_UPDATE'] = mode
        tofi_host.write_env(values)
        self.commands.clear()

    def history(self):
        if not self.P.update_history.exists():
            return []
        return [json.loads(line) for line in self.P.update_history.read_text().splitlines()]

    def test_off_or_missing_does_nothing_at_all(self):
        for mode in ('off', ''):
            with self.subTest(mode=mode):
                self.set_mode(mode)
                self.fetch.reset_mock()
                code, out = self.main('update', '--auto')
                self.assertEqual(code, 0)
                self.assertIn('off; nothing to do', out)
                self.fetch.assert_not_called()
                self.pull.assert_not_called()
        self.assertEqual(self.history(), [])
        values = tofi_host.read_env()
        del values['TOFI_AUTO_UPDATE']
        tofi_host.write_env(values)
        self.assertEqual(tofi_host.auto_update()['result'], 'off')

    def test_installs_a_newer_patch_and_records_history(self):
        code, out = self.main('update', '--auto')
        self.assertEqual(code, 0, self.stderr)
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], 'v0.1.1')
        self.assertIn('tofi auto-update: updated v0.1.0 -> v0.1.1.', out)
        entry = self.history()[-1]
        self.assertEqual((entry['trigger'], entry['result'], entry['from'], entry['to']),
                         ('auto', 'updated', OLD, 'v0.1.1'))
        self.assertTrue(entry['at'].endswith('Z'))
        self.assertEqual(self.P.update_history.stat().st_mode & 0o777, 0o600)
        # A backup was taken first.
        self.assertEqual(tofi_host.list_backups()[0]['meta']['reason'], 'pre-update')

    def test_never_installs_rc_minor_major_or_downgrades(self):
        for version in ('v0.1.1-rc.1', 'v0.2.0', 'v1.0.0', 'v0.1.0'):
            with self.subTest(version=version):
                self.fetch.return_value = tofi_host.validate_manifest(base.manifest(version, '2'))
                self.pull.reset_mock()
                code, out = self.main('update', '--auto')
                self.assertEqual(code, 0)
                self.pull.assert_not_called()
                self.stopped.assert_not_called()
                self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
        results = {entry['to']: entry['result'] for entry in self.history()}
        self.assertEqual(results, {'v0.1.1-rc.1': 'not-eligible', 'v0.2.0': 'not-eligible',
                                   'v1.0.0': 'not-eligible', 'v0.1.0': 'up-to-date'})

    def test_busy_host_is_skipped_without_waiting(self):
        self.active_runs.return_value = 2
        with mock.patch.object(tofi_host.time, 'sleep') as sleep:
            code, out = self.main('update', '--auto')
        self.assertEqual(code, 0)
        sleep.assert_not_called()
        self.assertIn('2 Bot runs active; trying again tomorrow', out)
        self.stopped.assert_not_called()
        self.pull.assert_not_called()
        entry = self.history()[-1]
        self.assertEqual((entry['result'], entry['reason'], entry['trigger']), ('skipped', 'active-runs', 'auto'))
        self.assertEqual(len(self.history()), 1)
        self.assertEqual(self.phase(), 'installed')

    def test_a_rejected_version_is_not_retried_every_night(self):
        self.start_services.side_effect = [HostError('candidate unhealthy'), None]
        code, out = self.main('update', '--auto')
        self.assertEqual(code, 1)
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
        self.assertEqual(self.history()[-1]['result'], 'rolled-back')
        self.start_services.side_effect = None
        self.stopped.reset_mock()
        code, out = self.main('update', '--auto')
        self.assertEqual(code, 0)
        self.stopped.assert_not_called()
        self.assertEqual(self.history()[-1]['reason'], 'rejected-before')
        # A newer fix is tried again.
        self.fetch.return_value = tofi_host.validate_manifest(base.manifest('v0.1.2', '3'))
        code, out = self.main('update', '--auto')
        self.assertEqual((code, tofi_host.read_env()['TOFI_VERSION']), (0, 'v0.1.2'))

    def test_offline_check_is_not_a_failure(self):
        self.fetch.side_effect = HostError('Cannot download the release manifest (timed out).')
        code, out = self.main('update', '--auto')
        self.assertEqual(code, 0)
        self.assertEqual(self.history()[-1]['result'], 'check-failed')
        self.stopped.assert_not_called()

    def test_another_tofi_operation_running_skips_quietly(self):
        with mock.patch.object(tofi_host, 'lifecycle_lock', side_effect=tofi_host.LockBusy('Another tofi operation')):
            code, out = self.main('update', '--auto')
        self.assertEqual(code, 0)
        self.assertEqual(self.history()[-1]['reason'], 'busy')

    def test_does_not_run_mid_transaction_and_refuses_odd_flag_mixes(self):
        state = json.loads(self.P.state_file.read_text())
        state['phase'] = 'upgrade-failed'
        self.P.state_file.write_text(json.dumps(state))
        code, out = self.main('update', '--auto')
        self.assertEqual(code, 0)
        self.assertEqual(self.history()[-1]['reason'], 'phase')
        self.assertEqual(self.main('update', '--auto', '--check')[0], 1)

    def test_a_schema_change_is_never_applied_automatically(self):
        changed = base.manifest('v0.1.1', '2')
        changed['data_schema'] = 'tofi-account-data-v2'
        self.fetch.return_value = tofi_host.validate_manifest(changed)
        self.label.side_effect = lambda ref, label: ('tofi-account-data-v1' if ref.endswith('1' * 64)
                                                     else 'tofi-account-data-v2')
        code, out = self.main('update', '--auto')
        self.assertEqual(code, 1)
        self.stopped.assert_not_called()
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)


class AutoUpdateUnitTests(base.LifecycleBase):
    def test_units_are_written_by_the_installer_config(self):
        self.installed()
        service, timer = self.P.auto_service.read_text(), self.P.auto_timer.read_text()
        self.assertIn('Type=oneshot', service)
        self.assertIn('ExecStart=/usr/local/bin/tofi update --auto', service)
        self.assertIn('OnCalendar=*-*-* 04:00:00', timer)
        self.assertIn('RandomizedDelaySec=45min', timer)
        self.assertIn('Persistent=true', timer)
        self.assertIn('WantedBy=timers.target', timer)
        self.assertEqual(self.P.auto_timer.name, 'tofi-auto-update.timer')
        self.assertEqual(self.P.auto_service.name, 'tofi-auto-update.service')

    def test_config_on_and_off_toggle_the_env_key_and_the_timer_keeping_other_keys(self):
        self.installed()
        add_custom_env(self)
        result = tofi_host.configure_auto_update('on')
        self.assertEqual(result, {'auto_update': 'patch'})
        env = tofi_host.read_env()
        self.assertEqual((env['TOFI_AUTO_UPDATE'], env['CUSTOM_X']), ('patch', '1'))
        self.assertIn(['systemctl', 'enable', '--now', 'tofi-auto-update.timer'], self.commands)
        self.commands.clear()
        tofi_host.configure_auto_update('off')
        self.assertEqual(tofi_host.read_env()['TOFI_AUTO_UPDATE'], 'off')
        self.assertIn(['systemctl', 'disable', '--now', 'tofi-auto-update.timer'], self.commands)
        with self.assertRaises(HostError):
            tofi_host.configure_auto_update('maybe')

    def test_config_cli(self):
        self.installed()
        self.assertEqual(tofi_host.main(['config', 'auto-update', 'on']), 0)
        self.assertEqual(tofi_host.read_env()['TOFI_AUTO_UPDATE'], 'patch')
        self.assertEqual(tofi_host.main(['config', 'auto-update', 'off']), 0)

    def test_update_keeps_the_choice(self):
        self.installed()
        tofi_host.configure_auto_update('on')
        tofi_host.upgrade(manifest_path=str(self.write_manifest(base.manifest())))
        self.assertEqual(tofi_host.read_env()['TOFI_AUTO_UPDATE'], 'patch')

    def test_systemd_start_enables_the_timer_only_for_patch(self):
        self.installed()
        self.commands.clear()
        tofi_host.systemd_start()
        self.assertIn(['systemctl', 'disable', '--now', 'tofi-auto-update.timer'], self.commands)
        values = tofi_host.read_env()
        values['TOFI_AUTO_UPDATE'] = 'patch'
        tofi_host.write_env(values)
        self.commands.clear()
        tofi_host.systemd_start()
        self.assertIn(['systemctl', 'enable', '--now', 'tofi-auto-update.timer'], self.commands)

    def test_uninstall_removes_the_units_and_purge_too(self):
        self.installed()
        tofi_host.uninstall()
        self.assertFalse(self.P.auto_service.exists() or self.P.auto_timer.exists())
        self.assertIn(['systemctl', 'disable', '--now', 'tofi-auto-update.timer'], self.commands)
        # Re-installing over the retained data brings them back.
        tofi_host.apply_host_config(self.P.releases / OLD, tofi_host.read_env())
        self.assertTrue(self.P.auto_timer.exists())
        tofi_host.purge_everything(tofi_host.load_state(), tofi_host.socket.gethostname())
        self.assertFalse(self.P.auto_service.exists() or self.P.auto_timer.exists())

    def test_install_default_follows_yes_and_flags(self):
        def options(*argv):
            return tofi_host.install_options(tofi_host.parse_args(['install'] + list(argv)))['auto_update']

        self.assertEqual(options(), 'off')
        self.assertEqual(options('--yes'), 'patch')
        self.assertEqual(options('--yes', '--no-auto-update'), 'off')
        self.assertEqual(options('--auto-update'), 'patch')
        with self.assertRaises(SystemExit), mock.patch('sys.stderr', new_callable=io.StringIO):
            options('--auto-update', '--no-auto-update')

    def test_fresh_install_records_the_choice_in_the_env(self):
        manifest = base.manifest(OLD, '1')
        for choice, expected in (('patch', 'patch'), (None, 'off'), ('bogus', 'off')):
            env = tofi_host.render_env(manifest, {'port': 8321, 'bind': '0.0.0.0', 'auto_update': choice},
                                       {'cpu': 3, 'memory_mib': 6144})
            self.assertEqual(env['TOFI_AUTO_UPDATE'], expected)


class ComposePassthroughTests(unittest.TestCase):
    def test_compose_passes_auto_update_to_the_app(self):
        text = (base.HERE / 'compose.yaml').read_text()
        self.assertIn('TOFI_AUTO_UPDATE: ${TOFI_AUTO_UPDATE:-off}', text)


if __name__ == '__main__':
    unittest.main()
