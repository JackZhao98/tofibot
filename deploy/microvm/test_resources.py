import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('manager', Path(__file__).with_name('manager.py'))
manager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manager)

class ResourcesTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config = dict(id='acceptance-resources', slot=22, state_dir=self.temp.name, image_dir='/opt/image', bin_dir='/opt/bin', socket_dir='/run/test')
        self.vm = manager.VM(self.config.copy())
        self.vm.host_resources = mock.Mock(return_value=dict(cpus=4, memory_total_mib=16384, memory_available_mib=12000, disk_available_gib=100))

    def test_save_does_not_interrupt_running_vm_and_applies_next_start(self):
        self.vm.state = 'ready'
        self.vm.stop = mock.Mock(side_effect=AssertionError('must not stop'))
        desired = dict(vcpus=3,memory_mib=6144,disk_gib=8)
        result = self.vm.configure_resources(desired)
        self.assertTrue(result['pending'])
        self.assertEqual(result['current']['memory_mib'],4096)
        self.assertEqual(result['state'],'ready')
        self.vm.apply_resources()
        self.assertEqual(self.vm.current_resources(),desired)
        restored = manager.VM(self.config.copy())
        self.assertEqual(restored.current_resources(),desired)

    def test_rejects_oversized_unknown_and_boolean_values(self):
        for value in (dict(vcpus=True,memory_mib=4096,disk_gib=8),dict(vcpus=5,memory_mib=4096,disk_gib=8),dict(vcpus=2,memory_mib=16384,disk_gib=8),dict(vcpus=2,memory_mib=4096,disk_gib=8,path='/tmp')):
            with self.assertRaises(ValueError): self.vm.configure_resources(value)
        self.assertFalse((self.vm.root/'resources-desired.json').exists())

    def test_growth_failure_preserves_original_and_pending_request(self):
        disk = self.vm.root/'workspace.ext4'
        disk.write_bytes(b'original')
        with disk.open('r+b') as stream: stream.truncate(8*1024**3)
        desired = dict(vcpus=2,memory_mib=4096,disk_gib=12)
        self.vm.configure_resources(desired)
        def failed_run(*args, **kwargs):
            if args[0]=='cp':
                Path(args[-1]).write_bytes(b'copy')
                return mock.Mock(returncode=0)
            if args[0]=='e2fsck':return mock.Mock(returncode=0)
            raise RuntimeError('resize failed')
        with mock.patch.object(manager,'run',side_effect=failed_run):
            with self.assertRaises(RuntimeError): self.vm.apply_resources()
        self.assertEqual(disk.stat().st_size,8*1024**3)
        with disk.open('rb') as stream:self.assertEqual(stream.read(8),b'original')
        self.assertTrue(self.vm.resources()['pending'])
        self.assertFalse((self.vm.root/'workspace-growing.ext4').exists())

    def test_growth_reports_resize_stderr_and_forces_full_fsck(self):
        disk = self.vm.root/'workspace.ext4'
        disk.write_bytes(b'original')
        with disk.open('r+b') as stream: stream.truncate(8*1024**3)
        self.vm.configure_resources(dict(vcpus=2,memory_mib=4096,disk_gib=12))
        calls=[]
        def failed_resize(*args, **kwargs):
            calls.append(args)
            if args[0]=='cp':
                Path(args[-1]).write_bytes(b'copy')
                return mock.Mock(returncode=0, stdout='', stderr='')
            if args[0]=='e2fsck':return mock.Mock(returncode=0, stdout='', stderr='')
            return mock.Mock(returncode=1, stdout='', stderr="Please run 'e2fsck -f image' first.")
        with mock.patch.object(manager,'run',side_effect=failed_resize):
            with self.assertRaisesRegex(RuntimeError, "Please run 'e2fsck -f image' first"):
                self.vm.apply_resources()
        self.assertIn(('e2fsck','-f','-p',str(self.vm.root/'workspace-growing.ext4')), calls)
        self.assertEqual(disk.stat().st_size,8*1024**3)
        self.assertTrue(self.vm.resources()['pending'])
        self.assertFalse((self.vm.root/'workspace-growing.ext4').exists())

    def test_growth_keeps_rollback_and_never_shrinks(self):
        disk=self.vm.root/'workspace.ext4'
        disk.write_bytes(b'original')
        with disk.open('r+b') as stream:stream.truncate(8*1024**3)
        self.vm.configure_resources(dict(vcpus=2,memory_mib=4096,disk_gib=12))
        def fake_run(*args,**kwargs):
            if args[0]=='cp':Path(args[-1]).write_bytes(b'copy')
            return mock.Mock(returncode=0)
        with mock.patch.object(manager,'run',side_effect=fake_run):self.vm.apply_resources()
        self.assertEqual(disk.stat().st_size,12*1024**3)
        backups=list(self.vm.root.glob('workspace-before-growth-*.ext4'))
        self.assertEqual(len(backups),1)
        with backups[0].open('rb') as stream:self.assertEqual(stream.read(8),b'original')
        with self.assertRaises(ValueError):self.vm.configure_resources(dict(vcpus=2,memory_mib=4096,disk_gib=8))

    def test_memory_admission_preserves_pending_capacity(self):
        self.vm.configure_resources(dict(vcpus=2,memory_mib=8192,disk_gib=8))
        self.vm.host_resources.return_value['memory_available_mib']=9000
        with self.assertRaises(RuntimeError):self.vm.apply_resources()
        self.assertEqual(self.vm.current_resources()['memory_mib'],4096)

