"""Resumable deletion of one Worker computer. Never removes App data or images.

The API handler's broker lock serializes this journal with admission and resize.
The journal survives a missing response, a Worker restart, and partial unlink.
"""
import contextlib
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import uuid

from account_capacity import AdmissionError, account_id


def initialize(db):
    db.execute("""CREATE TABLE IF NOT EXISTS computer_lifecycle(
        account_id TEXT PRIMARY KEY, generation TEXT NOT NULL,
        state TEXT NOT NULL, operation_id TEXT, phase TEXT NOT NULL DEFAULT '',
        error TEXT NOT NULL DEFAULT '')""")
    db.execute("""CREATE TABLE IF NOT EXISTS computer_operations(
        operation_id TEXT PRIMARY KEY, account_id TEXT NOT NULL,
        generation TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL,
        manifest TEXT, quota_gib INTEGER NOT NULL DEFAULT 0, original_state TEXT,
        created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)""")
    if 'quota_gib' not in {r[1] for r in db.execute('PRAGMA table_info(computer_operations)')}:
        db.execute('ALTER TABLE computer_operations ADD COLUMN quota_gib INTEGER NOT NULL DEFAULT 0')
    if 'original_state' not in {r[1] for r in db.execute('PRAGMA table_info(computer_operations)')}:
        db.execute('ALTER TABLE computer_operations ADD COLUMN original_state TEXT')


def assert_active(db, identity):
    row = db.execute("SELECT state FROM computer_lifecycle WHERE account_id=?", (identity,)).fetchone()
    if row and row[0] != "active":
        raise AdmissionError("computer deletion fenced; explicit recreation required")


def status(broker, identity):
    with broker.ledger.connection() as db:
        row = db.execute("SELECT * FROM computers WHERE account_id=?", (identity,)).fetchone()
        if row:
            db.execute("INSERT OR IGNORE INTO computer_lifecycle(account_id,generation,state) VALUES(?,?,'active')",
                       (identity, str(uuid.uuid4())))
        life = db.execute("SELECT * FROM computer_lifecycle WHERE account_id=?", (identity,)).fetchone()
        legacy = db.execute("SELECT 1 FROM legacy_adoptions WHERE account_id=?", (identity,)).fetchone()
        resizing = db.execute("SELECT 1 FROM resize_fences WHERE account_id=?", (identity,)).fetchone()
    if not life:
        raise AdmissionError("unregistered account computer")
    out = dict(life)
    out.update(supported=broker.supports_computer_deletion and not bool(legacy or resizing), slot=row['slot'] if row else 0,
               quota_bytes=row['quota_bytes'] if row else 0,
               resources_released=life['state'] == 'deleted')
    if legacy:
        out['error'] = 'adopted legacy computer deletion requires a separate ownership workflow'
    elif resizing:
        out['error'] = 'offline workspace resize unresolved; complete its existing operation before deletion'
    elif not broker.supports_computer_deletion:
        out['error'] = 'computer deletion requires an isolated Worker; host systemd Broker is unsupported'
    return out


@contextlib.contextmanager
def directory(path):
    """Pin every ancestor without following symlinks, including the root itself."""
    path = Path(path)
    if not path.is_absolute() or str(path) == '/' or Path(os.path.abspath(path)) != path:
        raise AdmissionError('noncanonical cleanup root')
    fd = os.open('/', os.O_RDONLY | os.O_DIRECTORY)
    try:
        for part in path.parts[1:]:
            next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = next_fd
        yield fd
    finally:
        os.close(fd)


def _record(root, relative, info, shared=False):
    return dict(root=root, path=relative, device=info.st_dev, inode=info.st_ino,
                mode=info.st_mode, uid=info.st_uid, gid=info.st_gid,
                size=info.st_size, links=info.st_nlink, shared=shared)


def _digest(path):
    digest=hashlib.sha256()
    with open(path,'rb') as stream:
        for chunk in iter(lambda:stream.read(1024*1024),b''):
            digest.update(chunk)
    return digest.digest()


