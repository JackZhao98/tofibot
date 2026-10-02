import importlib.util
from pathlib import Path
import tempfile
import unittest
spec=importlib.util.spec_from_file_location('ops',Path(__file__).with_name('ops.py'))
ops=importlib.util.module_from_spec(spec);spec.loader.exec_module(ops)
class ResourceCheckpointTests(unittest.TestCase):
    def test_checkpoint_restores_capacity_and_removes_settings_absent_in_old_checkpoint(self):
        with tempfile.TemporaryDirectory() as root:
            state=Path(root)/'state';checkpoint=Path(root)/'checkpoint'
            state.mkdir();checkpoint.mkdir()
            (state/'resources-applied.json').write_text('{"memory_mib":4096}')
            ops.copy_resource_settings(state,checkpoint)
            (state/'resources-applied.json').write_text('{"memory_mib":8192}')
            (state/'resources-desired.json').write_text('{"memory_mib":12000}')
            ops.copy_resource_settings(checkpoint,state,restore=True)
            self.assertEqual((state/'resources-applied.json').read_text(),'{"memory_mib":4096}')
            self.assertFalse((state/'resources-desired.json').exists())

    def test_checkpoint_boot_hides_pending_then_restores_it_exactly(self):
        with tempfile.TemporaryDirectory() as root:
            state=Path(root)/'state';saved=Path(root)/'checkpoint'
            state.mkdir();saved.mkdir()
            pending='{"vcpus":2,"memory_mib":4096,"disk_gib":30}\n'
            (saved/'resources-desired.json').write_text(pending)
            (state/'resources-desired.json').write_text('{"disk_gib":8}')
            observed=[]
            def start():
                observed.append((state/'resources-desired.json').exists())
                self.assertEqual((state/'resources-desired.ops-hold.json').read_text(),pending)
            ops.start_with_checkpoint_allocation(state,saved/'resources-desired.json',start)
            self.assertEqual(observed,[False])
            self.assertEqual((state/'resources-desired.json').read_text(),pending)
            self.assertFalse((state/'resources-desired.ops-hold.json').exists())

    def test_failed_checkpoint_boot_restores_pending_request(self):
        with tempfile.TemporaryDirectory() as root:
            state=Path(root);saved=state/'saved.json'
            saved.write_text('{"disk_gib":30}')
            def failed_start():
                self.assertFalse((state/'resources-desired.json').exists())
                raise RuntimeError('not ready')
            with self.assertRaisesRegex(RuntimeError,'not ready'):
                ops.start_with_checkpoint_allocation(state,saved,failed_start)
            self.assertEqual((state/'resources-desired.json').read_text(),'{"disk_gib":30}')
            self.assertFalse((state/'resources-desired.ops-hold.json').exists())

    def test_checkpoint_boot_without_original_pending_keeps_desired_absent(self):
        with tempfile.TemporaryDirectory() as root:
            state=Path(root)
            (state/'resources-desired.json').write_text('checkpoint-pending')
            def start():
                self.assertFalse((state/'resources-desired.json').exists())
            ops.start_with_checkpoint_allocation(state,None,start)
            self.assertFalse((state/'resources-desired.json').exists())
            self.assertFalse((state/'resources-desired.ops-hold.json').exists())

    def test_interrupted_hold_is_recovered_before_next_operation(self):
        with tempfile.TemporaryDirectory() as root:
            state=Path(root)
            (state/'resources-desired.json').write_text('checkpoint-pending')
            (state/'resources-desired.ops-hold.json').write_text('original-user-request')
            ops.restore_held_desired(state)
            self.assertEqual((state/'resources-desired.json').read_text(),'original-user-request')
            self.assertFalse((state/'resources-desired.ops-hold.json').exists())
if __name__=='__main__':unittest.main()
