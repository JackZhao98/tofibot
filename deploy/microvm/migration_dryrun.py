#!/usr/bin/env python3
"""Read-only inventory and destination plan for a legacy-owner migration.

This tool never copies data, changes services, or writes into the destination.
It reads a legacy Tofi data directory, personal computer JSON, and Runner
volumes, then emits a preservation manifest and deterministic ownership map.
"""

import argparse
import hashlib
import json
import os
import sqlite3
import sys
import uuid
from pathlib import Path
from urllib.parse import quote


OWNER_NAMESPACE = uuid.UUID("79d8c04b-3d65-4b99-97bf-f4e8a5630d47")
DB_NAMES = ("tofi.db", "tofi.sqlite", "app.db")


class DryRunError(ValueError):
    pass


def _real_directory(path, label):
    path = Path(path).expanduser().absolute()
    if path.is_symlink() or not path.is_dir():
        raise DryRunError(f"{label} must be an existing real directory: {path}")
    return path.resolve(strict=True)


def _real_file(path, label):
    path = Path(path).expanduser().absolute()
    if path.is_symlink() or not path.is_file():
        raise DryRunError(f"{label} must be an existing regular file: {path}")
    return path.resolve(strict=True)


def _hash_file(path):
    path = Path(path)
    before = path.stat()
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    after = path.stat()
    identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if identity(before) != identity(after):
        raise DryRunError(f"source file changed while hashing: {path}")
    return digest.hexdigest()


def _tree_snapshot(root):
    """Capture paths and metadata so additions/removals/writes during a run fail closed."""
    result = {}
    for parent, dirs, files in os.walk(root, followlinks=False):
        base = Path(parent)
        for name in dirs:
            path = base / name
            if path.is_symlink():
                raise DryRunError(f"symlink in source tree: {path}")
            info = path.stat()
            result[path.relative_to(root).as_posix() + "/"] = (info.st_dev, info.st_ino, info.st_size,
                                                                  info.st_mtime_ns, info.st_ctime_ns)
        for name in files:
            path = base / name
            info = path.lstat()
            if path.is_symlink() or not path.is_file():
                raise DryRunError(f"non-regular source asset: {path}")
            result[path.relative_to(root).as_posix()] = (info.st_dev, info.st_ino, info.st_size,
                                                          info.st_mtime_ns, info.st_ctime_ns)
    return result


def _inventory(root, *, exclude=()):
    """Inventory tree contents without following symlinks."""
    excluded = {Path(item).resolve(strict=False) for item in exclude}
    entries = []
    for parent, dirs, files in os.walk(root, followlinks=False):
        base = Path(parent)
        for name in list(dirs):
            path = base / name
            if path.is_symlink():
                raise DryRunError(f"symlink in source tree: {path}")
        for name in sorted(files):
            path = base / name
            if path.resolve(strict=False) in excluded:
                continue
            info = path.lstat()
            if not path.is_file() or path.is_symlink():
                raise DryRunError(f"non-regular source asset: {path}")
            entries.append({
                "path": path.relative_to(root).as_posix(),
                "size_bytes": info.st_size,
                "allocated_bytes": info.st_blocks * 512,
                "sha256": _hash_file(path),
            })
    return sorted(entries, key=lambda entry: entry["path"])


def _database(data_dir):
    found = [data_dir / name for name in DB_NAMES if (data_dir / name).is_file()]
    if len(found) != 1:
        raise DryRunError(f"expected exactly one legacy database ({', '.join(DB_NAMES)}); found {len(found)}")
    db_path = _real_file(found[0], "legacy database")
    # immutable=1 prevents SQLite from creating or updating -shm/-wal sidecars.
    # Reject WAL data because immutable mode intentionally ignores it.
    wal = Path(str(db_path) + "-wal")
    if wal.exists() and wal.stat().st_size:
        raise DryRunError(f"blocked: legacy database has a non-empty WAL ({wal.stat().st_size} bytes): {wal}; a safe quiesced source snapshot is required. This command creates no backup or copy")
    uri = "file:" + quote(str(db_path), safe="/") + "?mode=ro&immutable=1"
    try:
        db = sqlite3.connect(uri, uri=True)
        db.row_factory = sqlite3.Row
        integrity = db.execute("PRAGMA integrity_check").fetchone()[0]
        if integrity != "ok":
            raise DryRunError(f"SQLite integrity_check failed: {integrity}")
        tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        if "workspace_owner" not in tables:
            raise DryRunError("legacy database is missing workspace_owner")
        owner_rows = db.execute("SELECT id,username,email FROM workspace_owner ORDER BY id").fetchall()
        if len(owner_rows) != 1:
            raise DryRunError(f"expected exactly one legacy owner for stable mapping; found {len(owner_rows)}")
        owner = dict(owner_rows[0])
        if owner["id"] != 1:
            raise DryRunError(f"legacy owner row has id {owner['id']}; gateway import requires workspace_owner.id=1")
        if not str(owner["username"]).strip() or not str(owner["email"]).strip():
            raise DryRunError("legacy owner username/email is empty; account identity cannot be resolved")
        account_id = "legacy-owner"
        if "accounts" in tables:
            accounts = db.execute("SELECT id FROM accounts WHERE legacy=1 ORDER BY id").fetchall()
            if len(accounts) > 1:
                raise DryRunError(f"expected at most one imported legacy account; found {len(accounts)}")
            if accounts:
                account_id = accounts[0]["id"]
        attachments = []
        if "attachments" in tables:
            columns = {row[1] for row in db.execute("PRAGMA table_info(attachments)")}
            required = {"id", "conversation_id", "name", "mime", "size", "disk_name", "created_at"}
            if not required.issubset(columns):
                raise DryRunError("attachments table does not match the legacy attachment metadata schema")
            attachments = [dict(row) for row in db.execute("SELECT id,disk_name,size FROM attachments ORDER BY id")]
        db.close()
    except sqlite3.Error as exc:
        raise DryRunError(f"cannot inspect legacy database read-only: {exc}") from exc
    return db_path, owner, account_id, attachments


