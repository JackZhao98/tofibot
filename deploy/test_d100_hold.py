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
import tempfile
import stat
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
            with self.assertRaisesRegex(ValueError,'plan-sha256'):installer.apply(self.fixture.root,True)
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


class FreshApply(unittest.TestCase):
    """Ten table-driven fresh transaction families; all host/engine calls mocked.

    Real local operations: exclusive copy, modes, flock, durable writes/fsync.
    Guest bytes are synthetic; its ext4/embedded-binary validator is mocked.
    """
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='tofi-fresh-')
        self.addCleanup(self.temp.cleanup)
        self.base=Path(self.temp.name).resolve();self.root=self.base/'install';self.review=self.base/'review';self.source=self.base/'guest'
        self.source.mkdir(mode=0o700);(self.source/'bin').mkdir(mode=0o700)
        hashes={}
        for name in installer.FILES:
            data=(installer.HERE/'microvm/manager.py').read_bytes() if name=='manager.py' else ('synthetic-'+name).encode()
            path=self.source/name;path.write_bytes(data);path.chmod(0o500 if name.startswith('bin/') else 0o400);hashes[name]=installer.digest(path)
        manifest=dict(schema=1,protocol='tofi-account-guest-v1',files_sha256=hashes,guest_binary_sha256='e'*64,capabilities=['blob-store-v1','runner-v1','storage-metrics-v1'])
        installer.write(self.source/'account-release.json',manifest,0o400);(self.source/'bin').chmod(0o500);self.source.chmod(0o500)
        self.events=[];self.containers={};self.free=100*installer.GIB;self.admission=16*installer.GIB;self.calls=[];self.starts=[];self.stop_ids=[]
        self.env=patch.dict(os.environ,{},clear=True);self.env.start();self.addCleanup(self.env.stop)
        self.mocks=patch.multiple(installer,CLAIM_ROOT=self.base/'claims',validate_release=self.validate_guest,check_engine_files=lambda e:None,
                                  preflight=self.preflight,vacant=self.vacant,run=self.command,compose=self.compose,permissions=self.permissions,
                                  health=self.health,available_bytes=lambda p:self.free)
        self.mocks.start();self.addCleanup(self.mocks.stop)
        self.chown=patch.object(installer.os,'chown');self.chown.start();self.addCleanup(self.chown.stop)
        binding=dict(app_image=prior.B,source_commit='1'*40,ui_source_commit='1'*40,ui_sha256='1'*64,bootstrap_contract='d100-v1')
        evidence=self.seal('app-evidence.json',dict(schema=1,kind='d100-app-validation',bindings=binding,checks={name:'pass' for name in installer.BOOTSTRAP_CHECKS}))
        self.app=dict(schema=1,**binding,evidence=evidence)
        self.app_ref=self.seal('app-contract.json',self.app)
        self.worker=dict(schema=1,image=prior.W,entrypoint=installer.WORKER_ENTRYPOINT,labels={})
        self.worker_ref=self.seal('worker-contract.json',self.worker)
        self.engine=dict(schema=1,docker={'path':str(self.base/'docker'),'sha256':'f'*64},compose={'path':str(self.base/'compose'),'sha256':'f'*64},socket={'path':'/run/docker.sock','device':1,'inode':1},engine_id='synthetic-engine')
        self.engine_ref=self.seal('engine-contract.json',self.engine)
        labels={'io.tofi.account-runtime':'1','io.tofi.bootstrap-contract':'d100-v1','org.opencontainers.image.revision':'1'*40,'io.tofi.ui.source':'1'*40,'io.tofi.ui.sha256':'1'*64}
        self.images={prior.B:dict(Id=prior.B,Architecture='amd64',Config=dict(Labels=labels,Entrypoint=['/app/tofi'],Cmd=None,Env=[])),
                     prior.W:dict(Id=prior.W,Architecture='amd64',Config=dict(Labels={},Entrypoint=installer.WORKER_ENTRYPOINT,Cmd=None,Env=[]))}
        self.plan=installer.generate(str(self.root),'synthetic',prior.B,prior.W,str(self.source),2,2048,18333,str(self.review),self.app_ref,self.worker_ref,self.engine_ref)
        self.seal_value=installer.digest(self.review/'plan.json')
        self.addCleanup(self.writable)

    def reset(self):
        self.doCleanups();self.setUp()

    def writable(self):
        for parent,dirs,_ in os.walk(self.base):
            Path(parent).chmod(0o700)
            for name in dirs:
                p=Path(parent)/name
                if not p.is_symlink():p.chmod(0o700)

    def seal(self,name,value):
        path=self.base/name;installer.write(path,value);return {'path':str(path),'sha256':installer.digest(path)}

    def validate_guest(self,*args,**kwargs):self.events.append('validate-guest');return {'valid':True}

    def preflight(self,directory):self.events.append('preflight');installer.load_plan(directory);return {'ready':True}

    def vacant(self,p):
        self.events.append('vacant')
        if Path(p['root']).exists():raise ValueError('occupied root')

    def state(self):return installer.private_json(self.root/'install-state.json')

    def permissions(self,root,p):
        state=self.state();self.assertEqual(state['phase'],'installing');self.assertEqual(state['install_intent']['effects'][-1],'install-owned-permissions');self.events.append('permissions')

    def health(self,root,state):self.events.append('health')

    def record(self,service):
        wanted=json.loads(installer.render(self.plan)['compose.yaml'])['services'][service]
        labels=dict(self.images[wanted['image']]['Config']['Labels'],**wanted['labels'])
        labels.update({'com.docker.compose.project':'synthetic','com.docker.compose.service':service,'com.docker.compose.oneoff':'False','com.docker.compose.project.working_dir':str(self.root),'com.docker.compose.project.config_files':str(self.root/'compose.yaml')})
        host=dict(ReadonlyRootfs=True,Privileged=False,Memory=wanted['mem_limit'],NanoCpus=int(wanted['cpus']*10**9),CapDrop=['ALL'],SecurityOpt=wanted['security_opt'],RestartPolicy={'Name':'unless-stopped','MaximumRetryCount':0},NetworkMode='synthetic_default',Tmpfs={v.split(':',1)[0]:v.split(':',1)[1] for v in wanted['tmpfs']},Binds=None,VolumesFrom=None,PidMode='',IpcMode='private',Links=None,ExtraHosts=None,Runtime='runc',CgroupnsMode='private',UsernsMode='',UTSMode='',PublishAllPorts=False,AutoRemove=False,OomKillDisable=False,CgroupParent='',DeviceRequests=None,StorageOpt=None)
        if service=='worker':host.update(PidsLimit=512,CgroupnsMode='private',CapAdd=installer.CAPS,DeviceCgroupRules=['c 10:257 m'],Sysctls={'net.ipv4.ip_forward':'1'},Devices=[{'PathOnHost':v.split(':')[0],'PathInContainer':v.split(':')[1],'CgroupPermissions':'rwm'} for v in wanted['devices']],PortBindings={})
        else:host.update(Init=True,CapAdd=None,Devices=[],DeviceCgroupRules=None,Sysctls=None,PortBindings={'8321/tcp':[{'HostIp':'127.0.0.1','HostPort':'18333'}]})
        return dict(Id=('1' if service=='worker' else '2')*64,Image=wanted['image'],State={'Running':True,'Pid':42,'ExitCode':0},Config=dict(User=wanted['user'],Entrypoint=wanted['entrypoint'],Cmd=[],Labels=labels,Env=[k+'='+v for k,v in wanted.get('environment',{}).items()]),HostConfig=host,
                    Mounts=[dict(Type='bind',Source=v['source'],Destination=v['target'],RW=not v['read_only'],Propagation='rprivate') for v in wanted['volumes']],NetworkSettings={'Networks':{'synthetic_default':{}}})

    def compose(self,root,*args):
        self.calls.append(['compose',*args])
        if args[:1]==('up',):
            service=args[-1];state=self.state();self.assertEqual(state['bootstrap_contract_floor'],'d100-v1');self.assertEqual(state['phase'],'installing')
            self.assertEqual(state['install_intent']['effects'][-1],'start-'+service);self.starts.append(service);self.events.append('start-'+service);self.containers[service]=self.record(service)
        return subprocess.CompletedProcess([],0,'')

    def command(self,args,**kwargs):
        self.calls.append(args)
        if args[:2]==['docker','info']:value={'ID':'synthetic-engine','OSType':'linux','CgroupVersion':'2','SecurityOptions':['apparmor']}
        elif args[:3]==['docker','image','inspect']:value=[self.images[args[3]]]
        elif args[:2]==['docker','ps']:
            self.assertIn('--no-trunc',args)
            service=next((v.split('=')[-1] for v in args if v.startswith('label=com.docker.compose.service=')),None)
            return subprocess.CompletedProcess([],0,'\n'.join(v['Id'] for k,v in self.containers.items() if service is None or k==service))
        elif args[:2]==['docker','inspect']:value=[copy.deepcopy(v) for v in self.containers.values() if v['Id'] in args[2:]]
        elif args[:2]==['docker','exec']:
            self.assertEqual(args[2:4],['--user','10001:10001']);self.assertEqual(args[4],self.containers['worker']['Id']);self.assertIn('{"op":"capacity"}',args[7]);self.events.append('capacity')
            value=dict(admission_remaining_bytes=self.admission,per_account_internal_reserved_bytes=8*installer.GIB,accounts=[],promised_bytes=123,unallocated_promises_bytes=456)
        elif args[:2]==['docker','stop']:
            self.stop_ids.extend(args[4:])
            for item in self.containers.values():item['State']=dict(Running=False,Pid=0,ExitCode=0)
            return subprocess.CompletedProcess([],0,'')
        else:raise AssertionError('unmocked engine command: '+repr(args))
        return subprocess.CompletedProcess([],0,json.dumps(value))

    def apply(self,**kwargs):return installer.apply(self.review,True,self.seal_value,**kwargs)

    def reseal(self,p):
        installer.write(self.review/'plan.json',p);self.seal_value=installer.digest(self.review/'plan.json')

    def test_01_seal_consent_zero_effects(self):
        for case in ['consent','missing-seal','null-seal','wrong-seal','duplicate','extra','boolean-schema','unsafe-reference']:
            with self.subTest(case=case):
                self.reset();p=copy.deepcopy(self.plan);accepted=case!='consent';seal=self.seal_value
                if case=='missing-seal':seal=None
                elif case=='null-seal':seal=False
                elif case=='wrong-seal':seal='0'*64
                elif case=='duplicate':
                    path=self.review/'plan.json';path.write_text('{"schema":2,"schema":2}');seal=installer.digest(path)
                elif case=='extra':p['unexpected']=True;self.reseal(p);seal=self.seal_value
                elif case=='boolean-schema':p['schema']=True;self.reseal(p);seal=self.seal_value
                elif case=='unsafe-reference':p['engine_contract']['path']='relative';self.reseal(p);seal=self.seal_value
                before={v.name:v.read_bytes() for v in self.review.iterdir()}
                with self.assertRaises(ValueError):installer.apply(self.review,accepted,seal)
                self.assertFalse(self.root.exists());self.assertFalse(installer.CLAIM_ROOT.exists());self.assertEqual(self.starts,[]);self.assertEqual(self.calls,[]);self.assertEqual(before,{v.name:v.read_bytes() for v in self.review.iterdir()})

    def test_02_full_render_tamper(self):
        for case in ['root','project','socket_root','profile','release','app_image','worker_image','cpu_budget','memory_budget_mib','port','guest','missing-file','extra-file','compose-mount','worker-authority','apparmor','seccomp','tmpfiles','duplicate-contract','worker-command','app-label']:
            with self.subTest(case=case):
                self.reset();p=copy.deepcopy(self.plan)
                if case in ['root','project','socket_root','profile','release','app_image','worker_image','cpu_budget','memory_budget_mib','port']:
                    p[case]=p[case]+1 if type(p[case]) is int else p[case]+'x'
                elif case=='guest':p['guest']['guest_sha256']='9'*64
                elif case=='missing-file':p['files_sha256'].pop('tmpfiles.conf')
                elif case=='extra-file':p['files_sha256']['other.json']='0'*64
                elif case in ['compose-mount','worker-authority','apparmor','seccomp','tmpfiles']:
                    name={'compose-mount':'compose.yaml','worker-authority':'worker.json','apparmor':'worker.apparmor','seccomp':'worker.seccomp.json','tmpfiles':'tmpfiles.conf'}[case]
                    path=self.review/name;path.write_bytes(path.read_bytes()+b' changed');p['files_sha256'][name]=installer.digest(path)
                elif case=='duplicate-contract':
                    path=Path(self.worker_ref['path']);path.write_text('{"schema":1,"schema":1}');p['worker_contract']['sha256']=installer.digest(path)
                    p.pop('files_sha256');rendered=installer.render(p);p['files_sha256']={}
                    for name,data in rendered.items():installer.write(self.review/name,data.decode());p['files_sha256'][name]=installer.digest(self.review/name)
                elif case=='worker-command':self.images[prior.W]['Config']['Cmd']=['--config','/alternate']
                elif case=='app-label':self.images[prior.B]['Config']['Labels']['io.tofi.ui.sha256']='0'*64
                self.reseal(p)
                with self.assertRaises((ValueError,OSError)):self.apply()
                self.assertFalse(self.root.exists());self.assertFalse(installer.CLAIM_ROOT.exists());self.assertEqual(self.starts,[])

    def test_03_claims_concurrency_and_crash(self):
        for case in ['busy','claim-before-state','same-project','same-root','missing-claim','root-replaced','legacy-adoption','legacy-project','legacy-socket','legacy-profile','unrelated-legacy','occupied-root','stopped-project','network','volume','profile-file','socket','loaded-profile','tmpfiles']:
            with self.subTest(case=case):
                self.reset()
                if case in ['occupied-root','stopped-project','network','volume','profile-file','socket','loaded-profile','tmpfiles']:
                    original_exists=Path.exists;original_read=Path.read_text
                    occupied={'profile-file':'/etc/apparmor.d/'+self.plan['profile'],'socket':self.plan['socket_root'],'tmpfiles':'/etc/tmpfiles.d/'+self.plan['profile']+'.conf'}.get(case)
                    if case=='occupied-root':self.root.mkdir()
                    def exists(path):return str(path)==occupied or original_exists(path)
                    def read(path,*args,**kwargs):
                        if str(path)=='/sys/kernel/security/apparmor/profiles':return self.plan['profile']+' (enforce)' if case=='loaded-profile' else ''
                        return original_read(path,*args,**kwargs)
                    def command(args,**kwargs):
                        kind='stopped-project' if args[1]=='ps' else args[1]
                        return subprocess.CompletedProcess([],0,'synthetic-occupied' if kind==case else '')
                    with patch.object(Path,'exists',exists),patch.object(Path,'read_text',read),patch.object(installer,'run',side_effect=command):
                        with self.assertRaisesRegex(ValueError,'occupied|already exists'):FreshApply.actual_vacant(self.plan)
                    self.assertEqual(self.starts,[]);continue
                if case=='busy':
                    with installer.claim_lock(create=True):
                        with self.assertRaisesRegex(ValueError,'busy'):self.apply()
                    self.assertFalse(self.root.exists());continue
                if case in ['claim-before-state','same-project','same-root']:
                    with installer.claim_lock(create=True) as registry:installer.new_claim(registry,self.plan,self.seal_value,{'app':self.app,'worker':self.worker})
                    other=copy.deepcopy(self.plan)
                    if case=='same-project':other['root']=str(self.base/'other');other['release']=str(self.base/'other/release')
                    elif case=='same-root':other.update(project='different',socket_root='/run/tofi-different',profile='tofi-worker-different')
                    with installer.claim_lock() as registry:
                        with self.assertRaisesRegex(ValueError,'claim already exists'):installer.new_claim(registry,other,self.seal_value,{'app':self.app,'worker':self.worker})
                    self.assertFalse(self.root.exists());continue
                self.apply();state=self.state();config=installer.private_json(self.root/'compose.yaml')
                if case in ['legacy-project','legacy-socket','legacy-profile','unrelated-legacy']:
                    legacy=self.base/'legacy';legacy.mkdir(mode=0o700);old=dict(root=str(legacy),project='unrelated')
                    if case=='legacy-project':old['project']=self.plan['project']
                    elif case=='legacy-socket':old['socket_root']=self.plan['socket_root']
                    elif case=='legacy-profile':old['profile']=self.plan['profile']
                    if case=='unrelated-legacy':installer.validate_claim(legacy,old,{},installer.CLAIM_ROOT)
                    else:
                        with self.assertRaisesRegex(ValueError,'collision'):installer.validate_claim(legacy,old,{},installer.CLAIM_ROOT)
                    continue
                if case=='missing-claim':state.pop('claim')
                elif case=='legacy-adoption':state.pop('claim');state.pop('authority');state.pop('install_intent')
                else:state['claim']['root_inode']+=1
                with self.assertRaisesRegex(ValueError,'claim|identity'):installer.validate_claim(self.root,state,config,installer.CLAIM_ROOT)
                with installer.claim_lock():
                    with self.assertRaisesRegex(ValueError,'busy'):
                        with installer.lifecycle(self.root):pass

    def test_04_release_copy_and_capacity(self):
        for case in ['success','pre-space','post-space','low-admission','headroom-not-twice','hardlink','symlink','changed-bytes','extra-source','logical-size','copy-exclusive']:
            with self.subTest(case=case):
                self.reset()
                if case=='pre-space':self.free=17*installer.GIB
                elif case=='post-space':
                    original=installer.copy_release
                    def copier(root,p):original(root,p);self.free=17*installer.GIB-1
                    self.mocks_extra=patch.object(installer,'copy_release',side_effect=copier);self.mocks_extra.start();self.addCleanup(self.mocks_extra.stop)
                elif case=='low-admission':self.admission=16*installer.GIB-1
                elif case in ['hardlink','symlink','changed-bytes','extra-source']:
                    self.source.chmod(0o700)
                    if case=='hardlink':os.link(self.source/'vmlinux',self.base/'linked')
                    elif case=='symlink':(self.source/'vmlinux').unlink();(self.source/'vmlinux').symlink_to(self.source/'rootfs.ext4')
                    elif case=='extra-source':(self.source/'unexpected').write_text('synthetic')
                    else:(self.source/'vmlinux').chmod(0o600);(self.source/'vmlinux').write_text('changed');(self.source/'vmlinux').chmod(0o400)
                elif case=='logical-size':
                    p=copy.deepcopy(self.plan);p['guest']['logical_bytes']['rootfs.ext4']=8*installer.GIB;self.reseal(p)
                elif case=='copy-exclusive':
                    original=installer.copy_release
                    def copier(root,p):(root/'release').mkdir();original(root,p)
                    self.mocks_extra=patch.object(installer,'copy_release',side_effect=copier);self.mocks_extra.start();self.addCleanup(self.mocks_extra.stop)
                if case in ['success','headroom-not-twice']:
                    self.assertTrue(self.apply()['installed'])
                    for name in (*installer.FILES,'account-release.json'):
                        a=(self.source/name).stat();b=(self.root/'release'/name).stat();self.assertNotEqual((a.st_dev,a.st_ino),(b.st_dev,b.st_ino));self.assertEqual(b.st_nlink,1);self.assertFalse(b.st_mode&0o222)
                    self.assertEqual(installer.release_binding(self.root/'release'),self.plan['guest']);self.assertEqual(self.starts,['worker','app'])
                else:
                    with self.assertRaises((ValueError,OSError)):self.apply()
                    self.assertNotIn('app',self.starts)

    def test_05_durable_ordering(self):
        for effect in ['prepare-directories-and-config','copy-owned-Guest','install-owned-permissions','start-worker','read-only-capacity','start-app']:
            with self.subTest(effect=effect):
                self.reset();original=installer.durable_write;events=[]
                def writer(path,value):
                    original(path,value)
                    if Path(path).name=='install-state.json':events.append(copy.deepcopy(value))
                with patch.object(installer,'durable_write',side_effect=writer):self.apply()
                selected=[v for v in events if v['install_intent']['effects'] and v['install_intent']['effects'][-1]==effect]
                self.assertTrue(selected);self.assertEqual(selected[0]['phase'],'installing');self.assertEqual(selected[0]['bootstrap_contract_floor'],'d100-v1');self.assertEqual(selected[0]['app_contract'],self.app);self.assertEqual(selected[0]['authority'],self.plan)
                self.assertEqual(events[-1]['phase'],'installed');self.assertEqual(events[-1]['install_intent']['stage'],'completed')
                self.assertLess(self.events.index('capacity'),self.events.index('start-app'))

    def test_06_exact_identity_and_health(self):
        for case in ['worker-image','app-image','labels','mount','environment','entrypoint','command','privileged','security','port','extra-mount','duplicate-env','unknown-service','extra-label','extra-device','runtime','userns','uts','publish-all','short-identity','normalized-caps','redirect','proxy','nonboolean-health','ambient-docker','ambient-compose','engine-id']:
            with self.subTest(case=case):
                self.reset()
                if case in ['ambient-docker','ambient-compose']:
                    with patch.dict(os.environ,{'DOCKER_HOST' if case=='ambient-docker' else 'COMPOSE_FILE':'synthetic-override'}):
                        with self.assertRaisesRegex(ValueError,'ambient'):self.apply()
                    self.assertFalse(self.root.exists());continue
                if case=='engine-id':
                    original=self.command
                    def command(args,**kwargs):return subprocess.CompletedProcess([],0,json.dumps({'ID':'foreign','OSType':'linux'})) if args[:2]==['docker','info'] else original(args,**kwargs)
                    with patch.object(installer,'run',side_effect=command):
                        with self.assertRaisesRegex(ValueError,'engine identity'):self.apply()
                    self.assertFalse(self.root.exists());continue
                if case in ['redirect','proxy','nonboolean-health']:
                    opener=unittest.mock.MagicMock();response=opener.open.return_value.__enter__.return_value;response.status=200;response.read.return_value=b'{"ok":true}'
                    if case=='redirect':opener.open.side_effect=ValueError('synthetic redirect refusal')
                    elif case=='nonboolean-health':response.read.return_value=b'{"ok":1}'
                    with patch.object(installer.urllib.request,'build_opener',return_value=opener) as build,patch.object(installer.time,'sleep'):
                        if case=='proxy':
                            # Restore the actual function from the module's fixture patch.
                            real_health=FreshApply.actual_health
                            real_health(self.root,{'port':18333});self.assertEqual(build.call_args.args[0].proxies,{})
                        else:
                            with self.assertRaises(ValueError):FreshApply.actual_health(self.root,{'port':18333})
                    continue
                original=self.compose
                def command(root,*args):
                    result=original(root,*args)
                    if args[-1]=='worker':
                        item=self.containers['worker']
                        if case=='worker-image':item['Image']=prior.A
                        elif case=='labels':item['Config']['Labels']['com.docker.compose.project']='foreign'
                        elif case=='mount':item['Mounts'][0]['RW']=False
                        elif case=='environment':item['Config']['Env']=['UNREVIEWED=1']
                        elif case=='entrypoint':item['Config']['Entrypoint']=['alternate']
                        elif case=='command':item['Config']['Cmd']=['--config','/alternate']
                        elif case=='privileged':item['HostConfig']['Privileged']=True
                        elif case=='security':item['HostConfig']['SecurityOpt']=[]
                        elif case=='port':item['HostConfig']['PortBindings']={'1/tcp':[]}
                        elif case=='extra-mount':item['Mounts'].append(dict(Type='volume'))
                        elif case=='duplicate-env':item['Config']['Env']=['X=1','X=2']
                        elif case=='unknown-service':item['Config']['Labels']['com.docker.compose.service']='foreign'
                        elif case=='extra-label':item['Config']['Labels']['io.unreviewed.route']='public'
                        elif case=='extra-device':item['HostConfig']['DeviceRequests']=[{'Driver':'nvidia','Count':-1,'Capabilities':[['gpu']]}]
                        elif case=='runtime':item['HostConfig']['Runtime']='nvidia'
                        elif case=='userns':item['HostConfig']['UsernsMode']='host'
                        elif case=='uts':item['HostConfig']['UTSMode']='host'
                        elif case=='publish-all':item['HostConfig']['PublishAllPorts']=True
                        elif case=='normalized-caps':item['HostConfig']['CapAdd']=['CAP_'+v for v in installer.CAPS]
                    elif case=='app-image':self.containers['app']['Image']=prior.A
                    return result
                original_run=self.command
                def runner(args,**kwargs):
                    result=original_run(args,**kwargs)
                    if case=='short-identity' and args[:2]==['docker','ps']:return subprocess.CompletedProcess([],0,'\n'.join(v[:12] for v in result.stdout.split()))
                    return result
                with patch.object(installer,'compose',side_effect=command),patch.object(installer,'run',side_effect=runner):
                    if case=='normalized-caps':self.assertTrue(self.apply()['installed']);continue
                    with self.assertRaises(installer.RecoveryFailure) as raised:self.apply()
                self.assertIsNotNone(raised.exception.details['stop_error']);self.assertEqual(self.stop_ids,[])

    def test_07_preparation_faults_retained(self):
        for stage in ['prepare','copy_release','permissions','initial-state','before-root','claim-fsync']:
            with self.subTest(stage=stage):
                self.reset();original=installer.durable_write
                def writer(path,value):
                    if stage=='initial-state' and not value['install_intent']['effects'] and 'recovery_failure' not in value:raise OSError('synthetic ORIGINAL initial journal fault')
                    original(path,value)
                if stage in ['prepare','copy_release','permissions']:fault=patch.object(installer,stage,side_effect=OSError('synthetic ORIGINAL preparation fault'))
                elif stage=='before-root':
                    original_mkdir=Path.mkdir
                    def mkdir(path,*args,**kwargs):
                        if path==self.root:raise OSError('synthetic ORIGINAL root creation fault')
                        return original_mkdir(path,*args,**kwargs)
                    fault=patch.object(Path,'mkdir',mkdir)
                elif stage=='claim-fsync':
                    original_fsync=installer.os.fsync;syncs=0
                    def fsync(fd):
                        nonlocal syncs
                        syncs+=1
                        if syncs==2:raise OSError('synthetic ORIGINAL claim fsync fault')
                        original_fsync(fd)
                    fault=patch.object(installer.os,'fsync',side_effect=fsync)
                else:fault=contextlib.nullcontext()
                with fault,patch.object(installer,'durable_write',side_effect=writer):
                    with self.assertRaises((OSError,installer.RecoveryFailure)):self.apply()
                self.assertTrue((installer.CLAIM_ROOT/'synthetic.json').exists());self.assertEqual(self.starts,[]);self.assertEqual(self.stop_ids,[])
                if self.root.exists():self.assertEqual(self.state()['phase'],'preparation-failed-retained')
                with self.assertRaises((ValueError,OSError)):self.apply()

    def test_08_start_and_commit_faults(self):
        for stage in ['worker-before','worker-after','capacity','app-after','health','commit-before','commit-after','interrupt']:
            with self.subTest(stage=stage):
                self.reset();original_compose=self.compose;original_write=installer.durable_write
                def compose(root,*args):
                    if stage=='worker-before' and args[-1]=='worker':raise OSError('synthetic ORIGINAL worker fault')
                    result=original_compose(root,*args)
                    if stage in ['worker-after','app-after'] and args[-1]==stage.split('-')[0]:raise OSError('synthetic ORIGINAL startup fault')
                    return result
                def writer(path,value):
                    if stage.startswith('commit') and value.get('phase')=='installed':
                        if stage=='commit-after':original_write(path,value)
                        raise OSError('synthetic ORIGINAL installed persistence fault')
                    original_write(path,value)
                fault=KeyboardInterrupt('synthetic ORIGINAL interruption') if stage=='interrupt' else OSError('synthetic ORIGINAL health fault')
                with patch.object(installer,'compose',side_effect=compose),patch.object(installer,'durable_write',side_effect=writer),patch.object(installer,'health',side_effect=fault if stage in ['health','interrupt'] else self.health),patch.object(installer,'worker_capacity',side_effect=OSError('synthetic ORIGINAL capacity fault') if stage=='capacity' else lambda identity,p:dict(admission_remaining_bytes=self.admission,per_account_internal_reserved_bytes=8*installer.GIB,accounts=[])):
                    with self.assertRaises((OSError,KeyboardInterrupt)):self.apply()
                self.assertEqual(self.state()['phase'],'stopped-retained');self.assertEqual(self.state()['install_intent']['stage'],'failed')
                self.assertEqual(set(self.stop_ids),{v['Id'] for v in self.containers.values()});self.assertTrue((self.root/'release/account-release.json').exists());self.assertNotIn('fallback',self.starts)
                with self.assertRaises(ValueError):installer.upgrade_contract(self.root,self.state(),installer.private_json(self.root/'compose.yaml'),prior.B,prior.W,'unused','0'*64)

    def test_09_unproven_recovery_errors(self):
        for case in ['stop','persistence','both','unclean','pending']:
            with self.subTest(case=case):
                self.reset();original_write=installer.durable_write;original_run=self.command
                def writer(path,value):
                    if 'recovery_failure' in value and case in ['persistence','both']:raise OSError('synthetic PERSISTENCE fault')
                    if value.get('phase')=='installed' and case=='pending':
                        (self.root/'install-state.json.pending').write_text('synthetic interrupted write');raise OSError('synthetic ORIGINAL pending write fault')
                    original_write(path,value)
                def command(args,**kwargs):
                    if args[:2]==['docker','stop'] and case in ['stop','both']:raise OSError('synthetic STOP fault')
                    result=original_run(args,**kwargs)
                    if args[:2]==['docker','stop'] and case=='unclean':
                        for item in self.containers.values():item['State']['ExitCode']=1
                    return result
                with patch.object(installer,'run',side_effect=command),patch.object(installer,'durable_write',side_effect=writer),patch.object(installer,'health',side_effect=self.health if case=='pending' else OSError('synthetic ORIGINAL health fault')):
                    with self.assertRaises(installer.RecoveryFailure) as raised:self.apply()
                d=raised.exception.details;self.assertIn('ORIGINAL',d['original_error']['message'])
                if case in ['stop','both','unclean']:self.assertIsNotNone(d['stop_error']);self.assertIn('STOP_UNPROVEN',str(raised.exception))
                if case in ['persistence','both','pending']:self.assertIsNotNone(d['persistence_error']);self.assertIn('DURABLE_FENCE_UNPROVEN',str(raised.exception))
                self.assertEqual(self.starts,['worker','app'])

    def test_10_success_and_later_d100_gate(self):
        for case in ['success','first-migration-refused','claim-tamper','incomplete','uninstall','no-secret-access','pinned-execution']:
            with self.subTest(case=case):
                self.reset();result=self.apply();self.assertTrue(result['installed']);state=self.state();config=installer.private_json(self.root/'compose.yaml')
                installer.validate_claim(self.root,state,config,installer.CLAIM_ROOT)
                self.assertFalse((self.root/'data/owner-bootstrap.secret').exists());self.assertEqual(list((self.root/'data').iterdir()),[])
                self.assertTrue(all('reserve' not in str(call) and 'owner-bootstrap' not in str(call) for call in self.calls))
                if case=='first-migration-refused':
                    reference=self.seal('later-upgrade.json',dict(schema=1,kind='d100-app-upgrade',root=str(self.root),current_app_image=prior.B,worker_image=prior.W,target=self.app_ref,failure={'mode':'first-migration-stop-retain'}))
                    with self.assertRaisesRegex(ValueError,'later D100'):installer.upgrade_contract(self.root,state,config,prior.B,prior.W,reference['path'],reference['sha256'])
                elif case=='claim-tamper':
                    path=Path(state['claim']['reference']['path']);record=installer.private_json(path);record['plan']['port']+=1;installer.write(path,record)
                    with self.assertRaisesRegex(ValueError,'claim binding'):installer.validate_claim(self.root,state,config,installer.CLAIM_ROOT)
                elif case=='incomplete':
                    state['install_intent']['stage']='preparing'
                    with self.assertRaisesRegex(ValueError,'incomplete'):installer.validate_claim(self.root,state,config,installer.CLAIM_ROOT)
                elif case=='uninstall':
                    # Exact-ID removal command is mocked; claim/release/data retained.
                    original=self.command
                    def command(args,**kwargs):return subprocess.CompletedProcess([],0,'') if args[:2]==['docker','rm'] else original(args,**kwargs)
                    with patch.object(installer,'run',side_effect=command),patch.object(installer,'owned_state',return_value=(self.root,state,config)):self.assertTrue(installer.uninstall(self.root)['uninstalled'])
                    self.assertTrue((installer.CLAIM_ROOT/'synthetic.json').exists());self.assertTrue((self.root/'release').exists());self.assertEqual(self.state()['phase'],'stopped-retained')
                elif case=='pinned-execution':
                    engine=installer.local_engine(self.engine_ref,self.root);token=installer.ENGINE.set(engine)
                    try:
                        with patch.object(installer.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'')) as process:
                            FreshApply.actual_run(['docker','compose','version']);args=process.call_args
                            self.assertEqual(args.args[0][0],engine['compose']['path']);self.assertEqual(args.kwargs['env']['DOCKER_HOST'],'unix:///run/docker.sock');self.assertEqual(args.kwargs['env']['COMPOSE_DISABLE_ENV_FILE'],'1')
                    finally:installer.ENGINE.reset(token)


FreshApply.actual_vacant=installer.vacant
FreshApply.actual_health=installer.health
FreshApply.actual_run=installer.run

if __name__=='__main__':unittest.main()
