"""Isolated shell integration with a fake Docker CLI; no real services changed."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


@unittest.skipUnless((Path(__file__).resolve().parents[2]/"scripts/server-ops.sh").is_file(),
                     "private legacy production operator script is excluded from core source")
class AccountInstallWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='tofi-account-install-',dir='/tmp');self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name);(self.root/'data').mkdir();(self.root/'bin').mkdir()
        (self.root/'compose.yaml').write_text('services: {}\n')
        (self.root/'release.env').write_text('TOFI_IMAGE=tofi:account-old\nTOFI_RETAINED_SETTING=yes\n')
        (self.root/'accounts.worker.overlay.yaml').write_text('services: {}\n')
        (self.root/'data/keep').write_bytes(b'current writes retained')
        self.script=Path(__file__).resolve().parents[2]/'scripts/server-ops.sh'
        if not self.script.exists():
            self.skipTest('scripts/server-ops.sh is private deployment tooling outside this source tree')
        self.helper=self.root/'transition.py'
        self.helper.write_text('import os,sys\nfrom pathlib import Path\np=Path(os.environ["TOFI_DEPLOY_ROOT"])\n(p/"helper-invoked").write_text(sys.argv[1])\n(p/"accounts.identity.overlay.yaml").write_text("services: {}\\n")\n')
        (self.root/'bin/id').write_text('#!/bin/sh\nprintf "0\\n"\n')
        (self.root/'bin/flock').write_text('#!/bin/sh\nexit 0\n')
        docker=self.root/'bin/docker'
        docker.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
root=Path(os.environ['TOFI_DEPLOY_ROOT']);args=sys.argv[1:]
with (root/'docker-log').open('a') as stream:stream.write(json.dumps(args)+'\\n')
if args[:2]==['image','inspect']:
 print('0' if os.environ.get('FIXTURE_IMAGE_REJECT')=='1' else '1');sys.exit(0)
if args[0]=='inspect':
 print(json.dumps([{'State':{'Running':False,'Pid':0}}]));sys.exit(0)
if args[0]!='compose':sys.exit(70)
args=args[1:]
while args and args[0] in ('--env-file','-f','--profile'):args=args[2:]
if args[:2]==['config','--services']:print('app\\nrunner\\nworker\\nunrelated');sys.exit(0)
if args[0]=='ps':print('fixture-'+args[-1]);sys.exit(0)
if args[0]=='stop':sys.exit(0)
if args[0]=='up':
 image=[v.split('=',1)[1] for v in (root/'release.env').read_text().splitlines() if v.startswith('TOFI_IMAGE=')][0]
 if image=='tofi:account-new' and os.environ.get('FIXTURE_FAIL_NEW')=='1':sys.exit(1)
 sys.exit(0)
if args[0]=='exec':sys.exit(0)
sys.exit(71)
''')
        for p in (self.root/'bin').iterdir():p.chmod(0o755)
        self.env=dict(os.environ,PATH=str(self.root/'bin')+':'+os.environ['PATH'],TOFI_DEPLOY_ROOT=str(self.root),TOFI_ACCOUNT_TRANSITION_HELPER=str(self.helper))

    def call(self,*args,**variables):
        return subprocess.run(['bash',str(self.script),*args],env=dict(self.env,**variables),capture_output=True,text=True)

    def log(self):return [json.loads(line) for line in (self.root/'docker-log').read_text().splitlines()]

    def test_explicit_adopt_stops_only_named_writers_selects_compatible_image_no_backup(self):
        result=self.call('account-adopt',str(self.root/'reviewed.json'),'tofi:account-new')
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual((self.root/'helper-invoked').read_text(),'adopt')
        self.assertIn('TOFI_IMAGE=tofi:account-new',(self.root/'release.env').read_text())
        self.assertIn('TOFI_RETAINED_SETTING=yes',(self.root/'release.env').read_text())
        self.assertIn('TOFI_ACCOUNT_MAINTENANCE=1',(self.root/'release.env').read_text())
        commands=self.log();stops=[a[-1] for a in commands if 'stop' in a]
        self.assertEqual(stops,['app','runner','worker'])
        self.assertFalse(any('up' in a or 'run' in a or 'tar' in a for a in commands))
        self.assertFalse((self.root/'backups').exists())
        self.assertEqual((self.root/'data/keep').read_bytes(),b'current writes retained')

    def test_incompatible_image_rejected_before_stop(self):
        result=self.call('account-adopt',str(self.root/'reviewed.json'),'tofi:account-new',FIXTURE_IMAGE_REJECT='1')
        self.assertNotEqual(result.returncode,0)
        self.assertFalse(any('stop' in a for a in self.log()))
        self.assertFalse((self.root/'helper-invoked').exists())

    def test_failed_account_upgrade_recovers_prior_code_with_current_data(self):
        (self.root/'accounts.identity.overlay.yaml').write_text('services: {}\n')
        result=self.call('account-upgrade','tofi:account-new',FIXTURE_FAIL_NEW='1')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('TOFI_IMAGE=tofi:account-old',(self.root/'release.env').read_text())
        self.assertEqual((self.root/'data/keep').read_bytes(),b'current writes retained')
        self.assertFalse((self.root/'backups').exists())
        self.assertEqual(len([a for a in self.log() if 'up' in a]),2)

    def test_old_snapshot_rollback_restore_and_upgrade_are_fenced(self):
        (self.root/'accounts.identity.overlay.yaml').write_text('services: {}\n')
        for args in [('rollback',),('restore','synthetic.tar.gz'),('upgrade','tofi:account-new')]:
            result=self.call(*args);self.assertNotEqual(result.returncode,0)
            self.assertIn('Snapshot/image rollback is blocked',result.stderr)
        self.assertFalse((self.root/'docker-log').exists())
