"""Synthetic checks for the existing-reverse-proxy install path and image removal on purge.

Addresses come from the documentation ranges (192.0.2.0/24, 198.51.100.0/24,
203.0.113.0/24); nothing here touches Docker, DNS or a real host.
"""
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_tofi_host as base  # noqa: E402
import tofi_host  # noqa: E402
import tofi_tui  # noqa: E402

HERE = Path(__file__).resolve().parent
OLD = base.OLD


class ProxyListTests(unittest.TestCase):
    def test_accepts_addresses_and_ranges_and_normalises(self):
        self.assertEqual(tofi_tui.parse_proxy_list('192.0.2.10'), (['192.0.2.10'], None))
        self.assertEqual(tofi_tui.parse_proxy_list('192.0.2.10, 198.51.100.7/24 192.0.2.10'),
                         (['192.0.2.10', '198.51.100.0/24'], None))
        self.assertEqual(tofi_tui.parse_proxy_list('2001:db8::1')[0], ['2001:db8::1'])

    def test_rejects_garbage_empty_and_internet_wide_ranges(self):
        for bad in ('', '   ', 'proxy.example.com', '192.0.2.300', '192.0.2.0/33', '0.0.0.0/0', '10.0.0.0/7', '::/0'):
            entries, error = tofi_tui.parse_proxy_list(bad)
            self.assertIsNone(entries, bad)
            self.assertTrue(error, bad)

    def test_snippets_carry_the_upstream_and_the_scoped_verification_skip(self):
        snippets = tofi_tui.proxy_snippets('192.0.2.20', 8321, 'tofi.example.com')
        caddy, nginx = snippets['caddy'], snippets['nginx']
        self.assertIn('reverse_proxy https://192.0.2.20:8321', caddy)
        self.assertIn('tls_insecure_skip_verify', caddy)
        self.assertIn('flush_interval -1', caddy)
        self.assertIn('proxy_pass https://192.0.2.20:8321;', nginx)
        self.assertIn('proxy_ssl_verify off;', nginx)
        for header in ('X-Forwarded-For $proxy_add_x_forwarded_for', 'X-Forwarded-Proto', 'Upgrade $http_upgrade',
                       'Host $host'):
            self.assertIn(header, nginx)
        self.assertIn('[2001:db8::5]:8321', tofi_tui.proxy_snippets('2001:db8::5', 8321)['nginx'])


class HostFlagTests(base.HostCase):
    def options(self, *argv):
        return tofi_host.install_options(tofi_host.parse_args(['install'] + list(argv)))

    def test_flag_is_parsed_and_normalised(self):
        options = self.options('--behind-proxy', '192.0.2.10,198.51.100.9/24')
        self.assertEqual(options['behind_proxy'], '192.0.2.10,198.51.100.0/24')
        self.assertEqual(options['bind'], '0.0.0.0')
        self.assertEqual(self.options()['behind_proxy'], '')

    def test_flag_validation_and_conflicts(self):
        for argv in (['--behind-proxy', 'not-an-ip'], ['--behind-proxy', '0.0.0.0/0'],
                     ['--behind-proxy', '192.0.2.10', '--domain', 'tofi.example.com'],
                     ['--behind-proxy', '192.0.2.10', '--local-only']):
            with self.assertRaises(tofi_host.HostError, msg=argv):
                self.options(*argv)

    def test_existing_flags_are_unchanged(self):
        options = self.options('--domain', 'Tofi.Example.com.', '--email', 'ops@example.com', '--port', '9000')
        self.assertEqual((options['domain'], options['behind_proxy'], options['port']),
                         ('tofi.example.com', '', 9000))

    def test_env_written_for_proxy_domain_and_plain(self):
        manifest = base.manifest(OLD, '1')
        budgets = {'cpu': 3, 'memory_mib': 6144}
        proxied = tofi_host.render_env(manifest, self.options('--behind-proxy', '192.0.2.10'), budgets)
        self.assertEqual(proxied['TOFI_TRUSTED_PROXIES'], '192.0.2.10')
        self.assertEqual(proxied['TOFI_DOMAIN'], '')
        self.assertEqual(proxied['TOFI_BIND'], '0.0.0.0')
        self.assertTrue(proxied['TOFI_TLS_CERT_FILE'])
        domain = tofi_host.render_env(manifest, self.options('--domain', 'tofi.example.com'), budgets)
        self.assertEqual(domain['TOFI_TRUSTED_PROXIES'], tofi_host.LOCAL_PROXY_CIDRS)
        self.assertEqual(tofi_host.render_env(manifest, self.options(), budgets)['TOFI_TRUSTED_PROXIES'], '')
        text = tofi_host.format_env(proxied)
        self.assertIn('TOFI_TRUSTED_PROXIES=192.0.2.10\n', text)

    def test_notes_print_snippets_for_the_upstream(self):
        env = dict(base.manifest and tofi_host.render_env(
            base.manifest(OLD, '1'), self.options('--behind-proxy', '192.0.2.10'), {'cpu': 3, 'memory_mib': 6144}))
        with mock.patch.object(tofi_host, 'interface_addresses', return_value=[('inet', '192.0.2.20')]):
            notes = tofi_host.render_proxy_notes(env)
            self.assertIn('reverse_proxy https://192.0.2.20:8321', notes)
            self.assertIn('proxy_pass https://192.0.2.20:8321;', notes)
            self.assertIn('192.0.2.10', notes)
            env['TOFI_DOMAIN'] = 'tofi.example.com'
            self.assertEqual(tofi_host.render_proxy_notes(env), '')


