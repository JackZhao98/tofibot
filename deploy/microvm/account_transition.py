#!/usr/bin/env python3
"""Explicit offline account install/ownership rollback; never a snapshot restore.

Invoked by server-ops under its deployment lock with Compose writers stopped.
The reviewed manifest names the actual host paths behind Worker bind/volumes.
This command does not start services, load policy, mount ext4, or make backups.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import stat
import subprocess

from account_adoption import stable_identity, sync_dir
from account_capacity import AdmissionError, _trusted_regular_path
from account_provisioner import Broker
from account_runner_transfer import RunnerTransfer


def command(args, **kwargs):
    return subprocess.run(args, timeout=180, capture_output=True, **kwargs)


def atomic_json(path, value):
    path = Path(path)
    temporary = path.with_name(path.name + '.pending')
    if temporary.exists() or temporary.is_symlink():
        raise AdmissionError('pending metadata must be investigated before retry')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, indent=2)
        stream.write('\n'); stream.flush(); os.fsync(stream.fileno())
    os.replace(temporary, path); sync_dir(path.parent)


def real_path(value):
    p = Path(value)
    if not p.is_absolute() or p != Path(os.path.abspath(p)):
        raise AdmissionError('canonical absolute operator paths required')
    current = Path(p.anchor)
    for part in p.parts[1:]:
        current /= part
        if current.is_symlink():
            raise AdmissionError('operator path contains a symlink')
    return p


def assert_no_disk_users(disk, proc=Path('/proc'), run=command):
    """Require complete host FD visibility; an inaccessible live PID fails closed."""
    info = disk.stat(); identity = (info.st_dev, info.st_ino)
    for process in proc.iterdir():
        if not process.name.isdecimal():
            continue
        try:
            for line in (process / 'maps').read_text().splitlines():
                fields = line.split()
                if len(fields) >= 5 and fields[4] != '0':
                    major, minor = (int(v, 16) for v in fields[3].split(':'))
                    if (major, minor, int(fields[4])) == (os.major(info.st_dev), os.minor(info.st_dev), info.st_ino):
                        raise AdmissionError('workspace has a live memory mapping')
            for fd in (process / 'fd').iterdir():
                try:
                    info = fd.stat()
                except FileNotFoundError:
                    continue
                if (info.st_dev, info.st_ino) == identity:
                    raise AdmissionError('workspace has a live process reference')
        except FileNotFoundError:
            continue
        except PermissionError as exc:
            raise AdmissionError('host process visibility incomplete; writer absence unproven') from exc
    # Kernel loop references survive closure of userspace FDs. Refuse even an
    # unmounted loop device, rather than risk direct debugfs writes racing I/O.
    result = run(['losetup', '--list', '--json', '--output', 'NAME,BACK-FILE'], check=False)
    if result.returncode:
        raise AdmissionError('host loop-device visibility unavailable')
    loops = json.loads(result.stdout)['loopdevices']
    for loop in loops:
        path = loop.get('back-file')
        if not isinstance(path, str) or not path or path.endswith(' (deleted)'):
            raise AdmissionError('loop backing identity unavailable; writer absence unproven')
        try:
            backing = Path(path).stat()
        except OSError as exc:
            raise AdmissionError('loop backing identity unavailable') from exc
        if (backing.st_dev, backing.st_ino) == identity:
            raise AdmissionError('workspace is attached to a host loop device')


class Transition:
    def __init__(self, manifest, broker, run=command, disk_users=assert_no_disk_users, runner=None):
        self.m = manifest; self.b = broker; self.run = run; self.disk_users = disk_users
        self.runner = runner or RunnerTransfer(run)
        required = {'deployment_root', 'worker_config', 'personal_config', 'runner_state', 'runner_prefix', 'runner_origin'}
        if set(manifest) != required:
            raise AdmissionError('operator manifest fields do not match supported schema')
        self.root = real_path(manifest['deployment_root'])
        self.personal_config = real_path(manifest['personal_config'])
        self.saved_config = self.personal_config.with_name(self.personal_config.name + '.account-adopted')
        self.runner_source = real_path(manifest['runner_state'])
        self.intent = real_path(broker.c['ledger_root']) / 'legacy-transition.json'
        instance = json.loads((self.root / 'data/instance.json').read_text())['id']
        self.identity = stable_identity(instance); self.instance = instance
        self.manifest_hash = hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest()
        if self.intent.exists():
            if self.intent.is_symlink():
                raise AdmissionError('transition journal is a symlink')
            self.j = json.loads(self.intent.read_text())
            if self.j['manifest_sha256'] != self.manifest_hash or self.j['account_id'] != self.identity:
                raise AdmissionError('operator manifest differs from durable ownership journal')
            self.config = self.j['personal_config']
        else:
            self.config = json.loads(self.personal_config.read_text())
            if self.config['id'] != 'personal':
                raise AdmissionError('expected the reviewed personal service')
            source = real_path(self.config['state_dir']) / 'workspace.ext4'
            info = _trusted_regular_path(source)
            self.b._check_filesystem_size(source, info.st_size)
            inventory = [v for v in broker.ledger.external_disks if v['asset_id'] == 'personal']
            if len(inventory) != 1 or inventory[0]['disk_path'] != str(source) or inventory[0]['quota_bytes'] != info.st_size:
                raise AdmissionError('personal disk does not match Worker external commitment')
            aliases = inventory[0]['alias_paths']
            expected_alias = source.parent / 'jails/firecracker/personal/root/workspace.ext4'
            if any(real_path(p) != expected_alias for p in aliases) or len(aliases) > 1:
                raise AdmissionError('only the known personal jail alias may be transferred')
            for alias in aliases:
                a = _trusted_regular_path(alias)
                if (a.st_dev, a.st_ino) != (info.st_dev, info.st_ino):
                    raise AdmissionError('personal jail alias changed')
            if info.st_nlink != len(aliases) + 1:
                raise AdmissionError('unaccounted workspace hardlink; writer boundary unresolved')
            self.j = dict(manifest_sha256=self.manifest_hash, account_id=self.identity,
                          instance_id=instance, source_disk=str(source), aliases=aliases,
                          personal_config=self.config, device=info.st_dev, inode=info.st_ino,
                          quota_bytes=info.st_size, phase='prepared', runner_imported=False)
        self.source = real_path(self.j['source_disk'])
        self.target = real_path(broker.c['state_root']) / self.identity / 'workspace.ext4'
        if self.source.parent.stat().st_dev != Path(broker.c['state_root']).stat().st_dev:
            raise AdmissionError('same-filesystem inode transfer required; copying is not permitted')

    def save(self):
        atomic_json(self.intent, self.j)

    @contextlib.contextmanager
    def fences(self):
        # The App/Runner stop is enforced by the installation wrapper. Never
        # trust a user-supplied stopped=true assertion for manager/Worker state.
        result = self.run(['systemctl', 'show', 'tofi-computer-personal.service', '--property=ExecStart', '--value'], check=False)
        text = result.stdout.decode() if isinstance(result.stdout, bytes) else result.stdout or ''
        argv = text.split('argv[]=', 1)[-1].split(' ;', 1)[0].split()
        if result.returncode or len(argv) != 3 or not argv[1].endswith('/manager.py') or argv[-1] != str(self.personal_config):
            raise AdmissionError('old service does not own the reviewed personal configuration')
        result = self.run(['systemctl', 'stop', 'tofi-computer-personal.service'], check=False)
        if result.returncode:
            raise AdmissionError('old personal manager could not be stopped')
        result = self.run(['systemctl', 'show', 'tofi-computer-personal.service', '--property=MainPID', '--value'], check=False)
        if result.returncode or str(result.stdout.decode() if isinstance(result.stdout, bytes) else result.stdout).strip() != '0':
            raise AdmissionError('old personal service still has a writer')
        with contextlib.ExitStack() as stack:
            for path in (Path(self.b.c['ledger_root']) / 'service.lock',
                         Path(self.b.c['ledger_root']) / 'broker.lock',
                         self.source.parent / 'manager.lock', self.source.parent / 'ops.lock'):
                fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
                stream = stack.enter_context(os.fdopen(fd, 'r+'))
                if not stat.S_ISREG(os.fstat(fd).st_mode):
                    raise AdmissionError('unexpected lifecycle lock')
                try:
                    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError as exc:
                    raise AdmissionError('an existing Worker/manager/ops writer owns its lock') from exc
            disk = self.current_disk()
            self.disk_users(disk)
            yield

    def current_disk(self):
        present = [p for p in (self.source, self.target) if p.exists() or p.is_symlink()]
        if len(present) != 1:
            raise AdmissionError('ownership paths ambiguous; retain both data and fence')
        info = _trusted_regular_path(present[0])
        if (info.st_dev, info.st_ino) != (self.j['device'], self.j['inode']):
            raise AdmissionError('authoritative disk inode changed')
        self.b._check_filesystem_size(present[0], info.st_size)
        return present[0]

    def detach_alias(self, path, disk):
        if path.exists() or path.is_symlink():
            info = _trusted_regular_path(path); actual = disk.stat()
            if (info.st_dev, info.st_ino) != (actual.st_dev, actual.st_ino):
                raise AdmissionError('unexpected workspace alias; preserve it')
            path.unlink(); sync_dir(path.parent)

    def move(self, target):
        disk = self.current_disk()
        # Only the two reviewed manager jail aliases are removable. The VM is
        # stopped and all descriptor users have been proved absent first.
        for alias in [*(Path(p) for p in self.j['aliases']), self.target.parent / ('jails/firecracker/ac-' + self.identity + '/root/workspace.ext4')]:
            self.detach_alias(alias, disk)
        if disk.stat().st_nlink != 1:
            raise AdmissionError('unaccounted workspace link remains')
        if disk != target:
            real_path(str(target))
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            os.rename(disk, target); sync_dir(disk.parent); sync_dir(target.parent)
        if self.current_disk() != target:
            raise AdmissionError('disk transfer could not be verified')
        os.chown(target, os.geteuid(), os.getegid()); os.chmod(target, 0o600)

    def adopt(self):
        vcpus, memory_mib = self.b.runtime_resources(self.identity)
        if (vcpus > self.b.c["runtime_vcpu_budget"] or
                memory_mib + 512 > self.b.c["runtime_memory_mib_budget"]):
            raise AdmissionError('adopted computer cannot fit configured runtime budget')
        with self.fences():
            if self.j['phase'] == 'worker':
                from account_adoption import proof
                return proof(self.b, self.identity)
            if self.j['phase'] not in ('prepared', 'adopting'):
                raise AdmissionError('rollback must finish before a new adoption')
            if self.j['phase'] == 'prepared':
                with self.b.ledger.connection() as db:
                    db.execute('BEGIN IMMEDIATE')
                    snapshot = self.b.ledger._snapshot(db)
                    if self.b.ledger.per_account_internal_reserved > snapshot['admission_remaining_bytes']:
                        raise AdmissionError('capacity cannot cover adopted internal image/service promise')
                    if db.execute('SELECT 1 FROM computers WHERE account_id=?', (self.identity,)).fetchone():
                        raise AdmissionError('legacy computer identity already registered')
                    occupied = self.b.ledger.reserved_slots | {a['slot'] for a in snapshot['accounts']}
                    slot = next((v for v in range(1, 251) if v not in occupied), None)
                    if slot is None:
                        raise AdmissionError('no account computer slot available')
                    # Persist intent before registration; crash recovery is
                    # idempotent across both SQLite commit and filesystem rename.
                    self.j['phase'] = 'adopting'; self.j['slot'] = slot; self.save()
                    db.execute("INSERT INTO computers VALUES(?,?,?,'disabled')", (self.identity, slot, self.j['quota_bytes']))
                    db.execute("INSERT INTO legacy_adoptions(account_id,instance_id,asset_id,source_disk,source_config,device,inode,quota_bytes,phase) VALUES(?,?,'personal',?,?,?,?,?,'pending')", (self.identity, self.instance, str(self.source), str(self.personal_config), self.j['device'], self.j['inode'], self.j['quota_bytes']))
                    db.execute('COMMIT')
            # A journal written before a rolled-back SQL transaction can retry
            # registration without allocating a different identity/slot.
            with self.b.ledger.connection() as db:
                db.execute('BEGIN IMMEDIATE')
                if not db.execute('SELECT 1 FROM legacy_adoptions WHERE account_id=?', (self.identity,)).fetchone():
                    db.execute("INSERT INTO computers VALUES(?,?,?,'disabled')", (self.identity, self.j['slot'], self.j['quota_bytes']))
                    db.execute("INSERT INTO legacy_adoptions(account_id,instance_id,asset_id,source_disk,source_config,device,inode,quota_bytes,phase) VALUES(?,?,'personal',?,?,?,?,?,'pending')", (self.identity, self.instance, str(self.source), str(self.personal_config), self.j['device'], self.j['inode'], self.j['quota_bytes']))
                db.execute('COMMIT')
            if self.personal_config.exists():
                if self.saved_config.exists() or json.loads(self.personal_config.read_text()) != self.config:
                    raise AdmissionError('old manager config changed or fence collision')
                os.rename(self.personal_config, self.saved_config); sync_dir(self.personal_config.parent)
            if not self.saved_config.is_file():
                raise AdmissionError('old manager configuration fence unavailable')
            self.move(self.target)
            if not self.j['runner_imported']:
                resume = self.j.get('runner_started', False)
                self.j['runner_started'] = True; self.save()
                self.runner.import_state(self.target, self.runner_source, self.m['runner_prefix'], self.b.release / 'rootfs.ext4', resume=resume)
                self.j['runner_imported'] = True; self.save()
            self.write_overlay()
            self.runner.import_attachments(self.target, self.root / 'data')
            self.rewire_mcp()
            with self.b.ledger.connection() as db:
                db.execute('BEGIN IMMEDIATE')
                db.execute("UPDATE legacy_adoptions SET phase='worker',runner_ready=1 WHERE account_id=?", (self.identity,))
                db.execute("UPDATE computers SET state='reserved' WHERE account_id=?", (self.identity,))
                db.execute('COMMIT')
            self.j['phase'] = 'worker'; self.save()
            from account_adoption import proof
            self.b.ledger.snapshot()
            return proof(self.b, self.identity)

    def rewire_mcp(self):
        path = self.root / 'data/mcp.json'
        if not path.exists(): return
        if path.is_symlink(): raise AdmissionError('MCP config is a symlink')
        config = json.loads(path.read_text())
        for collection in ('mcpServers', 'servers'):
            entries = config.get(collection) or {}
            if not isinstance(entries, dict):
                raise AdmissionError('MCP server collection must be a mapping or null')
            for server in entries.values():
                endpoint = server.get('url', '')
                origin = self.m['runner_origin'].rstrip('/')
                old_guest_origin = 'http://account-computer'
                if endpoint.startswith(old_guest_origin + '/mcp/'):
                    server['url'] = old_guest_origin + '/v1/runner' + endpoint[len(old_guest_origin):]
                if origin and endpoint.startswith(origin + '/mcp/'):
                    server['url'] = 'http://account-computer/v1/runner' + endpoint[len(origin):]
                    headers = server.get('headers') or {}
                    server['headers'] = {key:value for key,value in headers.items() if key.lower() != 'authorization'}
        atomic_json(path, config)
        os.chown(path, (self.root / 'data').stat().st_uid, (self.root / 'data').stat().st_gid)

    def write_overlay(self):
        # JSON is also YAML. The separately reviewed Worker/policy overlay is
        # installed by the parent; this identity layer adds no host privileges.
        environment = {'TOFI_MULTI_ACCOUNT':'1', 'TOFI_ACCOUNT_LEGACY_COMPUTER_UUID':self.identity if self.j['runner_imported'] else '',
                       'TOFI_ACCOUNT_MAINTENANCE':'${TOFI_ACCOUNT_MAINTENANCE:-1}'}
        value = {'services':{'app':{'environment':environment}}}
        atomic_json(self.root / 'accounts.identity.overlay.yaml', value)

    def rollback(self):
        with self.fences():
            if self.j['phase'] == 'legacy':
                if not self.personal_config.is_file() or self.personal_config.is_symlink():
                    raise AdmissionError('legacy config changed after rollback')
                return {'phase':'legacy','account_id':self.identity,'retained_data':True}
            if self.j['phase'] not in ('adopting', 'worker', 'rolling-back'):
                raise AdmissionError('no adoption to roll back')
            self.j['phase'] = 'rolling-back'; self.save()
            with self.b.ledger.connection() as db:
                db.execute("UPDATE legacy_adoptions SET phase='rollback' WHERE account_id=?", (self.identity,))
                row = db.execute('SELECT quota_bytes FROM computers WHERE account_id=?', (self.identity,)).fetchone()
            disk = self.current_disk(); quota = disk.stat().st_size
            if row and row[0] != quota:
                raise AdmissionError('unresolved quota resize; rollback retains fence')
            self.disk_users(disk); self.move(self.source)
            # Restore current geometry, never the pre-cutover disk contents.
            config = dict(self.config, disk_gib=quota // 1024**3)
            if self.personal_config.exists() and json.loads(self.personal_config.read_text()) != config:
                raise AdmissionError('legacy manager config collision')
            atomic_json(self.personal_config, config)
            for filename in ('resources-applied.json', 'resources-desired.json', 'resources-desired.restart-hold.json'):
                path = self.source.parent / filename
                if path.exists():
                    if path.is_symlink(): raise AdmissionError('legacy resource file is a symlink')
                    resource = json.loads(path.read_text()); resource['disk_gib'] = quota // 1024**3
                    atomic_json(path, resource)
            for alias in self.j['aliases']:
                alias = Path(alias)
                if not alias.exists(): os.link(self.source, alias); sync_dir(alias.parent)
            with self.b.ledger.connection() as db:
                db.execute('BEGIN IMMEDIATE')
                db.execute('DELETE FROM runtime_claims WHERE account_id=?', (self.identity,))
                db.execute('DELETE FROM computers WHERE account_id=?', (self.identity,))
                db.execute("""INSERT INTO legacy_adoptions VALUES(?,?,'personal',?,?,?,?,?,'legacy',?)
                    ON CONFLICT(account_id) DO UPDATE SET phase='legacy',quota_bytes=excluded.quota_bytes,
                    runner_ready=MAX(legacy_adoptions.runner_ready,excluded.runner_ready)""",
                    (self.identity,self.instance,str(self.source),str(self.personal_config),self.j['device'],self.j['inode'],quota,int(self.j['runner_imported'])))
                db.execute('COMMIT')
            self.j['phase'] = 'legacy'; self.j['quota_bytes'] = quota; self.save()
            self.write_overlay()
            # Do not restore old App/Runner snapshots. The compatible account
            # App keeps historical and new rows/files; Guest Runner remains in
            # this same disk. New tenant computers are retained unchanged.
            return {'phase':'legacy', 'account_id':self.identity, 'retained_data':True,
                    'compatible_account_app_required':True, 'services_started':False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('adopt', 'rollback'))
    parser.add_argument('manifest')
    args = parser.parse_args()
    if os.geteuid() != 0: raise SystemExit('root operator required')
    os.umask(0o077)
    path = real_path(args.manifest)
    info = _trusted_regular_path(path)
    if info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600:
        raise SystemExit('root-owned mode-0600 reviewed manifest required')
    manifest = json.loads(path.read_text())
    if manifest.get('deployment_root') != os.environ.get('TOFI_DEPLOY_ROOT'):
        raise SystemExit('manifest does not match locked deployment')
    config_path = real_path(manifest['worker_config'])
    info = _trusted_regular_path(config_path)
    if info.st_uid != 0 or info.st_mode & 0o077:
        raise SystemExit('root-owned private Worker configuration required')
    config = json.loads(config_path.read_text())
    from account_release_check import validate_release
    validate_release(config['release_dir'], str(Path(__file__).with_name('manager.py').resolve()),
                     config['expected_guest_sha256'], expected_manifest_sha256=config['release_manifest_sha256'])
    broker = Broker(config)
    transition = Transition(manifest, broker)
    print(json.dumps(getattr(transition, args.action)(), indent=2))


if __name__ == '__main__':
    main()
