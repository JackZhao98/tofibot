import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import uuid

sys.path.insert(0, str(Path(__file__).parent))
from account_adoption import proof, stable_identity
from account_capacity import AdmissionError
from account_provisioner import Broker
from account_transition import Transition, assert_no_disk_users
from account_runner_transfer import RunnerTransfer, DISK_ROOT, GUEST_ROOT

GIB = 1024 ** 3


def disk_fixture(path, size=8*GIB):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('wb') as stream:
        stream.truncate(size)
        stream.seek(1024)
        sb = bytearray(1024); sb[56:58] = b'\x53\xef'
        sb[4:8] = (size // 4096).to_bytes(4, 'little'); sb[24:28] = (2).to_bytes(4, 'little')
        stream.write(sb)
    os.chmod(path, 0o600)


class FixtureRunner:
    def __init__(self): self.calls = 0
    def import_attachments(self, *args): return 0
    def import_state(self, disk, *args, **kwargs):
        self.calls += 1
        with disk.open('r+b') as stream:
            stream.seek(4096); stream.write(b'synthetic imported Runner state')


class TransitionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-account-transition-', dir='/tmp')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve(); self.deploy = self.root/'deploy'
        (self.deploy/'data').mkdir(parents=True)
        self.instance = str(uuid.uuid4())
        (self.deploy/'data/instance.json').write_text(json.dumps({'id':self.instance}))
        (self.deploy/'data/current.db').write_bytes(b'synthetic current database')
        (self.deploy/'data/mcp.json').write_text(json.dumps({'mcpServers':{
            'local_fixture':{'url':'http://legacy-runner:8390/mcp/fixture','headers':{'Authorization':'synthetic-token','X-Reviewed':'yes'},'tool_allowlist':['echo']},
            'remote_fixture':{'url':'https://example.test/mcp','headers':{'Authorization':'synthetic-remote-token'}}}}))
        self.source = self.root/'personal/workspace.ext4'; disk_fixture(self.source)
        self.alias = self.source.parent/'jails/firecracker/personal/root/workspace.ext4'
        self.alias.parent.mkdir(parents=True); os.link(self.source, self.alias)
        self.config = self.root/'personal.json'
        self.config.write_text(json.dumps({'id':'personal','slot':1,'state_dir':str(self.source.parent),'disk_gib':8}))
        os.chmod(self.config, 0o600)
        self.runner_source = self.root/'runner'; self.runner_source.mkdir()
        (self.runner_source/'manifest.json').write_text('[]')
        release = self.root/'release'; release.mkdir(); (release/'manager.py').write_text('# fixture')
        cfg = {k:str(self.root/k) for k in ('state_root','config_root','unit_root','socket_root','ledger_root')}
        cfg.update(release_dir=str(release), headroom_bytes=GIB, warning_bytes=2*GIB,
                   reserved_slots=[1], vcpus=1, memory_mib=1024, runtime_vcpu_budget=4, runtime_memory_mib_budget=8192, per_account_internal_reserved_bytes=8*GIB,
                   external_disks=[dict(asset_id='personal', disk_path=str(self.source), quota_bytes=8*GIB, alias_paths=[str(self.alias)])])
        self.b = Broker(cfg, metrics=lambda:dict(total_bytes=320*GIB, available_bytes=300*GIB, filesystem_type='ext4'))
        self.manifest = dict(deployment_root=str(self.deploy), worker_config=str(self.root/'worker.json'),
                             personal_config=str(self.config),runner_state=str(self.runner_source),
                             runner_prefix='/runner/state', runner_origin='http://legacy-runner:8390')
        self.runner = FixtureRunner(); self.users = []
        def run(args, **kwargs):
            output = ('{ argv[]=/usr/bin/python3 /fixture/manager.py ' + str(self.config) + ' ; }').encode() if '--property=ExecStart' in args else b'0\n'
            return subprocess.CompletedProcess(args, 0, output, b'')
        self.run = run
        self.t = self.transition()

    def transition(self):
        return Transition(self.manifest, self.b, run=self.run,
                          disk_users=lambda disk:self.users.append((disk.stat().st_dev,disk.stat().st_ino)),runner=self.runner)

    def test_preserved_resource_override_and_budget_rejection_before_stop(self):
        self.b.c['account_resource_overrides'] = {self.t.identity:dict(vcpus=2,memory_mib=4096)}
        self.assertEqual(self.b.runtime_resources(self.t.identity), (2,4096))
        self.b.c['runtime_vcpu_budget'] = 1
        self.t.run = lambda *a, **k: self.fail('budget refusal must precede service stop')
        with self.assertRaisesRegex(AdmissionError, 'runtime budget'):
            self.t.adopt()
        self.assertTrue(self.source.exists())
        self.assertFalse(self.t.intent.exists())
        self.assertEqual(self.b.runtime_resources(str(uuid.uuid4())), (1,1024))

    def test_real_operator_chain_same_inode_capacity_and_retained_write_rollback(self):
        before = self.source.stat()
        original_db = (self.deploy/'data/current.db').read_bytes()
        mcp_path = self.deploy/'data/mcp.json'
        mcp = json.loads(mcp_path.read_text());mcp['servers'] = None;mcp_path.write_text(json.dumps(mcp))
        result = self.t.adopt()
        self.assertTrue(result['verified']); self.assertEqual(result['account_id'], stable_identity(self.instance))
        self.assertFalse(self.config.exists()); self.assertFalse(self.source.exists()); self.assertFalse(self.alias.exists())
        overlay=json.loads((self.deploy/'accounts.identity.overlay.yaml').read_text())
        self.assertEqual(overlay['services']['app']['environment']['TOFI_ACCOUNT_MAINTENANCE'],'${TOFI_ACCOUNT_MAINTENANCE:-1}')
        after = self.t.target.stat(); self.assertEqual((before.st_dev,before.st_ino),(after.st_dev,after.st_ino))
        capacity = self.b.ledger.snapshot()
        self.assertEqual(capacity['external_promised_bytes'], 0)
        self.assertEqual(capacity['promised_bytes'],8*GIB)
        self.assertEqual(capacity['internal_reserved_bytes'],8*GIB)
        config=json.loads((self.deploy/'data/mcp.json').read_text())['mcpServers']
        self.assertEqual(config['local_fixture']['url'],'http://account-computer/v1/runner/mcp/fixture')
        self.assertNotIn('Authorization',config['local_fixture']['headers'])
        self.assertEqual(config['local_fixture']['headers']['X-Reviewed'],'yes')
        self.assertEqual(config['remote_fixture']['headers']['Authorization'],'synthetic-remote-token')
        self.assertEqual(self.transition().adopt()['phase'],'worker'); self.assertEqual(self.runner.calls,1)
        new_id=str(uuid.uuid4()); self.b.ledger.reserve(new_id,8*GIB)
        disk_fixture(Path(self.b.c['state_root'])/new_id/'workspace.ext4')
        (self.deploy/'data/new-generated').write_bytes(b'new generated file retained')
        with self.t.target.open('r+b') as stream: stream.seek(8192); stream.write(b'new disk write retained')
        result=self.transition().rollback(); self.assertTrue(result['retained_data'])
        self.assertFalse(result['services_started']); self.assertTrue(result['compatible_account_app_required'])
        self.assertEqual((self.source.stat().st_dev,self.source.stat().st_ino),(before.st_dev,before.st_ino))
        with self.source.open('rb') as stream:stream.seek(8192);self.assertEqual(stream.read(23),b'new disk write retained')
        self.assertEqual((self.deploy/'data/current.db').read_bytes(),original_db)
        self.assertEqual((self.deploy/'data/new-generated').read_bytes(),b'new generated file retained')
        self.assertTrue((Path(self.b.c['state_root'])/new_id/'workspace.ext4').exists())
        capacity=self.b.ledger.snapshot(); self.assertEqual(capacity['external_promised_bytes'],8*GIB)
        self.assertEqual([a['account_id'] for a in capacity['accounts']],[new_id])
        self.assertEqual(proof(self.b,self.t.identity)['phase'],'legacy')
        self.assertEqual(self.transition().rollback()['phase'],'legacy')

    def test_recovery_after_inode_rename(self):
        original=os.rename
        def interrupted(source,target):
            original(source,target)
            if target == self.t.target: raise OSError('synthetic interruption after rename')
        with patch('account_transition.os.rename',side_effect=interrupted):
            with self.assertRaises(OSError):self.t.adopt()
        with self.assertRaises(AdmissionError):self.b.ledger.snapshot()
        with self.assertRaises(AdmissionError):self.b.dispatch(dict(op='ensure',account_id=self.t.identity))
        self.assertEqual(self.transition().adopt()['phase'],'worker')

    def test_failed_import_rollback_preserves_current_disk_and_files(self):
        before=self.source.stat().st_ino
        self.runner.import_state=lambda *a,**kw:(_ for _ in ()).throw(AdmissionError('synthetic import failure'))
        with self.assertRaises(AdmissionError):self.t.adopt()
        with self.t.target.open('r+b') as stream:stream.seek(8192);stream.write(b'failure investigation marker')
        self.assertTrue(self.transition().rollback()['retained_data'])
        self.assertEqual(self.source.stat().st_ino,before)
        with self.source.open('rb') as stream:stream.seek(8192);self.assertEqual(stream.read(28),b'failure investigation marker')
        self.assertTrue(self.config.exists())

    def test_live_writer_and_lock_refuse_before_transfer(self):
        self.t.disk_users=lambda _:(_ for _ in ()).throw(AdmissionError('live writer'))
        with self.assertRaises(AdmissionError):self.t.adopt()
        self.assertTrue(self.config.exists());self.assertTrue(self.source.exists());self.assertFalse(self.t.intent.exists())
        import fcntl
        with (Path(self.b.c['ledger_root'])/'service.lock').open('a') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            with self.assertRaises(AdmissionError):self.transition().adopt()

    def test_service_pid_nonzero_refuses_and_unknown_alias_refuses(self):
        self.t.run=lambda *a,**kw:subprocess.CompletedProcess(a,0,b'123\n',b'')
        with self.assertRaises(AdmissionError):self.t.adopt()
        self.assertFalse(self.t.intent.exists())
        os.link(self.source,self.root/'untracked-disk-link')
        with self.assertRaises(AdmissionError):self.transition()

    def test_capacity_requires_internal_promise_without_double_disk_promise(self):
        self.b.ledger.metrics=lambda:dict(total_bytes=20*GIB,available_bytes=10*GIB,filesystem_type='ext4')
        with self.assertRaises(AdmissionError):self.t.adopt()
        self.assertTrue(self.source.exists());self.assertTrue(self.config.exists());self.assertFalse(self.t.intent.exists())

    def test_rollback_retains_grown_geometry(self):
        self.t.adopt();disk_fixture(self.t.target,16*GIB)
        with self.b.ledger.connection() as db:db.execute('UPDATE computers SET quota_bytes=? WHERE account_id=?',(16*GIB,self.t.identity))
        self.transition().rollback()
        self.assertEqual(json.loads(self.config.read_text())['disk_gib'],16)
        self.assertEqual(self.b.ledger.snapshot()['external_promised_bytes'],16*GIB)

    def test_config_reappearance_disk_replacement_and_manifest_change_fail_closed(self):
        self.t.adopt();self.config.write_text('{}')
        with self.assertRaises(AdmissionError):proof(self.b,self.t.identity)
        self.config.unlink()
        self.t.target.rename(self.t.target.with_suffix('.retained'));disk_fixture(self.t.target)
        with self.assertRaises(AdmissionError):proof(self.b,self.t.identity)
        with self.assertRaises(AdmissionError):self.transition().rollback()
        self.manifest['runner_origin']='http://changed.test'
        with self.assertRaises(AdmissionError):self.transition()

    def test_actual_host_fd_writer_probe(self):
        if not Path('/proc/self/fd').is_dir():self.skipTest('Linux host FD inventory required')
        with self.source.open('rb'):
            with self.assertRaises(AdmissionError):assert_no_disk_users(self.source)

    def test_fd_mapping_and_kernel_loop_visibility_are_all_required(self):
        proc=self.root/'proc';(proc/'123/fd').mkdir(parents=True)
        maps=proc/'123/maps';maps.write_text('')
        empty=lambda *a,**kw:subprocess.CompletedProcess(a,0,b'{"loopdevices":[]}',b'')
        assert_no_disk_users(self.source,proc,empty)
        os.symlink(self.source,proc/'123/fd/5')
        with self.assertRaises(AdmissionError):assert_no_disk_users(self.source,proc,empty)
        (proc/'123/fd/5').unlink();info=self.source.stat()
        maps.write_text('0000-1000 rw-p 0000 '+format(os.major(info.st_dev),'x')+':'+format(os.minor(info.st_dev),'x')+' '+str(info.st_ino)+' synthetic\n')
        with self.assertRaises(AdmissionError):assert_no_disk_users(self.source,proc,empty)
        maps.write_text('')
        loop=lambda *a,**kw:subprocess.CompletedProcess(a,0,json.dumps({'loopdevices':[{'name':'fixture-loop','back-file':str(self.source)}]}).encode(),b'')
        with self.assertRaises(AdmissionError):assert_no_disk_users(self.source,proc,loop)
        unknown=lambda *a,**kw:subprocess.CompletedProcess(a,0,b'{"loopdevices":[{"name":"fixture-loop","back-file":null}]}',b'')
        with self.assertRaises(AdmissionError):assert_no_disk_users(self.source,proc,unknown)

    def test_recovery_after_sql_commit_before_journal_completion(self):
        save=self.t.save
        def interrupted():
            if self.t.j['phase']=='worker':raise OSError('synthetic interruption after commit')
            save()
        self.t.save=interrupted
        with self.assertRaises(OSError):self.t.adopt()
        self.assertEqual(proof(self.b,self.t.identity)['phase'],'worker')
        self.assertEqual(self.transition().adopt()['phase'],'worker')

    @unittest.skipUnless(shutil.which('debugfs') and shutil.which('mkfs.ext4') and shutil.which('e2fsck'),'Linux e2fsprogs required')
    def test_actual_ext4_operator_transfer_and_current_data_rollback(self):
        # Sparse real 8GiB ext4, few inodes, no journal: small owned test
        # allocation, not a production capacity or live-VM acceptance claim.
        subprocess.run(['mkfs.ext4','-q','-F','-N','128','-m','0','-O','^has_journal',str(self.source)],check=True,capture_output=True)
        import sqlite3
        identity=str(uuid.uuid4());uploads=self.deploy/'data/attachments';uploads.mkdir()
        upload=uploads/(identity+'.upload');upload.write_bytes(b'old upload moved into quota')
        db=sqlite3.connect(self.deploy/'data/tofi.db');db.execute('CREATE TABLE attachments(id TEXT PRIMARY KEY,disk_name TEXT,size INTEGER)')
        db.execute('INSERT INTO attachments VALUES(?,?,?)',(identity,identity+'.upload',upload.stat().st_size));db.commit();db.close()
        adapter=RunnerTransfer(lambda args,**kw:subprocess.run(args,capture_output=True,**kw))
        transition=self.transition();transition.runner=adapter;before=self.source.stat().st_ino
        self.assertEqual(transition.adopt()['phase'],'worker')
        with tempfile.TemporaryDirectory(prefix='tofi-readback-',dir='/tmp') as temporary:
            readback=Path(temporary)/'upload'
            adapter.debug(transition.target,'dump /shared/.tofi/blobs/'+identity+' '+str(readback))
            self.assertEqual(readback.read_bytes(),upload.read_bytes())
        self.assertEqual(subprocess.run(['e2fsck','-f','-n',str(transition.target)],capture_output=True).returncode,0)
        rollback=self.transition();rollback.runner=adapter;rollback.rollback()
        self.assertEqual(self.source.stat().st_ino,before)
        db=sqlite3.connect(self.deploy/'data/tofi.db');self.assertEqual(db.execute('SELECT disk_name FROM attachments').fetchone()[0],'vm:'+identity);db.close()
        self.assertEqual(proof(self.b,self.t.identity)['phase'],'legacy')
        self.assertEqual(subprocess.run(['e2fsck','-f','-n',str(self.source)],capture_output=True).returncode,0)


@unittest.skipUnless(shutil.which('debugfs') and shutil.which('mkfs.ext4'),'e2fsprogs required for actual ext4 offline transfer')
class ActualExt4RunnerTests(unittest.TestCase):
    def test_existing_adapter_import_bytes_credentials_manifest_and_no_overwrite(self):
        with tempfile.TemporaryDirectory(prefix='tofi-ext4-runner-',dir='/tmp') as root:
            root=Path(root);disk=root/'workspace.ext4'
            with disk.open('wb') as stream:stream.truncate(16*1024**2)
            subprocess.run(['mkfs.ext4','-q','-F',str(disk)],check=True,capture_output=True)
            source=root/'runner';(source/'plugins/fixture/home').mkdir(parents=True)
            (source/'plugins/fixture/key').write_bytes(b'synthetic credential bytes')
            (source/'manifest.json').write_text(json.dumps([{'request':{'id':'fixture'},'spec':{
                'id':'fixture','command':'/runner/state/plugins/fixture/tool','work_dir':'/runner/state/plugins/fixture',
                'env':{'HOME':'/runner/state/plugins/fixture/home'},'secret_env':{'KEY':'/runner/state/plugins/fixture/key'}}}]))
            (source/'plugins/fixture/tool').write_text('#!/runner/state/plugins/fixture/python\nprint("fixture")\n')
            adapter=RunnerTransfer(lambda args,**kw:subprocess.run(args,capture_output=True,**kw))
            result=adapter.import_state(disk,source,'/runner/state',disk)
            self.assertEqual(result['plugins'],1)
            dumped=root/'dump'
            adapter.debug(disk,'dump '+DISK_ROOT+'/manifest.json '+str(dumped))
            record=json.loads(dumped.read_text())[0]['spec']
            self.assertEqual(record['work_dir'],GUEST_ROOT+'/plugins/fixture')
            adapter.debug(disk,'dump '+DISK_ROOT+'/plugins/fixture/key '+str(root/'key'))
            self.assertEqual((root/'key').read_bytes(),b'synthetic credential bytes')
            with self.assertRaises(AdmissionError):adapter.import_state(disk,source,'/runner/state',disk)
            self.assertEqual(adapter.import_state(disk,source,'/runner/state',disk,resume=True)['plugins'],1)
            # Historical attachment IDs/metadata stay intact, while file bytes
            # move to the same real ext4 quota as generated workspace files.
            import sqlite3
            app=root/'app';(app/'attachments').mkdir(parents=True)
            identity=str(uuid.uuid4());upload=app/'attachments'/(identity+'.upload');upload.write_bytes(b'original upload bytes')
            db=sqlite3.connect(app/'tofi.db');db.execute('CREATE TABLE attachments(id TEXT PRIMARY KEY,disk_name TEXT,size INTEGER)')
            db.execute('INSERT INTO attachments VALUES(?,?,?)',(identity,identity+'.upload',len(upload.read_bytes())));db.commit();db.close()
            self.assertEqual(adapter.import_attachments(disk,app),1)
            db=sqlite3.connect(app/'tofi.db');self.assertEqual(db.execute('SELECT disk_name FROM attachments').fetchone()[0],'vm:'+identity);db.close()
            destination='/shared/.tofi/blobs/'+identity
            adapter.debug(disk,'dump '+destination+' '+str(root/'upload-readback'))
            self.assertEqual((root/'upload-readback').read_bytes(),upload.read_bytes())
            self.assertEqual(adapter.import_attachments(disk,app),1)
            self.assertTrue(upload.exists())
            adapter.debug(disk,'rm '+destination,True)
            with self.assertRaises(AdmissionError):adapter.import_attachments(disk,app)
