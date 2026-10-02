#!/usr/bin/env python3
"""Create a review-only source directory from a committed explicit allowlist.

This never publishes, modifies Git history or exports untracked working data.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def selected_files():
    policy = json.loads((ROOT / 'deploy/open-source-allowlist.json').read_text())
    tracked = subprocess.check_output(['git', 'ls-files'], cwd=ROOT, text=True).splitlines()
    files = [p for p in tracked if p in policy['files'] or any(p.startswith(prefix) for prefix in policy['prefixes'])]
    for name in files:
        p = Path(name)
        if p.is_absolute() or '..' in p.parts or (ROOT / p).is_symlink():
            raise ValueError('unsafe selected path: ' + name)
        if any(name.startswith(prefix) for prefix in policy['forbidden_prefixes']):
            raise ValueError('allowlist overlaps private material: ' + name)
        if any(part in {'node_modules', '.git', 'data', 'secrets', 'credentials', 'dist', 'build'} for part in p.parts):
            raise ValueError('runtime/generated material selected: ' + name)
    return sorted(files)


def export(destination):
    files = selected_files()
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip():
        raise ValueError('commit the reviewed working tree before exporting')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    if destination.exists():
        raise ValueError('destination must not exist')
    destination.mkdir(parents=False, mode=0o700)
    hashes = {}
    for name in files:
        data = subprocess.check_output(['git', 'show', revision + ':' + name], cwd=ROOT)
        policy = json.loads((ROOT / "deploy/open-source-allowlist.json").read_text())
        exported_name = policy.get("renames", {}).get(name, name)
        if Path(exported_name).is_absolute() or ".." in Path(exported_name).parts: raise ValueError("unsafe export rename")
        target = destination / exported_name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        mode = subprocess.check_output(['git', 'ls-files', '-s', '--', name], cwd=ROOT, text=True).split()[0]
        if mode == '100755': target.chmod(0o755)
        hashes[exported_name] = hashlib.sha256(data).hexdigest()
    (destination / 'EXPORT-MANIFEST.json').write_text(json.dumps({
        'source_revision': revision, 'files_sha256': hashes,
        'publication_authorized': False,
        'license_status': 'No root license selected; operator must resolve distribution rights before publication',
        'review_required': 'Review source, dependencies, third-party notices and all documentation before publication',
    }, indent=2) + '\n')
    return len(files)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    if args.check:
        print(json.dumps({'selected_file_count': len(selected_files()), 'publishes': False}))
    elif args.out:
        print(json.dumps({'exported_file_count': export(args.out.resolve()), 'publishes': False}))
    else:
        parser.error('select --check or --out NEW_DIRECTORY')
