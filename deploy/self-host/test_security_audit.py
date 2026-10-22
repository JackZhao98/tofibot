"""Synthetic checks for `tofi security audit` (docs/process.md section 7.6, docs/decisions/0048).

Fake root and fake host facts; no Docker, root or KVM. One case per check, plus
the two properties that matter most: the audit never prints a secret value and
never changes anything. These do not replace a run on a real host.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_tofi_host as base  # noqa: E402
import tofi_host  # noqa: E402

HostError = tofi_host.HostError
OLD, NEW = base.OLD, base.NEW
ENV_CANARY = 'CANARY-env-value-8f3a'
KEY_CANARY = 'sk-ant-CANARYplaintextkey0123456789abcdef'
SETUP_CANARY = 'CANARY-setup-key-77c1'
VAULT_CANARY = 'CANARY-ciphertext-b64-QUJD'
PUBLIC = '93.184.216.34'


class AuditCase(base.LifecycleBase):
    def setUp(self):
        super().setUp()
        self.owners = {}
        self.addresses = [('inet', '192.168.7.2')]
        mock.patch.object(tofi_host, 'interface_addresses', side_effect=lambda: list(self.addresses)).start()
        mock.patch.object(tofi_host, 'path_facts', side_effect=self.facts).start()
        self.env = self.installed()
        self.make_healthy()

    def facts(self, path):
        """Real modes; ownership as a root-run install would have it unless a test overrides it."""
        real = os.lstat(path) if os.path.lexists(path) else None
        if real is None:
            return None
        override = self.owners.get(Path(path))
        under_data = Path(path) == self.P.data or self.P.data in Path(path).parents
        uid, gid = override or ((tofi_host.APP_UID, tofi_host.APP_UID) if under_data else (0, 0))
        if override is None and Path(path) in (self.P.tls, self.P.tls_key):
            gid = tofi_host.APP_UID
        import stat
        return uid, gid, stat.S_IMODE(real.st_mode), stat.S_ISLNK(real.st_mode)

    def make_healthy(self):
        """A clean install: vault files in place, a fresh backup, a current release check."""
        secrets = self.P.data / 'secrets'
        secrets.mkdir(mode=0o700)
        os.chmod(secrets, 0o700)
        (secrets / 'key').write_bytes(b'\x00' * 32)
        os.chmod(secrets / 'key', 0o600)
        self.write_vault({})
        self.make_backup('20261022T000000Z-%s' % OLD, tofi_host.iso_now())
        self.write_cache(OLD)

    def write_vault(self, records):
        path = self.P.data / 'secrets' / 'vault.json'
        path.write_text(json.dumps(records))
        os.chmod(path, 0o600)

    def write_cache(self, version):
        tofi_host.write_json(self.P.latest_cache, {'version': version, 'guest': version, 'checked_at': '2026-10-22T00:00:00Z',
                                                   'error': None, 'attempted': time.time()}, 0o644)

    def make_backup(self, backup_id, created_at, mode=0o600, disks=False):
        directory = self.P.backups / backup_id
        directory.mkdir(parents=True, mode=0o700)
        os.chmod(self.P.backups, 0o700)
        os.chmod(directory, 0o700)
        sha = {}
        for name in ('data.tar.gz', 'etc.tar.gz'):
            (directory / name).write_bytes(b'archive')
            os.chmod(directory / name, mode)
            sha[name] = hashlib.sha256(b'archive').hexdigest()
        if disks:
            (directory / 'disks').mkdir(mode=0o700)
            os.chmod(directory / 'disks', 0o700)
            (directory / 'disks' / 'workspace.ext4.zst').write_bytes(b'disk')
            os.chmod(directory / 'disks' / 'workspace.ext4.zst', mode)
        meta = {'schema': 1, 'id': backup_id, 'version': OLD, 'created_at': created_at, 'reason': 'manual',
                'archives': {'data': 'data.tar.gz', 'etc': 'etc.tar.gz'}, 'sha256': sha,
                'sizes': {'data_archive': 7, 'etc_archive': 7}}
        tofi_host.write_json(directory / 'meta.json', meta, 0o600)
        return directory

    def set_env(self, **values):
        env = tofi_host.read_env()
        env.update(values)
        tofi_host.write_env(env)

    def audit(self, online=False):
        report = tofi_host.security_audit(online)
        self.report = report
        return {finding['id']: finding for finding in report['findings']}

    def level(self, check, **kwargs):
        return self.audit(**kwargs)[check]['level']


class CleanInstallTests(AuditCase):
    def test_clean_install_passes_every_check_and_exits_0(self):
        found = self.audit()
        self.assertEqual({f['level'] for f in found.values()}, {'PASS'}, {k: v['detail'] for k, v in found.items() if v['level'] != 'PASS'})
        self.assertEqual(self.report['exit_code'], 0)
        self.assertEqual(set(found), {'exposure.tls', 'exposure.listen', 'exposure.lan_http', 'auth.admin', 'auth.rate_limit',
                                      'files.host', 'files.tls', 'files.data', 'files.backups', 'secrets.env',
                                      'secrets.model_keys', 'versions.tofi', 'versions.computers', 'backups.recent'})
        self.assertIn('self-signed', found['exposure.tls']['detail'])
        code, out = self.run_main('security', 'audit')
        self.assertEqual(code, 0)
        self.assertIn('0 WARN, 0 FAIL', out)

    def run_main(self, *argv):
        sys.stdout.seek(0)
        sys.stdout.truncate()
        with mock.patch('sys.stderr', new_callable=io.StringIO) as err:
            code = tofi_host.main(list(argv))
        self.stderr = err.getvalue()
        return code, sys.stdout.getvalue()

    def test_not_installed_is_an_error_not_a_finding(self):
        self.P.state_file.unlink()
        code, _ = self.run_main('security', 'audit')
        self.assertEqual(code, 1)
        self.assertIn('not installed', self.stderr)

    def test_unfinished_operation_is_a_warning(self):
        state = json.loads(self.P.state_file.read_text())
        state['phase'] = 'upgrade-failed'
        self.P.state_file.write_text(json.dumps(state))
        found = self.audit()
        self.assertEqual(found['install.journal']['level'], 'WARN')
        self.assertIn('upgrade-failed', found['install.journal']['detail'])


class ExposureTests(AuditCase):
    def test_plain_http_beyond_loopback_fails_and_on_loopback_warns(self):
        self.set_env(TOFI_TLS_CERT_FILE='', TOFI_TLS_KEY_FILE='')
        found = self.audit()
        self.assertEqual(found['exposure.tls']['level'], 'FAIL')
        self.assertIn('plain HTTP', found['exposure.tls']['detail'])
        self.assertNotIn('files.tls', found)  # no key to protect without HTTPS
        self.set_env(TOFI_BIND='127.0.0.1')
        self.assertEqual(self.level('exposure.tls'), 'WARN')

    def test_expired_and_expiring_certificates(self):
        for level, text in (('FAIL', 'expired'), ('WARN', 'expires within 30 days')):
            with mock.patch.object(tofi_host, 'audit_certificate', return_value=('CN = host', 'CN = host', level)):
                finding = self.audit()['exposure.tls']
            self.assertEqual(finding['level'], level)
            self.assertIn(text, finding['detail'])
            self.assertEqual(finding['fix'], 'sudo tofi regenerate-cert')

    def test_certificate_type_is_reported(self):
        with mock.patch.object(tofi_host, 'audit_certificate', return_value=('CN = Some CA', 'CN = host', 'PASS')):
            finding = self.audit()['exposure.tls']
        self.assertEqual(finding['level'], 'PASS')
        self.assertIn('issued by CN = Some CA', finding['detail'])

    def test_missing_certificate_file_fails(self):
        self.P.tls_cert.unlink()
        finding = self.audit()['exposure.tls']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('missing', finding['detail'])

    def test_checkend_results_of_the_real_certificate_drive_the_level(self):
        real = tofi_host.run.side_effect
        for failing, level in (({'0'}, 'FAIL'), ({str(tofi_host.CERT_RENEW_SECONDS)}, 'WARN')):
            def fake(args, check=True, timeout=300, _failing=failing, **kw):
                result = real(args, check=check, timeout=timeout, **kw)
                if '-checkend' in args and args[args.index('-checkend') + 1] in _failing:
                    return subprocess.CompletedProcess(args, 1, result.stdout, '')
                return result
            with mock.patch.object(tofi_host, 'run', side_effect=fake):
                self.assertEqual(self.level('exposure.tls'), level)

    def test_domain_mode_certificate_belongs_to_caddy(self):
        self.set_env(TOFI_DOMAIN='tofi.example.com', TOFI_TLS_CERT_FILE='', TOFI_TLS_KEY_FILE='', TOFI_BIND='127.0.0.1',
                     TOFI_OWNER_ALLOW_LAN_HTTP='1', TOFI_TRUSTED_PROXIES=tofi_host.LOCAL_PROXY_CIDRS)
        found = self.audit()
        self.assertEqual(found['exposure.tls']['level'], 'PASS')
        self.assertIn('Caddy', found['exposure.tls']['detail'])
        self.assertEqual(found['exposure.listen']['level'], 'PASS')
        self.assertEqual(found['exposure.lan_http']['level'], 'PASS')

    def test_public_address_on_a_lan_install_warns(self):
        self.addresses = [('inet', '192.168.7.2'), ('inet', PUBLIC)]
        finding = self.audit()['exposure.listen']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn(PUBLIC, finding['detail'])
        self.addresses = [('inet6', 'fd00::2'), ('inet', '100.64.1.2')]  # ULA and CGNAT are not public
        self.assertEqual(self.level('exposure.listen'), 'PASS')

    def test_local_only_passes_even_with_a_public_address(self):
        self.addresses = [('inet', PUBLIC)]
        self.set_env(TOFI_BIND='127.0.0.1')
        self.assertEqual(self.level('exposure.listen'), 'PASS')

    def test_domain_with_the_app_port_published_beside_caddy_warns(self):
        self.set_env(TOFI_DOMAIN='tofi.example.com', TOFI_TLS_CERT_FILE='', TOFI_TLS_KEY_FILE='', TOFI_BIND='0.0.0.0')
        self.assertEqual(self.level('exposure.listen'), 'WARN')

    def test_plain_http_sign_in_allowed_in_direct_mode_warns(self):
        self.set_env(TOFI_OWNER_ALLOW_LAN_HTTP='1')
        self.assertEqual(self.level('exposure.lan_http'), 'WARN')
        env = tofi_host.read_env()
        del env['TOFI_OWNER_ALLOW_LAN_HTTP']  # an older env file: compose defaults it to 1
        tofi_host.write_env(env)
        self.assertEqual(self.level('exposure.lan_http'), 'WARN')


class AuthTests(AuditCase):
    def test_pending_setup_key_warns_and_never_prints_it(self):
        self.P.bootstrap_secret.write_text(SETUP_CANARY + '\n')
        os.chmod(self.P.bootstrap_secret, 0o600)
        finding = self.audit()['auth.admin']
        self.assertEqual(finding['level'], 'WARN')
        self.assertNotIn(SETUP_CANARY, json.dumps(self.report))

    def test_domain_without_a_trusted_proxy_shares_one_bucket(self):
        self.set_env(TOFI_DOMAIN='tofi.example.com', TOFI_TLS_CERT_FILE='', TOFI_TLS_KEY_FILE='', TOFI_BIND='127.0.0.1')
        finding = self.audit()['auth.rate_limit']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn(tofi_host.LOCAL_PROXY_CIDRS, finding['fix'])

    def test_too_broad_proxy_list_fails(self):
        self.set_env(TOFI_TRUSTED_PROXIES='0.0.0.0/0')
        finding = self.audit()['auth.rate_limit']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('forge', finding['detail'])

    def test_named_proxy_passes_with_its_address(self):
        self.set_env(TOFI_TRUSTED_PROXIES='192.0.2.10')
        finding = self.audit()['auth.rate_limit']
        self.assertEqual(finding['level'], 'PASS')
        self.assertIn('192.0.2.10', finding['detail'])

    def test_compose_without_owner_auth_fails(self):
        compose = self.P.releases / OLD / 'compose.yaml'
        compose.write_text(compose.read_text().replace('TOFI_OWNER_AUTH: "1"', 'TOFI_OWNER_AUTH: "0"'))
        self.assertEqual(self.level('auth.rate_limit'), 'FAIL')


class FileTests(AuditCase):
    def test_loose_secret_file_in_etc_fails_with_a_fix(self):
        os.chmod(self.P.env_file, 0o644)
        finding = self.audit()['files.host']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('tofi.env: mode 0644, allowed 0600', finding['detail'].replace(str(self.P.etc) + '/', ''))
        self.assertEqual(finding['fix'], 'sudo chmod 0600 %s' % self.P.env_file)

    def test_tls_key_readable_by_everyone_fails(self):
        os.chmod(self.P.tls_key, 0o644)
        self.assertEqual(self.level('files.tls'), 'FAIL')

    def test_data_dir_and_vault_modes_and_owner(self):
        os.chmod(self.P.data / 'secrets' / 'key', 0o640)
        self.assertEqual(self.level('files.data'), 'FAIL')
        os.chmod(self.P.data / 'secrets' / 'key', 0o600)
        self.owners[self.P.data] = (0, 0)  # root-owned: the App (uid 10001) could not write it
        finding = self.audit()['files.data']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('owner uid 0, expected %d' % tofi_host.APP_UID, finding['detail'])
        self.assertIn('chown %d' % tofi_host.APP_UID, finding['fix'])

    def test_backup_files_must_be_private(self):
        self.make_backup('20261021T000000Z-%s' % OLD, '2026-10-21T00:00:00Z', mode=0o644)
        finding = self.audit()['files.backups']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('data.tar.gz', finding['detail'])

    def test_backup_with_disks_directory_passes(self):
        self.make_backup('20261021T000000Z-%s' % OLD, '2026-10-21T00:00:00Z', disks=True)
        self.assertEqual(self.level('files.backups'), 'PASS')

    def test_backup_disk_image_must_be_private(self):
        directory = self.make_backup('20261021T000000Z-%s' % OLD, '2026-10-21T00:00:00Z', disks=True)
        os.chmod(directory / 'disks' / 'workspace.ext4.zst', 0o644)
        finding = self.audit()['files.backups']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('workspace.ext4.zst', finding['detail'])
        self.assertIn('chmod 0600', finding['fix'])

    def test_backup_disks_directory_fix_keeps_it_traversable(self):
        directory = self.make_backup('20261021T000000Z-%s' % OLD, '2026-10-21T00:00:00Z', disks=True)
        os.chmod(directory / 'disks', 0o755)
        finding = self.audit()['files.backups']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('chmod 0700 %s' % (directory / 'disks'), finding['fix'])
        self.assertNotIn('chmod 0600 %s' % (directory / 'disks'), finding['fix'])

    def test_symbolic_link_is_refused(self):
        self.P.seccomp.unlink()
        os.symlink('/etc/passwd', self.P.seccomp)
        finding = self.audit()['files.host']
        self.assertEqual(finding['level'], 'FAIL')
        self.assertIn('symbolic link', finding['detail'])


class SecretTests(AuditCase):
    def test_credential_looking_variables_are_counted_by_name(self):
        self.set_env(MY_SERVICE_TOKEN=ENV_CANARY, DB_PASSWORD=ENV_CANARY, EMPTY_SECRET='', TOFI_NOTE='fine')
        finding = self.audit()['secrets.env']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn('2 credential-looking variables', finding['detail'])
        self.assertIn('DB_PASSWORD, MY_SERVICE_TOKEN', finding['detail'])
        self.assertNotIn('EMPTY_SECRET', finding['detail'])
        self.assertNotIn(ENV_CANARY, json.dumps(self.report))

    def test_managed_tls_key_path_is_not_a_secret(self):
        self.assertIn('TOFI_TLS_KEY_FILE', tofi_host.read_env())
        self.assertEqual(self.level('secrets.env'), 'PASS')

    def test_provider_key_in_env_warns(self):
        self.set_env(TOFI_MODEL_API_KEY=KEY_CANARY, SOMETHING_ELSE=KEY_CANARY)
        finding = self.audit()['secrets.model_keys']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn('TOFI_MODEL_API_KEY', finding['detail'])
        self.assertIn('SOMETHING_ELSE', finding['detail'])
        self.assertNotIn(KEY_CANARY, json.dumps(self.report))

    def test_sealed_vault_record_passes_and_unsealed_fails(self):
        sealed = {'provider:anthropic': {'id': 'provider:anthropic', 'kind': 'model_provider', 'ciphertext': VAULT_CANARY}}
        self.write_vault(sealed)
        finding = self.audit()['secrets.model_keys']
        self.assertEqual(finding['level'], 'PASS')
        self.assertIn('1 sealed provider key', finding['detail'])
        self.write_vault({'provider:anthropic': {'id': 'provider:anthropic', 'kind': 'model_provider'}})
        self.assertEqual(self.level('secrets.model_keys'), 'FAIL')

    def test_plaintext_key_inside_the_vault_file_fails(self):
        self.write_vault({'x': {'kind': 'model_provider', 'ciphertext': VAULT_CANARY, 'label': KEY_CANARY}})
        self.assertEqual(self.level('secrets.model_keys'), 'FAIL')
        self.assertNotIn(KEY_CANARY, json.dumps(self.report))

    def test_unreadable_vault_is_reported(self):
        (self.P.data / 'secrets' / 'vault.json').write_text('{not json')
        self.assertEqual(self.level('secrets.model_keys'), 'WARN')


class VersionTests(AuditCase):
    def test_newer_release_warns(self):
        self.write_cache(NEW)
        finding = self.audit()['versions.tofi']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn('latest release %s' % NEW, finding['detail'])

    def test_final_release_is_newer_than_its_rc(self):
        self.set_env(TOFI_VERSION='v0.1.4-rc.29')
        self.write_cache('v0.1.4')
        self.assertEqual(self.level('versions.tofi'), 'WARN')

    def test_no_stored_check_is_unknown_not_current(self):
        self.P.latest_cache.unlink()
        finding = self.audit()['versions.tofi']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn('unknown', finding['detail'])
        self.assertFalse(self.P.latest_cache.exists(), 'the audit must not write the release cache')

    def test_online_check_fetches_without_writing_the_cache(self):
        self.P.latest_cache.unlink()
        with mock.patch.object(tofi_host, 'fetch_manifest', return_value=tofi_host.validate_manifest(base.manifest(NEW))) as fetch:
            finding = self.audit(online=True)['versions.tofi']
        fetch.assert_called_once_with(timeout=5)
        self.assertEqual(finding['level'], 'WARN')
        self.assertFalse(self.P.latest_cache.exists())
        with mock.patch.object(tofi_host, 'fetch_manifest', side_effect=HostError('offline')):
            self.assertEqual(self.level('versions.tofi', online=True), 'WARN')

    def test_hibernated_computer_on_an_older_guest_warns(self):
        directory = self.P.worker_state / 'acct-1' / 'snapshot'
        directory.mkdir(parents=True)
        (directory / 'meta.json').write_text(json.dumps({'format': 1, 'release': 'v0.0.9', 'created_at': '2026-10-08T01:02:03Z'}))
        finding = self.audit()['versions.computers']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn('v0.0.9', finding['detail'])
        self.assertEqual(finding['fix'], 'sudo tofi computers upgrade --all')

    def test_missing_guest_release_fails(self):
        import shutil
        shutil.rmtree(self.P.guest / OLD)
        self.assertEqual(self.level('versions.computers'), 'FAIL')


class BackupTests(AuditCase):
    def test_no_backup_warns(self):
        import shutil
        shutil.rmtree(self.P.backups)
        finding = self.audit()['backups.recent']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn('no backup', finding['detail'])

    def test_old_backup_warns_and_recent_passes(self):
        import shutil
        shutil.rmtree(self.P.backups)
        week_ago = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(time.time() - 9 * 86400))
        self.make_backup('20261013T000000Z-%s' % OLD, week_ago)
        finding = self.audit()['backups.recent']
        self.assertEqual(finding['level'], 'WARN')
        self.assertIn('9.0 days ago', finding['detail'])
        self.make_backup('20261022T010000Z-%s' % OLD, tofi_host.iso_now())
        self.assertEqual(self.level('backups.recent'), 'PASS')


class OutputTests(AuditCase):
    def test_json_output_and_exit_code_2_on_fail(self):
        os.chmod(self.P.env_file, 0o644)
        self.set_env(TOFI_VERSION=OLD)
        os.chmod(self.P.env_file, 0o644)
        code, out = CleanInstallTests.run_main(self, 'security', 'audit', '--json')
        report = json.loads(out)
        self.assertEqual(code, 2)
        self.assertEqual(report['exit_code'], 2)
        self.assertEqual(report['schema'], 1)
        self.assertEqual(report['summary']['fail'], 1)
        failing = [f for f in report['findings'] if f['level'] == 'FAIL']
        self.assertEqual([f['id'] for f in failing], ['files.host'])
        self.assertEqual(set(failing[0]), {'id', 'level', 'title', 'detail', 'fix'})

    def test_warnings_alone_exit_0_and_every_non_pass_line_has_a_fix(self):
        self.write_cache(NEW)
        code, out = CleanInstallTests.run_main(self, 'security', 'audit')
        self.assertEqual(code, 0)
        self.assertIn('WARN  versions.tofi', out)
        self.assertIn('fix: sudo tofi update', out)
        self.audit()
        for finding in self.report['findings']:
            self.assertEqual(bool(finding['fix']), finding['level'] != 'PASS', finding['id'])

    def test_a_broken_check_does_not_hide_the_others(self):
        with mock.patch.object(tofi_host, 'audit_admin', side_effect=OSError('disk went away')):
            found = self.audit()
        self.assertEqual(found['auth.admin']['level'], 'WARN')
        self.assertIn('could not be checked', found['auth.admin']['detail'])
        self.assertIn('backups.recent', found)

    def test_never_prints_a_secret_value(self):
        self.set_env(TOFI_MODEL_API_KEY=KEY_CANARY, MY_TOKEN=ENV_CANARY, DB_PASSWORD=ENV_CANARY, ANTHROPIC_API_KEY=KEY_CANARY)
        self.P.bootstrap_secret.write_text(SETUP_CANARY)
        os.chmod(self.P.bootstrap_secret, 0o600)
        self.write_vault({'provider:anthropic': {'kind': 'model_provider', 'ciphertext': VAULT_CANARY, 'label': 'sk-...wxyz'}})
        (self.P.data / 'secrets' / 'key').write_bytes(b'CANARY-vault-key')
        os.chmod(self.P.data / 'secrets' / 'key', 0o600)
        seen = []
        for argv in (('security', 'audit'), ('security', 'audit', '--json')):
            code, out = CleanInstallTests.run_main(self, *argv)
            seen.append(out + self.stderr)
        text = '\n'.join(seen)
        for canary in (ENV_CANARY, KEY_CANARY, SETUP_CANARY, VAULT_CANARY, 'CANARY-vault-key'):
            self.assertNotIn(canary, text)
        # Names and counts are shown, so the report is still useful.
        self.assertIn('DB_PASSWORD', text)
        self.assertIn('TOFI_MODEL_API_KEY', text)

    def test_audit_changes_nothing(self):
        self.write_cache(NEW)
        self.P.bootstrap_secret.write_text(SETUP_CANARY)
        os.chmod(self.P.bootstrap_secret, 0o600)

        def tree():
            state = {}
            for path in sorted(self.root.rglob('*')):
                info = os.lstat(path)
                digest = hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() and not path.is_symlink() else ''
                state[str(path)] = (info.st_mode, info.st_size, info.st_mtime_ns, digest)
            return state

        before = tree()
        self.commands.clear()
        self.audit()
        self.audit(online=False)
        CleanInstallTests.run_main(self, 'security', 'audit', '--json')
        self.assertEqual(tree(), before)
        self.assertTrue(all(c[0] == 'openssl' and c[1] == 'x509' for c in self.commands), self.commands)
        self.assertFalse(any('-in' not in c for c in self.commands))

    def test_needs_root(self):
        with mock.patch.object(tofi_host, 'require_root', side_effect=HostError('Run this command as root')):
            with self.assertRaisesRegex(HostError, 'as root'):
                tofi_host.security_audit()


if __name__ == '__main__':
    unittest.main()