class ConfigTrustedProxiesTests(base.LifecycleBase):
    def test_set_show_clear_and_unknown_keys_survive(self):
        env = self.installed()
        env['TOFI_CUSTOM_NOTE'] = 'keep-me'
        tofi_host.write_env(env)
        with mock.patch.object(tofi_host, 'interface_addresses', return_value=[('inet', '192.0.2.20')]):
            result = tofi_host.configure_trusted_proxies(['192.0.2.10', '198.51.100.0/24'])
        self.assertEqual(result['trusted_proxies'], '192.0.2.10,198.51.100.0/24')
        stored = tofi_host.read_env()
        self.assertEqual(stored['TOFI_TRUSTED_PROXIES'], '192.0.2.10,198.51.100.0/24')
        self.assertEqual(stored['TOFI_CUSTOM_NOTE'], 'keep-me')
        self.assertIn('reverse_proxy https://192.0.2.20:8321', base.sys.stdout.getvalue())
        self.assertEqual(tofi_host.configure_trusted_proxies([])['trusted_proxies'], '192.0.2.10,198.51.100.0/24')
        self.assertEqual(tofi_host.configure_trusted_proxies([], clear=True)['trusted_proxies'], '')
        self.assertEqual(tofi_host.read_env()['TOFI_TRUSTED_PROXIES'], '')
        self.assertEqual(tofi_host.read_env()['TOFI_CUSTOM_NOTE'], 'keep-me')

    def test_bad_input_and_domain_installs_are_refused(self):
        self.installed()
        with self.assertRaises(tofi_host.HostError):
            tofi_host.configure_trusted_proxies(['nope'])
        with self.assertRaises(tofi_host.HostError):
            tofi_host.configure_trusted_proxies(['192.0.2.10'], clear=True)
        env = tofi_host.read_env()
        env['TOFI_DOMAIN'] = 'tofi.example.com'
        tofi_host.write_env(env)
        with self.assertRaisesRegex(tofi_host.HostError, 'bundled Caddy'):
            tofi_host.configure_trusted_proxies(['192.0.2.10'])

    def test_cli_parses_config_trusted_proxies(self):
        args = tofi_host.parse_args(['config', 'trusted-proxies', '192.0.2.10', '198.51.100.1'])
        self.assertEqual((args.config_command, args.proxies, args.clear),
                         ('trusted-proxies', ['192.0.2.10', '198.51.100.1'], False))
        self.assertTrue(tofi_host.parse_args(['config', 'trusted-proxies', '--clear']).clear)


