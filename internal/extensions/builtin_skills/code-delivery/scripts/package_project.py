#!/usr/bin/env python3
"""Package a project directory into a zip and verify the archive.

Leaves out version control data, dependency and build folders, caches, editor
files and anything that looks like a secret (.env files, private keys,
credentials). Every exclusion is listed. After writing, the archive is reopened:
its integrity is tested and its entries are compared with the files selected,
then the file count, size and SHA-256 are printed. Entries sit under a single
top-level folder named after the project. A missing README is a warning.
Usage: package_project.py PROJECT_DIR OUT.zip [--exclude GLOB ...] [--name FOLDER]
Exit status 1 when verification fails.
"""
from __future__ import annotations

import argparse
import fnmatch
import hashlib
import os
import sys
import zipfile

SKIP_DIRS = {".git", ".hg", ".svn", "node_modules", "__pycache__", ".venv", "venv", ".mypy_cache", ".pytest_cache", ".idea", ".vscode",
             "dist", "build", "target", ".next", ".cache", ".gradle", ".tox"}
SKIP_FILES = ["*.pyc", ".DS_Store", "Thumbs.db", "*.swp", "*.log"]
SECRET_FILES = [".env", ".env.*", "*.pem", "*.key", "id_rsa", "id_rsa.*", "id_ed25519", "id_ed25519.*", "credentials*", "*.p12", "*.pfx", ".netrc", ".npmrc", ".pypirc"]
FIXED_TIME = (2020, 1, 1, 0, 0, 0)


def select(root, extra):
    chosen, excluded = [], []
    for folder, dirs, files in os.walk(root):
        rel_folder = os.path.relpath(folder, root)
        keep = []
        for name in sorted(dirs):
            rel = os.path.normpath(os.path.join(rel_folder, name))
            if name in SKIP_DIRS or os.path.islink(os.path.join(folder, name)):
                excluded.append((rel + "/", "folder"))
            else:
                keep.append(name)
        dirs[:] = keep
        for name in sorted(files):
            full = os.path.join(folder, name)
            rel = os.path.normpath(os.path.join(rel_folder, name))
            if any(fnmatch.fnmatch(name, p) for p in SECRET_FILES):
                excluded.append((rel, "possible secret"))
            elif any(fnmatch.fnmatch(name, p) for p in SKIP_FILES) or any(fnmatch.fnmatch(rel, p) or fnmatch.fnmatch(name, p) for p in extra):
                excluded.append((rel, "excluded pattern"))
            elif os.path.islink(full) or not os.path.isfile(full):
                excluded.append((rel, "not a regular file"))
            else:
                chosen.append(rel)
    return sorted(chosen), excluded


def package(root, out, extra=(), name=None):
    root = os.path.abspath(root)
    top = name or os.path.basename(root.rstrip(os.sep)) or "project"
    chosen, excluded = select(root, extra)
    out_abs = os.path.abspath(out)
    chosen = [c for c in chosen if os.path.join(root, c) != out_abs]
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as archive:
        for rel in chosen:
            info = zipfile.ZipInfo(top + "/" + rel.replace(os.sep, "/"), FIXED_TIME)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (os.stat(os.path.join(root, rel)).st_mode & 0o777) << 16
            with open(os.path.join(root, rel), "rb") as handle:
                archive.writestr(info, handle.read())
    problems = []
    with zipfile.ZipFile(out) as archive:
        bad = archive.testzip()
        if bad:
            problems.append("corrupt entry: " + bad)
        listed = sorted(n[len(top) + 1:] for n in archive.namelist())
        if listed != sorted(c.replace(os.sep, "/") for c in chosen):
            problems.append("archive entries differ from the selected files")
    digest = hashlib.sha256()
    with open(out, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    warnings = []
    if not any(os.path.basename(c).lower().startswith("readme") for c in chosen):
        warnings.append("no README in the project")
    return {"top": top, "files": chosen, "excluded": excluded, "bytes": os.path.getsize(out), "sha256": digest.hexdigest(),
            "problems": problems, "warnings": warnings}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("project")
    parser.add_argument("out")
    parser.add_argument("--exclude", action="append", default=[])
    parser.add_argument("--name")
    args = parser.parse_args(argv)
    if not os.path.isdir(args.project):
        print("not a directory: %s" % args.project, file=sys.stderr)
        return 2
    result = package(args.project, args.out, args.exclude, args.name)
    print("archive: %s (%d bytes, sha256 %s)" % (args.out, result["bytes"], result["sha256"]))
    print("top-level folder: %s/" % result["top"])
    print("files: %d" % len(result["files"]))
    for rel in result["files"]:
        print("  + " + rel)
    for rel, why in result["excluded"]:
        print("  - %s (%s)" % (rel, why))
    for text in result["warnings"]:
        print("WARNING: " + text)
    for text in result["problems"]:
        print("ERROR: " + text)
    return 1 if result["problems"] else 0


if __name__ == "__main__":
    sys.exit(main())
