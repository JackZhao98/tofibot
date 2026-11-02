"""Access choices (Tailscale), install time, the no-KVM note and the doctor exposure checks.

Fake facts only: no Tailscale, root or KVM needed.
"""
import json
from pathlib import Path
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_tofi_host as base  # noqa: E402
import tofi_host  # noqa: E402
import tofi_tui  # noqa: E402

IP_OUTPUT = '\n'.join([
    '1: lo    inet 127.0.0.1/8 scope host lo',
    '2: eth0    inet 192.168.7.2/24 brd 192.168.7.255 scope global eth0',
    '3: tailscale0    inet 100.101.102.103/32 scope global tailscale0',
    '4: tailscale0    inet6 fd7a:115c:a1e0::1/128 scope global',
])
STATUS = json.dumps({'Self': {'DNSName': 'Box.tail1234.ts.net.', 'TailscaleIPs': ['100.101.102.103']}})


class DetectionTests(unittest.TestCase):
    def test_tailscale_interface_and_cgnat_are_found(self):
        self.assertEqual(tofi_tui.parse_tailscale_addresses(IP_OUTPUT), ['100.101.102.103'])
        other = '2: ens3    inet 100.64.0.9/10 scope global ens3'
        self.assertEqual(tofi_tui.parse_tailscale_addresses(other), ['100.64.0.9'])
        self.assertEqual(tofi_tui.parse_tailscale_addresses('2: eth0    inet 10.0.0.2/24 scope global eth0'), [])

    def test_range_edges(self):
        for address, expected in (('100.64.0.0', True), ('100.127.255.255', True), ('100.128.0.1', False),
                                  ('100.63.255.255', False), ('not-an-ip', False)):
            self.assertIs(tofi_tui.is_tailscale_address(address), expected, address)

    def test_magicdns_name(self):
        self.assertEqual(tofi_tui.parse_magicdns_name(STATUS), 'box.tail1234.ts.net')
        self.assertIsNone(tofi_tui.parse_magicdns_name('not json'))
        self.assertIsNone(tofi_tui.parse_magicdns_name('{"Self": {"DNSName": "bad name;"}}'))

    def test_tailscale_info(self):
        def fake(args, timeout=10):
            return (0, IP_OUTPUT) if args[0] == 'ip' else (0, STATUS)
        with mock.patch.object(tofi_tui, 'command', side_effect=fake), \
                mock.patch.object(tofi_tui.shutil, 'which', return_value='/usr/bin/tailscale'):
            self.assertEqual(tofi_tui.tailscale_info(), {'ips': ['100.101.102.103'], 'dns': 'box.tail1234.ts.net'})
        with mock.patch.object(tofi_tui, 'command', return_value=(0, '2: eth0    inet 10.0.0.2/24 scope global eth0')), \
                mock.patch.object(tofi_tui.shutil, 'which', return_value=None):
            self.assertIsNone(tofi_tui.tailscale_info())

    def test_access_url_for_vpn(self):
        facts = {'tailscale': {'ips': ['100.101.102.103'], 'dns': 'box.tail1234.ts.net'}, 'lan': [], 'public': []}
        self.assertEqual(tofi_tui.access_url('vpn', facts, 8321), 'https://box.tail1234.ts.net:8321')
        facts['tailscale']['dns'] = None
        self.assertEqual(tofi_tui.access_url('vpn', facts, 8321), 'https://100.101.102.103:8321')