def _attachment_checks(data_dir, attachments):
    root = data_dir / "attachments"
    checks, problems = [], []
    seen_names = set()
    for row in attachments:
        name = row["disk_name"]
        if (not isinstance(name, str) or not name or Path(name).name != name or
                name in (".", "..") or "\\" in name):
            problems.append(f"unsafe attachment disk_name for id {row['id']!r}: {name!r}")
            continue
        if name in seen_names:
            problems.append(f"duplicate attachment disk_name: {name}")
            continue
        seen_names.add(name)
        path = root / name
        if path.is_symlink() or not path.is_file():
            problems.append(f"attachment file missing or unsafe: {path}")
            continue
        size = path.stat().st_size
        if size != row["size"]:
            problems.append(f"attachment size mismatch for {name}: database={row['size']} file={size}")
        checks.append({"id": row["id"], "path": path.relative_to(data_dir).as_posix(),
                       "database_size_bytes": row["size"], "actual_size_bytes": size,
                       "sha256": _hash_file(path)})
    if root.exists():
        if root.is_symlink() or not root.is_dir():
            problems.append(f"attachment root is not a real directory: {root}")
        else:
            actual = {p.name for p in root.iterdir() if p.is_file() and not p.is_symlink()}
            orphans = sorted(actual - seen_names)
            if orphans:
                problems.append("orphan attachment files: " + ", ".join(orphans))
            unsafe = [p.name for p in root.iterdir() if p.is_symlink() or not p.is_file()]
            if unsafe:
                problems.append("non-regular attachment entries: " + ", ".join(sorted(unsafe)))
    elif attachments:
        problems.append(f"attachment directory missing: {root}")
    return checks, problems


def _computer(config_path):
    config_path = _real_file(config_path, "personal computer config")
    try:
        before = config_path.stat()
        config = json.loads(config_path.read_text(encoding="utf-8"))
        after = config_path.stat()
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise DryRunError(f"cannot read personal computer config: {exc}") from exc
    identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if identity(before) != identity(after):
        raise DryRunError(f"source file changed while reading computer config: {config_path}")
    if config.get("id") != "personal":
        raise DryRunError("personal computer config must have id='personal'")
    state_dir = _real_directory(config.get("state_dir", ""), "personal workspace state_dir")
    disk = _real_file(state_dir / "workspace.ext4", "personal workspace disk")
    return config_path, config, state_dir, disk


def _planned_path(root, rel):
    target = root.joinpath(*Path(rel).parts)
    if target.exists() or target.is_symlink():
        raise DryRunError(f"destination collision: {target}")
    return str(target)


