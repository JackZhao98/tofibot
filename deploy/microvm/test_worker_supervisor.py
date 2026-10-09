import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch
import uuid

sys.path.insert(0,str(Path(__file__).parent))
spec=importlib.util.spec_from_file_location("worker_supervisor",Path(__file__).with_name("worker_supervisor.py"))
worker=importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


class SupervisorTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(dir="/tmp")
        self.root=Path(self.temp.name)
        self.launch=Mock()
        self.supervisor=worker.ProcessSupervisor(self.root,self.root/"cgroups",popen=self.launch)

    def tearDown(self):
        self.temp.cleanup()

    def test_fixed_argv_idempotence_and_scoped_stop(self):
        ids=[str(uuid.uuid4()) for _ in range(2)]
        procs=[Mock(pid=101),Mock(pid=202)]
        for proc in procs:proc.poll.return_value=None
        self.launch.side_effect=procs
        for identity in ids:self.supervisor.ensure(identity,self.root/(identity+".json"))
        self.supervisor.ensure(ids[0],self.root/(ids[0]+".json"))
        self.assertEqual(self.launch.call_count,2)
        argv=self.launch.call_args_list[0].args[0]
        self.assertEqual(argv,[sys.executable,str(self.root/"manager.py"),str(self.root/(ids[0]+".json"))])
        self.assertEqual(self.launch.call_args_list[0].kwargs,{"start_new_session":True})
        with patch.object(worker.os,"killpg") as kill:
            self.supervisor.stop(ids[0])
            kill.assert_called_once_with(101,worker.signal.SIGTERM)
        self.assertIn(ids[1],self.supervisor.processes)

    def test_running_reports_only_a_live_manager(self):
        identity=str(uuid.uuid4());proc=Mock(pid=404);proc.poll.return_value=None
        self.launch.return_value=proc
        self.assertFalse(self.supervisor.running(identity))
        self.supervisor.ensure(identity,self.root/(identity+".json"))
        self.assertTrue(self.supervisor.running(identity))
        proc.poll.return_value=0
        self.assertFalse(self.supervisor.running(identity))
        with self.assertRaises(ValueError):self.supervisor.running("../other")
        broker=object.__new__(worker.WorkerBroker)
        broker.supervisor=self.supervisor
        self.assertFalse(broker.manager_running(identity))

    def test_no_pid_reuse_kill_and_unresolved_cleanup_blocks_restart(self):
        identity=str(uuid.uuid4());proc=Mock(pid=303);proc.poll.return_value=None
        proc.wait.side_effect=[subprocess.TimeoutExpired("manager",60),0]
        self.launch.return_value=proc
        self.supervisor.ensure(identity,self.root/(identity+".json"))
        with patch.object(worker.os,"killpg"):
            with self.assertRaises(worker.AdmissionError):self.supervisor.stop(identity)
        with self.assertRaises(worker.AdmissionError):self.supervisor.ensure(identity,self.root/(identity+".json"))
        with self.assertRaises(worker.AdmissionError):self.supervisor.stop(identity)
        with self.assertRaises(worker.AdmissionError):self.supervisor.ensure(identity,self.root/(identity+".json"))
        self.assertEqual(self.launch.call_count,1)

    def test_cgroup_population_and_config_identity_are_checked(self):
        identity=str(uuid.uuid4());group=self.root/"cgroups"/("ac-"+identity);group.mkdir(parents=True)
        (group/"cgroup.events").write_text("populated 1\nfrozen 0\n")
        with self.assertRaises(worker.AdmissionError):self.supervisor.stop(identity)
        with self.assertRaises(worker.AdmissionError):self.supervisor.ensure(identity,self.root/"other.json")
        self.launch.assert_not_called()

    def test_worker_manager_and_supervisor_share_the_same_vm_parent(self):
        broker=object.__new__(worker.WorkerBroker)
        broker.c={"host_memory_headroom_mib":1024}
        identity=str(uuid.uuid4())
        cfg=broker.manager_config({"id":"ac-"+identity,"cgroup_parent":"../../host"})
        self.assertEqual(cfg["cgroup_parent"],"tofi-vms")
        self.assertEqual(cfg["id"],"ac-"+identity)
        self.assertEqual(cfg["host_memory_headroom_mib"],1024)
        with self.assertRaises(ValueError):
            worker.WorkerBroker({"cgroup_root":"/sys/fs/cgroup"})

    def test_missing_vm_population_evidence_retains_cleanup_fence(self):
        identity=str(uuid.uuid4())
        (self.root/"cgroups"/("ac-"+identity)).mkdir(parents=True)
        with self.assertRaises(worker.AdmissionError): self.supervisor.stop(identity)
        with self.assertRaises(worker.AdmissionError):
            self.supervisor.ensure(identity,self.root/(identity+".json"))
        self.launch.assert_not_called()

    def _make_verified_leaf(self, identity, membership="/tofi-vms/ac-{identity}"):
        mount=self.root/"cgroup-mount"; parent=mount/"tofi-vms"
        leaf=parent/("ac-"+identity); leaf.mkdir(parents=True)
        (leaf/"cgroup.procs").write_text("4242\n")
        (leaf/"cgroup.events").write_text("populated 1\nfrozen 0\n")
        proc=self.root/"proc"/"4242"; proc.mkdir(parents=True)
        (proc/"cgroup").write_text("0::"+membership.format(identity=identity)+"\n")
        return mount,parent,leaf

    def test_stop_kills_verified_vm_after_quick_or_prior_manager_exit(self):
        for exited, timeout in ((False, False), (True, False), (False, True)):
            with self.subTest(exited=exited, timeout=timeout):
                identity=str(uuid.uuid4())
                mount,parent,leaf=self._make_verified_leaf(identity)
                self.supervisor.cgroups=parent
                self.supervisor.cgroup_mount_root=mount
                self.supervisor.proc_root=self.root/"proc"
                kill_file=leaf/"cgroup.kill"; kill_file.touch()
                proc=Mock(pid=303); proc.poll.return_value=1 if exited else None
                if timeout:
                    proc.wait.side_effect=[subprocess.TimeoutExpired("manager",60),0]
                self.supervisor.processes[identity]=proc
                original_write=Path.write_text
                def write(path, value, *args, **kwargs):
                    result=original_write(path,value,*args,**kwargs)
                    if path==kill_file:
                        original_write(leaf/"cgroup.events","populated 0\n")
                        original_write(leaf/"cgroup.procs","")
                    return result
                with patch.object(Path,"write_text",write), patch.object(worker.os,"killpg") as signal:
                    self.supervisor.stop(identity)
                self.assertEqual(kill_file.read_text(),"1")
                self.assertNotIn(identity,self.supervisor.processes)
                self.assertNotIn(identity,self.supervisor.quarantined)
                self.assertEqual(signal.call_count,0 if exited else 1)
                # The fixture PID path is shared between these scoped cases.
                (self.root/"proc"/"4242"/"cgroup").unlink()
                (self.root/"proc"/"4242").rmdir()

    def test_manager_cleanup_timeout_retains_manager_and_budget_without_sigkill(self):
        identity=str(uuid.uuid4())
        mount,parent,leaf=self._make_verified_leaf(identity)
        self.supervisor.cgroups=parent;self.supervisor.cgroup_mount_root=mount;self.supervisor.proc_root=self.root/"proc"
        (leaf/"cgroup.kill").touch()
        proc=Mock(pid=303);proc.poll.return_value=None
        proc.wait.side_effect=subprocess.TimeoutExpired("manager",60)
        self.supervisor.processes[identity]=proc
        with patch.object(self.supervisor,"_kill_verified_vm_leaf"),patch.object(self.supervisor,"_wait_vm_stopped"),patch.object(worker.os,"killpg") as signal:
            with self.assertRaisesRegex(worker.AdmissionError,"cleanup did not finish"):self.supervisor.stop(identity)
        signal.assert_called_once_with(303,worker.signal.SIGTERM)
        self.assertIn(identity,self.supervisor.quarantined)
        self.assertIn(identity,self.supervisor.processes)

    def test_quick_exit_invalid_vm_identity_never_kills_and_retains_fence(self):
        identity=str(uuid.uuid4())
        mount,parent,leaf=self._make_verified_leaf(identity,"/tofi-vms/ac-other")
        self.supervisor.cgroups=parent
        self.supervisor.cgroup_mount_root=mount
        self.supervisor.proc_root=self.root/"proc"
        kill_file=leaf/"cgroup.kill"; kill_file.touch()
        proc=Mock(pid=303); proc.poll.return_value=1
        self.supervisor.processes[identity]=proc
        with patch.object(worker.os,"killpg") as signal:
            with self.assertRaises(worker.AdmissionError): self.supervisor.stop(identity)
            signal.assert_not_called()
        self.assertEqual(kill_file.read_text(),"")
        self.assertIn(identity,self.supervisor.processes)
        self.assertIn(identity,self.supervisor.quarantined)
        with self.assertRaises(worker.AdmissionError):
            self.supervisor.ensure(identity,self.root/(identity+".json"))

    def test_forced_kill_requires_exact_mount_and_pid_leaf_membership(self):
        identity=str(uuid.uuid4())
        mount,parent,leaf=self._make_verified_leaf(identity)
        self.supervisor.cgroups=parent
        self.supervisor.cgroup_mount_root=mount
        self.supervisor.proc_root=self.root/"proc"
        self.assertEqual(self.supervisor._verified_leaf_for_kill(identity),leaf)

        other=str(uuid.uuid4())
        with self.assertRaises(worker.AdmissionError):
            self.supervisor._verified_leaf_for_kill(other)

    def test_forced_kill_rejects_pid_outside_leaf_and_missing_membership(self):
        identity=str(uuid.uuid4())
        mount,parent,leaf=self._make_verified_leaf(identity,"/tofi-vms/ac-other")
        self.supervisor.cgroups=parent
        self.supervisor.cgroup_mount_root=mount
        self.supervisor.proc_root=self.root/"proc"
        with self.assertRaises(worker.AdmissionError):
            self.supervisor._verified_leaf_for_kill(identity)
        (self.root/"proc"/"4242"/"cgroup").unlink()
        with self.assertRaises(worker.AdmissionError):
            self.supervisor._verified_leaf_for_kill(identity)

    def test_forced_kill_accepts_bind_alias_by_mount_identity_not_path_text(self):
        identity=str(uuid.uuid4())
        _mount,parent,leaf=self._make_verified_leaf(identity)
        sysfs_alias=self.root/"sysfs-cgroup-alias"; sysfs_alias.mkdir()
        self.supervisor.cgroups=parent
        self.supervisor.cgroup_mount_root=sysfs_alias
        self.supervisor.proc_root=self.root/"proc"
        original_stat=Path.stat

        def aliased_stat(path, *args, **kwargs):
            if path in (parent.parent,sysfs_alias):
                return SimpleNamespace(st_dev=77,st_ino=8801,st_mode=0o40755)
            return original_stat(path,*args,**kwargs)

        with patch.object(Path,"stat",aliased_stat):
            self.assertEqual(self.supervisor._verified_leaf_for_kill(identity),leaf)

            def mismatched_stat(path, *args, **kwargs):
                if path == sysfs_alias:
                    return SimpleNamespace(st_dev=77,st_ino=9902,st_mode=0o40755)
                return aliased_stat(path,*args,**kwargs)

            with patch.object(Path,"stat",mismatched_stat):
                with self.assertRaises(worker.AdmissionError):
                    self.supervisor._verified_leaf_for_kill(identity)
if __name__=="__main__":unittest.main()
