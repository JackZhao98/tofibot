#!/usr/bin/env python3
"""Exercise stable legacy adoption and data-preserving rollback in a fresh fixture.

Only newly created synthetic data under a dedicated temporary directory is
accepted. This module never opens production data, transfers a real VM, creates
production backups, or claims that compatibility runtime adoption is deployed.
"""
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import uuid

NAMESPACE = uuid.UUID('79d8c04b-3d65-4b99-97bf-f4e8a5630d47')


class AdoptionError(ValueError):
    pass


def stable_account_uuid(instance_id, source_account='legacy-owner'):
    instance = uuid.UUID(instance_id)
    if str(instance) != instance_id or not source_account:
        raise AdoptionError('canonical instance UUID and source identity required')
    return str(uuid.uuid5(NAMESPACE, instance_id + ':' + source_account))


def _sync_directory(path):
    fd=os.open(path,os.O_RDONLY)
    try:os.fsync(fd)
    finally:os.close(fd)


class FixtureAdoption:
    """A journaled single-writer transfer model using only owned fixture paths."""
    def __init__(self, root, instance_id):
        if Path(root).is_symlink():raise AdoptionError('fixture root must not be a symlink')
        self.root = Path(root).resolve(strict=True)
        if not self.root.name.startswith('tofi-account-legacy-dryrun-') or self.root.parent != Path(tempfile.gettempdir()).resolve():
            raise AdoptionError('dedicated direct temporary fixture root required')
        if (self.root/'synthetic-fixture').is_symlink() or (self.root/'registry.db').is_symlink():raise AdoptionError('fixture metadata symlink refused')
        if (self.root/'synthetic-fixture').read_text() != 'owned synthetic adoption fixture\n':
            raise AdoptionError('owned synthetic marker required')
        self.identity = stable_account_uuid(instance_id)
        self.db = sqlite3.connect(self.root/'registry.db', isolation_level=None)
        self.db.execute('PRAGMA foreign_keys=ON')

    def journal(self):
        return self.db.execute('SELECT phase,writer,disk_path,account_id FROM ownership WHERE id=1').fetchone()

    def _owned_disk(self, relative):
        path = self.root/relative
        if path.is_symlink() or path.resolve(strict=True).parent not in (self.root/'personal',self.root/'worker'/self.identity):
            raise AdoptionError('unsafe fixture disk path')
        if not path.is_file(): raise AdoptionError('fixture disk unavailable')
        return path

    def _identity(self, path):
        info=path.stat()
        return info.st_dev,info.st_ino,info.st_size

    def fence(self):
        self.db.execute('BEGIN IMMEDIATE')
        try:
            phase,writer,_,_=self.journal()
            if writer != 'none': raise AdoptionError('live writer must be stopped before transfer')
            self.db.execute("UPDATE ownership SET fenced=1 WHERE id=1")
            self.db.execute('COMMIT')
        except BaseException:
            if self.db.in_transaction:self.db.execute('ROLLBACK')
            raise

    def transfer(self, destination_phase, simulate_crash=False):
        if destination_phase not in ('worker','legacy'):raise AdoptionError('invalid destination owner')
        self.db.execute('BEGIN IMMEDIATE')
        try:
            phase,writer,relative,account=self.journal()
            if (self.root/'transfer-intent.json').exists():raise AdoptionError('pending transfer requires recovery first')
            fenced=self.db.execute('SELECT fenced FROM ownership WHERE id=1').fetchone()[0]
            if writer!='none' or not fenced:raise AdoptionError('exclusive stopped writer and fence required')
            if phase==destination_phase:self.db.execute('COMMIT');return
            old=self._owned_disk(relative)
            target_relative='worker/'+self.identity+'/workspace.ext4' if destination_phase=='worker' else 'personal/workspace.ext4'
            target=self.root/target_relative;target.parent.mkdir(parents=True,exist_ok=True)
            # Persist intent before rename. Recovery inspects both exact inode
            # paths and completes or rolls back ownership without copying bytes.
            before=self._identity(old)
            intent={'source':relative,'target':target_relative,'device':before[0],'inode':before[1],'account':self.identity,'phase':destination_phase}
            existing=self.root/'transfer-intent.json'
            if existing.exists():raise AdoptionError('pending transfer requires recovery first')
            if target.exists():raise AdoptionError('destination collision')
            with existing.open('x') as f:json.dump(intent,f);f.flush();os.fsync(f.fileno())
            _sync_directory(self.root)
            os.rename(old,target)
            _sync_directory(old.parent);_sync_directory(target.parent)
            if self._identity(target)!=before:raise AdoptionError('disk inode/bytes changed during transfer')
            if simulate_crash:raise AdoptionError('simulated interruption after rename')
            self._commit_mapping(destination_phase,target_relative)
            self.db.execute('COMMIT');existing.unlink();_sync_directory(self.root)
        except BaseException:
            if self.db.in_transaction:self.db.execute('ROLLBACK')
            raise

    def _commit_mapping(self, phase, relative):
        target_account=self.identity if phase=='worker' else 'legacy-owner'
        current=self.journal()[3]
        self.db.execute('PRAGMA defer_foreign_keys=ON')
        self.db.execute('UPDATE account_sessions SET account_id=? WHERE account_id=?',(target_account,current))
        self.db.execute('UPDATE accounts SET id=? WHERE id=?',(target_account,current))
        self.db.execute('UPDATE ownership SET phase=?,disk_path=?,account_id=? WHERE id=1',(phase,relative,target_account))
        self.db.execute('INSERT INTO stable_mapping(source_id,account_uuid) VALUES(?,?) ON CONFLICT(source_id) DO NOTHING',('legacy-owner',self.identity))
        assert self.db.execute('SELECT account_uuid FROM stable_mapping WHERE source_id=?',('legacy-owner',)).fetchone()[0]==self.identity

    def recover(self):
        p=self.root/'transfer-intent.json'
        if not p.exists():return
        if p.is_symlink():raise AdoptionError('unsafe transfer intent')
        intent=json.loads(p.read_text());target=self.root/intent['target'];source=self.root/intent['source']
        expected='worker/'+self.identity+'/workspace.ext4' if intent['phase']=='worker' else 'personal/workspace.ext4'
        if intent['phase'] not in ('worker','legacy') or intent['target']!=expected:raise AdoptionError('transfer intent does not match journal')
        phase,writer,current,account=self.journal()
        if current!=intent['source'] and not (phase==intent['phase'] and current==intent['target']):raise AdoptionError('transfer intent does not match journal')
        if source.exists() or not target.exists():raise AdoptionError('ambiguous transfer intent; retain fence')
        if self._identity(self._owned_disk(intent['target']))[:2]!=(intent['device'],intent['inode']):raise AdoptionError('unexpected disk replacement; retain fence')
        if intent['account']!=self.identity:raise AdoptionError('mapping changed; retain fence')
        self.db.execute('BEGIN IMMEDIATE')
        try:
            if self.journal()[1]!='none':raise AdoptionError('live writer; recovery refused')
            self._commit_mapping(intent['phase'],intent['target']);self.db.execute('COMMIT');p.unlink();_sync_directory(self.root)
        except BaseException:
            if self.db.in_transaction:self.db.execute('ROLLBACK')
            raise