def build_report(data_dir, personal_config, runner_state, runner_auth, destination):
    data_dir = _real_directory(data_dir, "legacy data directory")
    personal_path, personal, workspace_root, workspace_disk = _computer(personal_config)
    runner_state = _real_directory(runner_state, "Runner state volume")
    runner_auth = _real_directory(runner_auth, "Runner auth volume")
    roots = [data_dir, workspace_root, runner_state, runner_auth]
    snapshots = {str(root): _tree_snapshot(root) for root in roots}
    config_stat = personal_path.stat()
    snapshots[str(personal_path)] = {personal_path.name: (
        config_stat.st_dev, config_stat.st_ino, config_stat.st_size,
        config_stat.st_mtime_ns, config_stat.st_ctime_ns)}

    db_path, owner, account_id, attachments = _database(data_dir)
    # accounts.go imports workspace_owner as the literal legacy-owner ID.
    # UUID is only a proposed future computer key and is not an adopted mapping.
    proposed_computer_uuid = str(uuid.uuid5(OWNER_NAMESPACE, "legacy-account:" + account_id))

    destination = Path(destination).expanduser().absolute()
    if destination.is_symlink():
        raise DryRunError(f"destination root is a symlink: {destination}")
    # Canonicalize existing parent aliases (for example macOS /tmp -> /private/tmp)
    # so collision checks inspect the actual target tree.
    destination = destination.resolve(strict=False)
    if destination.exists() and not destination.is_dir():
        raise DryRunError("destination root must be absent or an existing real directory")
    dest_owner = destination / "owners" / proposed_computer_uuid
    attachment_checks, attachment_problems = _attachment_checks(data_dir, attachments)
    db_files = _inventory(data_dir)
    workspace_entries = _inventory(workspace_root)
    runner_state_entries = _inventory(runner_state)
    runner_auth_entries = _inventory(runner_auth)
    config_manifest = {"path": str(personal_path), "size_bytes": personal_path.stat().st_size,
                       "sha256": _hash_file(personal_path)}

    sources = [
        ("legacy_app_data", data_dir, "data"),
        ("personal_workspace", workspace_root, "computer/workspace"),
        ("runner_state", runner_state, "runner/state"),
        ("runner_auth", runner_auth, "runner/auth"),
        ("computer_metadata", personal_path, "computer/metadata/personal.json"),
    ]
    planned = []
    copied_roots = []
    for category, source, relative_destination in sources:
        source = source.resolve(strict=True)
        parent = next((item for item in reversed(copied_roots)
                       if source == item["source"] or item["source"] in source.parents), None)
        if parent:
            parent["entry"].setdefault("included_assets", []).append({"category": category, "source": str(source)})
            continue
        entry = {"source": str(source), "destination": _planned_path(dest_owner, relative_destination),
                 "category": category}
        planned.append(entry)
        copied_roots.append({"source": source, "entry": entry})
    path_collisions = len({entry["destination"] for entry in planned}) != len(planned)
    if path_collisions:
        raise DryRunError("internal destination path collision in migration plan")

    for root in roots:
        if snapshots[str(root)] != _tree_snapshot(root):
            raise DryRunError(f"source tree changed during dry-run: {root}")
    current_config = personal_path.stat()
    config_snapshot = snapshots[str(personal_path)][personal_path.name]
    if config_snapshot != (current_config.st_dev, current_config.st_ino, current_config.st_size,
                           current_config.st_mtime_ns, current_config.st_ctime_ns):
        raise DryRunError(f"source file changed during dry-run: {personal_path}")

    unresolved = [
        {"domain": "personal_computer_handoff", "source_identity": "personal", "owner_uuid": None,
         "reason": "computer JSON identifies the asset but has no verified legacy-owner binding"},
        {"domain": "runner_state_handoff", "source_identity": str(runner_state), "owner_uuid": None,
         "reason": "Runner state may contain account credentials; per-user ownership and authorization are not recorded by the volume"},
        {"domain": "runner_auth_handoff", "source_identity": str(runner_auth), "owner_uuid": None,
         "reason": "Runner service token volume is deployment identity; destination secret provisioning/rotation is unresolved"},
    ]
    all_ok = not attachment_problems
    return {
        "schema": 1,
        "mode": "read-only-dry-run",
        "source_stability_check": "file stat before/after hashing plus complete tree metadata before/after scan",
        "production_changed": False,
        "source_mutated": False,
        "destination_written": False,
        "destination_root": str(destination),
        "legacy_owner": {"source_row_id": owner["id"], "account_id": account_id,
                         "username": owner["username"], "email": owner["email"]},
        "proposed_computer_uuid": proposed_computer_uuid,
        "proposed_computer_uuid_basis": "UUIDv5 of the gateway legacy account ID; proposed only, not an adopted account or handoff mapping",
        "integrity": {"sqlite": "ok", "attachments": "ok" if all_ok else "mismatch",
                      "attachment_checks": attachment_checks, "problems": attachment_problems},
        "preservation_manifest": {
            "legacy_app_data": {"root": str(data_dir), "files": db_files},
            "personal_workspace": {"computer_config": config_manifest, "state_dir": str(workspace_root),
                                   "workspace_disk": str(workspace_disk), "files": workspace_entries},
            "runner_state": {"root": str(runner_state), "files": runner_state_entries},
            "runner_auth": {"root": str(runner_auth), "files": runner_auth_entries},
        },
        "destination_plan": planned,
        "handoff_ownership": {"status": "unresolved", "items": unresolved},
        "activation": {"state": "not-planned", "reason": "dry-run only; ownership handoff and an execution procedure remain unapproved"},
        "ready_for_copy": False,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data-dir", required=True, help="legacy Tofi data directory")
    parser.add_argument("--personal-config", required=True, help="personal computer JSON metadata")
    parser.add_argument("--runner-state", required=True, help="Runner state volume directory")
    parser.add_argument("--runner-auth", required=True, help="Runner auth volume directory")
    parser.add_argument("--destination", required=True, help="planned destination root (never written)")
    args = parser.parse_args(argv)
    try:
        report = build_report(args.data_dir, args.personal_config, args.runner_state, args.runner_auth, args.destination)
    except (DryRunError, OSError) as exc:
        print(json.dumps({"schema": 1, "mode": "read-only-dry-run", "ready_for_copy": False,
                          "production_changed": False, "error": str(exc)}, sort_keys=True, indent=2), file=sys.stderr)
        return 2
    print(json.dumps(report, sort_keys=True, indent=2))
    return 0 if not report["integrity"]["problems"] else 3


if __name__ == "__main__":
    raise SystemExit(main())