if __name__=='__main__':unittest.main()

class RestartTests(unittest.TestCase):
    def test_restart_is_explicit_serialized_and_releases_ops_lock(self):
        import threading
        import types
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            vm = mock.Mock(root=root, state='ready')
            vm.resources.return_value={'desired':dict(vcpus=2,memory_mib=4096,disk_gib=8)}
            server = types.SimpleNamespace(vm=vm, preparation_lock=threading.Lock())
            task = mock.Mock()
            task.is_alive.return_value = True
            with mock.patch.object(manager.threading, 'Thread', return_value=task) as thread:
                self.assertTrue(manager.Server.restart(server))
                self.assertEqual(vm.state, 'restarting')
                vm.stop.assert_not_called()
                self.assertFalse(manager.Server.restart(server))
                worker = thread.call_args.kwargs['target']
            with mock.patch.object(manager, 'Path', return_value=root/'admission'):
                worker()
            vm.stop.assert_called_once()
            vm.start.assert_called_once()
            with (root/'ops.lock').open('a') as lease:
                manager.fcntl.flock(lease, manager.fcntl.LOCK_EX | manager.fcntl.LOCK_NB)

    def test_restart_refuses_maintenance_lock(self):
        import threading
        import types
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            vm = mock.Mock(root=root, state='ready')
            vm.resources.return_value={'desired':dict(vcpus=2,memory_mib=4096,disk_gib=8)}
            server = types.SimpleNamespace(vm=vm, preparation_lock=threading.Lock())
            with (root/'ops.lock').open('a') as lease:
                manager.fcntl.flock(lease, manager.fcntl.LOCK_EX | manager.fcntl.LOCK_NB)
                self.assertFalse(manager.Server.restart(server))
            self.assertEqual(vm.state, 'ready')
            vm.stop.assert_not_called()

    def test_resources_observable_while_lifecycle_lock_held(self):
        import threading
        with tempfile.TemporaryDirectory() as directory:
            vm=manager.VM(dict(id='test',slot=22,state_dir=directory,image_dir='/tmp',bin_dir='/tmp',socket_dir='/tmp/socket'))
            vm.host_resources=mock.Mock(return_value=dict(cpus=4,memory_total_mib=16384,disk_available_gib=100))
            ready=threading.Event()
            release=threading.Event()
            def hold():
                with vm.lock:
                    ready.set()
                    release.wait(2)
            thread=threading.Thread(target=hold)
            thread.start()
            ready.wait(1)
            try:
                import time
                started=time.monotonic()
                self.assertEqual(vm.resources()['current']['vcpus'],2)
                self.assertLess(time.monotonic()-started,0.5)
            finally:
                release.set()
                thread.join()

    def test_failed_restart_recovers_previous_allocation_and_keeps_request(self):
        import threading
        import types
        import json
        with tempfile.TemporaryDirectory() as directory:
            vm=manager.VM(dict(id='test',slot=22,state_dir=directory,image_dir='/tmp',bin_dir='/tmp',socket_dir='/tmp/socket'))
            vm.state='ready'
            vm.host_resources=mock.Mock(return_value=dict(cpus=4,memory_total_mib=16384,disk_available_gib=100))
            desired=dict(vcpus=3,memory_mib=6144,disk_gib=8)
            vm.persist_resources('resources-desired.json',desired)
            calls=[]
            def boot():
                calls.append(vm.desired_resources())
                if len(calls)==1:raise RuntimeError('capacity changed')
                vm.state='ready'
            vm.start=boot
            vm.stop=mock.Mock()
            server=types.SimpleNamespace(vm=vm,preparation_lock=threading.Lock())
            task=mock.Mock()
            with mock.patch.object(manager.threading,'Thread',return_value=task) as thread:
                self.assertTrue(manager.Server.restart(server))
                worker=thread.call_args.kwargs['target']
            with mock.patch.object(manager,'Path',return_value=Path(directory)/'admission'):
                worker()
            self.assertEqual(calls,[desired,dict(vcpus=2,memory_mib=4096,disk_gib=8)])
            self.assertEqual(vm.state,'ready')
            self.assertIn('capacity changed',vm.error)
            self.assertEqual(vm.desired_resources(),desired)
            self.assertFalse((vm.root/'resources-desired.restart-hold.json').exists())

    def test_failed_restart_and_failed_recovery_keep_fallback_and_request(self):
        import json
        import threading
        import types
        with tempfile.TemporaryDirectory() as directory:
            vm=manager.VM(dict(id='test',slot=22,state_dir=directory,image_dir='/tmp',bin_dir='/tmp',socket_dir='/tmp/socket'))
            vm.state='ready'
            vm.host_resources=mock.Mock(return_value=dict(cpus=4,memory_total_mib=16384,memory_available_mib=12000,disk_available_gib=100))
            desired=dict(vcpus=3,memory_mib=6144,disk_gib=8)
            fallback=dict(vcpus=2,memory_mib=4096,disk_gib=8)
            vm.persist_resources('resources-desired.json',desired)
            vm.start=mock.Mock(side_effect=[RuntimeError('requested boot failed'),RuntimeError('fallback boot failed')])
            vm.stop=mock.Mock()
            server=types.SimpleNamespace(vm=vm,preparation_lock=threading.Lock())
            task=mock.Mock()
            with mock.patch.object(manager.threading,'Thread',return_value=task) as thread:
                self.assertTrue(manager.Server.restart(server))
                worker=thread.call_args.kwargs['target']
            with mock.patch.object(manager,'Path',return_value=Path(directory)/'admission'):
                worker()
            self.assertEqual(vm.state,'error')
            self.assertIn('requested boot failed',vm.error)
            self.assertIn('recovery failed: fallback boot failed',vm.error)
            self.assertEqual(vm.desired_resources(),fallback)
            self.assertEqual(json.loads((vm.root/'resources-applied.json').read_text()),fallback)
            self.assertEqual(json.loads((vm.root/'resources-desired.restart-hold.json').read_text()),desired)
            self.assertEqual(vm.resources()['desired'],desired)
            self.assertTrue(vm.resources()['pending'])

    def test_prepare_with_restart_hold_boots_fallback_then_restores_request(self):
        import threading
        import types
        with tempfile.TemporaryDirectory() as directory:
            vm=manager.VM(dict(id='test',slot=22,state_dir=directory,image_dir='/tmp',bin_dir='/tmp',socket_dir='/tmp/socket'))
            fallback=dict(vcpus=2,memory_mib=4096,disk_gib=8)
            desired=dict(vcpus=3,memory_mib=6144,disk_gib=8)
            vm.persist_resources('resources-desired.json',fallback)
            vm.persist_resources('resources-applied.json',fallback)
            vm.persist_resources('resources-desired.restart-hold.json',desired)
            vm.state='error'
            vm.host_resources=mock.Mock(return_value=dict(cpus=4,memory_total_mib=16384,memory_available_mib=12000,disk_available_gib=100))
            starts=[]
            def start():
                starts.append(vm.desired_resources())
                vm.state='ready'
            vm.start=start
            server=types.SimpleNamespace(vm=vm,preparation_lock=threading.Lock())
            task=mock.Mock()
            with mock.patch.object(manager.threading,'Thread',return_value=task) as thread:
                self.assertTrue(manager.Server.prepare(server))
                worker=thread.call_args.kwargs['target']
            with mock.patch.object(manager,'Path',return_value=Path(directory)/'admission'):
                worker()
            self.assertEqual(starts,[fallback])
            self.assertEqual(vm.state,'ready')
            self.assertEqual(vm.desired_resources(),desired)
            self.assertFalse((vm.root/'resources-desired.restart-hold.json').exists())
            self.assertTrue(vm.resources()['pending'])
