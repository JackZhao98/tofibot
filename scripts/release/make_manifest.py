#!/usr/bin/env python3
"""Write the release manifest.json that install.sh and `tofi update` trust.

Inputs are the pushed image digests and the built release files; every
artifact is hashed here, and the result is checked with the same validator the
host uses (deploy/self-host/tofi_host.py validate_manifest).

Example:
  scripts/release/make_manifest.py --version v0.1.0 \
    --app-image ghcr.io/jackzhao98/tofi@sha256:... \
    --worker-image ghcr.io/jackzhao98/tofi-worker@sha256:... \
    --caddy-image docker.io/library/caddy@sha256:... \
    --guest-archive dist/tofi-guest-v0.1.0-x86_64.tar.zst \
    --guest-release-json /opt/tofi-guest-build/v0.1.0/account-release.json \
    --bundle dist/tofi-host-v0.1.0.tar.gz --out dist/manifest.json
"""
import argparse
import hashlib
import json
from pathlib import Path
import sys

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / 'deploy/self-host'))
import tofi_host  # noqa: E402

DATA_SCHEMA = 'tofi-account-data-v1'
MIN_HOST = {
    'arch': 'x86_64',
    'kvm': True,
    'cpus': 2,
    'memory_gib': 4,
    'disk_gib': 30,
    'recommended_memory_gib': 8,
    'recommended_disk_gib': 40,
    'os': ['ubuntu-22.04', 'ubuntu-24.04', 'debian-12', 'debian-13'],
}


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def build_manifest(version, images, guest_archive, guest_release_json, bundle, data_schema=DATA_SCHEMA,
                   guest_version=None, notes_url=None):
    guest_version = guest_version or version
    guest_archive = Path(guest_archive)
    bundle = Path(bundle)
    release = json.loads(Path(guest_release_json).read_text())
    download = '%s/download/%s/' % (tofi_host.RELEASE_BASE, version)
    guest_download = '%s/download/%s/' % (tofi_host.RELEASE_BASE, guest_version)
    if guest_archive.name != 'tofi-guest-%s-x86_64.tar.zst' % guest_version:
        raise ValueError('guest archive must be named tofi-guest-%s-x86_64.tar.zst' % guest_version)
    if bundle.name != 'tofi-host-%s.tar.gz' % version:
        raise ValueError('host bundle must be named tofi-host-%s.tar.gz' % version)
    manifest = {
        'schema': 1,
        'version': version,
        'images': dict(images),
        'guest': {
            'version': guest_version,
            'url': guest_download + guest_archive.name,
            'sha256': sha256_file(guest_archive),
            'manifest_sha256': sha256_file(guest_release_json),
            'guest_binary_sha256': release['guest_binary_sha256'],
        },
        'bundle': {
            'url': download + bundle.name,
            'sha256': sha256_file(bundle),
        },
        'min_host': MIN_HOST,
        'data_schema': data_schema,
        # Optional for readers: older manifests have none.
        'notes_url': notes_url or '%s/tag/%s' % (tofi_host.RELEASE_BASE, version),
    }
    try:
        tofi_host.validate_manifest(manifest)
    except tofi_host.HostError as error:
        raise ValueError(str(error)) from error
    return manifest


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--version', required=True)
    parser.add_argument('--app-image', required=True)
    parser.add_argument('--worker-image', required=True)
    parser.add_argument('--caddy-image', required=True)
    parser.add_argument('--guest-archive', required=True)
    parser.add_argument('--guest-release-json', required=True)
    parser.add_argument('--bundle', required=True)
    parser.add_argument('--data-schema', default=DATA_SCHEMA)
    parser.add_argument('--notes-url', help='release notes page (default: the GitHub release for --version)')
    parser.add_argument('--out', required=True)
    args = parser.parse_args(argv)
    images = {'app': args.app_image, 'worker': args.worker_image, 'caddy': args.caddy_image}
    try:
        manifest = build_manifest(args.version, images, args.guest_archive, args.guest_release_json,
                                  args.bundle, args.data_schema, notes_url=args.notes_url)
    except (OSError, ValueError, KeyError) as error:
        print('make_manifest: ' + str(error), file=sys.stderr)
        return 1
    Path(args.out).write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
    print(args.out)
    return 0


if __name__ == '__main__':
    sys.exit(main())