def _allowed_state(path, identity):
    jail = 'jails/firecracker/ac-' + identity
    directories = {'', 'jails', 'jails/firecracker', jail, jail+'/root',
                   jail+'/root/run', jail+'/root/dev', jail+'/root/dev/net'}
    files = {'workspace.ext4', 'console.log', 'manager.lock', 'resources-applied.json',
             'resources-desired.json', 'resources-desired.restart-hold.json',
             jail+'/root/workspace.ext4', jail+'/root/rootfs.ext4', jail+'/root/vmlinux',
             jail+'/root/config.json', jail+'/root/firecracker', jail+'/root/firecracker.pid',
             jail+'/root/dev/kvm', jail+'/root/dev/net/tun', jail+'/root/dev/urandom', jail+'/root/dev/userfaultfd'}
    # These are manager-owned recovery copies of this computer, disclosed in
    # the product confirmation. No backup outside this exact root is touched.
    backup = re.fullmatch(r'workspace-before-(?:growth|purge)-[0-9]+\.ext4', path)
    socket = re.fullmatch(re.escape(jail) + r'/root/run/(?:api\.sock|v\.sock(?:_[0-9]+)?)', path)
    return path in directories, path in files or bool(backup or socket)


def _scan(broker, identity, row):
    roots = {key: Path(broker.c[key]) for key in ('state_root', 'socket_root', 'config_root', 'unit_root')}
    for a, path in roots.items():
        with directory(path) as fd:
            info=os.fstat(fd)
            if info.st_uid != os.geteuid() or info.st_mode & 0o022:
                raise AdmissionError('cleanup root ownership changed')
        for b, other in roots.items():
            if a != b and (path == other or path in other.parents or other in path.parents):
                raise AdmissionError('overlapping cleanup roots')
        if path == broker.release or path in broker.release.parents or broker.release in path.parents:
            raise AdmissionError('cleanup root overlaps shared release')
    records = []
    uid = 61000 + row['slot']
    def tree(root, relative, inner, device):
        with directory(roots[root] / Path(relative).parent) as parent:
            info = os.stat(Path(relative).name, dir_fd=parent, follow_symlinks=False)
        if stat.S_ISLNK(info.st_mode) or info.st_dev != device:
            raise AdmissionError('cleanup path is a symlink or crosses a mount')
        is_dir = stat.S_ISDIR(info.st_mode)
        allowed_dir, allowed_file = _allowed_state(inner, identity) if root == 'state_root' else (inner == '', inner == 'control.sock')
        if not (allowed_dir if is_dir else allowed_file):
            raise AdmissionError('unrecognized computer artifact; cleanup retained')
        if info.st_uid != os.geteuid() and (info.st_uid != uid or info.st_gid != uid):
            raise AdmissionError('computer artifact belongs to another owner')
        if is_dir:
            # Account/manager-owned directories cannot be modified by peers.
            if info.st_mode & 0o022:
                raise AdmissionError('computer directory is writable by another owner')
        elif not (stat.S_ISREG(info.st_mode) or stat.S_ISSOCK(info.st_mode) or stat.S_ISCHR(info.st_mode)):
            raise AdmissionError('unsupported computer artifact type')
        elif stat.S_ISSOCK(info.st_mode):
            if not (inner=='control.sock' or re.fullmatch(r'.*/root/run/(?:api\.sock|v\.sock(?:_[0-9]+)?)',inner)):
                raise AdmissionError('unexpected computer socket')
        elif not stat.S_ISCHR(info.st_mode) and (inner=='control.sock' or '/root/run/' in inner):
            raise AdmissionError('unexpected socket artifact type')
        elif '/root/dev/' in inner and not stat.S_ISCHR(info.st_mode):
            raise AdmissionError('unexpected jail device type')
        if stat.S_ISCHR(info.st_mode):
            expected = {'kvm': (10, 232), 'tun': (10, 200), 'urandom': (1, 9)}.get(Path(inner).name)
            if Path(inner).name=='userfaultfd':
                expected=broker.delete_userfault_device()
            if expected != (os.major(info.st_rdev), os.minor(info.st_rdev)):
                raise AdmissionError('unexpected jail device')
        shared = False
        if inner.endswith(('/root/rootfs.ext4', '/root/vmlinux', '/root/firecracker')):
            source = broker.release / ('bin/firecracker' if inner.endswith('/firecracker') else Path(inner).name)
            with directory(source.parent) as parent:
                release_info = os.stat(source.name, dir_fd=parent, follow_symlinks=False)
            if not stat.S_ISREG(info.st_mode) or not stat.S_ISREG(release_info.st_mode) or info.st_mode & 0o222 or release_info.st_mode & 0o222:
                raise AdmissionError('immutable image type changed')
            if (info.st_dev, info.st_ino) == (release_info.st_dev, release_info.st_ino):
                shared = True
            elif info.st_size != release_info.st_size or _digest(roots[root]/relative) != _digest(source):
                raise AdmissionError('immutable jail image does not match release')
        records.append(_record(root, relative, info, shared))
        if len(records) > 256:
            raise AdmissionError('computer artifact inventory exceeds supported limit')
        if is_dir:
            with directory(roots[root] / relative) as fd:
                names = os.listdir(fd)
            for name in names:
                tree(root, relative + '/' + name, (inner + '/' + name).lstrip('/'), device)
    for key in ('state_root', 'socket_root'):
        with directory(roots[key]) as fd:
            info = os.fstat(fd)
            if info.st_uid != os.geteuid() or info.st_mode & 0o022:
                raise AdmissionError('cleanup root ownership changed')
            try:
                os.stat(identity, dir_fd=fd, follow_symlinks=False)
            except FileNotFoundError:
                continue
        tree(key, identity, '', info.st_dev)
    state = {record['path'][len(identity)+1:]: record for record in records if record['root']=='state_root'}
    disk = state.get('workspace.ext4')
    alias = state.get('jails/firecracker/ac-'+identity+'/root/workspace.ext4')
    if alias and (not disk or (alias['device'],alias['inode']) != (disk['device'],disk['inode'])):
        raise AdmissionError('workspace jail alias belongs to another disk')
    if disk and (not stat.S_ISREG(disk['mode']) or disk['size'] > row['quota_bytes']):
        raise AdmissionError('workspace conflicts with reservation')
    if row['state']=='ready' and not disk:
        raise AdmissionError('ready computer disk is missing')
    unit = 'tofi-computer-ac-' + identity + '.service'
    for root, name, mode in (('config_root',identity+'.json',0o600), ('unit_root',unit,0o644)):
        with directory(roots[root]) as fd:
            try:
                info = os.stat(name, dir_fd=fd, follow_symlinks=False)
            except FileNotFoundError:
                continue
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != mode:
                raise AdmissionError('generated artifact ownership changed')
            path = roots[root]/name
            with broker.ledger.connection() as db:
                owned = db.execute('SELECT sha256,mode FROM owned_files WHERE path=?',(str(path),)).fetchone()
            if not owned or owned['mode'] != mode or hashlib.sha256(path.read_bytes()).hexdigest() != owned['sha256']:
                raise AdmissionError('generated artifact manifest changed')
            if root=='config_root':
                config=json.loads(path.read_text())
                if any(config.get(key)!=value for key,value in {
                    'id':'ac-'+identity, 'slot':row['slot'], 'state_dir':str(roots['state_root']/identity),
                    'socket_dir':str(roots['socket_root']/identity), 'image_dir':str(broker.release),
                    'bin_dir':str(broker.release/'bin')}.items()):
                    raise AdmissionError('manager config belongs to another computer')
            records.append(_record(root,name,info))
        # Pending generated writes need their own journal; do not guess at them.
        if any((roots[root]/(name+suffix)).exists() or (roots[root]/(name+suffix)).is_symlink()
               for suffix in ('.pending','.quota-pending')):
            raise AdmissionError('unresolved generated artifact')
    _verify(broker, identity, records)
    return records


