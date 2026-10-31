"""The opt-in anonymous install report: what it contains, that it is off by default,
and that a send that fails never changes the install."""
import http.server
import importlib.util
import json
from pathlib import Path
import re
import threading
import unittest

from test_install_sh import InstallBase, VERSION
import test_tofi_host as base
import tofi_host

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('tofi_tui_under_test', HERE / 'tofi_tui.py')
tui = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tui)

ALLOWED = {'schema', 'tofi_version', 'os_id', 'os_version', 'arch', 'kvm', 'result', 'failed_step', 'duration_s'}
IDENTIFYING = re.compile(r'@|\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|[0-9a-f]{2}(:[0-9a-f]{2}){5}|\s|/')


class Collector:
    """A local HTTP endpoint that records every report it receives."""

    def __init__(self, status=200):
        self.bodies = []
        outer = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                length = int(self.headers.get('Content-Length', 0))
                outer.bodies.append(json.loads(self.rfile.read(length)))
                self.send_response(status)
                self.end_headers()

            def log_message(self, *args):
                pass

        self.server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
        self.url = 'http://127.0.0.1:%d/v1/install' % self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()


class PayloadTests(unittest.TestCase):
    def test_default_url_is_the_placeholder(self):
        self.assertEqual(tui.TELEMETRY_URL, 'https://telemetry.tofibot.com/v1/install')

    def test_ok_payload_has_exactly_the_documented_fields(self):
        payload = tui.telemetry_payload('v0.6.1', 'ubuntu', '24.04', 'x86_64', 'yes', 'ok', None, 93.7)
        self.assertEqual(payload, {'schema': 1, 'tofi_version': 'v0.6.1', 'os_id': 'ubuntu', 'os_version': '24.04',
                                   'arch': 'x86_64', 'kvm': 'yes', 'result': 'ok', 'duration_s': 93})

    def test_failed_payload_names_the_step(self):
        payload = tui.telemetry_payload('v0.6.1', 'debian', '12', 'aarch64', 'no', 'failed', 7, 5)
        self.assertEqual(payload['result'], 'failed')
        self.assertEqual(payload['failed_step'], 'manifest')
        self.assertLessEqual(set(payload), ALLOWED)

    def test_every_field_is_a_known_name(self):
        self.assertEqual(set(tui.TELEMETRY_FIELDS), ALLOWED)

    def test_no_identifying_value_gets_through(self):
        hostile = dict(version='v0.6.1 on host-name.example.com', os_id='ubuntu jack@example.com',
                       os_version='24.04 10.0.0.5', arch='host-name', kvm='maybe', result='failed',
                       step='/home/jack/secret', duration='x')
        payload = tui.telemetry_payload(hostile['version'], hostile['os_id'], hostile['os_version'],
                                        hostile['arch'], hostile['kvm'], hostile['result'], hostile['step'],
                                        hostile['duration'])
        self.assertLessEqual(set(payload), ALLOWED)
        self.assertEqual(payload['tofi_version'], 'unknown')
        self.assertEqual(payload['os_id'], 'unknown')
        self.assertEqual(payload['os_version'], 'unknown')
        self.assertEqual(payload['arch'], 'other')
        self.assertEqual(payload['failed_step'], 'unknown')
        self.assertEqual(payload['duration_s'], 0)
        for key, value in payload.items():
            if isinstance(value, str):
                self.assertIsNone(IDENTIFYING.search(value), (key, value))

    def test_failing_step_comes_only_from_the_fixed_list(self):
        for number, name in tui.TELEMETRY_STEPS.items():
            self.assertEqual(tui.telemetry_payload(VERSION, 'ubuntu', '24.04', 'x86_64', 'no', 'failed',
                                                   number)['failed_step'], name)
        self.assertEqual(tui.telemetry_payload(VERSION, 'ubuntu', '24.04', 'x86_64', 'no', 'failed',
                                               99)['failed_step'], 'unknown')

    def test_ok_payload_has_no_failed_step(self):
        self.assertNotIn('failed_step', tui.telemetry_payload(VERSION, 'ubuntu', '24.04', 'x86_64', 'no', 'ok', 7))


class SendTests(unittest.TestCase):
    def test_send_posts_json(self):
        collector = Collector()
        self.addCleanup(collector.close)
        payload = tui.telemetry_payload(VERSION, 'ubuntu', '24.04', 'x86_64', 'no', 'ok', None, 3)
        self.assertTrue(tui.send_telemetry(payload, collector.url))
        self.assertEqual(collector.bodies, [payload])

    def test_send_failures_never_raise(self):
        payload = tui.telemetry_payload(VERSION, 'ubuntu', '24.04', 'x86_64', 'no', 'ok', None, 3)
        self.assertFalse(tui.send_telemetry(payload, 'http://127.0.0.1:9/nothing', timeout=1))
        self.assertFalse(tui.send_telemetry(payload, 'file:///etc/passwd'))
        self.assertFalse(tui.send_telemetry(payload, 'not a url'))

    def test_cli_exits_zero_on_garbage_and_on_a_dead_endpoint(self):
        self.assertEqual(tui.telemetry_send_main(['--bogus', 'x'], {}), 0)
        self.assertEqual(tui.telemetry_send_main(['--version', 'v0.6.1', '--result', 'ok'],
                                                 {'TOFI_TELEMETRY_URL': 'http://127.0.0.1:9/x'}), 0)


