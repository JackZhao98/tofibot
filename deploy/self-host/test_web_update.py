"""Synthetic checks for `tofi update --from-request` and its units (decision D69).

Fake root, no Docker, no network: fetch_manifest is a mock. These do not replace a
run on a real host (systemd path unit, App container mount).
"""
import json
import os
import stat
from pathlib import Path
import sys
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_tofi_host as base  # noqa: E402
import tofi_host  # noqa: E402

OLD, NEW = base.OLD, base.NEW  # v0.1.0 -> a newer stable
RC = 'v0.3.0-rc.1'


def stamp(offset=0):
    return time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(time.time() + offset))


def request(version='v0.1.1', channel='stable', nonce='a' * 32, requested_at=None, **extra):
    value = {'version': version, 'channel': channel, 'requested_at': requested_at or stamp(), 'nonce': nonce}
    value.update(extra)
    return value


class WebUpdateCase(base.LifecycleBase):
    def setUp(self):
        super().setUp()
        self.installed()
        self.free = mock.patch.object(tofi_host, 'free_bytes', return_value=50 * tofi_host.GIB).start()
        self.fetch = mock.patch.object(tofi_host, 'fetch_manifest', side_effect=self.fake_fetch).start()
        self.published = {'v0.1.1': base.manifest('v0.1.1', '2'), RC: base.manifest(RC, '3')}
        self.latest = 'v0.1.1'
        mock.patch.object(tofi_host, 'UPDATE_REQUEST_COOLDOWN', 0).start()  # tests below turn it on
        self.states = []
        real = tofi_host.write_update_status

        def record(state, **fields):
            self.states.append(state)
            return real(state, **fields)

        mock.patch.object(tofi_host, 'write_update_status', side_effect=record).start()

    def fake_fetch(self, version=None, timeout=60):
        wanted = version or self.latest
        if wanted not in self.published:
            raise tofi_host.HostError('Cannot download the release manifest (HTTP Error 404)')
        return tofi_host.validate_manifest(self.published[wanted])

    def drop(self, value, raw=None):
        tofi_host.prepare_update_directories()
        path = self.P.update_request_dir / 'request.json'
        path.write_bytes(raw if raw is not None else json.dumps(value).encode())
        return path

    def status(self):
        return json.loads((self.P.update_status_dir / 'status.json').read_text())

    def run_request(self):
        return tofi_host.update_from_request()

    def history(self):
        if not self.P.update_history.exists():
            return []
        return [json.loads(line) for line in self.P.update_history.read_text().splitlines()]