class PurgeImageTests(base.LifecycleBase):
    def docker_calls(self):
        return [c for c in self.commands if c[:3] == ['docker', 'image', 'rm']]

    def test_purge_removes_only_this_installations_images_and_says_so(self):
        self.installed(phase='stopped-retained')
        old, new = base.manifest(OLD, '1'), base.manifest('v0.2.0', '2')
        (self.P.releases / OLD / 'manifest.json').write_text(json.dumps(old))
        self.make_bundle('v0.2.0')
        (self.P.releases / 'v0.2.0' / 'manifest.json').write_text(json.dumps(new))
        # A hostile or foreign manifest entry is not ours to remove.
        foreign = base.manifest('v0.3.0', '3')
        foreign['images']['app'] = 'docker.io/someone/else@sha256:' + '4' * 64
        self.make_bundle('v0.3.0')
        (self.P.releases / 'v0.3.0' / 'manifest.json').write_text(json.dumps(foreign))
        tofi_host.uninstall()                       # plain uninstall keeps images
        self.assertEqual(self.docker_calls(), [])
        tofi_host.uninstall(purge=True, confirm=tofi_host.socket.gethostname())
        removed = [c[3] for c in self.docker_calls()]
        expected = {old['images'][k] for k in ('app', 'worker', 'caddy')} | \
            {new['images'][k] for k in ('app', 'worker', 'caddy')} | {foreign['images'][k] for k in ('worker', 'caddy')}
        self.assertEqual(set(removed), expected)
        self.assertEqual(len(removed), len(set(removed)))
        self.assertNotIn(foreign['images']['app'], removed)
        for call in self.docker_calls():
            self.assertNotIn('-f', call)
            self.assertNotIn('--force', call)
        self.assertFalse([c for c in self.commands if c[:3] in (['docker', 'image', 'prune'],
                                                                ['docker', 'system', 'prune'])])
        output = base.sys.stdout.getvalue()
        self.assertIn('Removed image ' + old['images']['app'], output)

    def test_images_in_use_are_reported_kept(self):
        self.installed(phase='stopped-retained')
        env = tofi_host.read_env()

        def fake(args, check=True, timeout=300, **kwargs):
            self.commands.append(list(args))
            code = 1 if args[:3] == ['docker', 'image', 'rm'] and args[3] == env['TOFI_CADDY_IMAGE'] else 0
            return base.subprocess.CompletedProcess(args, code, '', '')
        with mock.patch.object(tofi_host, 'run', side_effect=fake):
            tofi_host.uninstall(purge=True, confirm=tofi_host.socket.gethostname())
        output = base.sys.stdout.getvalue()
        self.assertIn('Kept image ' + env['TOFI_CADDY_IMAGE'], output)
        self.assertIn('Removed image ' + env['TOFI_APP_IMAGE'], output)


