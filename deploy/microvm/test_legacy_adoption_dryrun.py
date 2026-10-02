import tempfile,unittest,uuid
from unittest import mock
from pathlib import Path
from legacy_adoption_dryrun import AdoptionError,create_fixture,run_fixture,stable_account_uuid
class AdoptionTests(unittest.TestCase):
 def test_rollback_keeps_new_writes_files_and_tenants(self):
  report=run_fixture();self.assertTrue(report['new_disk_writes_preserved']);self.assertTrue(report['disk_inode_preserved']);self.assertTrue(report['new_tenant_records_retained']);self.assertFalse(report['actual_vm_adoption_verified'])
 def test_mapping_is_stable_and_installation_scoped(self):
  i=str(uuid.uuid4());self.assertEqual(stable_account_uuid(i),stable_account_uuid(i));self.assertNotEqual(stable_account_uuid(i),stable_account_uuid(str(uuid.uuid4())))
 def test_live_writer_blocks_without_renaming(self):
  with tempfile.TemporaryDirectory(prefix='tofi-account-legacy-dryrun-') as root:
   a=create_fixture(root,str(uuid.uuid4()));a.db.execute("UPDATE ownership SET writer='legacy' WHERE id=1")
   with self.assertRaisesRegex(AdoptionError,'live writer'):a.fence()
   self.assertTrue((Path(root)/'personal/workspace.ext4').exists());a.db.close()
 def test_interruption_after_rename_recovers_same_inode_and_rolls_back(self):
  with tempfile.TemporaryDirectory(prefix='tofi-account-legacy-dryrun-') as root:
   i=str(uuid.uuid4());a=create_fixture(root,i);a.fence();before=a._identity(Path(root)/a.journal()[2])
   with self.assertRaisesRegex(AdoptionError,'simulated interruption'):a.transfer('worker',simulate_crash=True)
   a.db.close();from legacy_adoption_dryrun import FixtureAdoption
   a=FixtureAdoption(root,i);a.recover();a.recover();self.assertEqual(a._identity(Path(root)/a.journal()[2]),before);a.transfer('legacy');a.db.close()
 def test_replaced_disk_fails_closed(self):
  with tempfile.TemporaryDirectory(prefix='tofi-account-legacy-dryrun-') as root:
   a=create_fixture(root,str(uuid.uuid4()));a.fence()
   with self.assertRaises(AdoptionError):a.transfer('worker',simulate_crash=True)
   p=Path(root)/'worker'/a.identity/'workspace.ext4';replacement=p.with_name('replacement');replacement.write_bytes(b'wrong');replacement.replace(p)
   with self.assertRaisesRegex(AdoptionError,'replacement'):a.recover()
   self.assertTrue((Path(root)/'transfer-intent.json').exists());a.db.close()

 def test_commit_before_intent_removal_is_idempotently_recovered(self):
  with tempfile.TemporaryDirectory(prefix='tofi-account-legacy-dryrun-') as root:
   a=create_fixture(root,str(uuid.uuid4()));a.fence();original=Path.unlink
   def interrupted(path,*args,**kwargs):
    if path.name=='transfer-intent.json':raise OSError('simulated interruption after SQL commit')
    return original(path,*args,**kwargs)
   with mock.patch.object(Path,'unlink',interrupted):
    with self.assertRaisesRegex(OSError,'after SQL commit'):a.transfer('worker')
   with self.assertRaisesRegex(AdoptionError,'pending transfer'):a.transfer('worker')
   a.recover();a.recover();self.assertEqual(a.journal()[0],'worker');a.transfer('legacy');a.db.close()
