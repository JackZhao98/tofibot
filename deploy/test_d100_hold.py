"""Three held-item groups, with a real in-process App/SQLite semantic fixture.

No images, Docker, native runtime, providers or live endpoints are exercised.
Synthetic image bindings test the contract, not actual artifact provenance.
"""
import contextlib
import copy
import io
import json
import os
from pathlib import Path
import re
import subprocess
import unittest
from unittest.mock import patch

import test_d100_upgrade as prior

installer = prior.installer


class D100HeldItems(unittest.TestCase):
    def setUp(self):
        self.fixture = prior.D100Upgrade('test_1_contract_preflight_zero_effects')
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)

    def test_1_exact_pair_evidence_and_current_data_semantics(self):
        repo = Path(__file__).resolve().parent.parent
        env = dict(os.environ, GOCACHE='/tmp/tofi-d100-go-cache', GOPROXY='off')
        run = subprocess.run(['go','test','./deploy','-run','^TestD100CurrentDataPairSemantics$','-count=1','-v'], cwd=repo, env=env, capture_output=True, text=True, timeout=90)
        self.assertEqual(run.returncode,0,run.stdout+run.stderr)
        print(run.stdout,end='')
        match=re.search(r'D100_PAIR_RESULT (\{[^\n]+\})',run.stdout)
        self.assertIsNotNone(match)
        actual=json.loads(match.group(1))
        self.assertEqual(actual['n_written'],actual['b_read'])
        cases=['valid','missing','null','stale-seal','target-image','fallback-image','target-contract','fallback-contract','missing-login','bootstrap-failed','missing-semantic','null-semantic','string-marker','changed-record','read-seq-type']
        for case in cases:
            with self.subTest(case=case):
                self.setUp();f=self.fixture;f.baseline_b();reference=f.plan(prior.N,prior.B)
                plan=json.loads(Path(reference['path']).read_text())
                record=json.loads(Path(plan['failure']['pair_evidence']['path']).read_text())
                proof=copy.deepcopy(actual)
                if case=='string-marker':proof=dict(unchanged_string='synthetic retained marker')
                elif case=='changed-record':proof['b_read']['message_seq']+=1
                elif case=='read-seq-type':proof['b_read']['message_seq']=float(proof['b_read']['message_seq'])
                record['semantic_evidence']=f.seal('actual-semantic-result.json',proof)
                if case=='target-image':record['bindings']['target_image']=prior.B
                elif case=='fallback-image':record['bindings']['fallback_image']=prior.N
                elif case=='target-contract':record['bindings']['target_contract_sha256']='0'*64
                elif case=='fallback-contract':record['bindings']['fallback_contract_sha256']='0'*64
                elif case=='missing-login':record['checks'].pop('b_login')
                elif case=='bootstrap-failed':record['checks']['b_setup_closed']='fail'
                elif case=='missing-semantic':Path(record['semantic_evidence']['path']).unlink()
                elif case=='null-semantic':record['semantic_evidence']['sha256']=None
                plan['failure']['pair_evidence']=f.seal('reviewed-pair.json',record)
                if case=='missing':plan['failure'].pop('pair_evidence')
                elif case=='null':plan['failure']['pair_evidence']['sha256']=None
                elif case=='stale-seal':plan['failure']['pair_evidence']['sha256']='0'*64
                reference=f.seal('upgrade-plan.json',plan)
                before={p.name:p.read_bytes() for p in f.root.iterdir()}
                if case=='valid':
                    def health(root,state):
                        if f.started[-1]==prior.N:raise ValueError('synthetic known N failure')
                    with self.assertRaisesRegex(ValueError,'CURRENT data'):f.upgrade(reference,prior.N,health=health)
                    self.assertEqual(f.started,[prior.N,prior.B])
                    self.assertEqual(f.read_state()['migration_intent']['pair_evidence']['semantic_result'],actual)
                else:
                    with self.assertRaises((ValueError,OSError)):f.upgrade(reference,prior.N)
                    self.assertEqual(f.stops,0);self.assertEqual(f.started,[])
                    self.assertEqual(before,{p.name:p.read_bytes() for p in f.root.iterdir()})

    def test_2_fresh_apply_and_unfenced_d100_state_are_blocked(self):
        with patch.object(installer,'preflight') as preflight,patch.object(installer,'load_plan') as load,patch.object(installer,'run') as command,patch.object(installer,'compose') as compose:
            before={p.name:p.read_bytes() for p in self.fixture.root.iterdir()}
            with self.assertRaisesRegex(ValueError,'fresh apply is blocked'):installer.apply(self.fixture.root,True)
            preflight.assert_not_called();load.assert_not_called();command.assert_not_called();compose.assert_not_called()
            self.assertEqual(before,{p.name:p.read_bytes() for p in self.fixture.root.iterdir()})
        for evidence in ['image-label','app-contract','migration-intent']:
            with self.subTest(evidence=evidence):
                self.setUp();f=self.fixture
                if evidence=='image-label':f.state['app_image']=prior.B;f.config['services']['app']['image']=prior.B
                elif evidence=='app-contract':f.state['app_contract']=f.artifacts[prior.B]
                else:f.state['migration_intent']={'stage':'completed'}
                f.save();reference=f.plan(prior.N)
                with self.assertRaisesRegex(ValueError,'without a persisted floor'):f.upgrade(reference,prior.N)
                self.assertEqual(f.stops,0);self.assertEqual(f.started,[])

    def test_3_unproven_stop_and_persistence_errors_remain_visible(self):
        for malformed in [None,'synthetic-scalar',[],{'stage':'unknown'},{'stage':'target-starting'}]:
            with self.subTest(prior_intent=malformed):
                self.setUp();f=self.fixture;f.baseline_b();f.state['migration_intent']=malformed;f.save();reference=f.plan(prior.N,prior.B)
                before={p.name:p.read_bytes() for p in f.root.iterdir()}
                with self.assertRaisesRegex(ValueError,'prior migration intent'):f.upgrade(reference,prior.N)
                self.assertEqual(f.stops,0);self.assertEqual(f.started,[])
                self.assertEqual(before,{p.name:p.read_bytes() for p in f.root.iterdir()})
        # Even direct recovery on malformed prior metadata must record a fence
        # rather than raising AttributeError before stop/persistence handling.
        saved=f.read_state();saved['migration_intent']=None
        with patch.object(installer,'stopped',side_effect=OSError('synthetic STOP error')):
            with self.assertRaises(installer.RecoveryFailure):installer.retain_stopped(f.root,saved,'synthetic failed operation',ValueError('synthetic ORIGINAL error'))
        self.assertEqual(f.read_state()['phase'],'STOP_UNPROVEN')
        for case in ['initial-stop','first-cleanup','fallback-stop','fallback-cleanup','persistence','both']:
            with self.subTest(case=case):
                self.setUp();f=self.fixture;later=case.startswith('fallback')
                if later:f.baseline_b()
                reference=f.plan(prior.N,prior.B) if later else f.plan()
                original=installer.durable_write
                def stop(root):
                    f.stops+=1
                    failing=1 if case=='initial-stop' else 3 if case=='fallback-cleanup' else 2
                    if case!='persistence' and f.stops==failing:raise OSError('synthetic STOP inspection error')
                def health(root,state):raise ValueError('synthetic ORIGINAL health error')
                def writer(path,value):
                    if case in ['persistence','both'] and Path(path).name=='install-state.json' and 'recovery_failure' in value:raise OSError('synthetic PERSISTENCE error before staging')
                    original(path,value)
                with self.assertRaises(installer.RecoveryFailure) as raised:
                    f.upgrade(reference,prior.N if later else prior.B,stopped=stop,health=health,durable_write=writer)
                details=raised.exception.details
                self.assertIn('original_error',details)
                if case=='fallback-stop':self.assertIn('ORIGINAL',details['target_error']['cause']['message'])
                elif case!='initial-stop':self.assertIn('ORIGINAL',details['original_error']['cause']['message'])
                if case!='persistence':
                    self.assertIn('STOP',details['stop_error']['message']);self.assertIn('STOP_UNPROVEN',str(raised.exception))
                if case in ['persistence','both']:
                    self.assertIn('PERSISTENCE',details['persistence_error']['message']);self.assertIn('DURABLE_FENCE_UNPROVEN',str(raised.exception))
                else:
                    saved=f.read_state();self.assertEqual(saved['phase'],'STOP_UNPROVEN');self.assertEqual(saved['recovery_failure'],details)
                if later:self.assertIsNotNone(details['target_error'])
                expected=[] if case=='initial-stop' else [prior.N,prior.B] if case=='fallback-cleanup' else [prior.N] if later else [prior.B]
                self.assertEqual(f.started,expected);self.assertNotIn(prior.A,f.started)
                count=f.stops
                with self.assertRaises(ValueError):f.upgrade(reference,prior.N if later else prior.B)
                self.assertEqual(count,f.stops)
                # The CLI reports the structured recovery error even when the
                # filesystem cannot persist the stop/fence result.
                output=io.StringIO()
                args=['self_host.py','upgrade',str(f.root),'--app-image',prior.B,'--worker-image',prior.W,'--upgrade-plan',reference['path'],'--upgrade-plan-sha256',reference['sha256']]
                with patch.object(installer.sys,'argv',args),patch.object(installer,'upgrade',side_effect=raised.exception),contextlib.redirect_stderr(output):self.assertEqual(installer.main(),1)
                self.assertIn('original_error',output.getvalue());self.assertIn('stop_error',output.getvalue())
                if case in ['persistence','both']:self.assertIn('PERSISTENCE',output.getvalue())


if __name__=='__main__':unittest.main()
