#!/usr/bin/env python3
"""Read-only validation of an operator-selected account guest release candidate.

This checker proves only that the local files match the supplied candidate
manifest and operator expectations. It does not establish that a currently
running Worker, guest, production installation, or legacy data migration uses
this contract. Feature and operator metadata are descriptive, not runtime proof.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import stat
import sys
import tempfile


PROTOCOL = "tofi-account-guest-v1"
REQUIRED_CAPABILITIES = {"blob-store-v1", "runner-v1", "storage-metrics-v1"}
FILES = (
    "manager.py",
    "rootfs.ext4",
    "vmlinux",
    "bin/firecracker",
    "bin/jailer",
)
MANIFEST_KEYS = {
    "schema", "protocol", "files_sha256", "guest_binary_sha256",
    "capabilities", "features", "operator_metadata",
}
SHA256 = re.compile(r"^[0-9a-f]{64}$")


class ReleaseCheckError(ValueError):
    """Candidate release failed a local integrity or contract check."""


def _no_symlink_path(path, *, must_exist=True):
    """Return an absolute canonical path while rejecting symlink components."""
    path = Path(path)
    if not path.is_absolute():
        raise ReleaseCheckError(f"path must be absolute: {path}")
    absolute = Path(os.path.abspath(path))
    current = Path(absolute.anchor)
    for part in absolute.parts[1:]:
        current = current / part
        try:
            info = current.lstat()
        except FileNotFoundError:
            if must_exist:
                raise ReleaseCheckError(f"path does not exist: {current}")
            continue
        if stat.S_ISLNK(info.st_mode):
            raise ReleaseCheckError(f"symlink path component: {current}")
    try:
        resolved = absolute.resolve(strict=must_exist)
    except OSError as exc:
        raise ReleaseCheckError(f"cannot resolve path: {absolute}") from exc
    if resolved != absolute:
        raise ReleaseCheckError(f"path is not canonical: {absolute}")
    return absolute


def _regular_nonempty(path):
    try:
        info = path.lstat()
    except OSError as exc:
        raise ReleaseCheckError(f"required release file unavailable: {path}") from exc
    if not stat.S_ISREG(info.st_mode) or info.st_size <= 0:
        raise ReleaseCheckError(f"required release file must be regular and nonempty: {path}")


def _readonly(path, kind):
    if path.lstat().st_mode & 0o222:
        raise ReleaseCheckError(f"release {kind} must be read-only: {path}")


def _sha256(path):
    digest = hashlib.sha256()
    try:
        with path.open("rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
    except OSError as exc:
        raise ReleaseCheckError(f"cannot read file for hashing: {path}") from exc
    return digest.hexdigest()


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ReleaseCheckError(f"duplicate manifest key: {key}")
        result[key] = value
    return result


def _load_manifest(path):
    _regular_nonempty(path)
    if path.stat().st_size > 1024 * 1024:
        raise ReleaseCheckError("release manifest exceeds 1 MiB")
    try:
        with path.open("r", encoding="utf-8") as source:
            value = json.load(source, object_pairs_hook=_unique_object)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ReleaseCheckError("release manifest is not valid UTF-8 JSON") from exc
    if not isinstance(value, dict):
        raise ReleaseCheckError("release manifest must be a JSON object")
    unknown = set(value) - MANIFEST_KEYS
    if unknown:
        raise ReleaseCheckError(f"unsafe or unsupported manifest keys: {sorted(unknown)}")
    return value


def _embedded_guest_sha256(rootfs):
    """Hash the guest executable extracted from ext4 without mounting it."""
    try:
        with tempfile.TemporaryDirectory(prefix="tofi-guest-check-") as scratch:
            output = Path(scratch) / "tofi-guest"
            result = subprocess.run(
                ["debugfs", "-R", f"dump /usr/local/bin/tofi-guest {output}", str(rootfs)],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=120,
                check=False,
            )
            if result.returncode != 0 or not output.is_file():
                detail = (result.stderr or result.stdout or "debugfs could not extract guest binary")
                raise ReleaseCheckError("cannot extract guest binary from rootfs.ext4: " + detail[-500:].strip())
            _regular_nonempty(output)
            return _sha256(output)
    except FileNotFoundError as exc:
        raise ReleaseCheckError("debugfs is required to verify the embedded guest binary") from exc
    except subprocess.TimeoutExpired as exc:
        raise ReleaseCheckError("debugfs timed out extracting guest binary from rootfs.ext4") from exc


def validate_release(release_dir, manager_source, guest_sha256, expected_manifest_sha256=None):
    """Validate a local release directory without changing it or the host."""
    if not isinstance(guest_sha256, str) or not SHA256.fullmatch(guest_sha256):
        raise ReleaseCheckError("expected guest SHA-256 must be 64 lowercase hex characters")
    if (expected_manifest_sha256 is not None
            and (not isinstance(expected_manifest_sha256, str)
                 or not SHA256.fullmatch(expected_manifest_sha256))):
        raise ReleaseCheckError("expected manifest SHA-256 must be 64 lowercase hex characters")
    root = _no_symlink_path(Path(release_dir))
    if not root.is_dir():
        raise ReleaseCheckError("release path is not a directory")
    _readonly(root, "directory")
    bin_dir = root / "bin"
    _no_symlink_path(bin_dir)
    if not bin_dir.is_dir():
        raise ReleaseCheckError("release bin path is not a directory")
    _readonly(bin_dir, "directory")
    manager = _no_symlink_path(Path(manager_source))
    _regular_nonempty(manager)
    manifest_path = root / "account-release.json"
    # Validate every traversed component, including release root and manifest.
    _no_symlink_path(manifest_path)
    manifest_digest = _sha256(manifest_path)
    _regular_nonempty(manifest_path)
    _readonly(manifest_path, "manifest")
    if expected_manifest_sha256 is not None and manifest_digest != expected_manifest_sha256:
        raise ReleaseCheckError("release manifest SHA-256 does not match operator expectation")
    manifest = _load_manifest(manifest_path)
    if type(manifest.get("schema")) is not int or manifest["schema"] != 1:
        raise ReleaseCheckError("release schema must be integer 1")
    if manifest.get("protocol") != PROTOCOL:
        raise ReleaseCheckError(f"release protocol must be {PROTOCOL}")
    expected_files = manifest.get("files_sha256")
    if not isinstance(expected_files, dict) or set(expected_files) != set(FILES):
        raise ReleaseCheckError("files_sha256 must name exactly the required release files")
    for name in FILES:
        # FILES is a fixed safe allowlist; manifest-supplied paths are never opened.
        path = root / name
        _no_symlink_path(path)
        _regular_nonempty(path)
        _readonly(path, "file")
        expected = expected_files[name]
        if not isinstance(expected, str) or not SHA256.fullmatch(expected):
            raise ReleaseCheckError(f"invalid SHA-256 for {name}")
        actual = _sha256(path)
        if actual != expected:
            raise ReleaseCheckError(f"SHA-256 mismatch for {name}")
    expected_tree = set(FILES) | {"account-release.json"}
    actual_tree = set()
    for parent, dirs, names in os.walk(root, followlinks=False):
        parent_path = Path(parent)
        for directory in dirs:
            entry = parent_path / directory
            if entry.is_symlink():
                raise ReleaseCheckError(f"symlink in release bundle: {entry}")
            if entry.relative_to(root).as_posix() != "bin":
                raise ReleaseCheckError(f"unexpected directory in release bundle: {entry}")
        for name in names:
            entry = parent_path / name
            relative = entry.relative_to(root).as_posix()
            if relative not in expected_tree:
                raise ReleaseCheckError(f"unexpected file in release bundle: {relative}")
            if not stat.S_ISREG(entry.lstat().st_mode):
                raise ReleaseCheckError(f"release entry must be a regular file: {relative}")
            actual_tree.add(relative)
    if actual_tree != expected_tree:
        raise ReleaseCheckError("release bundle does not contain exactly the pinned files")
    guest_digest = manifest.get("guest_binary_sha256")
    if not isinstance(guest_digest, str) or not SHA256.fullmatch(guest_digest):
        raise ReleaseCheckError("manifest guest_binary_sha256 must be 64 lowercase hex characters")
    if guest_digest != guest_sha256:
        raise ReleaseCheckError("guest binary SHA-256 does not match operator expectation")
    embedded_guest_digest = _embedded_guest_sha256(root / "rootfs.ext4")
    if guest_digest != embedded_guest_digest:
        raise ReleaseCheckError("guest binary SHA-256 does not match binary embedded in rootfs.ext4")
    capabilities = manifest.get("capabilities")
    if (not isinstance(capabilities, list)
            or any(not isinstance(item, str) for item in capabilities)
            or not REQUIRED_CAPABILITIES.issubset(capabilities)):
        raise ReleaseCheckError("release is missing required account guest capabilities")
    for optional in ("features", "operator_metadata"):
        if optional in manifest and not isinstance(manifest[optional], (dict, list, str)):
            raise ReleaseCheckError(f"{optional} must be descriptive JSON metadata")
    source_digest = _sha256(manager)
    if source_digest != expected_files["manager.py"]:
        raise ReleaseCheckError("operator manager source does not match candidate manager.py")
    return {
        "valid": True,
        "release_dir": str(root),
        "protocol": PROTOCOL,
        "schema": 1,
        "files_sha256": dict(expected_files),
        "guest_binary_sha256": guest_digest,
        "manifest_sha256": manifest_digest,
        "capabilities": sorted(REQUIRED_CAPABILITIES),
        "manager_source_sha256": source_digest,
        "runtime_verified": False,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-dir", required=True, help="absolute candidate release directory")
    parser.add_argument("--manager-source", required=True, help="absolute operator checkout manager.py")
    parser.add_argument("--guest-sha256", required=True, help="expected guest artifact SHA-256")
    parser.add_argument("--manifest-sha256", help="expected account-release.json SHA-256")
    args = parser.parse_args(argv)
    try:
        result = validate_release(args.release_dir, args.manager_source, args.guest_sha256,
                                  args.manifest_sha256)
    except ReleaseCheckError as exc:
        print(json.dumps({"valid": False, "error": str(exc)}), file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