def _verify(broker, identity, records):
    """Recheck survivors against the immutable journal, including all hardlinks."""
    present = []
    expected = {(r['root'],r['path']):r for r in records}
    for root,name in (('config_root',identity+'.json'),('unit_root','tofi-computer-ac-'+identity+'.service')):
        with directory(broker.c[root]) as fd:
            for candidate in (name,name+'.pending',name+'.quota-pending'):
                try:os.stat(candidate,dir_fd=fd,follow_symlinks=False)
                except FileNotFoundError:continue
                if (root,candidate) not in expected:
                    raise AdmissionError('unexpected generated artifact appeared while fenced')
    for record in records:
        path = Path(broker.c[record['root']])/record['path']
        try:
            with directory(path.parent) as fd:
                info = os.stat(path.name,dir_fd=fd,follow_symlinks=False)
        except FileNotFoundError:
            continue
        if any(getattr(info,field)!=record[key] for field,key in (
                ('st_dev','device'),('st_ino','inode'),('st_mode','mode'),('st_uid','uid'),('st_gid','gid'))):
            raise AdmissionError('computer artifact changed after deletion intent')
        if stat.S_ISREG(info.st_mode) and info.st_size!=record['size']:
            raise AdmissionError('computer file changed while fenced')
        present.append((record, info))
        if stat.S_ISDIR(info.st_mode):
            with directory(path) as fd:
                if any((record['root'],record['path']+'/'+name) not in expected for name in os.listdir(fd)):
                    raise AdmissionError('unrecognized artifact appeared while fenced')
    links={}
    for record,info in present:
        if stat.S_ISREG(info.st_mode):
            key=(info.st_dev,info.st_ino)
            links[key]=links.get(key,0)+1
    for record,info in present:
        if stat.S_ISREG(info.st_mode) and not record['shared'] and info.st_nlink != links[(info.st_dev,info.st_ino)]:
            raise AdmissionError('computer file has an external or foreign hardlink')
    return present


