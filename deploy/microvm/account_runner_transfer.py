"""Offline Runner state import into the authoritative workspace ext4.

Uses existing e2fsprogs debugfs; never mounts the disk on the host. Unknown
external executable dependencies, unsafe links and guest collisions fail closed.
Credentials are copied as file bytes, never returned in diagnostics.
"""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import sqlite3
import tempfile
import uuid
from urllib.parse import quote

from account_capacity import AdmissionError

GUEST_ROOT = '/workspace/shared/.tofi/runner'
DISK_ROOT = '/shared/.tofi/runner'


def quoted(value):
    value = str(value)
    if any(c in value for c in ('"', '\\', '\n', '\r', '\x00')):
        raise AdmissionError('Runner path cannot be represented safely in debugfs')
    return '"' + value + '"'


class RunnerTransfer:
    def __init__(self, command):
        self.command = command

    def debug(self, disk, instruction, write=False):
        args = ['debugfs'] + (['-w'] if write else []) + ['-R', instruction, str(disk)]
        result = self.command(args, check=False)
        if result.returncode:
            raise AdmissionError('offline Runner filesystem operation failed')
        return (result.stdout or b'').decode(errors='replace') if isinstance(result.stdout, bytes) else result.stdout or ''

    def exists(self, disk, path):
        return bool(re.search(r'Inode:\s*\d+\s+Type:', self.debug(disk, 'stat ' + quoted(path))))

    def import_attachments(self, disk, data_root):
        """Preserve attachment IDs while moving bytes into the same disk quota.

        Source upload files are retained as rollback evidence. The current DB
        is updated only after every guest alias has been read back and verified.
        Retry after a lost SQLite commit verifies/reuses the exact same bytes.
        """
        data_root = Path(data_root)
        database = data_root / 'tofi.db'
        if database.is_symlink() or not database.is_file():
            raise AdmissionError('existing authoritative App database required')
        db = sqlite3.connect('file:' + quote(str(database), safe='/') + '?mode=rw', uri=True, isolation_level=None)
        try:
            db.execute('PRAGMA synchronous=FULL'); db.execute('BEGIN IMMEDIATE')
            if not db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='attachments'").fetchone():
                db.execute('COMMIT'); return 0
            rows = db.execute('SELECT id,disk_name,size FROM attachments').fetchall()
            for directory in ('/shared', '/shared/.tofi', '/shared/.tofi/blobs'):
                if not self.exists(disk, directory): self.debug(disk, 'mkdir ' + quoted(directory), True)
                for field, value in (('uid', 1000), ('gid', 1000), ('mode', 0o40700)):
                    self.debug(disk, 'set_inode_field ' + quoted(directory) + ' ' + field + ' ' + str(value), True)
            with tempfile.TemporaryDirectory(prefix='tofi-attachment-transfer-') as temporary:
                verified = Path(temporary) / 'verify'
                for identity, name, size in rows:
                    if str(uuid.UUID(identity)) != identity: raise AdmissionError('invalid stored attachment identity')
                    target = '/shared/.tofi/blobs/' + identity
                    if name == 'vm:' + identity:
                        if not self.exists(disk, target): raise AdmissionError('existing guest attachment unavailable')
                        continue
                    if name != identity + '.upload': raise AdmissionError('legacy attachment ownership requires reconciliation')
                    source = data_root / 'attachments' / name
                    if source.is_symlink() or source.parent.is_symlink() or not source.is_file() or source.stat().st_size != size:
                        raise AdmissionError('legacy attachment source changed')
                    expected = hashlib.sha256(source.read_bytes()).digest()
                    if not self.exists(disk, target): self.debug(disk, 'write ' + quoted(source) + ' ' + quoted(target), True)
                    verified.unlink(missing_ok=True)
                    self.debug(disk, 'dump ' + quoted(target) + ' ' + quoted(verified))
                    if not verified.exists() or hashlib.sha256(verified.read_bytes()).digest() != expected:
                        raise AdmissionError('attachment transfer verification failed; retain original reference')
                    for field, value in (('uid', 1000), ('gid', 1000), ('mode', 0o100400)):
                        self.debug(disk, 'set_inode_field ' + quoted(target) + ' ' + field + ' ' + str(value), True)
                with disk.open('rb') as stream: os.fsync(stream.fileno())
            for identity, name, _ in rows:
                db.execute('UPDATE attachments SET disk_name=? WHERE id=? AND disk_name=?', ('vm:' + identity, identity, name))
            db.execute('COMMIT'); return len(rows)
        finally:
            db.close()

    def import_state(self, disk, source, prefix, rootfs, resume=False):
        source = Path(source)
        if source.is_symlink() or not source.is_dir() or not prefix.startswith('/') or prefix.endswith('/'):
            raise AdmissionError('trusted Runner state and absolute source prefix required')
        with tempfile.TemporaryDirectory(prefix='tofi-runner-transfer-') as temporary:
            stage = Path(temporary)
            os.chmod(stage, 0o700)
            entries = []
            for base, directories, files in os.walk(source, followlinks=False):
                for name in sorted(directories + files):
                    path = Path(base) / name
                    relative = path.relative_to(source)
                    quoted(relative)
                    if path.is_symlink():
                        # Installed npm executable links are relative and
                        # confined to this plugin tree. Absolute links require
                        # explicit dependency reconciliation before migration.
                        link = os.readlink(path)
                        if Path(link).is_absolute() or not path.resolve(strict=True).is_relative_to(source.resolve()):
                            raise AdmissionError('Runner link leaves installed state; reconcile dependency first')
                        entries.append((relative, 'link', link))
                    elif path.is_dir():
                        entries.append((relative, 'directory', None))
                    elif path.is_file():
                        target = stage / relative
                        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                        data = path.read_bytes()
                        if data.startswith(('#!' + prefix + '/').encode()):
                            first, separator, rest = data.partition(b'\n')
                            data = first.replace(prefix.encode(), GUEST_ROOT.encode(), 1) + separator + rest
                        target.write_bytes(data)
                        os.chmod(target, path.stat().st_mode & 0o777)
                        entries.append((relative, 'file', target))
                    else:
                        raise AdmissionError('Runner state contains a nonportable runtime artifact')
            manifest = stage / 'manifest.json'
            records = json.loads(manifest.read_text()) if manifest.exists() else []
            if not isinstance(records, list):
                raise AdmissionError('invalid installed Runner records')
            def rebase(value):
                if not isinstance(value, str):
                    raise AdmissionError('invalid Runner executable path')
                return GUEST_ROOT + value[len(prefix):] if value == prefix or value.startswith(prefix + '/') else value
            for record in records:
                spec = record['spec']
                for key in ('command', 'work_dir'):
                    spec[key] = rebase(spec[key])
                spec['args'] = [rebase(v) for v in spec.get('args', [])]
                spec['env'] = {key: rebase(value) for key, value in spec.get('env', {}).items()}
                spec['secret_env'] = {key: rebase(value) for key, value in spec.get('secret_env', {}).items()}
                for value in [spec['work_dir'], *spec['secret_env'].values()]:
                    if not value.startswith(GUEST_ROOT + '/') or '..' in PurePosixPath(value).parts:
                        raise AdmissionError('Runner state dependency is outside transferred plugin tree')
                if not spec['command'].startswith(GUEST_ROOT + '/'):
                    if not spec['command'].startswith('/') or not self.exists(rootfs, spec['command']):
                        raise AdmissionError('installed Runner executable is absent from selected Guest image')
            if manifest.exists():
                manifest.write_text(json.dumps(records, indent=2) + '\n')
                os.chmod(manifest, 0o600)
            # Refuse an existing Guest Runner instead of replacing credentials
            # or losing independently installed plugins. This is a cutover gate.
            if self.exists(disk, DISK_ROOT) and not resume:
                raise AdmissionError('Guest Runner state already exists; explicit state reconciliation required')
            for path in ('/shared', '/shared/.tofi', DISK_ROOT):
                if not self.exists(disk, path):
                    self.debug(disk, 'mkdir ' + quoted(path), True)
                self.debug(disk, 'set_inode_field ' + quoted(path) + ' uid 1000', True)
                self.debug(disk, 'set_inode_field ' + quoted(path) + ' gid 1000', True)
                if not self.exists(disk, path):
                    raise AdmissionError('Guest Runner directory creation was not verified')
            for relative, kind, value in entries:
                target = DISK_ROOT + '/' + relative.as_posix()
                if kind == 'directory':
                    if not self.exists(disk, target):
                        self.debug(disk, 'mkdir ' + quoted(target), True)
                    mode = 0o40700
                elif kind == 'link':
                    if self.exists(disk, target):
                        detail = self.debug(disk, 'stat ' + quoted(target))
                        if 'Fast link dest: ' + quoted(value) not in detail:
                            raise AdmissionError('Runner link collision; preserve current state')
                    else:
                        self.debug(disk, 'symlink ' + quoted(target) + ' ' + quoted(value), True)
                    mode = 0o120777
                else:
                    if not self.exists(disk, target):
                        self.debug(disk, 'write ' + quoted(value) + ' ' + quoted(target), True)
                    mode = 0o100000 | (value.stat().st_mode & 0o777)
                    verify = stage / '.verify'
                    verify.unlink(missing_ok=True)
                    self.debug(disk, 'dump ' + quoted(target) + ' ' + quoted(verify))
                    if not verify.exists() or hashlib.sha256(verify.read_bytes()).digest() != hashlib.sha256(value.read_bytes()).digest():
                        raise AdmissionError('Runner file transfer verification failed; ownership remains fenced')
                    verify.unlink()
                for field, setting in (('uid', 1000), ('gid', 1000), ('mode', mode)):
                    self.debug(disk, 'set_inode_field ' + quoted(target) + ' ' + field + ' ' + str(setting), True)
                if not self.exists(disk, target):
                    raise AdmissionError('Runner artifact transfer was not verified')
            with disk.open('rb') as stream:
                os.fsync(stream.fileno())
            return {'files': sum(kind == 'file' for _, kind, _ in entries), 'plugins': len(records)}