def create_fixture(root, instance_id):
    root=Path(root);(root/'synthetic-fixture').write_text('owned synthetic adoption fixture\n')
    (root/'personal').mkdir();(root/'personal/workspace.ext4').write_bytes(b'SYNTHETIC DISK PLACEHOLDER; NOT EXT4\n')
    (root/'app-data').mkdir();(root/'app-data/old-upload').write_bytes(b'original attachment')
    (root/'runner-state').mkdir();(root/'runner-state/fixture-state').write_bytes(b'synthetic runner state')
    db=sqlite3.connect(root/'registry.db')
    db.executescript('''CREATE TABLE accounts(id TEXT PRIMARY KEY,credential_fingerprint TEXT NOT NULL);
    INSERT INTO accounts VALUES('legacy-owner','synthetic-credential-fingerprint');
    CREATE TABLE account_sessions(token_fingerprint TEXT PRIMARY KEY,account_id TEXT REFERENCES accounts(id));
    INSERT INTO account_sessions VALUES('synthetic-session-fingerprint','legacy-owner');
    CREATE TABLE stable_mapping(source_id TEXT PRIMARY KEY,account_uuid TEXT UNIQUE);
    CREATE TABLE ownership(id INTEGER PRIMARY KEY CHECK(id=1),phase TEXT,writer TEXT,disk_path TEXT,account_id TEXT,fenced INTEGER);
    INSERT INTO ownership VALUES(1,'legacy','none','personal/workspace.ext4','legacy-owner',0);''')
    db.commit();db.close()
    return FixtureAdoption(root,instance_id)


def run_fixture():
    with tempfile.TemporaryDirectory(prefix='tofi-account-legacy-dryrun-') as root:
        instance=str(uuid.uuid4());a=create_fixture(root,instance);a.fence()
        disk=Path(root)/a.journal()[2];before=a._identity(disk)
        a.transfer('worker');a.transfer('worker')
        disk=Path(root)/a.journal()[2]
        with disk.open('ab') as f:f.write(b'new workspace writes retained\n')
        new_asset=Path(root)/'app-data/new-generated';new_asset.write_bytes(b'new generated file retained')
        a.db.execute("INSERT INTO accounts VALUES(?,?)",(str(uuid.uuid4()),'synthetic-new-tenant'))
        a.transfer('legacy');a.transfer('legacy')
        restored=Path(root)/a.journal()[2]
        assert a._identity(restored)[:2]==before[:2] and b'new workspace writes retained' in restored.read_bytes()
        assert new_asset.read_bytes()==b'new generated file retained'
        assert a.db.execute('SELECT count(*) FROM accounts').fetchone()[0]==2
        assert a.db.execute('SELECT account_id FROM account_sessions').fetchone()[0]=='legacy-owner'
        assert a.db.execute('SELECT account_uuid FROM stable_mapping').fetchone()[0]==a.identity
        report={'mode':'synthetic-adoption-and-rollback-dry-run','stable_uuid':a.identity,'stable_mapping_retained':True,'disk_inode_preserved':True,'new_disk_writes_preserved':True,'new_generated_file_preserved':True,'new_tenant_records_retained':True,'legacy_sessions_mapped_back':True,'single_writer_fence':True,'production_changed':False,'production_backup_created':False,'actual_vm_adoption_verified':False,'actual_ext4_transfer_verified':False}
        a.db.close();return report


if __name__=='__main__':print(json.dumps(run_fixture(),indent=2))