class OptInTests(unittest.TestCase):
    def test_nothing_is_chosen_by_default(self):
        _, chosen, yes = tui.parse_installer_args(['--result', 'r', '--yes'], {})
        self.assertTrue(yes)
        self.assertNotIn('telemetry', chosen)

    def test_flags_and_variable(self):
        self.assertTrue(tui.parse_installer_args(['--result', 'r', '--telemetry'], {})[1]['telemetry'][0])
        self.assertFalse(tui.parse_installer_args(['--result', 'r', '--no-telemetry'], {})[1]['telemetry'][0])
        self.assertTrue(tui.parse_installer_args(['--result', 'r'], {'TOFI_TELEMETRY': '1'})[1]['telemetry'][0])
        self.assertNotIn('telemetry', tui.parse_installer_args(['--result', 'r'], {'TOFI_TELEMETRY': 'yes'})[1])


class InstallerTests(InstallBase):
    def setUp(self):
        super().setUp()
        self.collector = Collector()
        self.addCleanup(self.collector.close)

    def run_report(self, *args, **extra):
        return self.run_script(*args, TOFI_TELEMETRY_URL=self.collector.url, **extra)

    def test_yes_alone_sends_nothing_and_records_off(self):
        result = self.run_report('--yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.collector.bodies, [])
        self.assertEqual((self.root / 'etc/tofi/telemetry').read_text() if (self.root / 'etc/tofi/telemetry').exists()
                         else 'off\n', 'off\n')

    def test_opt_in_sends_one_report_without_identifying_fields(self):
        result = self.run_report('--yes', '--telemetry')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.collector.bodies), 1)
        body = self.collector.bodies[0]
        self.assertLessEqual(set(body), ALLOWED)
        self.assertEqual((body['result'], body['os_id'], body['os_version'], body['tofi_version']),
                         ('ok', 'ubuntu', '24.04', VERSION))
        self.assertIn(body['kvm'], ('yes', 'no'))
        self.assertNotIn('failed_step', body)
        text = json.dumps(body)
        self.assertNotIn('203.0.113.5', text)
        self.assertNotIn('192.168.1.20', text)

    def test_opt_in_by_variable(self):
        result = self.run_report('--yes', TOFI_TELEMETRY='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.collector.bodies), 1)

    def test_no_telemetry_flag_beats_the_variable(self):
        result = self.run_report('--yes', '--no-telemetry', TOFI_TELEMETRY='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.collector.bodies, [])

    def test_a_failed_install_reports_the_failing_step(self):
        self.publish({**json.loads((self.releases / VERSION / 'manifest.json').read_text()), 'bundle': {
            'url': '%s/download/%s/tofi-host-%s.tar.gz' % ('https://github.com/JackZhao98/tofibot/releases',
                                                         VERSION, VERSION), 'sha256': '0' * 64}})
        result = self.run_report('--yes', '--telemetry')
        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(self.collector.bodies), 1)
        body = self.collector.bodies[0]
        self.assertEqual((body['result'], body['failed_step']), ('failed', 'host-tools'))
        self.assertLessEqual(set(body), ALLOWED)

    def test_a_dead_endpoint_does_not_change_the_install(self):
        result = self.run_script('--yes', '--telemetry', TOFI_TELEMETRY_URL='http://127.0.0.1:9/x')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / 'handoff.args').exists())

    def test_no_report_for_a_bad_argument(self):
        result = self.run_report('--telemetry', '--port', 'nope')
        self.assertEqual(result.returncode, 1)
        self.assertEqual(self.collector.bodies, [])


class ConfigTelemetryTests(base.LifecycleBase):
    def test_off_by_default_and_switchable(self):
        self.installed()
        self.assertFalse(tofi_host.telemetry_enabled())
        self.assertEqual(tofi_host.configure_telemetry('status'), {'telemetry': False})
        self.assertEqual(tofi_host.configure_telemetry('on'), {'telemetry': True})
        self.assertTrue(tofi_host.telemetry_enabled())
        self.assertEqual(tofi_host.configure_telemetry('off'), {'telemetry': False})
        self.assertFalse(tofi_host.telemetry_enabled())

    def test_unknown_mode_is_refused(self):
        self.installed()
        with self.assertRaises(tofi_host.HostError):
            tofi_host.configure_telemetry('maybe')


if __name__ == '__main__':
    unittest.main()
