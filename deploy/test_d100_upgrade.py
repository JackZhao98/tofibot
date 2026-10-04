"""Six bounded synthetic acceptance groups. No Docker, images or live services."""
import copy
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from test_self_host import installer

A, B, N, W = ['sha256:' + digit * 64 for digit in 'abcd']


class D100Upgrade(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='tofi-d100-installer-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.data = self.root / 'synthetic-current-data'
        self.data.write_text('existing synthetic writes')
        self.images = {A: {'Id': A, 'Architecture': 'amd64', 'Config': {'Labels': {'io.tofi.account-runtime': '1'}}}, W: {'Id': W, 'Architecture': 'amd64', 'Config': {}}}
        self.artifacts, self.refs = {}, {}
        for image, digit in [(B, '1'), (N, '2')]:
            binding = dict(app_image=image, source_commit=digit*40, ui_source_commit=digit*40, ui_sha256=digit*64, bootstrap_contract='d100-v1')
            record = dict(schema=1, kind='d100-app-validation', bindings=binding, checks={name:'pass' for name in installer.BOOTSTRAP_CHECKS})
            evidence = self.seal(digit+'-evidence.json', record)
            artifact = dict(schema=1, **binding, evidence=evidence)
            self.artifacts[image] = artifact
            self.refs[image] = self.seal(digit+'-contract.json', artifact)
            self.images[image] = {'Id': image, 'Architecture': 'amd64', 'Config': {'Labels': {'io.tofi.account-runtime':'1', 'org.opencontainers.image.revision':digit*40, 'io.tofi.ui.source':digit*40, 'io.tofi.ui.sha256':digit*64, 'io.tofi.bootstrap-contract':'d100-v1'}}}
        self.state = dict(root=str(self.root), project='synthetic', port=18333, phase='installed', app_image=A, worker_image=W, release='synthetic-unchanged-guest')
        self.config = {'name':'synthetic', 'services':{'app':{'image':A, 'volumes':['synthetic-data:/app/data'], 'environment':{'TOFI_MULTI_ACCOUNT':'1'}}, 'worker':{'image':W,'volumes':['synthetic-guest:/release']}}}
        self.started = []
        self.stops = 0
        self.save()

    def seal(self, name, value):
        path = self.root / name
        installer.write(path, value)
        return {'path':str(path), 'sha256':installer.digest(path)}

    def save(self):
        installer.write(self.root/'install-state.json', self.state)
        installer.write(self.root/'compose.yaml', self.config)

    def baseline_b(self):
        self.state.update(app_image=B, bootstrap_contract_floor='d100-v1', app_contract=self.artifacts[B])
        self.config['services']['app']['image'] = B
        self.save()

    def plan(self, target=B, fallback=None):
        data=dict(account_id='synthetic-account',bot_id='synthetic-bot',conversation_id='synthetic-conversation',message_id='synthetic-message',message_seq=1,message_role='user',message_content='synthetic typed message')
        proof=self.seal('pair-semantics.json',dict(schema=1,kind='d100-current-data-semantic-result',model='account-workspace-messages-v1',n_written=data,b_read=data,checks={name:'pass' for name in installer.PAIR_CHECKS}))
        pair=self.seal('pair-evidence.json',dict(schema=1,kind='d100-current-data-pair-validation',bindings=dict(target_image=target,fallback_image=fallback,target_contract_sha256=self.refs[target]['sha256'],fallback_contract_sha256=self.refs.get(fallback,{}).get('sha256')),checks={name:'pass' for name in installer.PAIR_CHECKS},semantic_evidence=proof))
        failure = {'mode':'first-migration-stop-retain'} if fallback is None else {'mode':'compatible-fallback','fallback':self.refs[fallback],'pair_evidence':pair}
        return self.seal('upgrade-plan.json', dict(schema=1, kind='d100-app-upgrade', root=str(self.root), current_app_image=self.state['app_image'], worker_image=W, target=self.refs[target], failure=failure))

    def read_state(self):
        return json.loads((self.root/'install-state.json').read_text())

    def fixture_owned(self, root):
        for name in ['install-state.json','compose.yaml']:
            if (self.root/(name+'.pending')).exists():raise ValueError('interrupted metadata')
        return self.root, self.read_state(), json.loads((self.root/'compose.yaml').read_text())

    def stop(self, root):
        self.stops += 1

    def command(self, root, *args):
        if args == ('up','-d','--no-build','app'):
            state = self.read_state()
            self.assertEqual(state['bootstrap_contract_floor'], 'd100-v1')
            self.assertEqual(state['phase'], 'd100-migrating')
            self.assertIn(state['migration_intent']['stage'], ['target-starting','fallback-starting'])
            current = json.loads((self.root/'compose.yaml').read_text())
            self.assertEqual(current['services']['worker'], self.config['services']['worker'])
            self.started.append(current['services']['app']['image'])
        return subprocess.CompletedProcess([], 0, 'synthetic-app\n' if args == ('ps','-q','app') else '')

    def inspect_running(self, args):
        self.assertEqual(args, ['docker','inspect','synthetic-app'])
        current = json.loads((self.root/'compose.yaml').read_text())['services']['app']['image']
        return subprocess.CompletedProcess([],0,json.dumps([{'Image':current, 'State':{'Running':True}, 'Config':{'Labels':{'com.docker.compose.project':'synthetic','com.docker.compose.service':'app'}}}]))

    def upgrade(self, reference, target=B, **overrides):
        defaults = dict(owned_state=self.fixture_owned, inspect_image=lambda identity,app=False:self.images[identity], stopped=self.stop, compose=self.command, run=self.inspect_running, health=lambda root,state:None)
        defaults.update(overrides)
        with patch.multiple(installer, **defaults):
            return installer.upgrade(self.root, target, W, reference['path'], reference['sha256'])

    def test_1_contract_preflight_zero_effects(self):
        cases = ['unknown-contract','bad-plan-digest','target-mismatch','missing-evidence','worker-change','unknown-floor','unknown-phase','missing-fallback','generic-stop-retain','duplicate-plan-key','pending-metadata','public-root','null-target-seal','null-evidence-seal','null-fallback-seal']
        for case in cases:
            with self.subTest(case=case):
                self.setUp()
                reference = self.plan()
                if case in ['missing-fallback','generic-stop-retain','null-fallback-seal']:
                    self.baseline_b(); reference = self.plan(N, B)
                plan = json.loads(Path(reference['path']).read_text())
                if case == 'unknown-contract': self.artifacts[B]['bootstrap_contract']='unknown'; self.refs[B]=self.seal('1-contract.json',self.artifacts[B]);plan['target']=self.refs[B]
                elif case == 'target-mismatch': plan['target']=self.refs[N]
                elif case == 'missing-evidence': Path(self.artifacts[B]['evidence']['path']).unlink()
                elif case == 'worker-change': plan['worker_image']=A
                elif case == 'unknown-floor': self.state['bootstrap_contract_floor']='unknown';self.save()
                elif case == 'unknown-phase': self.state['phase']='d100-migrating';self.save()
                elif case == 'pending-metadata': (self.root/'install-state.json.pending').write_text('synthetic interrupted write')
                elif case == 'public-root': self.root.chmod(0o755)
                elif case == 'missing-fallback': plan['failure']={'mode':'compatible-fallback'}
                elif case == 'generic-stop-retain': plan['failure']={'mode':'first-migration-stop-retain'}
                elif case == 'null-target-seal': plan['target']['sha256']=None
                elif case == 'null-fallback-seal': plan['failure']['fallback']['sha256']=None
                elif case == 'null-evidence-seal':
                    self.artifacts[B]['evidence']['sha256']=None
                    plan['target']=self.seal('1-contract.json',self.artifacts[B])
                reference=self.seal('upgrade-plan.json',plan)
                if case == 'bad-plan-digest': reference['sha256']='0'*64
                elif case == 'duplicate-plan-key':
                    path=Path(reference['path']);path.write_text('{"schema":1,"schema":1}');reference['sha256']=installer.digest(path)
                before={p.name:p.read_bytes() for p in self.root.iterdir()}
                with self.assertRaises((ValueError,OSError)):self.upgrade(reference, N if case in ['missing-fallback','generic-stop-retain','null-fallback-seal'] else B)
                self.assertEqual(self.stops,0);self.assertEqual(self.started,[])
                self.assertEqual(before,{p.name:p.read_bytes() for p in self.root.iterdir()})

    def test_2_first_migration_failure_and_interruption_never_start_a(self):
        for failure in ['health','interrupt','after-floor','final-state']:
            with self.subTest(failure=failure):
                self.setUp();reference=self.plan();original=installer.durable_write
                def writer(path,value):
                    if failure=='final-state' and Path(path).name=='install-state.json' and value['phase']=='installed':raise OSError('synthetic final write failure')
                    original(path,value)
                    if failure=='after-floor' and Path(path).name=='install-state.json' and value['phase']=='d100-migrating':raise KeyboardInterrupt('synthetic interruption')
                def health(root,state):
                    if failure=='interrupt':raise KeyboardInterrupt('synthetic interruption')
                    if failure=='health':raise ValueError('synthetic unhealthy App')
                with self.assertRaises((ValueError,OSError,KeyboardInterrupt)):
                    self.upgrade(reference, durable_write=writer, health=health)
                self.assertNotIn(A,self.started)
                self.assertEqual(self.read_state()['bootstrap_contract_floor'],'d100-v1')
                self.assertEqual(self.read_state()['phase'],'stopped-retained')
                count=self.stops
                with self.assertRaises(ValueError):self.upgrade(reference)
                self.assertEqual(count,self.stops)

    def test_3_n_to_b_retains_new_data_and_records_actual_b(self):
        self.baseline_b();reference=self.plan(N,B);before=copy.deepcopy(self.config)
        def health(root,state):
            if self.started[-1]==N:
                self.data.write_text(self.data.read_text()+'; synthetic writes under N')
                raise ValueError('synthetic target failure')
        with self.assertRaisesRegex(ValueError,'CURRENT data'):self.upgrade(reference,N,health=health)
        self.assertEqual(self.started,[N,B]);self.assertIn('writes under N',self.data.read_text())
        saved=self.read_state();self.assertEqual(saved['app_image'],B);self.assertEqual(saved['app_contract'],self.artifacts[B]);self.assertEqual(saved['phase'],'installed');self.assertEqual(saved['bootstrap_contract_floor'],'d100-v1')
        self.assertEqual(json.loads((self.root/'compose.yaml').read_text()),before)

    def test_4_b_requires_closed_bootstrap_evidence_and_durable_floor(self):
        reference=self.plan();fsync=installer.os.fsync;events=[]
        def synced(fd):events.append('directory' if stat.S_ISDIR(os.fstat(fd).st_mode) else 'file');fsync(fd)
        with patch.object(installer.os,'fsync',side_effect=synced):result=self.upgrade(reference)
        self.assertTrue(result['upgraded']);self.assertEqual(events[:4],['file','directory','file','directory'])
        self.assertEqual(self.read_state()['app_contract']['evidence'],self.artifacts[B]['evidence'])
        record=json.loads(Path(self.artifacts[B]['evidence']['path']).read_text());record['checks']['consumed_replay']='unknown'
        reference_evidence=self.seal('1-evidence.json',record);self.artifacts[B]['evidence']=reference_evidence;self.refs[B]=self.seal('1-contract.json',self.artifacts[B])
        self.state.update(app_image=B,bootstrap_contract_floor='d100-v1',app_contract=self.artifacts[B]);self.config['services']['app']['image']=B;self.save()
        with self.assertRaisesRegex(ValueError,'passing bootstrap'):self.upgrade(self.plan(N,B),N)

    def test_5_pending_setup_requires_matching_ui_source_and_hash(self):
        for field in ['ui_source_commit','ui_sha256']:
            with self.subTest(field=field):
                self.setUp();self.artifacts[B][field]='9'*(40 if field=='ui_source_commit' else 64)
                self.refs[B]=self.seal('1-contract.json',self.artifacts[B])
                with self.assertRaises(ValueError):self.upgrade(self.plan())
                self.assertEqual(self.stops,0);self.assertEqual(self.started,[])

    def test_6_unclean_stop_fallback_failure_and_unknown_do_not_restart(self):
        for failure in ['initial-stop','before-fallback','fallback-health','unknown-identity','startup-timeout','health-timeout','wrapped-health-timeout','busy-lock']:
            with self.subTest(failure=failure):
                self.setUp();self.baseline_b();reference=self.plan(N,B)
                def stop(root):
                    self.stops+=1
                    if failure=='initial-stop' or (failure=='before-fallback' and self.stops==2):raise ValueError('synthetic unclean stop')
                def health(root,state):
                    if self.started[-1]==N or failure=='fallback-health':raise ValueError('synthetic unhealthy App')
                def inspect(args):
                    if failure=='unknown-identity':return subprocess.CompletedProcess([],0,json.dumps([{'Image':A,'State':{'Running':True},'Config':{'Labels':{}}}]))
                    return self.inspect_running(args)
                def command(root,*args):
                    result=self.command(root,*args)
                    if failure=='startup-timeout' and args==('up','-d','--no-build','app'):raise subprocess.TimeoutExpired('synthetic startup',1)
                    return result
                if failure=='busy-lock':
                    with patch.object(installer,'owned_state',side_effect=self.fixture_owned),installer.lifecycle(self.root):
                        with self.assertRaisesRegex(ValueError,'busy'):self.upgrade(reference,N)
                    self.assertEqual(self.started,[]);continue
                if failure in ['health-timeout','wrapped-health-timeout']:
                    error=TimeoutError('synthetic health timeout')
                    if failure=='wrapped-health-timeout':error=installer.urllib.error.URLError(error)
                    with patch.object(installer.urllib.request,'urlopen',side_effect=error):
                        with self.assertRaises((TimeoutError,installer.urllib.error.URLError)):
                            self.upgrade(reference,N,stopped=stop,health=installer.health,run=inspect,compose=command)
                else:
                    with self.assertRaises((ValueError,subprocess.TimeoutExpired)):self.upgrade(reference,N,stopped=stop,health=health,run=inspect,compose=command)
                self.assertNotIn(A,self.started)
                self.assertEqual(self.read_state()['phase'],'STOP_UNPROVEN' if failure in ['initial-stop','before-fallback'] else 'stopped-retained')
                expected=[] if failure=='initial-stop' else [N,B] if failure=='fallback-health' else [N]
                self.assertEqual(self.started,expected)
                self.assertEqual(self.read_state()['bootstrap_contract_floor'],'d100-v1')


if __name__ == '__main__':unittest.main()
