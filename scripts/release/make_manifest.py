#!/usr/bin/env python3
"""Write the release manifest.json that install.sh and `tofi update` trust.

Inputs are the pushed image digests (multi-arch manifest lists) and the built
release files; every artifact is hashed here, and the result is checked with
the same validator the host uses (deploy/self-host/tofi_host.py
validate_manifest).

Compatibility: the top-level `guest` entry is always the x86_64 Guest, exactly
as before arm64, so a host that predates `arches` reads the same file. The
`arches` map carries one Guest entry per architecture (x86_64 repeats the
top-level one). Image digests are manifest lists and serve both architectures.

Example:
  scripts/release/make_manifest.py --version v0.1.0 \
    --app-image ghcr.io/jackzhao98/tofi@sha256:... \
    --worker-image ghcr.io/jackzhao98/tofi-worker@sha256:... \
    --caddy-image docker.io/library/caddy@sha256:... \
    --guest-archive dist/tofi-guest-v0.1.0-x86_64.tar.zst \
    --guest-release-json /opt/tofi-guest-build/v0.1.0/account-release.json \
    --guest-aarch64-archive dist/tofi-guest-v0.1.0-aarch64.tar.zst \
    --guest-aarch64-release-json dist/account-release-aarch64.json \
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


def guest_entry(arch, guest_version, archive, release_json):
    archive = Path(archive)
    release = json.loads(Path(release_json).read_text())
    if archive.name != 'tofi-guest-%s-%s.tar.zst' % (guest_version, arch):
        raise ValueError('guest archive must be named tofi-guest-%s-%s.tar.zst' % (guest_version, arch))
    # account-release.json carries `arch` since arm64; older ones are x86_64.
    built_for = release.get('arch', 'x86_64')
    if built_for != arch:
        raise ValueError('%s says arch %s but was passed as the %s Guest' % (release_json, built_for, arch))
    return {
        'version': guest_version,
        'url': '%s/download/%s/%s' % (tofi_host.RELEASE_BASE, guest_version, archive.name),
        'sha256': sha256_file(archive),
        'manifest_sha256': sha256_file(release_json),
        'guest_binary_sha256': release['guest_binary_sha256'],
    }


def build_manifest(version, images, guest_archive, guest_release_json, bundle, data_schema=DATA_SCHEMA,
                   guest_version=None, notes_url=None, extra_guests=None):
    """extra_guests: {arch: (archive, account-release.json)} for architectures
    besides x86_64, which stays the positional pair."""
    guest_version = guest_version or version
    bundle = Path(bundle)
    download = '%s/download/%s/' % (tofi_host.RELEASE_BASE, version)
    x86 = guest_entry('x86_64', guest_version, guest_archive, guest_release_json)
    if bundle.name != 'tofi-host-%s.tar.gz' % version:
        raise ValueError('host bundle must be named tofi-host-%s.tar.gz' % version)
    manifest = {
        'schema': 1,
        'version': version,
        'images': dict(images),
        'guest': x86,
        'bundle': {
            'url': download + bundle.name,
            'sha256': sha256_file(bundle),
        },
        'min_host': MIN_HOST,
        'data_schema': data_schema,
        # Optional for readers: older manifests have none.
        'notes_url': notes_url or '%s/tag/%s' % (tofi_host.RELEASE_BASE, version),
    }
    if extra_guests:
        arches = {'x86_64': {'guest': dict(x86)}}
        for arch, (archive, release_json) in sorted(extra_guests.items()):
            if arch == 'x86_64' or arch not in tofi_host.RELEASE_ARCHES:
                raise ValueError('unsupported extra Guest architecture %r' % arch)
            arches[arch] = {'guest': guest_entry(arch, guest_version, archive, release_json)}
        manifest['arches'] = arches
        manifest['min_host'] = dict(MIN_HOST, arches=['x86_64'] + sorted(extra_guests))
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
    parser.add_argument('--guest-aarch64-archive')
    parser.add_argument('--guest-aarch64-release-json')
    parser.add_argument('--bundle', required=True)
    parser.add_argument('--data-schema', default=DATA_SCHEMA)
    parser.add_argument('--notes-url', help='release notes page (default: the GitHub release for --version)')
    parser.add_argument('--out', required=True)
    args = parser.parse_args(argv)
    images = {'app': args.app_image, 'worker': args.worker_image, 'caddy': args.caddy_image}
    extra = {}
    if bool(args.guest_aarch64_archive) != bool(args.guest_aarch64_release_json):
        print('make_manifest: pass --guest-aarch64-archive and --guest-aarch64-release-json together',
              file=sys.stderr)
        return 1
    if args.guest_aarch64_archive:
        extra['aarch64'] = (args.guest_aarch64_archive, args.guest_aarch64_release_json)
    try:
        manifest = build_manifest(args.version, images, args.guest_archive, args.guest_release_json,
                                  args.bundle, args.data_schema, notes_url=args.notes_url,
                                  extra_guests=extra)
    except (OSError, ValueError, KeyError) as error:
        print('make_manifest: ' + str(error), file=sys.stderr)
        return 1
    Path(args.out).write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
    print(args.out)
    return 0


if __name__ == '__main__':
    sys.exit(main())