class RequestValidationTests(WebUpdateCase):
    def refused(self, code, **kwargs):
        result = self.run_request()
        self.assertEqual(result, {'result': 'refused', 'code': code}, self.status())
        self.assertEqual(self.status()['state'], 'failed')
        self.assertEqual(self.status()['code'], code)
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
        self.pull.assert_not_called()
        self.assertFalse((self.P.update_request_dir / 'request.json').exists(), 'the request is always consumed')

    def test_no_request_does_nothing(self):
        tofi_host.prepare_update_directories()
        self.assertEqual(self.run_request(), {'result': 'none'})
        self.assertFalse((self.P.update_status_dir / 'status.json').exists())

    def test_bad_json(self):
        self.drop(None, raw=b'{not json')
        self.refused('request_invalid')
        self.drop(None, raw=b'\xff\xfe')
        self.refused('request_invalid')
        self.drop(None, raw=b'[]')
        self.refused('request_invalid')

    def test_oversize(self):
        self.drop(None, raw=b'{"pad":"' + b'x' * 5000 + b'"}')
        self.refused('request_too_large')

    def test_symlink_and_non_regular_file_are_not_followed(self):
        target = self.root / 'secret.json'
        target.write_text(json.dumps(request()))
        tofi_host.prepare_update_directories()
        os.symlink(target, self.P.update_request_dir / 'request.json')
        self.refused('request_invalid')
        self.assertTrue(target.exists())
        (self.P.update_request_dir / 'request.json').mkdir()
        with self.assertRaises(tofi_host.RequestRefused):
            tofi_host.take_update_request()

    def test_directory_request_is_set_aside_not_walked(self):
        # The App owns the request directory; root must not chmod or delete a tree it built.
        tofi_host.prepare_update_directories()
        outside = self.root / 'outside'
        outside.mkdir()
        os.chmod(outside, 0o751)
        planted = self.P.update_request_dir / 'request.json'
        planted.mkdir()
        os.symlink(outside, planted / 'link')
        (planted / 'inner').mkdir()
        os.chmod(planted / 'inner', 0o500)
        with self.assertRaises(tofi_host.RequestRefused):
            tofi_host.take_update_request()
        self.assertFalse(planted.exists())
        self.assertEqual(stat.S_IMODE(os.stat(outside).st_mode), 0o751)
        aside = [p for p in self.P.update_request_dir.iterdir() if p.name.startswith('.rejected-')]
        self.assertEqual(len(aside), 1)
        self.assertEqual(stat.S_IMODE(os.stat(aside[0] / 'inner').st_mode), 0o500)
        self.assertIsNone(tofi_host.take_update_request())

    def test_schema_is_exact(self):
        for bad in (request(extra='x'), {k: v for k, v in request().items() if k != 'nonce'},
                    request(version='v0.1.1\n'), request(version='latest'), request(version='v0.1.1-beta.1'),
                    request(channel='beta'), request(nonce='A' * 32), request(nonce='a' * 31),
                    request(requested_at='yesterday'), dict(request(), nonce=5)):
            with self.subTest(bad=bad):
                self.drop(bad)
                self.refused('request_invalid')

    def test_channel_must_match_version(self):
        self.drop(request(version=RC, channel='stable'))
        self.refused('channel_mismatch')
        self.drop(request(version='v0.1.1', channel='rc'))
        self.refused('channel_mismatch')

    def test_stale_or_future_request(self):
        self.drop(request(requested_at=stamp(-7200)))
        self.refused('request_stale')
        self.drop(request(requested_at=stamp(3600)))
        self.refused('request_stale')

    def test_downgrade_and_same_version(self):
        self.published['v0.0.9'] = base.manifest('v0.0.9', '2')
        self.drop(request(version='v0.0.9'))
        self.refused('not_newer')
        self.drop(request(version=OLD, nonce='b' * 32))
        self.refused('not_newer')
        self.fetch.assert_not_called()

    def test_unpublished_version(self):
        self.drop(request(version='v0.3.0-rc.9', channel='rc'))
        self.refused('version_unpublished')

    def test_stable_must_be_the_latest_stable(self):
        self.published['v0.1.2'] = base.manifest('v0.1.2', '4')
        self.latest = 'v0.1.2'
        self.drop(request(version='v0.1.1'))
        self.refused('not_latest_stable')

    def test_manifest_naming_another_version_is_refused(self):
        self.published[RC] = base.manifest('v0.3.0-rc.2', '3')
        self.drop(request(version=RC, channel='rc'))
        self.refused('version_unpublished')

    def test_release_rejected_before_is_not_retried(self):
        state = tofi_host.load_state()
        state['last_update_failure'] = {'version': 'v0.1.1', 'message': 'x', 'at': stamp()}
        tofi_host.save_state(state)
        self.drop(request())
        self.refused('rejected_before')

    def test_every_rolled_back_version_stays_rejected(self):
        state = tofi_host.load_state()
        tofi_host.remember_rejected(state, 'v0.1.1')
        tofi_host.remember_rejected(state, 'v0.1.2')
        tofi_host.save_state(state)
        self.published['v0.1.2'] = base.manifest('v0.1.2', '4')
        self.latest = 'v0.1.2'
        self.drop(request(version='v0.1.2'))
        self.refused('rejected_before')
        self.drop(request(version='v0.1.1', nonce='b' * 32))
        self.refused('rejected_before')

    def test_failed_update_lands_on_the_list_and_old_field_is_read(self):
        self.start_services.side_effect = [tofi_host.HostError('candidate unhealthy'), None, None]
        self.drop(request())
        self.assertEqual(self.run_request()['code'], 'update_rejected')
        state = tofi_host.load_state()
        self.assertEqual(state['rejected_versions'], ['v0.1.1'])
        self.assertEqual(tofi_host.rejected_versions({'last_update_failure': {'version': 'v0.1.7'},
                                                      'rejected_versions': ['v0.1.1']}), ['v0.1.1', 'v0.1.7'])

    def test_rejected_list_keeps_the_last_twenty(self):
        state = {}
        for number in range(30):
            tofi_host.remember_rejected(state, 'v0.1.%d' % number)
        self.assertEqual(len(state['rejected_versions']), 20)
        self.assertEqual(state['rejected_versions'][-1], 'v0.1.29')

    def test_ascii_digits_only(self):
        arabic = '\u0661'  # ARABIC-INDIC DIGIT ONE
        self.drop(request(version='v0.1.%s' % arabic))
        self.refused('request_invalid')
        self.drop(request(requested_at=stamp().replace('2', '\u0662'), nonce='b' * 32))
        self.refused('request_invalid')
        self.assertIsNone(tofi_host.version_key('v0.1.%s' % arabic))
        self.assertIsNone(tofi_host.version_parts('v0.1.%s' % arabic))
        with self.assertRaises(tofi_host.HostError):
            tofi_host.validate_manifest(dict(base.manifest('v0.1.%s' % arabic, '2')))

    def test_rc_must_be_newer_than_the_latest_stable(self):
        self.published['v0.3.0'] = base.manifest('v0.3.0', '5')
        self.latest = 'v0.3.0'
        self.drop(request(version=RC, channel='rc'))
        self.refused('rc_superseded')
        self.published['v0.3.0-rc.2'] = base.manifest('v0.3.0-rc.2', '6')
        self.drop(request(version='v0.3.0-rc.2', channel='rc', nonce='b' * 32))
        self.refused('rc_superseded')

    def test_rc_check_needs_the_stable_manifest(self):
        del self.published['v0.1.1']
        self.drop(request(version=RC, channel='rc'))
        self.refused('release_check_failed')

    def test_duplicate_nonce_is_ignored_without_touching_status(self):
        self.drop(request())
        self.assertEqual(self.run_request()['result'], 'done')
        before = self.status()
        self.states.clear()
        self.drop(request())
        self.assertEqual(self.run_request(), {'result': 'duplicate'})
        self.assertEqual(self.status(), before)
        self.assertEqual(self.states, [])
        self.assertFalse((self.P.update_request_dir / 'request.json').exists())

    def test_refusals_are_in_the_update_history(self):
        self.drop(request(version=OLD))
        self.run_request()
        entry = self.history()[-1]
        self.assertEqual((entry['trigger'], entry['result'], entry['reason']), ('web', 'refused', 'not_newer'))