def _remove(broker, identity, records):
    _verify(broker,identity,records)
    # Delete leaves before parents. Each unlink uses a pinned parent descriptor
    # and immediately rechecks the journal identity; no recursive rmtree.
    for record in sorted(records,key=lambda r:r['path'].count('/'),reverse=True):
        path=Path(broker.c[record['root']])/record['path']
        try:
            with directory(path.parent) as fd:
                info=os.stat(path.name,dir_fd=fd,follow_symlinks=False)
                if (info.st_dev,info.st_ino,info.st_mode)!=(record['device'],record['inode'],record['mode']):
                    raise AdmissionError('computer artifact changed before unlink')
                if stat.S_ISDIR(info.st_mode):
                    os.rmdir(path.name,dir_fd=fd)
                else:
                    os.unlink(path.name,dir_fd=fd)
                os.fsync(fd)
        except FileNotFoundError:
            continue
    for root in ('state_root','socket_root'):
        with directory(broker.c[root]) as fd:
            try:
                os.stat(identity,dir_fd=fd,follow_symlinks=False)
            except FileNotFoundError:
                continue
            raise AdmissionError('computer cleanup footprint still exists')
    for root,name in (('config_root',identity+'.json'),('unit_root','tofi-computer-ac-'+identity+'.service')):
        with directory(broker.c[root]) as fd:
            for candidate in (name,name+'.pending',name+'.quota-pending'):
                try:os.stat(candidate,dir_fd=fd,follow_symlinks=False)
                except FileNotFoundError:continue
                raise AdmissionError('computer generated footprint still exists')
    if _verify(broker,identity,records):
        raise AdmissionError('computer artifacts remain')


def delete(broker, identity, generation, operation):
    account_id(generation); account_id(operation)
    if not broker.supports_computer_deletion:
        raise AdmissionError('computer deletion requires an isolated Worker')
    with broker.ledger.connection() as db:
        db.execute('BEGIN IMMEDIATE')
        life=db.execute('SELECT * FROM computer_lifecycle WHERE account_id=?',(identity,)).fetchone()
        row=db.execute('SELECT * FROM computers WHERE account_id=?',(identity,)).fetchone()
        if not life or life['generation']!=generation:
            raise AdmissionError('stale computer generation')
        old=db.execute('SELECT * FROM computer_operations WHERE operation_id=?',(operation,)).fetchone()
        if old and (old['account_id'],old['generation'],old['kind'])!=(identity,generation,'delete'):
            raise AdmissionError('operation belongs to another computer')
        if life['state']=='deleted' and old and old['state']=='deleted':
            db.execute('COMMIT')
            return status(broker,identity)
        if life['state']!='active' and life['operation_id']!=operation:
            raise AdmissionError('retry the existing deletion operation')
        if not row or db.execute('SELECT 1 FROM legacy_adoptions WHERE account_id=?',(identity,)).fetchone():
            raise AdmissionError('legacy or missing ownership requires a separate workflow')
        if db.execute('SELECT 1 FROM resize_fences WHERE account_id=?',(identity,)).fetchone():
            raise AdmissionError('offline resize unresolved')
        db.execute("INSERT OR IGNORE INTO computer_operations(operation_id,account_id,generation,kind,state,original_state) VALUES(?,?,?,'delete','deleting',?)",(operation,identity,generation,row['state']))
        db.execute("UPDATE computer_lifecycle SET state='deleting',operation_id=?,phase='stopping',error='' WHERE account_id=?",(operation,identity))
        db.execute("UPDATE computers SET state='disabled' WHERE account_id=?",(identity,))
        db.execute('COMMIT')
    try:
        broker.stop_manager(identity,broker.identity(identity)[2])
        broker.verify_delete_cleanup(identity,row['slot'])
        with broker.ledger.connection() as db:
            db.execute('DELETE FROM runtime_claims WHERE account_id=?',(identity,))
            old=db.execute('SELECT manifest,original_state FROM computer_operations WHERE operation_id=?',(operation,)).fetchone()
        original=dict(row,state=old['original_state'] or row['state'])
        records=json.loads(old['manifest']) if old['manifest'] is not None else _scan(broker,identity,original)
        with broker.ledger.connection() as db:
            db.execute('BEGIN IMMEDIATE')
            db.execute('UPDATE computer_operations SET manifest=?,updated_at=CURRENT_TIMESTAMP WHERE operation_id=?',(json.dumps(records,sort_keys=True),operation))
            db.execute("UPDATE computer_lifecycle SET phase='removing' WHERE account_id=?",(identity,))
            db.execute('COMMIT')
        _remove(broker,identity,records)
        broker.verify_delete_cleanup(identity,row['slot'])
        with broker.ledger.connection() as db:
            db.execute('BEGIN IMMEDIATE')
            db.execute('DELETE FROM computers WHERE account_id=?',(identity,))
            for record in records:
                db.execute('DELETE FROM owned_files WHERE path=?',(str(Path(broker.c[record['root']])/record['path']),))
            db.execute("UPDATE computer_lifecycle SET state='deleted',phase='complete',error='' WHERE account_id=?",(identity,))
            db.execute("UPDATE computer_operations SET state='deleted',updated_at=CURRENT_TIMESTAMP WHERE operation_id=?",(operation,))
            db.execute('COMMIT')
    except Exception as exc:
        # Bounded diagnostics stay separate from capacity claims. A missing
        # disk or failed response can never clear the durable tombstone.
        message=str(exc)[:500] if isinstance(exc,AdmissionError) else 'computer cleanup failed ('+type(exc).__name__+'); retry original operation after cleanup'
        with broker.ledger.connection() as db:
            db.execute("UPDATE computer_lifecycle SET state='cleanup_failed',error=? WHERE account_id=?",(message,identity))
            db.execute("UPDATE computer_operations SET state='cleanup_failed',updated_at=CURRENT_TIMESTAMP WHERE operation_id=?",(operation,))
        raise AdmissionError(message) from exc
    return status(broker,identity)