class CertificateAndBannerTests(base.HostCase):
    def test_san_has_tailscale_ip_and_magicdns_name(self):
        info = {'ips': ['100.101.102.103'], 'dns': 'box.tail1234.ts.net'}
        with mock.patch.object(tofi_host, 'interface_addresses', return_value=[('inet', '192.168.7.2')]), \
                mock.patch.object(tofi_host.tofi_tui, 'tailscale_info', return_value=info):
            dns, ips = tofi_host.certificate_names()
            urls = tofi_host.access_urls({'TOFI_BIND': '0.0.0.0', 'TOFI_TLS_CERT_FILE': '/c'})
        self.assertIn('box.tail1234.ts.net', dns)
        self.assertIn('100.101.102.103', ips)
        self.assertIn('https://box.tail1234.ts.net:8321', urls)

    def test_no_tailscale_leaves_names_alone(self):
        with mock.patch.object(tofi_host, 'interface_addresses', return_value=[('inet', '192.168.7.2')]):
            dns, ips = tofi_host.certificate_names()
        self.assertEqual(ips, ['127.0.0.1', '::1', '192.168.7.2'])
        self.assertFalse([name for name in dns if name.endswith('.ts.net')])

    def info(self, **extra):
        values = {'version': 'v0.1.0', 'urls': ['https://192.0.2.39:8321'], 'setup_key': 'KEY', 'domain': '',
                  'port': '8321', 'fingerprint': 'AB:CD', 'local_only': False}
        values.update(extra)
        return values

    def test_elapsed_format_and_line(self):
        self.assertEqual(tofi_host.format_elapsed(0), '0:00')
        self.assertEqual(tofi_host.format_elapsed(247.9), '4:07')
        self.assertEqual(tofi_host.format_elapsed(3725), '62:05')
        text = tofi_host.render_banner(self.info(elapsed=247), None, 120)
        self.assertIn('\n  Installed in 4:07\n', text)
        self.assertNotIn('Installed in', tofi_host.render_banner(self.info(), None, 120))

    def test_installer_seconds_from_environment(self):
        with mock.patch.dict(tofi_host.os.environ, {'TOFI_INSTALL_ELAPSED': '90'}):
            self.assertEqual(tofi_host.installer_seconds(), 90)
        with mock.patch.dict(tofi_host.os.environ, {'TOFI_INSTALL_ELAPSED': 'x'}):
            self.assertEqual(tofi_host.installer_seconds(), 0)

    def test_three_ways_are_explained_with_the_phone_advice(self):
        text = tofi_host.render_banner(self.info(tailscale='box.tail1234.ts.net'), None, 120)
        self.assertIn('Three ways to open TOFI', text)
        self.assertIn('Best for phones', text)
        self.assertIn('https://box.tail1234.ts.net:8321', text)
        start = text.index('Three ways')
        for line in text[start:].splitlines()[:5]:
            self.assertLessEqual(len(line), 90, line)
        domain = tofi_host.render_banner(self.info(domain='a.example.com', urls=['https://a.example.com']), None, 120)
        self.assertNotIn('Three ways', domain)
        local = tofi_host.render_banner(self.info(local_only=True), None, 120)
        self.assertNotIn('Three ways', local)

    def test_missing_kvm_note_cites_only_known_clouds(self):
        text = tofi_host.render_banner(self.info(computer='none', kvm_missing=True), None, 120)
        self.assertIn('Google Cloud, Azure', text)
        self.assertIn('Hetzner: check your plan', text)
        self.assertIn('any host without a Bot computer', text)
        quiet = tofi_host.render_banner(self.info(computer='none', kvm_missing=False), None, 120)
        self.assertNotIn('Nested virtualization', quiet)
        kvm = tofi_host.render_banner(self.info(computer='kvm', kvm_missing=True), None, 120)
        self.assertNotIn('Nested virtualization', kvm)


class ExposureTests(base.LifecycleBase):
    def setUp(self):
        super().setUp()
        self.env = self.installed()
        self.public = []
        mock.patch.object(tofi_host, 'public_addresses', side_effect=lambda: list(self.public)).start()
        mock.patch.object(tofi_host, 'read_setup_secret', return_value='').start()

    def found(self, **env):
        values = dict(self.env, **env)
        return {name: (level, detail, fix) for level, name, detail, fix in tofi_host.exposure_findings(values)}

    def test_lan_only_install_passes_everything(self):
        findings = self.found()
        self.assertEqual({level for level, _, _ in findings.values()}, {'PASS'})
        self.assertEqual(len(findings), 4)

    def test_public_address_without_domain_warns_with_one_fix(self):
        self.public = ['93.184.216.34']
        findings = self.found(TOFI_BIND='0.0.0.0', TOFI_OWNER_ALLOW_LAN_HTTP='1')
        level, detail, fix = findings['Open to the Internet']
        self.assertEqual(level, 'WARN')
        self.assertIn('93.184.216.34', detail)
        self.assertIn('Tailscale', fix)
        self.assertEqual(findings['Plain-HTTP sign-in'][0], 'WARN')
        self.assertIn('TOFI_OWNER_ALLOW_LAN_HTTP=0', findings['Plain-HTTP sign-in'][2])

    def test_domain_and_local_only_pass(self):
        self.public = ['93.184.216.34']
        self.assertEqual(self.found(TOFI_DOMAIN='a.example.com')['Open to the Internet'][0], 'PASS')
        self.assertEqual(self.found(TOFI_BIND='127.0.0.1')['Open to the Internet'][0], 'PASS')

    def test_unused_setup_key_warns(self):
        with mock.patch.object(tofi_host, 'read_setup_secret', return_value='SECRET-KEY'):
            findings = self.found()
        level, detail, fix = findings['Setup key unused']
        self.assertEqual(level, 'WARN')
        self.assertNotIn('SECRET-KEY', detail + fix)

    def test_published_runner_port_warns(self):
        compose = tofi_host.compose_file()
        compose.write_text(compose.read_text() + '\n    ports:\n      - "8390:8390"\n')
        self.assertEqual(self.found()['MCP Runner port'][0], 'WARN')

    def test_runner_port_not_published_passes(self):
        self.assertEqual(self.found(TOFI_RUNNER_URL='http://runner:8390')['MCP Runner port'][0], 'PASS')

    def test_doctor_prints_exposure_and_changes_nothing(self):
        for name in ('health', 'inspect_image', 'validate_release', 'certificate_usable'):
            mock.patch.object(tofi_host, name, return_value=True).start()
        self.public = ['93.184.216.34']
        before = tofi_host.P.env_file.read_text()
        tofi_host.doctor()
        out = tofi_host.sys.stdout.getvalue()
        self.assertIn('Exposure (read-only)', out)
        self.assertRegex(out, r'WARN +Open to the Internet')
        self.assertIn('fix: ', out)
        self.assertEqual(tofi_host.P.env_file.read_text(), before)


if __name__ == '__main__':
    unittest.main()
