#!/usr/bin/env python3
"""Plan/install a fresh dedicated Linux x86_64 Compose account deployment.

Images and sealed Guest release are operator-provided; nothing is published or
pulled. Plan writes only a new review directory. Apply needs explicit permission.
Upgrade preserves current data; uninstall stops services and retains all files.
"""
import argparse
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
import urllib.request

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / 'microvm'))
from account_release_check import validate_release, _load_manifest, _no_symlink_path
from worker_entrypoint import validate_config

CAPS = ['SYS_ADMIN','NET_ADMIN','MKNOD','SYS_CHROOT','SETUID','SETGID','CHOWN','DAC_OVERRIDE','FOWNER']
GIB = 1024**3


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
        except (OSError,ValueError):pass
        time.sleep(1)
    raise ValueError('App health failed; files/data retained, inspect the owned project')


def apply(directory,accepted):
    if not accepted:raise ValueError('review permission files, then explicitly use --accept-permissions')
    result=preflight(directory);p=load_plan(directory);root=absolute(p['root']);directory=absolute(directory)
    policy=Path('/etc/apparmor.d')/p['profile'];tmp=Path('/etc/tmpfiles.d')/(p['profile']+'.conf')
    if policy.exists() or tmp.exists() or Path(p['socket_root']).exists():raise ValueError('owned profile/socket identity already exists; investigate, never overwrite')
    root.mkdir(mode=0o700,parents=False);(root/'data').mkdir(mode=0o700);os.chown(root/'data',10001,10001);(root/'worker').mkdir(mode=0o700);(root/'config').mkdir(mode=0o700)
    for src,target in [('compose.yaml',root/'compose.yaml'),('worker.json',root/'config/worker.json'),('worker.seccomp.json',root/'config/worker.seccomp.json'),('worker.apparmor',policy),('tmpfiles.conf',tmp)]:
        shutil.copyfile(directory/src,target);os.chmod(target,0o644 if target in [policy,tmp] else 0o600)
    write(root/'install-state.json',dict(p,phase='prepared'))
    run(['systemd-tmpfiles','--create',str(tmp)]);run(['apparmor_parser','-r',str(policy)])
    compose(root,'up','-d','--no-build','worker');compose(root,'up','-d','--no-build','app');health(root,p)
    write(root/'install-state.json',dict(p,phase='installed'))
    return dict(installed=True,url='http://127.0.0.1:'+str(p['port']),next='Open via localhost/SSH/TLS and create the first Admin; later accounts are Admin-only.',data_retained=True)


def owned_state(root):
    root=absolute(root)
    if os.geteuid()!=0:raise ValueError('host operator required')
    state=root/'install-state.json';s=state.stat()
    if s.st_uid!=0 or s.st_mode&0o077:raise ValueError('root-owned private install state required')
    p=json.loads(state.read_text())
    if p['root']!=str(root):raise ValueError('ownership root differs')
    c=json.loads((root/'compose.yaml').read_text())
    if c['name']!=p['project']:raise ValueError('Compose ownership differs')
    return root,p,c


def stopped(root):
    compose(root,'stop','--timeout','180')
    ids=compose(root,'ps','-a','-q').stdout.split()
    if ids:
        for c in json.loads(run(['docker','inspect',*ids]).stdout):
            if c['State']['Running'] or c['State']['Pid'] or c['State']['ExitCode']:raise ValueError('unclean stop; ownership/budgets retained, no cleanup/restart')


def upgrade(root,app,worker):
    root,p,c=owned_state(root);inspect_image(app,True);inspect_image(worker)
    inspect_image(c['services']['app']['image'],True)
    old=json.loads(json.dumps(c));stopped(root)
    c['services']['app']['image']=app;c['services']['worker']['image']=worker
    write(root/'compose.yaml',c)
    try:
        compose(root,'up','-d','--no-build','worker');compose(root,'up','-d','--no-build','app');health(root,p)
    except Exception:
        stopped(root);write(root/'compose.yaml',old)
        compose(root,'up','-d','--no-build','worker');compose(root,'up','-d','--no-build','app');health(root,p)
        raise ValueError('upgrade rejected; compatible previous code restored with CURRENT data')
    p.update(app_image=app,worker_image=worker,phase='installed');write(root/'install-state.json',p)
    return dict(upgraded=True,data_retained=True,guest_release_unchanged=True)


def uninstall(root):
    root,p,c=owned_state(root);stopped(root)
    compose(root,'rm','-f','app','worker')
    p['phase']='stopped-retained';write(root/'install-state.json',p)
    return dict(uninstalled=True,data_retained=True,root=str(root),permissions_retained=True,next='All DBs/disks/credentials/config/release/profile/socket paths retained; no down -v, rm -rf or automatic purge. Resume using this owned Compose project after review.')


def main():
    parser=argparse.ArgumentParser(description=__doc__);sub=parser.add_subparsers(dest='action',required=True)
    plan=sub.add_parser('plan')
    for name in ['root','project','app-image','worker-image','release','out']:plan.add_argument('--'+name,required=True)
    plan.add_argument('--cpu-budget',type=int,required=True);plan.add_argument('--memory-budget-mib',type=int,required=True);plan.add_argument('--port',type=int,default=8333)
    for action in ['preflight','apply']:
        q=sub.add_parser(action);q.add_argument('plan');
        if action=='apply':q.add_argument('--accept-permissions',action='store_true')
    q=sub.add_parser('upgrade');q.add_argument('root');q.add_argument('--app-image',required=True);q.add_argument('--worker-image',required=True)
    q=sub.add_parser('uninstall');q.add_argument('root')
    a=parser.parse_args()
    try:
        if a.action=='plan':result=generate(a.root,a.project,a.app_image,a.worker_image,a.release,a.cpu_budget,a.memory_budget_mib,a.port,a.out)
        elif a.action=='preflight':result=preflight(a.plan)
        elif a.action=='apply':result=apply(a.plan,a.accept_permissions)
        elif a.action=='upgrade':result=upgrade(a.root,a.app_image,a.worker_image)
        else:result=uninstall(a.root)
        print(json.dumps(result,indent=2))
    except (ValueError,OSError,subprocess.SubprocessError) as e:
        print(str(e),file=sys.stderr);return 1
    return 0

if __name__=='__main__':sys.exit(main())
