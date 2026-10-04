#!/usr/bin/env python3
"""Review and maintain a dedicated Linux x86_64 Compose account deployment.

Images and sealed Guest release are operator-provided; nothing is published or
pulled. Plan writes only a new review directory. Fresh apply requires its seal.
Upgrade preserves current data; uninstall stops services and retains all files.
"""
import argparse
import contextlib
import contextvars
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
from account_release_check import validate_release, _load_manifest, _no_symlink_path, FILES
from worker_entrypoint import validate_config

CAPS = ['SYS_ADMIN','NET_ADMIN','MKNOD','SYS_CHROOT','SETUID','SETGID','CHOWN','DAC_OVERRIDE','FOWNER']
GIB = 1024**3
BOOTSTRAP_CONTRACT = 'd100-v1'
BOOTSTRAP_CHECKS = {'first_admin_secret', 'initialized_restart', 'consumed_replay', 'pending_setup_matching_ui'}
PAIR_CHECKS = {'n_data_written','current_data_retained','b_data_read','b_login','b_setup_closed','b_consumed_replay_rejected'}
WORKER_ENTRYPOINT = ['python3','/opt/tofi-worker/worker_entrypoint.py','--config','/etc/tofi-worker/config.json']
CLAIM_ROOT = Path('/var/lib/tofi-installer-claims')
ENGINE = contextvars.ContextVar('installer_engine',default=None)
RENDER_NAMES = {'compose.yaml','worker.json','worker.apparmor','worker.seccomp.json','tmpfiles.conf'}


class AppStartupFailure(ValueError):
    """An explicitly attempted startup or health check failed."""


class RecoveryFailure(ValueError):
    def __init__(self,details):
        self.details=details
        status='STOP_UNPROVEN' if details['stop_error'] else 'stopped-retained'
        if details['persistence_error']:status+='; DURABLE_FENCE_UNPROVEN'
        super().__init__(status+'; CURRENT data retained; operator inspection required; '+json.dumps(details,sort_keys=True))


def run(args, **kwargs):
    engine=ENGINE.get()
    if engine is not None:
        reject_ambient()
        check_engine_files(engine)
        env={'PATH':'/usr/sbin:/usr/bin:/sbin:/bin','LANG':'C.UTF-8',
             'DOCKER_HOST':'unix://'+engine['socket']['path'],'DOCKER_CONFIG':engine['config_dir'],
             'COMPOSE_DISABLE_ENV_FILE':'1'}
        if args[:2]==['docker','compose']:
            args=[engine['compose']['path'],*args[2:]]
        elif args[:1]==['docker']:
            args=[engine['docker']['path'],'--host',env['DOCKER_HOST'],'--config',engine['config_dir'],*args[1:]]
        kwargs['env']=env
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=180, **kwargs)


def absolute(value):
    if not isinstance(value,(str,Path)):raise ValueError('absolute path string required')
    p = Path(value)
    if not re.fullmatch(r'/[A-Za-z0-9_./-]+', str(p)) or '..' in p.parts or p == Path('/'):
        raise ValueError('canonical absolute path without whitespace or policy metacharacters required')
    return _no_symlink_path(p, must_exist=False)


def image_id(value):
    if not isinstance(value,str) or not re.fullmatch(r'sha256:[0-9a-f]{64}', value):
        raise ValueError('local immutable image ID required; resolve the imported/built tag first')
    return value


