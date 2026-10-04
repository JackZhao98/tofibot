#!/usr/bin/env python3
"""Review and maintain a dedicated Linux x86_64 Compose account deployment.

Images and sealed Guest release are operator-provided; nothing is published or
pulled. Plan writes only a new review directory. Fresh apply is blocked.
Upgrade preserves current data; uninstall stops services and retains all files.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import socket
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / 'microvm'))
from account_release_check import validate_release, _load_manifest, _no_symlink_path
from worker_entrypoint import validate_config

CAPS = ['SYS_ADMIN','NET_ADMIN','MKNOD','SYS_CHROOT','SETUID','SETGID','CHOWN','DAC_OVERRIDE','FOWNER']
GIB = 1024**3
BOOTSTRAP_CONTRACT = 'd100-v1'
BOOTSTRAP_CHECKS = {'first_admin_secret', 'initialized_restart', 'consumed_replay', 'pending_setup_matching_ui'}
PAIR_CHECKS = {'n_data_written','current_data_retained','b_data_read','b_login','b_setup_closed','b_consumed_replay_rejected'}


class AppStartupFailure(ValueError):
    """An explicitly attempted startup or health check failed."""


class RecoveryFailure(ValueError):
    def __init__(self,details):
        self.details=details
        status='STOP_UNPROVEN' if details['stop_error'] else 'stopped-retained'
        if details['persistence_error']:status+='; DURABLE_FENCE_UNPROVEN'
        super().__init__(status+'; CURRENT data retained; operator inspection required; '+json.dumps(details,sort_keys=True))


def run(args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=180, **kwargs)


def absolute(value):
    p = Path(value)
    if not re.fullmatch(r'/[A-Za-z0-9_./-]+', str(p)) or '..' in p.parts or p == Path('/'):
        raise ValueError('canonical absolute path without whitespace or policy metacharacters required')
    return _no_symlink_path(p, must_exist=False)


def image_id(value):
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', value):
        raise ValueError('local immutable image ID required; resolve the imported/built tag first')
    return value


def digest(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()


def write(p, value, mode=0o600):
    p.write_text(json.dumps(value,indent=2)+'\n' if not isinstance(value,str) else value)
    os.chmod(p,mode)


def private_json(path, expected=None):
    path=absolute(path);info=path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid!=os.geteuid() or info.st_mode&0o077 or info.st_size>65536:
        raise ValueError('private regular operator-owned metadata required')
    with open(path,'rb') as stream:
        if not os.path.samestat(info,os.fstat(stream.fileno())):raise ValueError('metadata identity changed')
        data=stream.read(65537)
    if expected is not None and hashlib.sha256(data).hexdigest()!=sha256_hex(expected):raise ValueError('reviewed metadata digest differs')
    def unique(pairs):
        result={}
        for key,value in pairs:
            if key in result:raise ValueError('duplicate metadata field')
            result[key]=value
        return result
    return json.loads(data,object_pairs_hook=unique)


def sha256_hex(value):
    if not isinstance(value,str) or not re.fullmatch('[0-9a-f]{64}',value):raise ValueError('SHA-256 digest required')
    return value


def durable_write(path,value):
    # A leftover staging file fences retries; never silently remove it.
    path=Path(path);pending=path.with_name(path.name+'.pending')
    fd=os.open(pending,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'w') as stream:
        json.dump(value,stream,indent=2);stream.write('\n');stream.flush();os.fsync(stream.fileno())
    os.replace(pending,path)
    directory=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
    try:os.fsync(directory)
    finally:os.close(directory)


@contextlib.contextmanager
def lifecycle(root):
    # Lock the existing directory: invalid preflight creates no lock file.
    root=absolute(root);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:
        info=os.fstat(fd)
        if info.st_uid!=os.geteuid() or info.st_mode&0o077:raise ValueError('private operator-owned install root required')
        try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError as exc:raise ValueError('owned installer lifecycle is busy') from exc
        yield owned_state(root)
    finally:os.close(fd)


def generate(root, project, app, worker, release, cpu, memory, port, out):
    root=absolute(root);release=absolute(release);out=absolute(out)
    if not re.fullmatch(r'[a-z][a-z0-9-]{0,31}',project):raise ValueError('invalid unique Compose project name')
    if cpu<1 or memory<2048 or not 1024<=port<=65535:raise ValueError('invalid runtime budget or loopback port')
    if root.exists():raise ValueError('fresh install root must not exist; use upgrade for existing data')
    manifest=_load_manifest(release/'account-release.json')
    image_id(app);image_id(worker)
    state=root/'worker';sockets=Path('/run')/('tofi-'+project);profile='tofi-worker-'+project
    cfg=dict(release_dir=str(release),state_root=str(state/'state'),config_root=str(state/'config'),unit_root=str(state/'unused-units'),socket_root=str(sockets/'accounts'),ledger_root=str(state/'ledger'),broker_socket=str(sockets/'broker.sock'),socket_gid=10001,app_uid=10001,headroom_bytes=GIB,warning_bytes=15*GIB,reserved_slots=[],vcpus=1,memory_mib=1024,runtime_vcpu_budget=cpu,runtime_memory_mib_budget=memory,host_memory_headroom_mib=1024,cgroup_root='/run/tofi-worker/cgroup/tofi-vms',external_reserved_bytes=0,per_account_internal_reserved_bytes=8*GIB,external_disks=[],expected_guest_sha256=manifest['guest_binary_sha256'],release_manifest_sha256=digest(release/'account-release.json'))
    validate_config(cfg)
    def bind(src,dst=None,ro=False):return dict(type='bind',source=str(src),target=str(dst or src),read_only=ro,bind=dict(create_host_path=False,propagation='rprivate'))
    worker_service=dict(image=worker,user='0:0',restart='unless-stopped',read_only=True,cgroup='private',cpus=cpu,mem_limit=(memory+256)*1024**2,pids_limit=512,cap_drop=['ALL'],cap_add=CAPS,devices=['/dev/kvm:/dev/kvm','/dev/net/tun:/dev/net/tun'],device_cgroup_rules=['c 10:257 m'],security_opt=['no-new-privileges:true','apparmor:'+profile,'seccomp:'+str(root/'config/worker.seccomp.json')],sysctls={'net.ipv4.ip_forward':'1'},tmpfs=['/run:rw,nosuid,nodev,mode=0755,size=64m','/tmp:rw,nosuid,nodev,noexec,mode=1777,size=64m'],volumes=[bind(state),bind(sockets),bind(root/'config/worker.json','/etc/tofi-worker/config.json',True),bind(release,ro=True)])
    env={'TOFI_LISTEN':'0.0.0.0:8321','TOFI_DATA_DIR':'/app/data','TOFI_UI_DIR':'/app/ui','TOFI_OWNER_AUTH':'1','TOFI_OWNER_ALLOW_LAN_HTTP':'1','TOFI_MULTI_ACCOUNT':'1','TOFI_ACCOUNT_MAINTENANCE':'0','TOFI_ACCOUNT_PROVISIONER_SOCKET':str(sockets/'broker.sock'),'TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT':str(sockets/'accounts'),'TOFI_ACCOUNT_COMPUTER_DISK_GIB':'8','TOFI_ACCOUNT_DB_MAX_BYTES':str(GIB)}
    app_service=dict(image=app,user='10001:10001',restart='unless-stopped',init=True,read_only=True,cpus=.5,mem_limit=512*1024**2,cap_drop=['ALL'],security_opt=['no-new-privileges:true'],tmpfs=['/tmp:rw,noexec,nosuid,size=64m'],environment=env,ports=['127.0.0.1:'+str(port)+':8321'],volumes=[bind(root/'data','/app/data'),bind(sockets,ro=True)],healthcheck=dict(test=['CMD','wget','--spider','--quiet','http://127.0.0.1:8321/health'],interval='15s',timeout='3s',retries=3,start_period='15s'))
    compose=dict(name=project,services=dict(app=app_service,worker=worker_service))
    policy=(HERE/'self-host/worker.apparmor.template').read_text().replace('tofi-account-worker',profile).replace('/var/lib/tofi-worker',str(state))
    out.mkdir(mode=0o700,parents=False,exist_ok=False)
    write(out/'compose.yaml',compose);write(out/'worker.json',cfg);write(out/'worker.apparmor',policy)
    shutil.copyfile(HERE/'self-host/worker.seccomp.json',out/'worker.seccomp.json');os.chmod(out/'worker.seccomp.json',0o600)
    write(out/'tmpfiles.conf','d '+str(sockets)+' 0750 0 10001 -\n')
    plan=dict(schema=1,root=str(root),project=project,socket_root=str(sockets),profile=profile,release=str(release),app_image=app,worker_image=worker,cpu_budget=cpu,memory_budget_mib=memory,port=port,first_account='atomic first Admin via browser; no installer password/account creation',public_registration=False,host_ready='unchecked',data_retained_on_upgrade_and_uninstall=True)
    plan['files_sha256']={p.name:digest(p) for p in out.iterdir()};write(out/'plan.json',plan)
    return plan


def load_plan(directory):
    directory=absolute(directory);p=json.loads((directory/'plan.json').read_text())
    if p.get('schema')!=1:raise ValueError('unsupported plan')
    for name,h in p['files_sha256'].items():
        if Path(name).name!=name or digest(directory/name)!=h:raise ValueError('reviewed plan file changed')
    absolute(p['root']);absolute(p['release']);absolute(p['socket_root'])
    if p['profile']!='tofi-worker-'+p['project']:raise ValueError('profile identity differs')
    return p


def inspect_image(identity,app=False):
    data=json.loads(run(['docker','image','inspect',image_id(identity)]).stdout)[0]
    if data['Id']!=identity:raise ValueError('image identity mismatch')
    if app and (data['Config'].get('Labels') or {}).get('io.tofi.account-runtime')!='1':raise ValueError('App image is not account-compatible')
    if data.get('Architecture')!='amd64':raise ValueError('amd64 images required')
    return data


def preflight(directory):
    p=load_plan(directory);root=absolute(p['root'])
    if platform.system()!='Linux' or platform.machine()!='x86_64':raise ValueError('supported host: dedicated Linux x86_64')
    if os.geteuid()!=0:raise ValueError('host root metadata visibility required for preflight')
    for device in ['/dev/kvm','/dev/net/tun']:
        if not stat.S_ISCHR(Path(device).stat().st_mode):raise ValueError('KVM/tun character devices required')
    if not Path('/sys/fs/cgroup/cgroup.controllers').exists():raise ValueError('cgroup v2 required')
    for name in ['docker','apparmor_parser','systemd-tmpfiles','debugfs','e2fsck']:
        if not shutil.which(name):raise ValueError('missing host prerequisite: '+name)
    info=json.loads(run(['docker','info','--format','{{json .}}']).stdout)
    if info.get('CgroupVersion')!='2' or not any('apparmor' in s for s in info.get('SecurityOptions',[])):raise ValueError('Docker cgroup v2 and enforced AppArmor required')
    if root.exists():raise ValueError('fresh root already exists; no adoption or overwrite is automatic')
    if run(['docker','ps','-q']).stdout.strip():raise ValueError('fresh dedicated host required: existing running containers need separate reviewed admission')
    for process in Path('/proc').iterdir():
        if process.name.isdecimal():
            try:name=(process/'comm').read_text().strip()
            except FileNotFoundError:continue
            if name=='firecracker':raise ValueError('existing VM must be inventoried; do not assume spare capacity')
    if p['cpu_budget']>os.cpu_count()-1:raise ValueError('CPU budget must preserve1CPU for App/host')
    mem={l.split(':')[0]:int(l.split()[1])//1024 for l in Path('/proc/meminfo').read_text().splitlines() if l.startswith(('MemTotal:','MemAvailable:'))}
    if p['memory_budget_mib']+256+512+1024>mem['MemAvailable']:raise ValueError('memory budget cannot cover Worker/App/1GiB safety')
    parent=root.parent
    while not parent.exists():parent=parent.parent
    fs=os.statvfs(parent)
    if fs.f_bavail*fs.f_frsize<17*GIB:raise ValueError('first Admin requires8GiB workspace+8GiB internal+1GiB headroom')
    inspect_image(p['app_image'],True);w=inspect_image(p['worker_image'])
    if w['Config'].get('Entrypoint')!=['python3','/opt/tofi-worker/worker_entrypoint.py','--config','/etc/tofi-worker/config.json']:raise ValueError('trusted account Worker entrypoint required')
    cfg=json.loads((Path(directory)/'worker.json').read_text());validate_config(cfg)
    validate_release(p['release'],str(HERE/'microvm/manager.py'),cfg['expected_guest_sha256'],expected_manifest_sha256=cfg['release_manifest_sha256'])
    run(['apparmor_parser','-Q','-T',str(Path(directory)/'worker.apparmor')])
    run(['docker','compose','-f',str(Path(directory)/'compose.yaml'),'config','--quiet'])
    with socket.socket() as s:s.bind(('127.0.0.1',p['port']))
    return dict(ready=True,first_admin_admissible=True,root=p['root'],permission_installation='not performed',available_bytes=fs.f_bavail*fs.f_frsize)


def compose(root,*args):return run(['docker','compose','-f',str(root/'compose.yaml'),*args])


def health(root,state):
    for _ in range(30):
        try:
            d=json.loads(urllib.request.urlopen('http://127.0.0.1:'+str(state['port'])+'/health',timeout=2).read())
            if d.get('ok'):return
        except (TimeoutError,socket.timeout):raise
        except urllib.error.URLError as exc:
            if isinstance(exc.reason,(TimeoutError,socket.timeout)):raise
        except (OSError,ValueError):pass
        time.sleep(1)
    raise ValueError('App health failed; files/data retained, inspect the owned project')


def apply(directory,accepted):
    if not accepted:raise ValueError('review permission files, then explicitly use --accept-permissions')
    raise ValueError('fresh apply is blocked until its reviewed D100 contract/floor transaction is implemented; plan/preflight only')


def owned_state(root):
    root=absolute(root)
    if os.geteuid()!=0:raise ValueError('host operator required')
    for name in ['install-state.json','compose.yaml']:
        if (root/(name+'.pending')).exists() or (root/(name+'.pending')).is_symlink():raise ValueError('interrupted metadata write requires operator review')
    p=private_json(root/'install-state.json')
    if p['root']!=str(root):raise ValueError('ownership root differs')
    c=private_json(root/'compose.yaml')
    if c['name']!=p['project']:raise ValueError('Compose ownership differs')
    return root,p,c


def stopped(root):
    compose(root,'stop','--timeout','180')
    ids=compose(root,'ps','-a','-q').stdout.split()
    if ids:
        containers=json.loads(run(['docker','inspect',*ids]).stdout)
        if len(containers)!=len(ids):raise ValueError('owned stop inspection incomplete; no restart')
        for c in containers:
            if c['State']['Running'] or c['State']['Pid'] or c['State']['ExitCode']:raise ValueError('unclean stop; ownership/budgets retained, no cleanup/restart')


def sealed_json(reference):
    if not isinstance(reference,dict) or set(reference)!={'path','sha256'}:raise ValueError('sealed artifact contract reference required')
    return private_json(reference['path'],sha256_hex(reference['sha256']))


def artifact_contract(reference):return validate_artifact(sealed_json(reference))


def validate_artifact(artifact):
    fields={'schema','app_image','source_commit','ui_source_commit','ui_sha256','bootstrap_contract','evidence'}
    if not isinstance(artifact,dict) or set(artifact)!=fields or artifact['schema']!=1 or artifact['bootstrap_contract']!=BOOTSTRAP_CONTRACT:raise ValueError('unknown App bootstrap contract')
    image_id(artifact['app_image']);sha256_hex(artifact['ui_sha256'])
    if not re.fullmatch('[0-9a-f]{40}',str(artifact['source_commit'])) or artifact['ui_source_commit']!=artifact['source_commit']:raise ValueError('matching App/UI source commits required')
    evidence=artifact['evidence']
    if not isinstance(evidence,dict) or set(evidence)!={'path','sha256'}:raise ValueError('sealed compatibility evidence required')
    record=sealed_json(evidence)
    binding={key:artifact[key] for key in fields-{'schema','evidence'}}
    if not isinstance(record,dict) or set(record)!={'schema','kind','bindings','checks'} or record['schema']!=1 or record['kind']!='d100-app-validation' or record['bindings']!=binding:
        raise ValueError('artifact compatibility evidence binding differs')
    if not isinstance(record['checks'],dict) or set(record['checks'])!=BOOTSTRAP_CHECKS or any(value!='pass' for value in record['checks'].values()):raise ValueError('complete passing bootstrap/UI evidence required')
    labels=inspect_image(artifact['app_image'],True)['Config'].get('Labels') or {}
    expected={'org.opencontainers.image.revision':artifact['source_commit'],'io.tofi.ui.source':artifact['ui_source_commit'],'io.tofi.ui.sha256':artifact['ui_sha256'],'io.tofi.bootstrap-contract':BOOTSTRAP_CONTRACT}
    if any(labels.get(key)!=value for key,value in expected.items()):raise ValueError('App image source/UI/bootstrap labels differ from contract')
    return artifact


def passing_checks(checks,names):
    return isinstance(checks,dict) and set(checks)==names and all(value=='pass' for value in checks.values())


def current_data_pair(reference,target_ref,fallback_ref,target,fallback):
    record=sealed_json(reference)
    binding=dict(target_image=target['app_image'],fallback_image=fallback['app_image'],target_contract_sha256=target_ref['sha256'],fallback_contract_sha256=fallback_ref['sha256'])
    if not isinstance(record,dict) or set(record)!={'schema','kind','bindings','checks','semantic_evidence'} or type(record['schema']) is not int or record['schema']!=1 or record['kind']!='d100-current-data-pair-validation' or record['bindings']!=binding or not passing_checks(record['checks'],PAIR_CHECKS):raise ValueError('complete passing exact N-to-B current-data pair evidence required')
    proof=sealed_json(record['semantic_evidence'])
    if not isinstance(proof,dict) or set(proof)!={'schema','kind','model','n_written','b_read','checks'} or type(proof['schema']) is not int or proof['schema']!=1 or proof['kind']!='d100-current-data-semantic-result' or proof['model']!='account-workspace-messages-v1' or not passing_checks(proof['checks'],PAIR_CHECKS):raise ValueError('structured account/workspace semantic evidence required')
    data=proof['n_written']
    if not isinstance(data,dict) or set(data)!={'account_id','bot_id','conversation_id','message_id','message_seq','message_role','message_content'} or proof['b_read']!=data or type(proof['b_read']['message_seq']) is not int or any(not isinstance(data[key],str) or not 1<=len(data[key])<=128 for key in ['account_id','bot_id','conversation_id','message_id']) or type(data['message_seq']) is not int or data['message_seq']<1 or data['message_role']!='user' or not isinstance(data['message_content'],str) or not 1<=len(data['message_content'])<=4096:raise ValueError('N-written account/message records must be read by B with unchanged semantics')
    return dict(reference=reference,record=record,semantic_result=proof)


def upgrade_contract(root,p,c,app,worker,path,sealed):
    if path is None or sealed is None:raise ValueError('reviewed D100 upgrade plan and digest required')
    if p.get('phase')!='installed':raise ValueError('interrupted/unknown lifecycle phase requires operator review; no automatic resume')
    if 'migration_intent' in p and (not isinstance(p['migration_intent'],dict) or p['migration_intent'].get('stage') not in ['completed','rolled-back']):raise ValueError('malformed/unfinished prior migration intent requires operator review')
    floor=p.get('bootstrap_contract_floor')
    if floor not in (None,BOOTSTRAP_CONTRACT):raise ValueError('unknown compatibility floor')
    services=c['services']
    if p.get('app_image')!=services['app']['image'] or p.get('worker_image')!=services['worker']['image']:raise ValueError('state/Compose image binding differs')
    if worker!=services['worker']['image']:raise ValueError('D100 upgrade cannot change Worker image')
    plan=private_json(path,sealed)
    if not isinstance(plan,dict) or set(plan)!={'schema','kind','root','current_app_image','worker_image','target','failure'} or plan['schema']!=1 or plan['kind']!='d100-app-upgrade' or plan['root']!=str(root) or plan['current_app_image']!=p['app_image'] or plan['worker_image']!=worker:
        raise ValueError('reviewed upgrade plan binding differs')
    target=artifact_contract(plan['target'])
    if target['app_image']!=app:raise ValueError('target image differs from reviewed plan')
    current=inspect_image(p['app_image'],True)
    if floor is None and ((current['Config'].get('Labels') or {}).get('io.tofi.bootstrap-contract') is not None or p.get('app_contract') is not None or p.get('migration_intent') is not None):raise ValueError('D100 installation without a persisted floor requires operator review')
    failure=plan['failure'];fallback=None;pair=None
    if floor is None:
        if failure!={'mode':'first-migration-stop-retain'}:raise ValueError('first D100 migration requires explicit stop-retain')
    else:
        if not isinstance(failure,dict) or set(failure)!={'mode','fallback','pair_evidence'} or failure['mode']!='compatible-fallback':raise ValueError('later D100 upgrade requires verified fallback and exact pair evidence')
        fallback=artifact_contract(failure['fallback'])
        if fallback['app_image']==app:raise ValueError('fallback must be a distinct reviewed App artifact')
        if not isinstance(p.get('app_contract'),dict) or p['app_contract'].get('app_image')!=p['app_image'] or p['app_contract'].get('bootstrap_contract')!=floor:
            raise ValueError('installed App contract/floor binding differs')
        validate_artifact(p['app_contract'])
        pair=current_data_pair(failure['pair_evidence'],plan['target'],failure['fallback'],target,fallback)
    inspect_image(worker)
    return dict(plan_sha256=sealed,previous_app_image=p['app_image'],target=target,fallback=fallback,pair_evidence=pair,failure_mode=failure['mode'],stage='target-starting')


def start_verified(root,p,image):
    try:
        compose(root,'up','-d','--no-build','worker');compose(root,'up','-d','--no-build','app')
    except subprocess.CalledProcessError as exc:raise AppStartupFailure('planned App startup failed') from exc
    ids=compose(root,'ps','-q','app').stdout.split()
    if len(ids)!=1:raise ValueError('owned App container identity is ambiguous')
    containers=json.loads(run(['docker','inspect',ids[0]]).stdout)
    if len(containers)!=1:raise ValueError('owned App container inspection differs')
    item=containers[0];labels=item.get('Config',{}).get('Labels') or {}
    if item.get('Image')!=image or not item.get('State',{}).get('Running') or labels.get('com.docker.compose.project')!=p['project'] or labels.get('com.docker.compose.service')!='app':raise ValueError('running App differs from planned artifact')
    try:health(root,p)
    except ValueError as exc:raise AppStartupFailure('planned App health failed') from exc


def error_record(error):
    result=dict(type=type(error).__name__,message=str(error))
    if error.__cause__ is not None:result['cause']=dict(type=type(error.__cause__).__name__,message=str(error.__cause__))
    return result


def retain_stopped(root,p,reason,original,stop_error=None):
    if stop_error is None:
        try:stopped(root)
        except BaseException as error:stop_error=error
    intent=p.get('migration_intent')
    details=dict(reason=reason,original_error=error_record(original),target_error=intent.get('target_error') if isinstance(intent,dict) else None,stop_error=error_record(stop_error) if stop_error else None,persistence_error=None)
    p['phase']='STOP_UNPROVEN' if stop_error else 'stopped-retained'
    p['recovery_failure']=details
    if isinstance(intent,dict):intent['stage']=reason
    try:durable_write(root/'install-state.json',p)
    except BaseException as error:details['persistence_error']=error_record(error)
    if stop_error or details['persistence_error']:raise RecoveryFailure(details) from original
    return reason+'; stopped-retained with CURRENT data; original='+json.dumps(details['original_error'],sort_keys=True)


def upgrade(root,app,worker,plan=None,plan_sha256=None):
    with lifecycle(root) as (root,p,c):
        intent=upgrade_contract(root,p,c,app,worker,plan,plan_sha256)
        try:stopped(root)
        except BaseException as error:
            retain_stopped(root,p,'initial stop unproven',error,stop_error=error)
            raise
        p.update(bootstrap_contract_floor=BOOTSTRAP_CONTRACT,migration_intent=intent,phase='d100-migrating')
        try:
            # This durable fence completes before Compose selection or startup.
            durable_write(root/'install-state.json',p)
            c['services']['app']['image']=app;durable_write(root/'compose.yaml',c)
        except BaseException as error:
            retain_stopped(root,p,'metadata persistence uncertain',error)
            raise
        try:start_verified(root,p,app)
        except AppStartupFailure as target_failure:
            intent['target_error']=error_record(target_failure)
            fallback=intent['fallback']
            if fallback is None:
                result=retain_stopped(root,p,'first D100 migration failed',target_failure)
                raise ValueError(result) from target_failure
            try:
                intent['stage']='fallback-stopping'
                stopped(root)
                intent['stage']='fallback-starting';durable_write(root/'install-state.json',p)
                c['services']['app']['image']=fallback['app_image'];durable_write(root/'compose.yaml',c)
                start_verified(root,p,fallback['app_image'])
                p.update(app_image=fallback['app_image'],app_contract=fallback,phase='installed')
                intent['stage']='rolled-back';durable_write(root/'install-state.json',p)
            except BaseException as error:
                retain_stopped(root,p,'fallback failed or uncertain',error,stop_error=error if intent['stage']=='fallback-stopping' else None)
                raise
            raise ValueError('upgrade failed; planned D100 fallback '+fallback['app_image']+' installed with CURRENT data') from target_failure
        except BaseException as error:
            retain_stopped(root,p,'target startup interrupted or unknown',error)
            raise
        try:
            p.update(app_image=app,app_contract=intent['target'],phase='installed')
            intent['stage']='completed';durable_write(root/'install-state.json',p)
        except BaseException as error:
            retain_stopped(root,p,'final metadata persistence uncertain',error)
            raise
        return dict(upgraded=True,app_image=app,data_retained=True,guest_release_unchanged=True,bootstrap_contract_floor=BOOTSTRAP_CONTRACT)


def uninstall(root):
    with lifecycle(root) as (root,p,c):
        stopped(root)
        compose(root,'rm','-f','app','worker')
        p['phase']='stopped-retained';durable_write(root/'install-state.json',p)
    return dict(uninstalled=True,data_retained=True,root=str(root),permissions_retained=True,next='All DBs/disks/credentials/config/release/profile/socket paths retained; no down -v, rm -rf or automatic purge. Resume using this owned Compose project after review.')


def main():
    parser=argparse.ArgumentParser(description=__doc__);sub=parser.add_subparsers(dest='action',required=True)
    plan=sub.add_parser('plan')
    for name in ['root','project','app-image','worker-image','release','out']:plan.add_argument('--'+name,required=True)
    plan.add_argument('--cpu-budget',type=int,required=True);plan.add_argument('--memory-budget-mib',type=int,required=True);plan.add_argument('--port',type=int,default=8333)
    for action in ['preflight','apply']:
        q=sub.add_parser(action);q.add_argument('plan');
        if action=='apply':q.add_argument('--accept-permissions',action='store_true')
    q=sub.add_parser('upgrade');q.add_argument('root');q.add_argument('--app-image',required=True);q.add_argument('--worker-image',required=True);q.add_argument('--upgrade-plan',required=True);q.add_argument('--upgrade-plan-sha256',required=True)
    q=sub.add_parser('uninstall');q.add_argument('root')
    a=parser.parse_args()
    try:
        if a.action=='plan':result=generate(a.root,a.project,a.app_image,a.worker_image,a.release,a.cpu_budget,a.memory_budget_mib,a.port,a.out)
        elif a.action=='preflight':result=preflight(a.plan)
        elif a.action=='apply':result=apply(a.plan,a.accept_permissions)
        elif a.action=='upgrade':result=upgrade(a.root,a.app_image,a.worker_image,a.upgrade_plan,a.upgrade_plan_sha256)
        else:result=uninstall(a.root)
        print(json.dumps(result,indent=2))
    except (ValueError,OSError,subprocess.SubprocessError) as e:
        print(str(e),file=sys.stderr);return 1
    return 0

if __name__=='__main__':sys.exit(main())