class CooldownTests(WebUpdateCase):
    def setUp(self):
        super().setUp()
        mock.patch.object(tofi_host, 'UPDATE_REQUEST_COOLDOWN', 600).start()

    def test_second_request_within_ten_minutes_is_refused_whatever_the_first_outcome(self):
        self.drop(request(version=OLD))  # refused: not_newer
        self.assertEqual(self.run_request(), {'result': 'refused', 'code': 'not_newer'})
        self.drop(request(nonce='b' * 32))
        self.assertEqual(self.run_request(), {'result': 'refused', 'code': 'request_cooldown'})
        self.assertEqual(self.status()['code'], 'request_cooldown')
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
        self.assertEqual(self.history()[-1]['reason'], 'request_cooldown')

    def test_failures_count_too(self):
        self.free.return_value = 10 * 1024 * 1024
        self.drop(request())
        self.assertEqual(self.run_request()['code'], 'insufficient_disk')
        self.free.return_value = 50 * tofi_host.GIB
        self.drop(request(nonce='b' * 32))
        self.assertEqual(self.run_request()['code'], 'request_cooldown')

    def test_cooldown_ends_and_a_refused_request_does_not_extend_it(self):
        self.drop(request())
        self.assertEqual(self.run_request()['result'], 'done')
        recorded = json.loads(self.P.update_last_attempt.read_text())['at']
        self.published['v0.1.2'] = base.manifest('v0.1.2', '4')
        self.latest = 'v0.1.2'
        self.drop(request(version='v0.1.2', nonce='b' * 32))
        self.assertEqual(self.run_request()['code'], 'request_cooldown')
        self.assertEqual(json.loads(self.P.update_last_attempt.read_text())['at'], recorded)
        self.P.update_last_attempt.write_text(json.dumps({'at': time.time() - 601}))
        self.drop(request(version='v0.1.2', nonce='c' * 32))
        self.assertEqual(self.run_request()['result'], 'done')

    def test_a_duplicate_does_not_start_a_cooldown(self):
        self.drop(request(version=OLD))
        self.run_request()
        self.P.update_last_attempt.unlink()
        self.drop(request(version=OLD))
        self.assertEqual(self.run_request(), {'result': 'duplicate'})
        self.assertFalse(self.P.update_last_attempt.exists())