def recreate(broker,identity,generation,operation,quota):
    account_id(generation);account_id(operation)
    if not broker.supports_computer_deletion:
        raise AdmissionError('computer recreation requires an isolated Worker')
    if type(quota) is not int or not 8<=quota<=1024:
        raise ValueError('disk quota must be 8..1024 GiB')
    with broker.ledger.connection() as db:
        db.execute('BEGIN IMMEDIATE')
        life=db.execute('SELECT * FROM computer_lifecycle WHERE account_id=?',(identity,)).fetchone()
        old=db.execute('SELECT * FROM computer_operations WHERE operation_id=?',(operation,)).fetchone()
        if old:
            if (old['account_id'],old['generation'],old['kind'],old['quota_gib'])!=(identity,generation,'recreate',quota) or life['operation_id']!=operation or life['state']!='active':
                raise AdmissionError('stale recreation operation')
            db.execute('COMMIT')
            return status(broker,identity)
        if not life or life['generation']!=generation or life['state']!='deleted':
            raise AdmissionError('verified deletion required before recreation')
        if db.execute('SELECT 1 FROM computers WHERE account_id=?',(identity,)).fetchone():
            raise AdmissionError('computer reservation remains')
        for root,name in (('state_root',identity),('socket_root',identity),('config_root',identity+'.json'),('unit_root',broker.identity(identity)[2])):
            with directory(broker.c[root]) as fd:
                candidates=(name,name+'.pending',name+'.quota-pending') if root in ('config_root','unit_root') else (name,)
                for candidate in candidates:
                    try:os.stat(candidate,dir_fd=fd,follow_symlinks=False)
                    except FileNotFoundError:continue
                    raise AdmissionError('computer artifact appeared before recreation')
        snapshot=broker.ledger._snapshot(db)
        if (quota*1024**3+broker.ledger.per_account_internal_reserved)>snapshot['admission_remaining_bytes']:
            raise AdmissionError('insufficient reserved disk headroom')
        occupied=broker.ledger.reserved_slots | {a['slot'] for a in snapshot['accounts']}
        slot=next((s for s in range(1,251) if s not in occupied),None)
        if slot is None:raise AdmissionError('computer slots exhausted')
        new_generation=str(uuid.uuid4())
        db.execute("INSERT INTO computers VALUES(?,?,?,'reserved')",(identity,slot,quota*1024**3))
        db.execute("UPDATE computer_lifecycle SET generation=?,state='active',operation_id=?,phase='reserved',error='' WHERE account_id=?",(new_generation,operation,identity))
        db.execute("INSERT INTO computer_operations(operation_id,account_id,generation,kind,state,quota_gib) VALUES(?,?,?,'recreate','active',?)",(operation,identity,generation,quota))
        db.execute('COMMIT')
    return status(broker,identity)
