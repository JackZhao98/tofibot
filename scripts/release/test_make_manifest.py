"""make_manifest.py and make_bundle.sh produce what install.sh and tofi_host accept."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import make_manifest  # noqa: E402

VERSION = 'v0.1.0-rc.1'
IMAGES = {
    'app': 'ghcr.io/jackzhao98/tofi@sha256:' + '1' * 64,
    'worker': 'ghcr.io/jackzhao98/tofi-worker@sha256:' + '2' * 64,
    'caddy': 'docker.io/library/caddy@sha256:' + '3' * 64,
}


class ManifestTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-manifest-')
        self.addCleanup(self.temp.cleanup)
        self.dir = Path(self.temp.name)
        self.guest = self.dir / ('tofi-guest-%s-x86_64.tar.zst' % VERSION)
        self.guest.write_bytes(b'synthetic guest archive')
        self.release_json = self.dir / 'account-release.json'
        self.release_json.write_text(json.dumps({'guest_binary_sha256': 'e' * 64, 'schema': 1}))
        self.bundle = self.dir / ('tofi-host-%s.tar.gz' % VERSION)
        self.bundle.write_bytes(b'synthetic bundle')

    def test_manifest_fields_and_hashes(self):
        out = self.dir / 'manifest.json'
        code = make_manifest.main(['--version', VERSION, '--app-image', IMAGES['app'],
                                   '--worker-image', IMAGES['worker'], '--caddy-image', IMAGES['caddy'],
                                   '--guest-archive', str(self.guest), '--guest-release-json', str(self.release_json),
                                   '--bundle', str(self.bundle), '--out', str(out)])
        self.assertEqual(code, 0)
        manifest = json.loads(out.read_text())
        base = 'https://github.com/JackZhao98/tofibot/releases/download/%s/' % VERSION
        self.assertEqual(manifest['images'], IMAGES)
        self.assertEqual(manifest['guest']['url'], base + self.guest.name)
        self.assertEqual(manifest['guest']['sha256'], hashlib.sha256(b'synthetic guest archive').hexdigest())
        self.assertEqual(manifest['guest']['manifest_sha256'], hashlib.sha256(self.release_json.read_bytes()).hexdigest())
        self.assertEqual(manifest['guest']['guest_binary_sha256'], 'e' * 64)
        self.assertEqual(manifest['bundle'], {'url': base + self.bundle.name,
                                              'sha256': hashlib.sha256(b'synthetic bundle').hexdigest()})
        self.assertEqual(manifest['data_schema'], 'tofi-account-data-v1')
        self.assertEqual(manifest['min_host']['disk_gib'], 30)

    def test_rejects_tags_and_misnamed_artifacts(self):
        with self.assertRaisesRegex(ValueError, 'pinned'):
            make_manifest.build_manifest(VERSION, dict(IMAGES, app='ghcr.io/jackzhao98/tofi:v0.1.0'),
                                         self.guest, self.release_json, self.bundle)
        renamed = self.dir / 'guest.tar.zst'
        renamed.write_bytes(b'x')
        with self.assertRaisesRegex(ValueError, 'tofi-guest-'):
            make_manifest.build_manifest(VERSION, IMAGES, renamed, self.release_json, self.bundle)
        with self.assertRaises(ValueError):
            make_manifest.build_manifest('0.1', IMAGES, self.guest, self.release_json, self.bundle)


class BundleTests(unittest.TestCase):
    def test_bundle_layout_and_reproducibility(self):
        with tempfile.TemporaryDirectory(prefix='tofi-bundle-') as temp:
            env = dict(os.environ, SOURCE_DATE_EPOCH='1700000000')
            digests = []
            for name in ('a', 'b'):
                out = Path(temp) / name
                subprocess.run(['bash', str(HERE / 'make_bundle.sh'), VERSION, str(out)], check=True,
                               env=env, capture_output=True)
                archive = out / ('tofi-host-%s.tar.gz' % VERSION)
                digests.append(hashlib.sha256(archive.read_bytes()).hexdigest())
                self.assertEqual((out / (archive.name + '.sha256')).read_text().strip(), digests[-1])
            self.assertEqual(digests[0], digests[1], 'bundle must be reproducible')
            with tarfile.open(archive) as tar:
                names = set(tar.getnames())
                tofi = tar.getmember('bin/tofi')
                self.assertEqual(tofi.mode & 0o777, 0o755)
                self.assertEqual((tofi.uid, tofi.gid), (0, 0))
                self.assertEqual(tar.extractfile('VERSION').read().decode().strip(), VERSION)
            expected = {'bin/tofi', 'lib/tofi_host.py', 'compose.yaml', 'Caddyfile.tmpl',
                        'worker.apparmor.template', 'worker.seccomp.json', 'tofi.service', 'tofi.conf',
                        'lib/microvm/manager.py', 'lib/microvm/worker_entrypoint.py',
                        'lib/microvm/account_release_check.py', 'lib/microvm/account_capacity.py',
                        'lib/microvm/worker_cgroups.py', 'lib/microvm/account_provisioner.py',
                        'lib/microvm/account_adoption.py', 'lib/microvm/worker_supervisor.py'}
            self.assertTrue(expected <= names, expected - names)
            # The bundled host tool imports its microvm modules from lib/microvm.
            extract = Path(temp) / 'x'
            with tarfile.open(archive) as tar:
                tar.extractall(extract)
            result = subprocess.run([sys.executable, '-I', str(extract / 'lib/tofi_host.py'), 'version'],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout)['host_bundle'], VERSION)


if __name__ == '__main__':
    unittest.main()