class RequestFlowTests(WebUpdateCase):
    def test_stable_update_runs_through_the_existing_path(self):
        self.drop(request())
        result = self.run_request()
        self.assertEqual(result, {'result': 'done', 'version': 'v0.1.1'})
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], 'v0.1.1')
        self.assertEqual(self.states, ['queued', 'downloading', 'verifying', 'switching', 'done'])
        status = self.status()
        self.assertEqual((status['state'], status['version'], status['from'], status['progress'], status['code']),
                         ('done', 'v0.1.1', OLD, 100, None))
        self.assertEqual(status['nonce'], 'a' * 32)
        self.assertEqual(oct((self.P.update_status_dir / 'status.json').stat().st_mode & 0o777), '0o644')
        entry = self.history()[-1]
        self.assertEqual((entry['trigger'], entry['result'], entry['to']), ('web', 'updated', 'v0.1.1'))
        self.assertTrue(list(self.P.backups.iterdir()), 'a backup is taken first, as for tofi update')

    def test_rc_update(self):
        self.drop(request(version=RC, channel='rc'))
        self.assertEqual(self.run_request()['result'], 'done')
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], RC)
        self.assertEqual(self.fetch.call_args_list, [mock.call(RC), mock.call()])

    def test_waits_for_runs_then_updates(self):
        counts = iter([2, 1, 0, 0, 0])
        self.active_runs.side_effect = lambda: next(counts)
        self.drop(request())
        self.assertEqual(self.run_request()['result'], 'done')
        self.assertEqual(self.states[:3], ['queued', 'waiting_for_runs', 'downloading'])

    def test_runs_that_never_finish_leave_everything_unchanged(self):
        self.active_runs.return_value = 1
        self.drop(request())
        result = self.run_request()
        self.assertEqual(result, {'result': 'failed', 'code': 'runs_still_active'})
        self.assertEqual(self.status()['state'], 'failed')
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
        self.stopped.assert_not_called()
        self.pull.assert_not_called()
        self.assertEqual(self.history()[-1]['result'], 'refused')

    def test_candidate_that_fails_is_rolled_back(self):
        calls = []

        def start(env, **kwargs):
            calls.append(env['TOFI_VERSION'])
            if len(calls) == 1:
                raise tofi_host.HostError('candidate unhealthy')

        self.start_services.side_effect = start
        self.drop(request())
        result = self.run_request()
        self.assertEqual(result, {'result': 'failed', 'code': 'update_rejected'})
        status = self.status()
        self.assertEqual((status['state'], status['rolled_back_to'], status['code']), ('rolled_back', OLD, 'update_rejected'))
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)
        self.assertEqual(self.history()[-1]['result'], 'rolled-back')

    def test_schema_change_is_never_allowed_from_the_web(self):
        self.label.side_effect = lambda ref, key: 'tofi-account-data-v1' if ref == tofi_host.read_env()['TOFI_APP_IMAGE'] else 'tofi-account-data-v2'
        self.published['v0.1.1']['data_schema'] = 'tofi-account-data-v2'
        self.drop(request())
        result = self.run_request()
        self.assertEqual(result['code'], 'schema_change_blocked')
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)

    def test_low_disk_is_a_named_failure_before_anything_changes(self):
        self.free.return_value = 10 * 1024 * 1024
        self.drop(request())
        self.assertEqual(self.run_request(), {'result': 'failed', 'code': 'insufficient_disk'})
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], OLD)

    def test_busy_host_lock(self):
        with mock.patch.object(tofi_host, 'upgrade', side_effect=tofi_host.LockBusy('another tofi command is running')):
            self.drop(request())
            self.assertEqual(self.run_request(), {'result': 'failed', 'code': 'host_busy'})

    def test_history_keeps_the_last_500_lines(self):
        for number in range(520):
            tofi_host.record_update_history({'trigger': 'web', 'result': 'refused', 'n': number})
        lines = self.history()
        self.assertEqual(len(lines), 500)
        self.assertEqual((lines[0]['n'], lines[-1]['n']), (20, 519))
        self.assertEqual(stat.S_IMODE(self.P.update_history.stat().st_mode), 0o600)

    def test_command_line_flag(self):
        self.drop(request())
        self.assertEqual(tofi_host.main(['update', '--from-request']), 0)
        self.assertEqual(tofi_host.read_env()['TOFI_VERSION'], 'v0.1.1')
        self.drop(request(version=OLD, nonce='c' * 32))
        self.assertEqual(tofi_host.main(['update', '--from-request']), 0)
        with mock.patch('sys.stderr'):
            self.assertEqual(tofi_host.main(['update', '--from-request', '--version', 'v9.9.9']), 1)