class InstallerFlowTests(unittest.TestCase):
    """The installer UI (tofi_tui.Installer) with detection, DNS and prompts replaced."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-proxy-')
        self.addCleanup(self.temp.cleanup)
        self.result = str(Path(self.temp.name) / 'answers')
        host = tofi_tui.Host({'TOFI_TEST_ROOT': self.temp.name})
        self.facts = {'host': host, 'platform': 'Linux x86_64', 'uid': '0',
                      'os': {'NAME': 'Debian GNU/Linux', 'ID': 'debian', 'VERSION_ID': '12'}, 'cpus': 4,
                      'memory_mib': 7960, 'swap_mib': 0, 'disk_gib': 35, 'docker': None, 'kvm': True,
                      'kvm_reason': None, 'tun': True, 'cgroup2': True, 'apparmor': 'Y',
                      'ports': mock.Mock(busy=lambda p: False, owner=lambda p: None, describe=lambda p: 'free'),
                      'port': 8321, 'phase': None, 'installed': None, 'journal_error': None,
                      'lan': ['192.0.2.20'], 'public': ['203.0.113.5']}
        for patcher in (mock.patch.object(tofi_tui, 'detect', return_value=self.facts),
                        mock.patch.object(tofi_tui, 'check_host', return_value=([], [])),
                        mock.patch.object(tofi_tui, 'resolve_release', return_value=('stable', 'v0.1.0')),
                        mock.patch('sys.stdout', new_callable=io.StringIO),
                        mock.patch('sys.stderr', new_callable=io.StringIO)):
            patcher.start()
            self.addCleanup(patcher.stop)

    def installer(self, *argv, prompts=None, **environ):
        installer = tofi_tui.Installer(['--result', self.result, *argv], dict(environ))
        if prompts is not None:
            installer.ask = prompts
        return installer

    def answers(self):
        return dict(line.split('=', 1) for line in Path(self.result).read_text().splitlines())

    def test_flag_writes_proxy_answers_and_no_domain(self):
        code = self.installer('--yes', '--behind-proxy', '192.0.2.10,198.51.100.4/24', '--no-auto-update').run()
        self.assertEqual(code, 0)
        answers = self.answers()
        self.assertEqual(answers['BEHIND_PROXY'], '192.0.2.10,198.51.100.0/24')
        self.assertEqual((answers['DOMAIN'], answers['LOCAL_ONLY']), ('', '0'))

    def test_environment_variable_is_equivalent(self):
        self.assertEqual(self.installer('--yes', TOFI_BEHIND_PROXY='192.0.2.10').run(), 0)
        self.assertEqual(self.answers()['BEHIND_PROXY'], '192.0.2.10')

    def test_flag_errors(self):
        for argv in (['--behind-proxy', 'nope'], ['--behind-proxy', '192.0.2.10', '--domain', 'tofi.example.com'],
                     ['--behind-proxy', '192.0.2.10', '--local-only']):
            self.assertEqual(self.installer('--yes', *argv).run(), 1, argv)
        self.assertIn('--behind-proxy', sys.stderr.getvalue())

    def test_domain_flag_still_works_without_the_proxy_question(self):
        with mock.patch.object(tofi_tui, 'resolve_dns', return_value=['203.0.113.5']):
            self.assertEqual(self.installer('--yes', '--domain', 'tofi.example.com').run(), 0)
        answers = self.answers()
        self.assertEqual((answers['DOMAIN'], answers['BEHIND_PROXY']), ('tofi.example.com', ''))

    def test_interactive_yes_asks_for_proxies_then_validates(self):
        class Prompts:
            def __init__(self):
                self.asked = []
                self.typed = iter(['proxy.example.com', '192.0.2.10'])
            ui = None

            def confirm(self, question, default=True):
                self.asked.append(question)
                return 'reverse proxy' in question or 'Proceed' in question or 'automatic' in question

            def ask_text(self, question, validate, default='', hint=''):
                self.asked.append(question)
                for typed in self.typed:
                    value, error = validate(typed)
                    if error is None:
                        return value
                raise AssertionError('no valid answer')

            def choose(self, *args, **kwargs):
                raise AssertionError('the exposure menu must not be shown after answering yes')

        prompts = Prompts()
        installer = self.installer()
        installer.ask = prompts
        prompts.ui = installer.ui
        # The computer question and the port prompt use choose/ask_text; answer them neutrally.
        prompts.choose = lambda question, options, default: options[0]
        original = prompts.ask_text
        prompts.ask_text = lambda question, validate, default='', hint='': (
            default or '8321') if question == 'Port for TOFI' else original(question, validate, default, hint)
        self.assertEqual(installer.run(), 0)
        self.assertTrue(prompts.asked[0].startswith('Is TOFI behind a reverse proxy you already run'))
        self.assertEqual(self.answers()['BEHIND_PROXY'], '192.0.2.10')

    def test_interactive_no_goes_on_to_the_exposure_menu(self):
        installer = self.installer()
        choices = []

        class Prompts:
            ui = installer.ui

            def confirm(self, question, default=True):
                return 'Proceed' in question

            def ask_text(self, question, validate, default='', hint=''):
                return default or '8321'

            def choose(self, question, options, default):
                choices.append(question)
                return options[0]
        installer.ask = Prompts()
        self.assertEqual(installer.run(), 0)
        self.assertIn('How will you open TOFI?', choices)
        self.assertEqual(self.answers()['BEHIND_PROXY'], '')

    def domain_prompts(self, installer, agree):
        asked = []

        class Prompts:
            ui = installer.ui

            def confirm(self, question, default=True):
                asked.append(question)
                if question.startswith('Continue installing with'):
                    return agree
                return not question.startswith('Is TOFI behind')

            def ask_text(self, question, validate, default='', hint=''):
                if question == 'Domain name':
                    return validate('tofi.example.com')[0]
                return default or ''

            def choose(self, question, options, default):
                return next((o for o in options if o.key == 'domain'), options[0]) \
                    if question == 'How will you open TOFI?' else options[0]
        installer.ask = Prompts()
        return asked

    def test_dns_mismatch_needs_explicit_confirmation(self):
        with mock.patch.object(tofi_tui, 'resolve_dns', return_value=['198.51.100.99']):
            installer = self.installer()
            asked = self.domain_prompts(installer, agree=False)
            self.assertEqual(installer.run(), 1)
            self.assertTrue(any(q.startswith('Continue installing with tofi.example.com anyway') for q in asked))
            self.assertFalse(Path(self.result).exists())
            output = sys.stdout.getvalue()
            self.assertIn('no certificate is issued', output)

            installer = self.installer()
            self.domain_prompts(installer, agree=True)
            self.assertEqual(installer.run(), 0)
            self.assertEqual(self.answers()['DOMAIN'], 'tofi.example.com')

    def test_matching_dns_asks_nothing_extra(self):
        with mock.patch.object(tofi_tui, 'resolve_dns', return_value=['203.0.113.5']):
            installer = self.installer()
            asked = self.domain_prompts(installer, agree=False)
            self.assertEqual(installer.run(), 0)
            self.assertFalse(any(q.startswith('Continue installing with') for q in asked))


if __name__ == '__main__':
    unittest.main()
