"""Shared ownership records for offline legacy adoption and the existing broker.

No lifecycle action is exposed through the App API. The operator transition
uses the same capacity ledger and manager, rather than a second supervisor.
"""
import os
from pathlib import Path
import uuid

from account_capacity import AdmissionError, _trusted_regular_path, _ext4_size

NAMESPACE = uuid.UUID('79d8c04b-3d65-4b99-97bf-f4e8a5630d47')


def stable_identity(instance):
    if str(uuid.UUID(instance)) != instance:
        raise AdmissionError('canonical installation UUID required')
    return str(uuid.uuid5(NAMESPACE, instance + ':legacy-owner'))


def initialize(db):
    db.execute('''CREATE TABLE IF NOT EXISTS legacy_adoptions(
        account_id TEXT PRIMARY KEY, instance_id TEXT NOT NULL UNIQUE,
        asset_id TEXT NOT NULL UNIQUE, source_disk TEXT NOT NULL,
        source_config TEXT NOT NULL, device INTEGER NOT NULL, inode INTEGER NOT NULL,
        quota_bytes INTEGER NOT NULL, phase TEXT NOT NULL,
        runner_ready INTEGER NOT NULL DEFAULT 0,
        CHECK(phase IN ('pending','worker','rollback','legacy')))''')
    if 'runner_ready' not in {r[1] for r in db.execute('PRAGMA table_info(legacy_adoptions)')}:
        db.execute('ALTER TABLE legacy_adoptions ADD COLUMN runner_ready INTEGER NOT NULL DEFAULT 0')


def proof(broker, identity):
    with broker.ledger.connection() as db:
        row = db.execute('SELECT * FROM legacy_adoptions WHERE account_id=?', (identity,)).fetchone()
        computer = db.execute('SELECT * FROM computers WHERE account_id=?', (identity,)).fetchone()
    if not row or row['phase'] not in ('worker', 'legacy'):
        raise AdmissionError('legacy ownership transition is unresolved')
    if not row['runner_ready']:
        raise AdmissionError('legacy Guest Runner transfer is not verified')
    if stable_identity(row['instance_id']) != identity:
        raise AdmissionError('legacy installation mapping changed')
    parent = Path(row['source_config']).parent
    if not parent.is_dir() or parent.is_symlink():
        raise AdmissionError('old configuration directory view required for ownership proof')
    if row['phase'] == 'worker':
        if not computer:
            raise AdmissionError('adopted computer reservation missing')
        disk = broker._disk_path(identity)
        quota = computer['quota_bytes']
        # The old service cannot restart with its original ExecStart config.
        if any(Path(p).exists() or Path(p).is_symlink() for p in (row['source_config'], row['source_disk'])):
            raise AdmissionError('old owner fence is missing')
    else:
        disk = Path(row['source_disk'])
        quota = row['quota_bytes']
        if not Path(row['source_config']).is_file() or computer:
            raise AdmissionError('legacy rollback ownership is unresolved')
    info = _trusted_regular_path(disk)
    if (info.st_dev, info.st_ino) != (row['device'], row['inode']) or info.st_size != quota or _ext4_size(disk) != quota:
        raise AdmissionError('adopted disk identity or geometry changed')
    return dict(account_id=identity, instance_id=row['instance_id'], asset_id=row['asset_id'],
                phase=row['phase'], quota_bytes=quota, verified=True)


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