class UnitTests(WebUpdateCase):
    def test_installer_config_writes_units_and_directories(self):
        service, path = self.P.update_request_service.read_text(), self.P.update_request_path.read_text()
        self.assertIn('ExecStart=/usr/local/bin/tofi update --from-request', service)
        self.assertIn('Type=oneshot', service)
        self.assertIn('ConditionPathExists=/etc/tofi/install-state.json', service)
        self.assertIn('PathExists=/var/lib/tofi/update-request/request.json', path)
        self.assertIn('Unit=tofi-update-request.service', path)
        self.assertEqual(self.P.update_request_path.name, 'tofi-update-request.path')
        self.assertTrue(self.P.update_request_dir.is_dir() and self.P.update_status_dir.is_dir())
        self.assertEqual(oct(self.P.update_request_dir.stat().st_mode & 0o777), '0o750')
        self.assertEqual(oct(self.P.update_status_dir.stat().st_mode & 0o777), '0o755')
        tmpfiles = self.P.tmpfiles.read_text()
        self.assertIn('d /var/lib/tofi/update-request 0750 10001 10001 -', tmpfiles)
        self.assertIn('d /var/lib/tofi/update-status 0755 0 0 -', tmpfiles)

    def test_compose_mounts_only_request_dir_writable(self):
        text = (Path(__file__).resolve().parent / 'compose.yaml').read_text()
        app = text[text.index('\n  app:'):]
        self.assertIn('source: /var/lib/tofi/update-request\n        target: /update/request\n      - <<', app)
        self.assertIn('source: /var/lib/tofi/update-status\n        target: /update/status\n        read_only: true', app)
        self.assertIn('TOFI_UPDATE_REQUEST_DIR: /update/request', app)

    def test_ensure_enables_the_path_unit_and_announces_the_host(self):
        self.commands.clear()
        tofi_host.ensure_update_request_channel()
        self.assertIn(['systemctl', 'enable', '--now', 'tofi-update-request.path'], self.commands)
        marker = json.loads((self.P.update_status_dir / 'host.json').read_text())
        self.assertEqual(marker['update_request'], 1)

    def test_service_is_rate_limited_and_ensure_resets_failed_units(self):
        service = self.P.update_request_service.read_text()
        self.assertIn('StartLimitIntervalSec=60', service.split('[Service]')[0])
        self.assertIn('StartLimitBurst=3', service.split('[Service]')[0])
        self.commands.clear()
        tofi_host.ensure_update_request_channel()
        reset = ['systemctl', 'reset-failed', 'tofi-update-request.path', 'tofi-update-request.service']
        enable = ['systemctl', 'enable', '--now', 'tofi-update-request.path']
        self.assertLess(self.commands.index(reset), self.commands.index(enable))

    def test_quiet_ensure_only_acts_on_an_installed_host(self):
        self.P.update_request_path.unlink()
        state = tofi_host.load_state()
        state['phase'] = 'upgrading'
        tofi_host.save_state(state)
        tofi_host.ensure_update_request_channel_quietly()
        self.assertFalse(self.P.update_request_path.exists())
        state['phase'] = 'installed'
        tofi_host.save_state(state)
        tofi_host.ensure_update_request_channel_quietly()
        self.assertTrue(self.P.update_request_path.exists())

    def test_removing_units_reloads_systemd(self):
        tofi_host.ensure_update_request_channel()
        self.commands.clear()
        tofi_host.remove_update_request_units()
        self.assertFalse(self.P.update_request_path.exists())
        self.assertLess(self.commands.index(['systemctl', 'disable', '--now', 'tofi-update-request.path']),
                        self.commands.index(['systemctl', 'daemon-reload']))

    def test_ensure_repairs_a_missing_or_changed_unit_and_reloads_only_then(self):
        tofi_host.ensure_update_request_channel()
        self.commands.clear()
        tofi_host.ensure_update_request_channel()
        self.assertNotIn(['systemctl', 'daemon-reload'], self.commands)
        self.P.update_request_service.write_text('stale')
        tofi_host.ensure_update_request_channel()
        self.assertIn(['systemctl', 'daemon-reload'], self.commands)
        self.assertIn('--from-request', self.P.update_request_service.read_text())

    def test_old_install_gets_units_on_start_update_and_config(self):
        for argv, patch in (('start', 'start'), ('config auto-update off', None)):
            for unit in (self.P.update_request_service, self.P.update_request_path):
                unit.unlink(missing_ok=True)
            (self.P.update_status_dir / 'host.json').unlink(missing_ok=True)
            if patch:
                with mock.patch.object(tofi_host, patch):
                    tofi_host.main(argv.split())
            else:
                tofi_host.main(argv.split())
            self.assertTrue(self.P.update_request_path.exists(), argv)
            self.assertTrue((self.P.update_status_dir / 'host.json').exists(), argv)

    def test_uninstall_removes_units_and_marker(self):
        tofi_host.ensure_update_request_channel()
        tofi_host.uninstall()
        self.assertFalse(self.P.update_request_service.exists())
        self.assertFalse(self.P.update_request_path.exists())
        self.assertFalse((self.P.update_status_dir / 'host.json').exists())
        self.assertIn(['systemctl', 'disable', '--now', 'tofi-update-request.path'], self.commands)

    def test_works_without_a_computer(self):
        # installed() above is a kvm install; the same code path has no computer-specific step.
        env = tofi_host.read_env()
        env['TOFI_COMPUTER_BACKEND'] = 'none'
        tofi_host.write_env(env)
        tofi_host.ensure_update_request_channel()
        self.assertTrue(self.P.update_request_path.exists())


if __name__ == '__main__':
    unittest.main()
