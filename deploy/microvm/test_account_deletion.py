"""Destructive tests use only newly created synthetic temporary footprints."""
import fcntl
import contextlib
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import sqlite3
import unittest
from unittest.mock import Mock, patch
import uuid

sys.path.insert(0,str(Path(__file__).parent))
from account_capacity import AdmissionError
from account_provisioner import Broker
import account_deletion as deletion
from worker_supervisor import WorkerBroker


class DeletionTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(dir=Path(__file__).parent)
        self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name).resolve()
        release=self.root/'release'; release.mkdir()
        (release/'bin').mkdir();(release/'bin/firecracker').write_bytes(b'synthetic firecracker');(release/'bin/firecracker').chmod(0o555)
        (release/'manager.py').write_text('# synthetic manager')
        for name in ('rootfs.ext4','vmlinux'):
            (release/name).write_bytes(b'synthetic immutable '+name.encode())
            (release/name).chmod(0o444)
        self.config={k:str(self.root/k) for k in ('state_root','config_root','unit_root','socket_root','ledger_root')}
        self.config.update(release_dir=str(release),headroom_bytes=10*1024**3,warning_bytes=15*1024**3,
                           reserved_slots=[],per_account_internal_reserved_bytes=2*1024**3,
                           vcpus=2,memory_mib=2048,runtime_vcpu_budget=8,
                           runtime_memory_mib_budget=16384,host_memory_headroom_mib=2048,
                           app_uid=10001,socket_gid=10001)
        self.metrics=lambda:{'total_bytes':100*1024**3,'available_bytes':80*1024**3}
        self.b=Broker(self.config,run=Mock(),metrics=self.metrics)
        # This seam replaces ALL process/network operations. No host VM or
        # retained account is touched, even when a test invokes delete.
        self.b.stop_manager=Mock()
        self.b.verify_delete_cleanup=Mock()
        self.b.supports_computer_deletion=True
        self.identity=str(uuid.uuid4())
        self.b.dispatch(dict(op='reserve',account_id=self.identity,quota_gib=8))
        self.status=self.b.dispatch(dict(op='computer_status',account_id=self.identity))
        self.request=dict(op='delete',account_id=self.identity,generation=self.status['generation'],operation_id=str(uuid.uuid4()))

    def footprint(self,identity=None):
        identity=identity or self.identity
        with patch.object(self.b,'runtime_admission'):
            self.b.dispatch(dict(op='ensure',account_id=identity))
        state=Path(self.config['state_root'])/identity;state.mkdir()
        disk=state/'workspace.ext4'; disk.write_bytes(b'synthetic disk')
        (state/'manager.lock').touch()
        jail=state/'jails/firecracker'/('ac-'+identity)/'root';(jail/'run').mkdir(parents=True)
        os.link(disk,jail/'workspace.ext4')
        for name in ('vmlinux','rootfs.ext4'):os.link(self.root/'release'/name,jail/name)
        (jail/'config.json').write_text('{}')
        (jail/'firecracker').write_bytes(b'synthetic firecracker');(jail/'firecracker').chmod(0o555)
        (jail/'firecracker.pid').write_text('123')
        (state/'workspace-before-growth-123.ext4').write_bytes(b'synthetic owned recovery copy')
        (state/'console.log').write_text('synthetic console')
        with self.b.ledger.connection() as db:
            db.execute("UPDATE computers SET state='ready' WHERE account_id=?",(identity,))
            db.execute('INSERT INTO runtime_claims VALUES(?,?,?)',(identity,2,2560))
        return state,disk

    def assert_reserved(self):
        snapshot=self.b.ledger.snapshot()
        self.assertTrue(any(row['account_id']==self.identity for row in snapshot['accounts']))
        self.assertEqual(self.b.dispatch(dict(op='computer_status',account_id=self.identity))['state'],'cleanup_failed')

    def test_complete_cleanup_releases_promises_and_slot_preserves_other_data(self):
        state,disk=self.footprint()
        other=str(uuid.uuid4());self.b.dispatch(dict(op='reserve',account_id=other,quota_gib=8))
        other_state,other_disk=self.footprint(other)
        app=self.root/'app';app.mkdir();vault=app/'credentials';vault.write_text('synthetic retained vault')
        backup=self.root/'backups';backup.mkdir();(backup/'retained').write_text('synthetic external backup')
        before=self.b.ledger.snapshot()
        result=self.b.dispatch(self.request)
        self.assertEqual(result['state'],'deleted');self.assertTrue(result['resources_released'])
        self.assertFalse(state.exists());self.assertEqual(other_disk.read_bytes(),b'synthetic disk')
        self.assertEqual(vault.read_text(),'synthetic retained vault')
        self.assertEqual((backup/'retained').read_text(),'synthetic external backup')
        self.assertEqual((self.root/'release/rootfs.ext4').read_bytes(),b'synthetic immutable rootfs.ext4')
        after=self.b.ledger.snapshot()
        self.assertEqual(before['promised_bytes']-after['promised_bytes'],8*1024**3)
        self.assertEqual(before['internal_reserved_bytes']-after['internal_reserved_bytes'],2*1024**3)
        with self.b.ledger.connection() as db:
            self.assertIsNone(db.execute('SELECT 1 FROM runtime_claims WHERE account_id=?',(self.identity,)).fetchone())
            self.assertEqual(db.execute('SELECT state FROM computer_operations WHERE operation_id=?',(self.request['operation_id'],)).fetchone()[0],'deleted')
        new=str(uuid.uuid4());reserved=self.b.dispatch(dict(op='reserve',account_id=new,quota_gib=8))
        self.assertEqual(reserved['slot'],self.status['slot'])
        # A retry of the deleted operation cannot stop a new slot occupant.
        self.b.stop_manager.reset_mock();self.b.dispatch(self.request);self.b.stop_manager.assert_not_called()

    def test_disabled_or_never_provisioned_computer_can_be_deleted(self):
        self.b.dispatch(dict(op='disable',account_id=self.identity))
        self.assertEqual(self.b.dispatch(self.request)['state'],'deleted')

    def test_stop_or_cleanup_proof_failure_retains_runtime_and_disk(self):
        state,disk=self.footprint()
        for seam in ('stop_manager','verify_delete_cleanup'):
            with self.subTest(seam=seam):
                mock=getattr(self.b,seam);mock.side_effect=AdmissionError('synthetic cleanup failure')
                with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
                self.assert_reserved();self.assertTrue(disk.exists())
                with self.b.ledger.connection() as db:
                    self.assertIsNotNone(db.execute('SELECT 1 FROM runtime_claims WHERE account_id=?',(self.identity,)).fetchone())
                mock.side_effect=None
        self.assertEqual(self.b.dispatch(self.request)['state'],'deleted')

    def test_resumes_partial_unlink_from_immutable_manifest_after_restart(self):
        state,disk=self.footprint()
        original=deletion.os.unlink
        calls=0
        def interrupted(*args,**kwargs):
            nonlocal calls
            calls+=1
            if calls==2:raise OSError('synthetic interrupted unlink')
            return original(*args,**kwargs)
        with patch.object(deletion.os,'unlink',interrupted):
            with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        self.assert_reserved()
        restarted=Broker(self.config,run=Mock(),metrics=self.metrics)
        restarted.stop_manager=Mock();restarted.verify_delete_cleanup=Mock()
        restarted.supports_computer_deletion=True
        with patch.object(deletion,'_scan',side_effect=AssertionError('must reuse original manifest')):
            result=restarted.dispatch(self.request)
        self.assertEqual(result['state'],'deleted');self.assertFalse(state.exists())

    def test_deleted_tombstone_blocks_every_implicit_recreation_path(self):
        self.b.dispatch(self.request)
        for op,extra in (('ensure',{}),('reserve',{'quota_gib':8}),('quota',{'quota_gib':16}),('abort',{})):
            with self.subTest(op=op),self.assertRaises(AdmissionError):
                self.b.dispatch(dict(op=op,account_id=self.identity,**extra))
        with self.assertRaises(AdmissionError):self.b.ledger.reserve(self.identity,8*1024**3)
        for op in ('restore','disable','stop'):
            self.assertTrue(self.b.dispatch(dict(op=op,account_id=self.identity))['fenced'])
        for _ in range(4):self.assertTrue(self.b.dispatch(dict(op='computer_status',account_id=self.identity))['resources_released'])
        self.assertFalse((Path(self.config['state_root'])/self.identity).exists())

    def test_explicit_recreate_uses_new_generation_and_rejects_stale_delete(self):
        self.b.dispatch(self.request)
        request=dict(op='recreate',account_id=self.identity,generation=self.status['generation'],operation_id=str(uuid.uuid4()),quota_gib=8)
        new=self.b.dispatch(request)
        self.assertNotEqual(new['generation'],self.status['generation']);self.assertEqual(new['state'],'active')
        self.assertEqual(self.b.dispatch(request)['generation'],new['generation'])
        with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        self.assertFalse((Path(self.config['state_root'])/self.identity).exists())
        self.assertEqual(self.b.ledger.snapshot()['promised_bytes'],8*1024**3)

    def test_recreate_admission_failure_keeps_deleted_tombstone(self):
        self.b.dispatch(self.request)
        self.b.ledger.metrics=lambda:{'total_bytes':100*1024**3,'available_bytes':12*1024**3}
        with self.assertRaises(AdmissionError):
            self.b.dispatch(dict(op='recreate',account_id=self.identity,generation=self.status['generation'],operation_id=str(uuid.uuid4()),quota_gib=8))
        self.assertEqual(self.b.dispatch(dict(op='computer_status',account_id=self.identity))['state'],'deleted')

    def test_final_ledger_failure_never_releases_promise_until_retry(self):
        state,disk=self.footprint()
        original=self.b.ledger.connection
        class Database:
            def __init__(self,db):self.db=db
            def execute(self,sql,*args):
                if sql.startswith('DELETE FROM computers'):
                    raise sqlite3.OperationalError('synthetic final ledger failure')
                return self.db.execute(sql,*args)
        @contextlib.contextmanager
        def failing_connection():
            with original() as db:yield Database(db)
        with patch.object(self.b.ledger,'connection',failing_connection):
            with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        self.assertFalse(state.exists());self.assert_reserved()
        self.assertFalse(self.b.dispatch(dict(op='computer_status',account_id=self.identity))['resources_released'])
        self.assertEqual(self.b.dispatch(self.request)['state'],'deleted')

    def test_missing_ready_disk_stays_unverifiable_on_same_operation_retry(self):
        with self.b.ledger.connection() as db:
            db.execute("UPDATE computers SET state='ready' WHERE account_id=?",(self.identity,))
        for _ in range(2):
            with self.assertRaisesRegex(AdmissionError,'disk is missing'):self.b.dispatch(self.request)
            self.assert_reserved()

    def test_replaced_file_after_partial_unlink_is_not_deleted(self):
        state,disk=self.footprint()
        with patch.object(deletion,'_remove',side_effect=OSError('synthetic stop before unlink')):
            with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        console=state/'console.log';replacement=state/'replacement'
        replacement.write_text('synthetic foreign replacement');os.replace(replacement,console)
        with self.assertRaisesRegex(AdmissionError,'changed'):self.b.dispatch(self.request)
        self.assertEqual(console.read_text(),'synthetic foreign replacement');self.assert_reserved()

    def test_generated_artifact_appearing_after_manifest_retains_claims(self):
        state,disk=self.footprint()
        with patch.object(deletion,'_remove',side_effect=OSError('synthetic pause before unlink')):
            with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        pending=Path(self.config['config_root'])/(self.identity+'.json.pending')
        pending.write_text('synthetic unexpected generated write')
        with self.assertRaisesRegex(AdmissionError,'unexpected generated artifact'):self.b.dispatch(self.request)
        self.assertTrue(disk.exists());self.assertTrue(pending.exists());self.assert_reserved()
        pending.unlink()
        self.assertEqual(self.b.dispatch(self.request)['state'],'deleted')

    def test_recreation_refuses_unresolved_generated_write(self):
        self.b.dispatch(self.request)
        pending=Path(self.config['config_root'])/(self.identity+'.json.quota-pending')
        pending.write_text('synthetic unexpected generated write')
        with self.assertRaisesRegex(AdmissionError,'artifact appeared'):
            self.b.dispatch(dict(op='recreate',account_id=self.identity,generation=self.status['generation'],operation_id=str(uuid.uuid4()),quota_gib=8))
        self.assertTrue(pending.exists());self.assertEqual(self.b.dispatch(dict(op='computer_status',account_id=self.identity))['state'],'deleted')

    def test_foreign_slot_disk_owner_is_not_deleted(self):
        state,disk=self.footprint();original=os.stat
        def foreign(path,*args,**kwargs):
            info=original(path,*args,**kwargs)
            if str(path)=='workspace.ext4':
                values=list(info);values[4]=62000;values[5]=62000
                return os.stat_result(values)
            return info
        with patch.object(deletion.os,'stat',foreign):
            with self.assertRaisesRegex(AdmissionError,'another owner'):self.b.dispatch(self.request)
        self.assertTrue(disk.exists());self.assert_reserved()

    def test_foreign_hardlink_and_symlink_fail_closed(self):
        state,disk=self.footprint()
        foreign=self.root/'foreign';foreign.write_text('foreign retained fixture')
        link=state/'resources-applied.json';link.symlink_to(foreign)
        with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        self.assert_reserved();self.assertEqual(foreign.read_text(),'foreign retained fixture')
        link.unlink();os.link(disk,self.root/'foreign-disk-alias')
        with self.assertRaisesRegex(AdmissionError,'hardlink'):self.b.dispatch(self.request)
        self.assert_reserved();self.assertTrue(disk.exists())

    def test_foreign_owner_directory_and_unknown_file_fail_closed(self):
        state,disk=self.footprint()
        unknown=state/'secret-vault';unknown.write_text('synthetic retained unowned data')
        with self.assertRaisesRegex(AdmissionError,'unrecognized'):self.b.dispatch(self.request)
        self.assert_reserved();self.assertTrue(unknown.exists())
        unknown.unlink();state.chmod(0o777)
        with self.assertRaisesRegex(AdmissionError,'writable'):self.b.dispatch(self.request)
        state.chmod(0o700);self.assertTrue(disk.exists())

    def test_foreign_config_or_jail_alias_fail_closed(self):
        state,disk=self.footprint()
        config=Path(self.config['config_root'])/(self.identity+'.json')
        config.write_text('{}')
        with self.assertRaisesRegex(AdmissionError,'manifest'):self.b.dispatch(self.request)
        self.assert_reserved();self.assertTrue(disk.exists())

    def test_unresolved_resize_or_legacy_adoption_never_stops(self):
        with self.b.ledger.connection() as db:
            db.execute('INSERT INTO resize_fences(account_id,target_bytes) VALUES(?,?)',(self.identity,8*1024**3))
        with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        self.b.stop_manager.assert_not_called()
        with self.b.ledger.connection() as db:
            db.execute('DELETE FROM resize_fences')
            db.execute("INSERT INTO legacy_adoptions VALUES(?,?,?,?,?,?,?,?,?,?)",(self.identity,str(uuid.uuid4()),'personal','/synthetic/disk','/synthetic/config',1,2,8*1024**3,'worker',1))
        with self.assertRaises(AdmissionError):self.b.dispatch(self.request)
        self.b.stop_manager.assert_not_called()
        self.assertFalse(self.b.dispatch(dict(op='computer_status',account_id=self.identity))['supported'])

    def test_host_systemd_broker_is_unsupported_before_stop_or_intent(self):
        self.b.supports_computer_deletion=False
        self.assertFalse(self.b.dispatch(dict(op='computer_status',account_id=self.identity))['supported'])
        with self.assertRaisesRegex(AdmissionError,'isolated Worker'):self.b.dispatch(self.request)
        self.b.stop_manager.assert_not_called()
        current=self.b.dispatch(dict(op='computer_status',account_id=self.identity))
        self.assertEqual(current['state'],'active');self.assertIsNone(current['operation_id'])

    def test_path_injection_wrong_operation_and_generation_never_stop(self):
        for request in (dict(self.request,account_id='../other'),dict(self.request,generation=str(uuid.uuid4())),dict(self.request,path='/retained'),dict(self.request,operation_id='not-uuid')):
            with self.assertRaises((AdmissionError,ValueError)):self.b.dispatch(request)
        self.b.stop_manager.assert_not_called()

    def test_fence_commits_before_stop_race_with_ensure_quota_restore(self):
        reached=threading.Event();resume=threading.Event();failures=[]
        def stop(*_):reached.set();resume.wait(5)
        self.b.stop_manager.side_effect=stop
        def delete():
            try:self.b.dispatch(self.request)
            except Exception as exc:failures.append(exc)
        thread=threading.Thread(target=delete);thread.start()
        self.assertTrue(reached.wait(5))
        for op,extra in (('ensure',{}),('quota',{'quota_gib':16}),('reserve',{'quota_gib':8})):
            with self.assertRaises(AdmissionError):self.b.dispatch(dict(op=op,account_id=self.identity,**extra))
        self.assertTrue(self.b.dispatch(dict(op='restore',account_id=self.identity))['fenced'])
        resume.set();thread.join(5);self.assertFalse(thread.is_alive());self.assertEqual(failures,[])

    def test_worker_cleanup_proof_checks_manager_network_mount_and_open_files(self):
        # Exercise real proof logic against synthetic proc/network output only.
        worker=object.__new__(WorkerBroker);worker.c=self.config
        proc=self.root/'proc';(proc/'self').mkdir(parents=True);(proc/'self/mountinfo').write_text('')
        worker.supervisor=Mock(proc_root=proc,assert_vm_stopped=Mock())
        worker.run=Mock(side_effect=[Mock(stdout=''),Mock(stdout='[]'),Mock(stdout=''),Mock(stdout='')])
        worker.verify_delete_cleanup(self.identity,self.status['slot'])
        worker.run=Mock(side_effect=[Mock(stdout='tofi-fc-1'),Mock(stdout='[]'),Mock(stdout=''),Mock(stdout='')])
        with self.assertRaisesRegex(AdmissionError,'network'):worker.verify_delete_cleanup(self.identity,1)
        for filter_rules,nat_rules in (('-A INPUT -i tfh1 -j REJECT',''),('', '-A POSTROUTING -s 10.246.1.2/32 -j MASQUERADE')):
            worker.run=Mock(side_effect=[Mock(stdout=''),Mock(stdout='[]'),Mock(stdout=filter_rules),Mock(stdout=nat_rules)])
            with self.assertRaisesRegex(AdmissionError,'network'):worker.verify_delete_cleanup(self.identity,1)
        worker.run=Mock(side_effect=[Mock(stdout=''),Mock(stdout='[]'),Mock(stdout=''),Mock(stdout='')])
        (proc/'self/mountinfo').write_text('1 2 0:1 / '+str(Path(self.config['state_root'])/self.identity)+' rw - ext4 /dev/synthetic rw\n')
        with self.assertRaisesRegex(AdmissionError,'mount'):worker.verify_delete_cleanup(self.identity,1)
        (proc/'self/mountinfo').write_text('');(proc/'123/fd').mkdir(parents=True);(proc/'123/maps').write_text('')
        (proc/'123/fd/1').symlink_to(Path(self.config['state_root'])/self.identity/'workspace.ext4')
        worker.run=Mock(side_effect=[Mock(stdout=''),Mock(stdout='[]'),Mock(stdout=''),Mock(stdout='')])
        with self.assertRaisesRegex(AdmissionError,'open'):worker.verify_delete_cleanup(self.identity,1)


if __name__=='__main__':unittest.main()
