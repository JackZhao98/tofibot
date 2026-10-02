#!/usr/bin/env python3
"""Assemble a new immutable account guest release from a build output directory."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys

import account_release_check as checker


def _fsync_directory(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _copy_regular(source, destination, mode):
    checker._no_symlink_path(source)
    checker._regular_nonempty(source)
    before = source.stat()
    digest = hashlib.sha256()
    zero_block = bytes(1024 * 1024)
    with source.open("rb") as incoming, destination.open("xb") as outgoing:
        for block in iter(lambda: incoming.read(1024 * 1024), b""):
            digest.update(block)
            if block == zero_block[:len(block)]:
                # Hash every logical byte, but leave wholly zero chunks sparse.
                # A final zero chunk needs truncate to preserve its logical EOF.
                outgoing.seek(len(block), os.SEEK_CUR)
            else:
                outgoing.write(block)
        outgoing.truncate(outgoing.tell())
        outgoing.flush()
        os.fsync(outgoing.fileno())
    after = source.stat()
    if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) != (
            after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns):
        raise checker.ReleaseCheckError(f"source changed during assembly: {source}")
    os.chmod(destination, mode)
    return digest.hexdigest()


def assemble_release(source_dir, release_dir, *, guest_binary=None):
    """Create a sealed release at a previously unused absolute path.

    ``guest_binary`` is optional provenance input from the same image build. It
    is compared with the binary extracted from the ext4 image; it never bypasses
    extraction from the packaged rootfs.
    """
    source = checker._no_symlink_path(Path(source_dir))
    if not source.is_dir():
        raise checker.ReleaseCheckError("source path is not a directory")
    destination = Path(release_dir)
    if not destination.is_absolute() or destination == Path("/"):
        raise checker.ReleaseCheckError("release destination must be an absolute non-root path")
    parent = checker._no_symlink_path(destination.parent)
    destination = parent / destination.name
    if destination.exists() or destination.is_symlink():
        raise checker.ReleaseCheckError("release destination must not already exist")
    if not destination.name or destination.name in (".", ".."):
        raise checker.ReleaseCheckError("invalid release destination")

    guest_source = None
    if guest_binary is not None:
        guest_source = checker._no_symlink_path(Path(guest_binary))
        checker._regular_nonempty(guest_source)

    # mkdir is the exclusive reservation: never replace an existing release,
    # including one created concurrently after the initial inspection.
    try:
        destination.mkdir(mode=0o700)
    except FileExistsError as exc:
        raise checker.ReleaseCheckError("release destination must not already exist") from exc
    staging = destination
    try:
        (staging / "bin").mkdir(mode=0o700)
        file_hashes = {}
        for name in checker.FILES:
            target = staging / name
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            mode = 0o555 if name.startswith("bin/") else 0o444
            file_hashes[name] = _copy_regular(source / name, target, mode)

        guest_digest = checker._embedded_guest_sha256(staging / "rootfs.ext4")
        if guest_source is not None and checker._sha256(guest_source) != guest_digest:
            raise checker.ReleaseCheckError(
                "supplied guest binary does not match binary embedded in rootfs.ext4")
        manifest = {
            "schema": 1,
            "protocol": checker.PROTOCOL,
            "files_sha256": file_hashes,
            "guest_binary_sha256": guest_digest,
            "capabilities": sorted(checker.REQUIRED_CAPABILITIES),
            "features": [],
            "operator_metadata": {},
        }
        manifest_bytes = (json.dumps(manifest, sort_keys=True, separators=(",", ":")) + "\n").encode()
        manifest_path = staging / "account-release.json"
        with manifest_path.open("xb") as output:
            output.write(manifest_bytes)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(manifest_path, 0o444)
        _fsync_directory(staging / "bin")
        os.chmod(staging / "bin", 0o555)
        _fsync_directory(staging)
        os.chmod(staging, 0o555)
        _fsync_directory(parent)

        _fsync_directory(parent)

        manifest_digest = hashlib.sha256(manifest_bytes).hexdigest()
        checker.validate_release(destination, source / "manager.py", guest_digest,
                                 expected_manifest_sha256=manifest_digest)
        return {
            "release_dir": str(destination),
            "guest_binary_sha256": guest_digest,
            "manifest_sha256": manifest_digest,
            "files_sha256": file_hashes,
            "immutable": True,
        }
    except BaseException:
        # Remove only the destination directory this invocation reserved.
        if destination.exists() and not destination.is_symlink():
            for directory, subdirs, files in os.walk(destination):
                os.chmod(directory, 0o700)
                for name in files:
                    path = Path(directory) / name
                    if not path.is_symlink():
                        os.chmod(path, 0o600)
            shutil.rmtree(destination, ignore_errors=True)
            _fsync_directory(parent)
        raise


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-dir", required=True, help="absolute build output with the five fixed files")
    parser.add_argument("--release-dir", required=True, help="new absolute release directory")
    parser.add_argument("--guest-binary", help="optional build guest binary to cross-check against ext4")
    args = parser.parse_args(argv)
    try:
        result = assemble_release(args.source_dir, args.release_dir, guest_binary=args.guest_binary)
    except (checker.ReleaseCheckError, OSError) as exc:
        print(json.dumps({"valid": False, "error": str(exc)}), file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