def digest(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()


def write(p, value, mode=0o600):
    p.write_text(json.dumps(value,indent=2)+'\n' if not isinstance(value,str) else value)
    os.chmod(p,mode)


def private_bytes(path):
    path=absolute(path);info=path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid!=os.geteuid() or info.st_mode&0o077 or info.st_nlink!=1 or info.st_size>65536:
        raise ValueError('private regular operator-owned metadata required')
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
    with os.fdopen(fd,'rb') as stream:
        if not os.path.samestat(info,os.fstat(stream.fileno())):raise ValueError('metadata identity changed')
        data=stream.read(65537)
        after=os.fstat(stream.fileno())
        if len(data)>65536 or not os.path.samestat(info,path.lstat()) or (info.st_size,info.st_mtime_ns,info.st_ctime_ns)!=(after.st_size,after.st_mtime_ns,after.st_ctime_ns):raise ValueError('metadata changed or exceeds limit')
    return data


def private_json(path, expected=None):
    data=private_bytes(path)
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


def fsync_dir(path):
    fd=os.open(path,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:os.fsync(fd)
    finally:os.close(fd)


def exclusive_bytes(path,data,mode=0o600):
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode)
    with os.fdopen(fd,'wb') as stream:
        stream.write(data);stream.flush();os.fchmod(stream.fileno(),mode);os.fsync(stream.fileno())
    fsync_dir(Path(path).parent)


def regular_hash(path):
    path=absolute(path);info=path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_size<=0:raise ValueError('unlinked regular nonempty file required')
    hashed=hashlib.sha256();count=0
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
    with os.fdopen(fd,'rb') as stream:
        if not os.path.samestat(info,os.fstat(stream.fileno())):raise ValueError('source identity changed')
        for block in iter(lambda:stream.read(1024*1024),b''):hashed.update(block);count+=len(block)
        after=os.fstat(stream.fileno())
    if (info.st_size,info.st_mtime_ns,info.st_ctime_ns)!=(after.st_size,after.st_mtime_ns,after.st_ctime_ns) or count!=info.st_size or not os.path.samestat(info,path.lstat()):raise ValueError('source changed while hashing')
    return hashed.hexdigest(),count


def release_binding(release):
    release=absolute(release);manifest=_load_manifest(release/'account-release.json')
    guest=manifest.get('guest_binary_sha256');sha256_hex(guest)
    manifest_sha,size=regular_hash(release/'account-release.json')
    validate_release(str(release),str(HERE/'microvm/manager.py'),guest,expected_manifest_sha256=manifest_sha)
    hashes={};sizes={}
    for name in (*FILES,'account-release.json'):hashes[name],sizes[name]=regular_hash(release/name)
    tree=set()
    for parent,dirs,names in os.walk(release,followlinks=False):
        for name in dirs:
            path=Path(parent)/name
            if path.is_symlink() or path.relative_to(release).as_posix()!='bin':raise ValueError('unexpected Guest directory/link')
        for name in names:tree.add((Path(parent)/name).relative_to(release).as_posix())
    if tree!=set(hashes) or manifest.get('files_sha256')!={name:hashes[name] for name in FILES} or hashes['account-release.json']!=manifest_sha or hashes['manager.py']!=regular_hash(HERE/'microvm/manager.py')[0]:raise ValueError('Guest source binding changed')
    return dict(manifest_sha256=manifest_sha,guest_sha256=guest,manager_sha256=hashes['manager.py'],files_sha256=hashes,logical_bytes=sizes)


def reject_ambient():
    names=[name for name in os.environ if name.startswith(('DOCKER_','COMPOSE_','LD_')) or name.upper() in {'HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','NO_PROXY'} or name in {'PYTHONPATH','PYTHONHOME','BASH_ENV','ENV'}]
    if names:raise ValueError('ambient execution overrides refused: '+','.join(sorted(names)))


def check_engine_files(engine):
    for key in ['docker','compose']:
        item=engine[key];path=absolute(item['path']);info=path.lstat()
        if info.st_uid!=os.geteuid() or info.st_mode&0o022 or not info.st_mode&0o111 or regular_hash(path)[0]!=item['sha256']:raise ValueError('pinned local tool changed')
    s=engine['socket'];info=absolute(s['path']).lstat()
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid!=0 or info.st_mode&0o002 or (info.st_dev,info.st_ino)!=(s['device'],s['inode']):raise ValueError('pinned local Docker socket changed')


def local_engine(reference,root):
    e=sealed_json(reference)
    if not isinstance(e,dict) or set(e)!={'schema','docker','compose','socket','engine_id'} or type(e['schema']) is not int or e['schema']!=1 or not isinstance(e['engine_id'],str) or not re.fullmatch('[A-Za-z0-9:._-]{1,128}',e['engine_id']):raise ValueError('strict local engine contract required')
    for key in ['docker','compose']:
        if not isinstance(e[key],dict) or set(e[key])!={'path','sha256'}:raise ValueError('pinned executable contract required')
        absolute(e[key]['path']);sha256_hex(e[key]['sha256'])
    s=e['socket']
    if not isinstance(s,dict) or set(s)!={'path','device','inode'} or s['path']!='/run/docker.sock' or any(type(s[k]) is not int or s[k]<0 for k in ['device','inode']):raise ValueError('local root Docker socket contract required')
    e=dict(e,config_dir=str(Path(root)/'config/docker-cli'));check_engine_files(e)
    return e


@contextlib.contextmanager
def engine_scope(engine):
    reject_ambient();check_engine_files(engine);token=ENGINE.set(engine)
    try:
        info=json.loads(run(['docker','info','--format','{{json .}}']).stdout)
        if info.get('ID')!=engine['engine_id'] or info.get('OSType')!='linux':raise ValueError('local Docker engine identity differs')
        yield info
    finally:ENGINE.reset(token)


def worker_artifact(reference,image):
    a=sealed_json(reference)
    if not isinstance(a,dict) or set(a)!={'schema','image','entrypoint','labels'} or type(a['schema']) is not int or a['schema']!=1 or a['image']!=image or a['entrypoint']!=WORKER_ENTRYPOINT or not isinstance(a['labels'],dict) or any(not isinstance(k,str) or not isinstance(v,str) for k,v in a['labels'].items()):raise ValueError('strict Worker artifact contract required')
    item=inspect_image(image)
    if item['Config'].get('Entrypoint')!=WORKER_ENTRYPOINT or item['Config'].get('Cmd') not in [None,[]] or (item['Config'].get('Labels') or {})!=a['labels']:raise ValueError('Worker artifact entrypoint/command/labels differ')
    return a


def contracts(p):
    app=artifact_contract(p['app_contract'])
    if app['app_image']!=p['app_image']:raise ValueError('fresh App contract/image differs')
    image=inspect_image(p['app_image'],True)
    if image['Config'].get('Entrypoint')!=['/app/tofi'] or image['Config'].get('Cmd') not in [None,[]]:raise ValueError('App artifact command differs')
    return dict(app=app,worker=worker_artifact(p['worker_contract'],p['worker_image']))


def available_bytes(path):
    info=os.statvfs(path)
    if info.f_frsize<=0 or info.f_bavail<0:raise ValueError('destination capacity unavailable')
    return info.f_bavail*info.f_frsize


def capacity_check(p,path,copy=False):
    root=absolute(p['root'])
    if root.exists() and root.stat().st_dev!=root.parent.stat().st_dev:raise ValueError('destination filesystem changed')
    needed=17*GIB+(sum(p['guest']['logical_bytes'].values()) if copy else 0)
    free=available_bytes(path)
    if free<needed:raise ValueError('destination cannot cover logical release copy and first-account promises/headroom')
    return free


def vacant(p):
    root=absolute(p['root'])
    if root.exists():raise ValueError('fresh root already exists; no adoption')
    parent=root.parent;info=parent.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.geteuid() or info.st_mode&0o022:raise ValueError('existing trusted destination parent required')
    for kind,args in [('container',['ps','-a','-q']),('network',['network','ls','-q']),('volume',['volume','ls','-q'])]:
        if run(['docker',*args,'--filter','label=com.docker.compose.project='+p['project']]).stdout.strip():raise ValueError('occupied Compose project '+kind)
    for path in [p['socket_root'],'/etc/apparmor.d/'+p['profile'],'/etc/tmpfiles.d/'+p['profile']+'.conf']:
        if Path(path).exists() or Path(path).is_symlink():raise ValueError('occupied permission/socket identity')
    profiles=Path('/sys/kernel/security/apparmor/profiles').read_text()
    if any(line.split(' ',1)[0]==p['profile'] for line in profiles.splitlines()):raise ValueError('occupied loaded profile identity')


@contextlib.contextmanager
def claim_lock(create=False):
    # An absent registry has no lock/claim to adopt, including on source-test hosts.
    if not create and not CLAIM_ROOT.exists() and not CLAIM_ROOT.is_symlink():yield None;return
    registry=absolute(CLAIM_ROOT)
    if create and not registry.exists():registry.mkdir(mode=0o700);fsync_dir(registry.parent)
    if not registry.exists():yield None;return
    fd=os.open(registry,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:
        info=os.fstat(fd)
        if info.st_uid!=os.geteuid() or info.st_mode&0o077:raise ValueError('private claim registry required')
        try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError as exc:raise ValueError('installer lifecycle claim is busy') from exc
        yield registry
    finally:os.close(fd)


def claim_records(registry):
    records=[]
    if registry is not None:
        for path in registry.iterdir():
            if path.name.endswith('.pending'):raise ValueError('interrupted claim requires operator inspection')
            record=private_json(path)
            if not isinstance(record,dict) or set(record)!={'schema','kind','plan_sha256','plan','contracts','effect_intents'} or type(record['schema']) is not int or record['schema']!=1 or record['kind']!='d100-install-claim':raise ValueError('unknown durable claim')
            validate_plan(record['plan']);sha256_hex(record['plan_sha256'])
            if not isinstance(record['contracts'],dict) or set(record['contracts'])!={'app','worker'} or record['effect_intents']!=['create-private-root','write-installing-journal'] or record['contracts']['app']!=sealed_json(record['plan']['app_contract']) or record['contracts']['worker']!=sealed_json(record['plan']['worker_contract']):raise ValueError('claim contract/intent binding differs')
            if path.name!=record['plan']['project']+'.json':raise ValueError('claim identity differs')
            records.append((path,record))
    return records


def new_claim(registry,p,seal,full_contracts):
    for _,record in claim_records(registry):
        old=record['plan']
        if any(old[k]==p[k] for k in ['root','project','socket_root','profile']):raise ValueError('durable identity claim already exists; no automatic resume')
    record=dict(schema=1,kind='d100-install-claim',plan_sha256=seal,plan=p,contracts=full_contracts,effect_intents=['create-private-root','write-installing-journal'])
    path=registry/(p['project']+'.json')
    try:exclusive_bytes(path,json_bytes(record))
    except BaseException as error:
        raise RecoveryFailure(dict(reason='initial claim persistence uncertain; no services attempted',original_error=error_record(error),target_error=None,stop_error=None,persistence_error=error_record(error))) from error
    return dict(path=str(path),sha256=digest(path))


def validate_claim(root,p,c,registry):
    records=claim_records(registry);matching=[(path,record) for path,record in records if record['plan']['root']==str(root)]
    identities={'root':str(root),'project':p.get('project'),'socket_root':p.get('socket_root','/run/tofi-'+str(p.get('project'))),'profile':p.get('profile','tofi-worker-'+str(p.get('project')))}
    collisions=[(path,record) for path,record in records if any(record['plan'][key]==value for key,value in identities.items())]
    if 'claim' not in p:
        if collisions or 'authority' in p or 'install_intent' in p:raise ValueError('fresh claim collision/missing; no legacy adoption')
        return  # Cleared legacy migrations retain their existing contract.
    claim=p['claim']
    if not isinstance(claim,dict) or set(claim)!={'reference','root_device','root_inode'} or len(matching)!=1 or len(collisions)!=1:raise ValueError('fresh claim missing or ambiguous')
    path,record=matching[0]
    if claim['reference']!={'path':str(path),'sha256':digest(path)} or record['plan']!=p.get('authority') or record['plan_sha256']!=p.get('plan_sha256'):raise ValueError('durable claim binding differs')
    info=root.lstat()
    if type(claim['root_device']) is not int or type(claim['root_inode']) is not int or (info.st_dev,info.st_ino)!=(claim['root_device'],claim['root_inode']):raise ValueError('claimed root identity changed')
    if p.get('bootstrap_contract_floor')!=BOOTSTRAP_CONTRACT or p.get('worker_contract')!=record['contracts']['worker']:raise ValueError('fresh contract floor/Worker binding differs')
    if not isinstance(p.get('install_intent'),dict) or p.get('phase')=='installed' and p['install_intent'].get('stage')!='completed':raise ValueError('incomplete fresh install intent')
    authority=p['authority'];expected=json.loads(render(authority)['compose.yaml']);expected['services']['app']['image']=selected_app(p,c)
    if c!=expected:raise ValueError('claimed Compose authority differs')
    for name,data in render(authority).items():
        if name=='compose.yaml':continue
        if private_bytes(root/'config'/name)!=data:raise ValueError('claimed config/policy changed')
    if private_bytes(root/'config/docker-cli/config.json')!=b'{}\n':raise ValueError('Docker CLI configuration changed')
    if release_binding(root/'release')!=authority['guest']:raise ValueError('owned Guest release changed')


def selected_app(p,c):
    image=c['services']['app']['image'];allowed={p['app_image']};migration=p.get('migration_intent')
    if isinstance(migration,dict):
        for key in ['target','fallback']:
            contract=migration.get(key)
            if isinstance(contract,dict) and contract.get('bootstrap_contract')==BOOTSTRAP_CONTRACT:allowed.add(contract['app_image'])
    if image not in allowed:raise ValueError('selected App lacks an owned contract')
    return image


@contextlib.contextmanager
def lifecycle(root):
    # Lock the existing directory: invalid preflight creates no lock file.
    with claim_lock():
        root=absolute(root);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
        try:
            info=os.fstat(fd)
            if info.st_uid!=os.geteuid() or info.st_mode&0o077:raise ValueError('private operator-owned install root required')
            try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
            except BlockingIOError as exc:raise ValueError('owned installer lifecycle is busy') from exc
            state=owned_state(root)
            if 'claim' in state[1]:
                with engine_scope(local_engine(state[1]['authority']['engine_contract'],root)):yield state
            else:yield state
        finally:os.close(fd)


def render(p):
    """The sole renderer: no caller-supplied config, mounts, or policy text."""
    root=absolute(p['root']);release=root/'release';project=p['project']
    app=p['app_image'];worker=p['worker_image'];cpu=p['cpu_budget'];memory=p['memory_budget_mib'];port=p['port']
    if not re.fullmatch(r'[a-z][a-z0-9-]{0,31}',project):raise ValueError('invalid unique Compose project name')
    if any(type(x) is not int for x in [cpu,memory,port]) or cpu<1 or memory<2048 or not 1024<=port<=65535:raise ValueError('invalid runtime budget or loopback port')
    image_id(app);image_id(worker)
    state=root/'worker';sockets=Path('/run')/('tofi-'+project);profile='tofi-worker-'+project
    cfg=dict(release_dir=str(release),state_root=str(state/'state'),config_root=str(state/'config'),unit_root=str(state/'unused-units'),socket_root=str(sockets/'accounts'),ledger_root=str(state/'ledger'),broker_socket=str(sockets/'broker.sock'),socket_gid=10001,app_uid=10001,headroom_bytes=GIB,warning_bytes=15*GIB,reserved_slots=[],vcpus=1,memory_mib=1024,runtime_vcpu_budget=cpu,runtime_memory_mib_budget=memory,host_memory_headroom_mib=1024,cgroup_root='/run/tofi-worker/cgroup/tofi-vms',external_reserved_bytes=0,per_account_internal_reserved_bytes=8*GIB,external_disks=[],expected_guest_sha256=p['guest']['guest_sha256'],release_manifest_sha256=p['guest']['manifest_sha256'])
    validate_config(cfg)
    def bind(src,dst=None,ro=False):return dict(type='bind',source=str(src),target=str(dst or src),read_only=ro,bind=dict(create_host_path=False,propagation='rprivate'))
    worker_service=dict(image=worker,user='0:0',restart='unless-stopped',read_only=True,cgroup='private',cpus=cpu,mem_limit=(memory+256)*1024**2,pids_limit=512,cap_drop=['ALL'],cap_add=CAPS,devices=['/dev/kvm:/dev/kvm','/dev/net/tun:/dev/net/tun'],device_cgroup_rules=['c 10:257 m'],security_opt=['no-new-privileges:true','apparmor:'+profile,'seccomp:'+str(root/'config/worker.seccomp.json')],sysctls={'net.ipv4.ip_forward':'1'},tmpfs=['/run:rw,nosuid,nodev,mode=0755,size=64m','/tmp:rw,nosuid,nodev,noexec,mode=1777,size=64m'],volumes=[bind(state),bind(sockets),bind(root/'config/worker.json','/etc/tofi-worker/config.json',True),bind(release,ro=True)])
    env={'TOFI_LISTEN':'0.0.0.0:8321','TOFI_DATA_DIR':'/app/data','TOFI_UI_DIR':'/app/ui','TOFI_OWNER_AUTH':'1','TOFI_OWNER_ALLOW_LAN_HTTP':'1','TOFI_MULTI_ACCOUNT':'1','TOFI_ACCOUNT_MAINTENANCE':'0','TOFI_ACCOUNT_PROVISIONER_SOCKET':str(sockets/'broker.sock'),'TOFI_ACCOUNT_COMPUTER_SOCKET_ROOT':str(sockets/'accounts'),'TOFI_ACCOUNT_COMPUTER_DISK_GIB':'8','TOFI_ACCOUNT_DB_MAX_BYTES':str(GIB)}
    app_service=dict(image=app,user='10001:10001',restart='unless-stopped',init=True,read_only=True,cpus=.5,mem_limit=512*1024**2,cap_drop=['ALL'],security_opt=['no-new-privileges:true'],tmpfs=['/tmp:rw,noexec,nosuid,size=64m'],environment=env,ports=['127.0.0.1:'+str(port)+':8321'],volumes=[bind(root/'data','/app/data'),bind(sockets,ro=True)],healthcheck=dict(test=['CMD','wget','--spider','--quiet','http://127.0.0.1:8321/health'],interval='15s',timeout='3s',retries=3,start_period='15s'))
    binding=authority_digest(p)
    for service,entry in [(app_service,['/app/tofi']),(worker_service,WORKER_ENTRYPOINT)]:
        service.update(entrypoint=entry,command=[],pull_policy='never',runtime='runc',cgroup='private',ipc='private',labels={'io.tofi.install-binding':binding})
    compose=dict(name=project,services=dict(app=app_service,worker=worker_service))
    policy=(HERE/'self-host/worker.apparmor.template').read_text().replace('tofi-account-worker',profile).replace('/var/lib/tofi-worker',str(state))
    rendered={'compose.yaml':json_bytes(compose),'worker.json':json_bytes(cfg),'worker.apparmor':policy.encode(),
            'worker.seccomp.json':(HERE/'self-host/worker.seccomp.json').read_bytes(),
            'tmpfiles.conf':('d '+str(sockets)+' 0750 0 10001 -\n').encode()}
    if 'files_sha256' in p and p['files_sha256']!={name:hashlib.sha256(data).hexdigest() for name,data in rendered.items()}:raise ValueError('reviewed authority/render binding changed')
    return rendered


def json_bytes(value):return (json.dumps(value,indent=2)+'\n').encode()


def authority_digest(p):
    return hashlib.sha256(json.dumps({k:v for k,v in p.items() if k!='files_sha256'},sort_keys=True,separators=(',',':')).encode()).hexdigest()


def generate(root,project,app,worker,release,cpu,memory,port,out,app_contract=None,worker_contract=None,engine_contract=None):
    root=absolute(root);release=absolute(release);out=absolute(out)
    if root.exists():raise ValueError('fresh install root must not exist; no adoption')
    if any(ref is None for ref in [app_contract,worker_contract,engine_contract]):raise ValueError('sealed App, Worker and local engine contracts required')
    guest=release_binding(release)
    p=dict(schema=2,kind='d100-fresh-install',root=str(root),project=project,socket_root='/run/tofi-'+project,profile='tofi-worker-'+project,
           source_release=str(release),release=str(root/'release'),app_image=app,worker_image=worker,cpu_budget=cpu,memory_budget_mib=memory,port=port,
           app_contract=app_contract,worker_contract=worker_contract,engine_contract=engine_contract,guest=guest)
    validate_plan(p)
    rendered=render(p);p['files_sha256']={name:hashlib.sha256(data).hexdigest() for name,data in rendered.items()}
    out.mkdir(mode=0o700)
    for name,data in rendered.items():exclusive_bytes(out/name,data)
    exclusive_bytes(out/'plan.json',json_bytes(p));fsync_dir(out)
    return p


def validate_plan(p):
    fields={'schema','kind','root','project','socket_root','profile','source_release','release','app_image','worker_image','cpu_budget','memory_budget_mib','port','app_contract','worker_contract','engine_contract','guest'}
    if not isinstance(p,dict) or set(p)-{'files_sha256'}!=fields or type(p['schema']) is not int or p['schema']!=2 or p['kind']!='d100-fresh-install':raise ValueError('strict fresh plan metadata required')
    root=absolute(p['root']);absolute(p['source_release'])
    if not isinstance(p['project'],str) or not re.fullmatch('[a-z][a-z0-9-]{0,31}',p['project']):raise ValueError('invalid project')
    if p['release']!=str(root/'release') or p['socket_root']!='/run/tofi-'+p['project'] or p['profile']!='tofi-worker-'+p['project']:raise ValueError('plan identity differs')
    image_id(p['app_image']);image_id(p['worker_image'])
    for name in ['app_contract','worker_contract','engine_contract']:
        ref=p[name]
        if not isinstance(ref,dict) or set(ref)!={'path','sha256'}:raise ValueError('sealed contract reference required')
        absolute(ref['path']);sha256_hex(ref['sha256'])
    g=p['guest'];names=set(FILES)|{'account-release.json'}
    if not isinstance(g,dict) or set(g)!={'manifest_sha256','guest_sha256','manager_sha256','files_sha256','logical_bytes'} or not isinstance(g['files_sha256'],dict) or set(g['files_sha256'])!=names or not isinstance(g['logical_bytes'],dict) or set(g['logical_bytes'])!=names:raise ValueError('Guest binding differs')
    for h in [g['manifest_sha256'],g['guest_sha256'],g['manager_sha256'],*g['files_sha256'].values()]:sha256_hex(h)
    if g['manifest_sha256']!=g['files_sha256']['account-release.json'] or g['manager_sha256']!=g['files_sha256']['manager.py'] or any(type(v) is not int or v<=0 for v in g['logical_bytes'].values()):raise ValueError('Guest sizes/manager/manifest differ')
    if g['logical_bytes']['rootfs.ext4']+g['logical_bytes']['vmlinux']>8*GIB:raise ValueError('internal promise cannot cover immutable jail image copies')


def load_plan(directory,seal=None):
    directory=absolute(directory);p=private_json(directory/'plan.json',seal);validate_plan(p)
    rendered=render(p)
    if not isinstance(p.get('files_sha256'),dict) or set(p['files_sha256'])!=RENDER_NAMES:raise ValueError('exact rendered file set required')
    for name,data in rendered.items():
        if private_bytes(directory/name)!=data:raise ValueError('full rendered binding differs: '+name)
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
    if ENGINE.get() is None:
        with engine_scope(local_engine(p['engine_contract'],root)):return preflight(directory)
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
    vacant(p);free=capacity_check(p,root.parent,copy=True)
    contracts(p)
    cfg=json.loads((Path(directory)/'worker.json').read_text());validate_config(cfg)
    if release_binding(Path(p['source_release']))!=p['guest']:raise ValueError('reviewed source Guest differs')
    run(['apparmor_parser','-Q','-T',str(Path(directory)/'worker.apparmor')])
    run(['docker','compose','--env-file','/dev/null','--project-directory',str(root),'--project-name',p['project'],'-f',str(Path(directory)/'compose.yaml'),'config','--quiet'])
    with socket.socket() as s:s.bind(('127.0.0.1',p['port']))
    return dict(ready=True,first_admin_admissible=True,root=p['root'],permission_installation='not performed',available_bytes=free)


def compose(root,*args):
    if ENGINE.get() is not None:
        c=private_json(root/'compose.yaml')
        return run(['docker','compose','--env-file','/dev/null','--project-directory',str(root),'--project-name',c['name'],'-f',str(root/'compose.yaml'),*args])
    return run(['docker','compose','-f',str(root/'compose.yaml'),*args])


def health(root,state):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*args,**kwargs):raise ValueError('App health redirect refused')
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
    for _ in range(30):
        try:
            with opener.open('http://127.0.0.1:'+str(state['port'])+'/health',timeout=2) as response:
                if response.status!=200:raise ValueError('App health status differs')
                data=response.read(65537)
            if len(data)>65536:raise ValueError('App health response too large')
            d=json.loads(data)
            if isinstance(d,dict) and d.get('ok') is True:return
        except (TimeoutError,socket.timeout):raise
        except urllib.error.URLError as exc:
            if isinstance(exc.reason,(TimeoutError,socket.timeout)):raise
        except (OSError,ValueError):pass
        time.sleep(1)
    raise ValueError('App health failed; files/data retained, inspect the owned project')


def intent(root,state,effect):
    state['install_intent']['effects'].append(effect)
    durable_write(root/'install-state.json',state)


def prepare(root,p):
    rendered=render(p)
    for name in ['config','config/docker-cli','worker','worker/state','worker/config','worker/unused-units','worker/ledger','data']:
        path=root/name;path.mkdir(mode=0o700);fsync_dir(path.parent)
    os.chown(root/'data',10001,10001)
    for name,data in rendered.items():exclusive_bytes(root/'config'/name,data)
    exclusive_bytes(root/'compose.yaml',rendered['compose.yaml'])
    exclusive_bytes(root/'config/docker-cli/config.json',b'{}\n')


def copy_release(root,p):
    src=absolute(p['source_release']);dst=root/'release';dst.mkdir(mode=0o700);(dst/'bin').mkdir(mode=0o700)
    g=p['guest']
    for name in (*FILES,'account-release.json'):
        path=absolute(src/name);info=path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_mode&0o222 or info.st_size!=g['logical_bytes'][name]:raise ValueError('Guest source identity/size/mode differs')
        incoming=os.open(path,os.O_RDONLY|os.O_NOFOLLOW);outgoing=None
        try:
            if not os.path.samestat(info,os.fstat(incoming)):raise ValueError('Guest source identity changed')
            outgoing=os.open(dst/name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
            hashed=hashlib.sha256();size=0
            with os.fdopen(incoming,'rb',closefd=False) as source,os.fdopen(outgoing,'wb',closefd=False) as target:
                for block in iter(lambda:source.read(1024*1024),b''):
                    target.write(block);hashed.update(block);size+=len(block)
                after=os.fstat(incoming)
                if hashed.hexdigest()!=g['files_sha256'][name] or size!=info.st_size or (info.st_size,info.st_mtime_ns,info.st_ctime_ns)!=(after.st_size,after.st_mtime_ns,after.st_ctime_ns) or not os.path.samestat(info,path.lstat()):raise ValueError('Guest changed during exclusive copy')
                target.flush();os.fchmod(outgoing,0o500 if name.startswith('bin/') else 0o400);os.fsync(outgoing)
        finally:
            os.close(incoming)
            if outgoing is not None:os.close(outgoing)
    fsync_dir(dst/'bin');os.chmod(dst/'bin',0o500);fsync_dir(dst/'bin')
    os.chmod(dst,0o500);fsync_dir(dst);fsync_dir(root)
    if release_binding(dst)!=g:raise ValueError('owned Guest validation/binding differs')


def permissions(root,p):
    # Exclusive paths remain retained on failure; acknowledgement is mandatory.
    exclusive_bytes(absolute('/etc/apparmor.d/'+p['profile']),private_bytes(root/'config/worker.apparmor'))
    exclusive_bytes(absolute('/etc/tmpfiles.d/'+p['profile']+'.conf'),private_bytes(root/'config/tmpfiles.conf'))
    sockets=absolute(p['socket_root']);sockets.mkdir(mode=0o750);os.chown(sockets,0,10001);fsync_dir(sockets);fsync_dir(sockets.parent)
    run(['/usr/sbin/apparmor_parser','-a',str(root/'config/worker.apparmor')])


def inspect_service(root,state,service,running=False,identity=None):
    authority=state['authority'];wanted=json.loads(render(authority)['compose.yaml'])['services'][service]
    wanted['image']=state['app_image'] if service=='app' else state['worker_image']
    ids=([identity] if identity is not None else run(['docker','ps','-a','-q','--no-trunc','--filter','label=com.docker.compose.project='+state['project'],'--filter','label=com.docker.compose.service='+service]).stdout.split())
    if len(ids)!=1 or not re.fullmatch('[0-9a-f]{64}',ids[0]):raise ValueError('owned '+service+' container identity is ambiguous')
    records=json.loads(run(['docker','inspect',ids[0]]).stdout)
    if not isinstance(records,list) or len(records)!=1:raise ValueError('owned container inspection incomplete')
    item=records[0];config=item.get('Config',{});host=item.get('HostConfig',{});labels=config.get('Labels') or {}
    image=inspect_image(wanted['image'],service=='app');image_config=image['Config']
    expected_labels=dict(image_config.get('Labels') or {},**wanted['labels'])
    expected_labels.update({'com.docker.compose.project':state['project'],'com.docker.compose.service':service,'com.docker.compose.oneoff':'False',
                           'com.docker.compose.project.working_dir':str(root),'com.docker.compose.project.config_files':str(root/'compose.yaml')})
    if item.get('Id')!=ids[0] or item.get('Image')!=wanted['image'] or config.get('User')!=wanted['user'] or config.get('Entrypoint')!=wanted['entrypoint'] or config.get('Cmd') not in [None,[]] or any(labels.get(k)!=v for k,v in expected_labels.items()):raise ValueError('container artifact/command/labels differ')
    generated={'com.docker.compose.config-hash','com.docker.compose.container-number','com.docker.compose.depends_on','com.docker.compose.image','com.docker.compose.version','com.docker.compose.project.environment_file','com.docker.compose.replace'}
    if set(labels)-set(expected_labels)-generated:raise ValueError('unreviewed container labels')
    for key in set(labels)&generated:
        value=labels[key]
        if not isinstance(value,str):raise ValueError('malformed generated Compose label')
        if key in ['com.docker.compose.config-hash','com.docker.compose.replace'] and not re.fullmatch('[0-9a-f]{64}',value):raise ValueError('malformed Compose identity label')
        if key=='com.docker.compose.container-number' and value!='1' or key=='com.docker.compose.depends_on' and value!='' or key=='com.docker.compose.image' and value!=wanted['image'] or key=='com.docker.compose.project.environment_file' and value!='/dev/null' or key=='com.docker.compose.version' and not re.fullmatch('[A-Za-z0-9._+-]{1,64}',value):raise ValueError('generated Compose label binding differs')
    mounts=item.get('Mounts',[])
    expected={(v['source'],v['target'],not v['read_only'],'rprivate') for v in wanted['volumes']}
    actual={(v.get('Source'),v.get('Destination'),v.get('RW'),v.get('Propagation')) for v in mounts if v.get('Type')=='bind'}
    if len(mounts)!=len(expected) or actual!=expected:raise ValueError('container mounts/authority differ')
    env={}
    for value in image_config.get('Env') or []:
        k,sep,v=value.partition('=')
        if not sep or k in env:raise ValueError('duplicate image environment')
        env[k]=v
    env.update(wanted.get('environment',{}));actual_env={}
    for value in config.get('Env') or []:
        k,sep,v=value.partition('=')
        if not sep or k in actual_env:raise ValueError('duplicate container environment')
        actual_env[k]=v
    if env!=actual_env:raise ValueError('container environment authority differs')
    checks={'ReadonlyRootfs':True,'Privileged':False,'Memory':wanted['mem_limit'],'NanoCpus':int(wanted['cpus']*10**9),'CapDrop':['ALL'],
            'SecurityOpt':wanted['security_opt'],'RestartPolicy':{'Name':'unless-stopped','MaximumRetryCount':0},'NetworkMode':state['project']+'_default',
            'Runtime':'runc','CgroupnsMode':'private','UsernsMode':'','UTSMode':'','PublishAllPorts':False,'AutoRemove':False,'OomKillDisable':False,'CgroupParent':''}
    if service=='worker':checks.update(PidsLimit=512,CgroupnsMode='private',CapAdd=CAPS,DeviceCgroupRules=['c 10:257 m'],Sysctls={'net.ipv4.ip_forward':'1'})
    else:checks.update(Init=True,CapAdd=None,Devices=[],DeviceCgroupRules=None,Sysctls=None)
    for key,value in checks.items():
        actual_value=host.get(key)
        if key=='CapAdd' and isinstance(actual_value,list):actual_value=[v.removeprefix('CAP_') for v in actual_value]
        if key=='OomKillDisable' and actual_value is None:actual_value=False
        if key in ['CapAdd','CapDrop','SecurityOpt','DeviceCgroupRules'] and isinstance(value,list) and isinstance(actual_value,list):
            if sorted(actual_value)!=sorted(value):raise ValueError('container authority config differs: '+key)
        elif actual_value!=value:raise ValueError('container authority config differs: '+key)
    if service=='worker' and host.get('Devices')!=[{'PathOnHost':v.split(':')[0],'PathInContainer':v.split(':')[1],'CgroupPermissions':'rwm'} for v in wanted['devices']]:raise ValueError('Worker device authority differs')
    tmpfs={entry.split(':',1)[0]:entry.split(':',1)[1] for entry in wanted['tmpfs']}
    if host.get('Tmpfs')!=tmpfs or host.get('Binds') or host.get('VolumesFrom') or host.get('PidMode') or host.get('IpcMode')!='private' or host.get('Links') or host.get('ExtraHosts') or host.get('DeviceRequests') not in [None,[]] or host.get('StorageOpt'):raise ValueError('unexpected container authority')
    ports={'8321/tcp':[{'HostIp':'127.0.0.1','HostPort':str(state['port'])}]} if service=='app' else {}
    if (host.get('PortBindings') or {})!=ports or set(item.get('NetworkSettings',{}).get('Networks',{}))!={state['project']+'_default'}:raise ValueError('container network/port authority differs')
    if running and (item.get('State',{}).get('Running') is not True or type(item['State'].get('Pid')) is not int or item['State']['Pid']<=0):raise ValueError('owned service not running')
    return item


def worker_capacity(identity,p):
    # Exact owned Worker, UID accepted by SO_PEERCRED; op has no account fields.
    script="import http.client,json,socket,sys\ns=socket.socket(socket.AF_UNIX);s.settimeout(10);s.connect(sys.argv[1]);s.sendall(b'POST /v1/accounts HTTP/1.0\\r\\nContent-Length: 17\\r\\nContent-Type: application/json\\r\\n\\r\\n{\"op\":\"capacity\"}');r=http.client.HTTPResponse(s);r.begin();d=r.read(65537);assert r.status==200 and len(d)<=65536;print(d.decode())"
    for attempt in range(30):
        try:
            result=json.loads(run(['docker','exec','--user','10001:10001',identity,'python3','-c',script,p['socket_root']+'/broker.sock']).stdout)
            if not isinstance(result,dict):raise ValueError('Worker capacity response differs')
            return result
        except subprocess.CalledProcessError:
            if attempt==29:raise
            time.sleep(1)


def proven_stop(root,state):
    # Validate every candidate before stopping any; foreign/ambiguous IDs stay untouched.
    ids=run(['docker','ps','-a','-q','--no-trunc','--filter','label=com.docker.compose.project='+state['project']]).stdout.split()
    if any(not re.fullmatch('[0-9a-f]{64}',value) for value in ids):raise ValueError('full owned stop identities required')
    records=json.loads(run(['docker','inspect',*ids]).stdout) if ids else []
    if not isinstance(records,list) or len(records)!=len(ids):raise ValueError('owned stop inventory incomplete')
    verified=[];seen=set()
    for record in records:
        service=(record.get('Config',{}).get('Labels') or {}).get('com.docker.compose.service')
        if service not in ['app','worker'] or service in seen:raise ValueError('stop ownership unproven')
        seen.add(service);item=inspect_service(root,state,service,identity=record.get('Id'));verified.append(item['Id'])
    if set(verified)!=set(ids):raise ValueError('stop ownership IDs differ')
    if verified:
        run(['docker','stop','--time','180',*verified])
        after=json.loads(run(['docker','inspect',*verified]).stdout)
        if len(after)!=len(verified) or {v.get('Id') for v in after}!=set(verified):raise ValueError('stop identity proof incomplete')
        for item in after:
            service=item['Config']['Labels']['com.docker.compose.service'];inspect_service(root,state,service,identity=item['Id'])
            s=item.get('State',{})
            if s.get('Running') is not False or s.get('Pid')!=0 or s.get('ExitCode')!=0:raise ValueError('unclean owned stop; no further starts')
    return verified


def fresh_failure(root,state,original,startup):
    stop_error=None
    if startup:
        try:proven_stop(root,state)
        except BaseException as error:stop_error=error
    details=dict(reason='fresh installation failed; owned files/claim retained',original_error=error_record(original),target_error=None,stop_error=error_record(stop_error) if stop_error else None,persistence_error=None)
    state['phase']='STOP_UNPROVEN' if stop_error else 'stopped-retained' if startup else 'preparation-failed-retained'
    state['install_intent']['stage']='failed';state['recovery_failure']=details
    try:durable_write(root/'install-state.json',state)
    except BaseException as error:details['persistence_error']=error_record(error)
    if stop_error or details['persistence_error']:raise RecoveryFailure(details) from original


def apply(directory,accepted,plan_sha256=None):
    if not accepted:raise ValueError('review permission files, then explicitly use --accept-permissions')
    if plan_sha256 is None:raise ValueError('explicit reviewed --plan-sha256 required')
    sha256_hex(plan_sha256);reject_ambient();p=load_plan(directory,plan_sha256);root=absolute(p['root'])
    engine=local_engine(p['engine_contract'],root)
    with engine_scope(engine):
        preflight(directory);full=contracts(p)
        if load_plan(directory,plan_sha256)!=p:raise ValueError('sealed plan changed during preflight')
        if release_binding(Path(p['source_release']))!=p['guest']:raise ValueError('sealed source Guest changed during preflight')
        with claim_lock(create=True) as registry:
            # Recheck under the shared lock; no check-then-adopt window.
            vacant(p);capacity_check(p,root.parent,copy=True)
            reference=new_claim(registry,p,plan_sha256,full)
            state=dict(root=p['root'],project=p['project'],port=p['port'],release=p['release'],app_image=p['app_image'],worker_image=p['worker_image'],
                       authority=p,plan_sha256=plan_sha256,bootstrap_contract_floor=BOOTSTRAP_CONTRACT,app_contract=full['app'],worker_contract=full['worker'],
                       phase='installing',install_intent={'stage':'preparing','effects':[]},claim={'reference':reference})
            startup=False
            try:
                root.mkdir(mode=0o700);fsync_dir(root.parent);info=root.lstat()
                state['claim'].update(root_device=info.st_dev,root_inode=info.st_ino)
                durable_write(root/'install-state.json',state)
                intent(root,state,'prepare-directories-and-config')
                prepare(root,p)
                intent(root,state,'copy-owned-Guest')
                capacity_check(p,root,copy=True);copy_release(root,p);capacity_check(p,root)
                intent(root,state,'install-owned-permissions')
                permissions(root,p)
                intent(root,state,'start-worker');startup=True
                compose(root,'up','-d','--no-build','--pull','never','worker')
                worker=inspect_service(root,state,'worker',running=True)
                intent(root,state,'read-only-capacity')
                admission=worker_capacity(worker['Id'],p)
                if type(admission.get('admission_remaining_bytes')) is not int or admission['admission_remaining_bytes']<16*GIB or admission.get('per_account_internal_reserved_bytes')!=8*GIB or admission.get('accounts')!=[]:raise ValueError('first-account Worker admission unavailable')
                intent(root,state,'start-app')
                compose(root,'up','-d','--no-build','--pull','never','app')
                inspect_service(root,state,'worker',running=True);inspect_service(root,state,'app',running=True);health(root,state)
                capacity_check(p,root)
                state['install_intent']['stage']='completed';state['phase']='installed'
                durable_write(root/'install-state.json',state)
            except BaseException as original:
                fresh_failure(root,state,original,startup)
                raise
    return dict(installed=True,root=str(root),bootstrap_contract_floor=BOOTSTRAP_CONTRACT,data_retained=True,next='Open the matching UI; installer has not read the bootstrap secret or created an account.')


def owned_state(root):
    root=absolute(root)
    if os.geteuid()!=0:raise ValueError('host operator required')
    for name in ['install-state.json','compose.yaml']:
        if (root/(name+'.pending')).exists() or (root/(name+'.pending')).is_symlink():raise ValueError('interrupted metadata write requires operator review')
    p=private_json(root/'install-state.json')
    if p['root']!=str(root):raise ValueError('ownership root differs')
    c=private_json(root/'compose.yaml')
    if c['name']!=p['project']:raise ValueError('Compose ownership differs')
    validate_claim(root,p,c,CLAIM_ROOT if CLAIM_ROOT.exists() else None)
    return root,p,c


def stopped(root):
    state=private_json(root/'install-state.json') if (root/'install-state.json').exists() else {}
    if 'claim' in state:
        root,p,c=owned_state(root);p=dict(p,app_image=selected_app(p,c))
        return proven_stop(root,p)
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
    if not isinstance(artifact,dict) or set(artifact)!=fields or type(artifact['schema']) is not int or artifact['schema']!=1 or artifact['bootstrap_contract']!=BOOTSTRAP_CONTRACT:raise ValueError('unknown App bootstrap contract')
    image_id(artifact['app_image']);sha256_hex(artifact['ui_sha256'])
    if not re.fullmatch('[0-9a-f]{40}',str(artifact['source_commit'])) or artifact['ui_source_commit']!=artifact['source_commit']:raise ValueError('matching App/UI source commits required')
    evidence=artifact['evidence']
    if not isinstance(evidence,dict) or set(evidence)!={'path','sha256'}:raise ValueError('sealed compatibility evidence required')
    record=sealed_json(evidence)
    binding={key:artifact[key] for key in fields-{'schema','evidence'}}
    if not isinstance(record,dict) or set(record)!={'schema','kind','bindings','checks'} or type(record['schema']) is not int or record['schema']!=1 or record['kind']!='d100-app-validation' or record['bindings']!=binding:
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
    if 'claim' in p:
        selected=dict(p,app_image=image)
        try:
            compose(root,'up','-d','--no-build','--pull','never','worker');compose(root,'up','-d','--no-build','--pull','never','app')
        except subprocess.CalledProcessError as exc:raise AppStartupFailure('planned App startup failed') from exc
        inspect_service(root,selected,'worker',running=True);inspect_service(root,selected,'app',running=True)
        try:health(root,p)
        except ValueError as exc:raise AppStartupFailure('planned App health failed') from exc
        return
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
        if 'claim' in p:
            if p.get('phase')!='installed' or p['install_intent'].get('stage')!='completed':raise ValueError('incomplete lifecycle requires operator recovery; no ordinary uninstall')
            selected=dict(p,app_image=selected_app(p,c));ids=proven_stop(root,selected)
            if ids:run(['docker','rm',*ids])
        else:
            stopped(root)
            compose(root,'rm','-f','app','worker')
        p['phase']='stopped-retained';durable_write(root/'install-state.json',p)
    return dict(uninstalled=True,data_retained=True,root=str(root),permissions_retained=True,next='All DBs/disks/credentials/config/release/profile/socket paths retained; no down -v, rm -rf or automatic purge. Resume using this owned Compose project after review.')


def main():
    parser=argparse.ArgumentParser(description=__doc__);sub=parser.add_subparsers(dest='action',required=True)
    plan=sub.add_parser('plan')
    for name in ['root','project','app-image','worker-image','release','out','app-contract','app-contract-sha256','worker-contract','worker-contract-sha256','engine-contract','engine-contract-sha256']:plan.add_argument('--'+name,required=True)
    plan.add_argument('--cpu-budget',type=int,required=True);plan.add_argument('--memory-budget-mib',type=int,required=True);plan.add_argument('--port',type=int,default=8333)
    for action in ['preflight','apply']:
        q=sub.add_parser(action);q.add_argument('plan');
        if action=='apply':q.add_argument('--accept-permissions',action='store_true');q.add_argument('--plan-sha256',required=True)
    q=sub.add_parser('upgrade');q.add_argument('root');q.add_argument('--app-image',required=True);q.add_argument('--worker-image',required=True);q.add_argument('--upgrade-plan',required=True);q.add_argument('--upgrade-plan-sha256',required=True)
    q=sub.add_parser('uninstall');q.add_argument('root')
    a=parser.parse_args()
    try:
        if a.action=='plan':result=generate(a.root,a.project,a.app_image,a.worker_image,a.release,a.cpu_budget,a.memory_budget_mib,a.port,a.out,
                                          {'path':a.app_contract,'sha256':a.app_contract_sha256},{'path':a.worker_contract,'sha256':a.worker_contract_sha256},{'path':a.engine_contract,'sha256':a.engine_contract_sha256})
        elif a.action=='preflight':result=preflight(a.plan)
        elif a.action=='apply':result=apply(a.plan,a.accept_permissions,a.plan_sha256)
        elif a.action=='upgrade':result=upgrade(a.root,a.app_image,a.worker_image,a.upgrade_plan,a.upgrade_plan_sha256)
        else:result=uninstall(a.root)
        print(json.dumps(result,indent=2))
    except (ValueError,OSError,subprocess.SubprocessError) as e:
        print(str(e),file=sys.stderr);return 1
    return 0

if __name__=='__main__':sys.exit(main())
