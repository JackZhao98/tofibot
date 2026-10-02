#!/usr/bin/env python3
"""Offline, paired image/config/workspace checkpoints for one fixed computer."""
import argparse
import fcntl
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import time


def run(*args):
    subprocess.run(args, check=True)


class Client(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(3)
        self.sock.connect(self.host)


def wait_ready(c):
    deadline = time.monotonic() + 100
    while time.monotonic() < deadline:
        conn = Client(c['socket_dir'] + '/control.sock', timeout=3)
        try:
            conn.request('GET', '/v1/info')
            info = json.loads(conn.getresponse().read())
            if info['state'] == 'ready':
                return
            if info['state'] == 'error':
                raise RuntimeError(info.get('error', 'Computer preparation failed'))
        except (OSError, http.client.HTTPException):
            pass
        finally:
            conn.close()
        time.sleep(.5)
    raise RuntimeError('Computer readiness timed out')


def verify_release(release):
    p = Path(release).resolve()
    if p.parent != Path('/opt/tofi-computer/releases') or not (p / 'SHA256SUMS').is_file():
        raise ValueError('Expected an immutable computer release')
    subprocess.run(['sha256sum', '-c', 'SHA256SUMS'], cwd=p, check=True)
    return str(p)


def copy_disk(source, target):
    run('cp', '--reflink=auto', '--sparse=always', '--preserve=mode,ownership', str(source), str(target))


RESOURCE_FILES = ('resources-desired.json', 'resources-applied.json')
DESIRED_RESOURCE = 'resources-desired.json'
HELD_DESIRED_RESOURCE = 'resources-desired.ops-hold.json'


def copy_resource_settings(source, target, restore=False):
    for name in RESOURCE_FILES:
        origin, destination = source / name, target / name
        if origin.exists():
            shutil.copy2(origin, destination)
        elif restore:
            destination.unlink(missing_ok=True)


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def restore_held_desired(state):
    """Recover an exact pending request left by an interrupted ops process."""
    held = state / HELD_DESIRED_RESOURCE
    if held.exists():
        os.replace(held, state / DESIRED_RESOURCE)
        sync_directory(state)


def start_with_checkpoint_allocation(state, saved_desired, start):
    """Boot restored allocation without applying a newer pending request."""
    desired = state / DESIRED_RESOURCE
    held = state / HELD_DESIRED_RESOURCE
    if held.exists():
        raise RuntimeError('A held resource request must be recovered before restore')
    if saved_desired is not None and saved_desired.exists():
        temporary = held.with_suffix('.tmp')
        shutil.copy2(saved_desired, temporary)
        with temporary.open('rb') as stream:
            os.fsync(stream.fileno())
        os.replace(temporary, held)
        sync_directory(state)
    desired.unlink(missing_ok=True)
    sync_directory(state)
    try:
        return start()
    finally:
        if held.exists():
            os.replace(held, desired)
            sync_directory(state)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('id')
    parser.add_argument('action', choices=['backup', 'upgrade', 'restore'])
    parser.add_argument('target', nargs='?')
    args = parser.parse_args()
    if os.geteuid() != 0 or not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,39}', args.id):
        raise SystemExit('Run as root with a fixed computer ID')
    os.umask(0o077)
    config = Path('/etc/tofi-computer') / (args.id + '.json')
    c = json.loads(config.read_text())
    service_name = 'tofi-computer-' + args.id
    service = Path('/etc/systemd/system') / (service_name + '.service')
    state = Path(c['state_dir'])
    checkpoints = state / 'checkpoints'
    checkpoints.mkdir(mode=0o700, exist_ok=True)
    lease = (state / 'ops.lock').open('a')
    fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
    # A SIGKILL or host crash can interrupt the finally below. The fixed,
    # root-only holding name makes the user's exact request recoverable on the
    # next serialized ops invocation.
    restore_held_desired(state)
    target_config = None
    target_service = None
    restore_disk = None
    if args.action == 'upgrade':
        release = verify_release(args.target or '')
        target_config = dict(c, image_dir=release, bin_dir=release + '/bin')
        target_service = re.sub(r'^ExecStart=.*$', 'ExecStart=/usr/bin/python3 ' + release + '/manager.py ' + str(config), service.read_text(), flags=re.M)
        target_service = re.sub(r'^TimeoutStopSec=.*$', 'TimeoutStopSec=60', target_service, flags=re.M)
    elif args.action == 'restore':
        checkpoint = Path(args.target or '').resolve()
        if (checkpoint.parent != checkpoints or
                not (checkpoint / 'complete').is_file() or
                not (checkpoint / 'config.json').is_file() or
                not (checkpoint / 'service').is_file() or
                not (checkpoint / 'workspace.ext4').is_file()):
            raise SystemExit('Expected a checkpoint belonging to this computer')
        target_config = json.loads((checkpoint / 'config.json').read_text())
        if any(target_config[k] != c[k] for k in ('id', 'slot', 'state_dir', 'socket_dir')):
            raise SystemExit('Checkpoint identity mismatch')
        verify_release(target_config['image_dir'])
        target_service = (checkpoint / 'service').read_text()
        restore_disk = checkpoint / 'workspace.ext4'
    stamp = time.strftime('%Y%m%dT%H%M%SZ', time.gmtime()) + '-' + str(time.time_ns() % 1000000)
    safety = checkpoints / stamp
    safety.mkdir(mode=0o700)
    run('systemctl', 'stop', service_name)
    changed = False
    try:
        shutil.copy2(config, safety / 'config.json')
        shutil.copy2(service, safety / 'service')
        copy_resource_settings(state, safety)
        copy_disk(state / 'workspace.ext4', safety / 'workspace.ext4')
        (safety / 'complete').write_text('Offline paired checkpoint\n')
        if target_config is not None:
            changed = True
            if restore_disk is not None:
                copy_disk(restore_disk, state / 'workspace.restore.new')
                (state / 'workspace.restore.new').replace(state / 'workspace.ext4')
                copy_resource_settings(checkpoint, state, restore=True)
            config.write_text(json.dumps(target_config, indent=2) + '\n')
            service.write_text(target_service)
            run('systemctl', 'daemon-reload')
        def start_target():
            run('systemctl', 'start', service_name)
            wait_ready(target_config or c)
        if restore_disk is not None:
            start_with_checkpoint_allocation(state, safety / DESIRED_RESOURCE, start_target)
        else:
            start_target()
    except BaseException:
        if changed:
            run('systemctl', 'stop', service_name)
            # Keep the rejected state for diagnosis, then recover its paired
            # predecessor, including Chrome profiles if an upgrade touched them.
            copy_disk(state / 'workspace.ext4', safety / 'rejected-workspace.ext4')
            copy_disk(safety / 'workspace.ext4', state / 'workspace.recover.new')
            (state / 'workspace.recover.new').replace(state / 'workspace.ext4')
            copy_resource_settings(safety, state, restore=True)
            shutil.copy2(safety / 'config.json', config)
            shutil.copy2(safety / 'service', service)
            run('systemctl', 'daemon-reload')
        def start_rollback():
            run('systemctl', 'start', service_name)
            wait_ready(c)
        start_with_checkpoint_allocation(state, safety / DESIRED_RESOURCE, start_rollback)
        raise
    print(json.dumps({'action': args.action, 'id': args.id, 'state': 'ready', 'checkpoint': str(safety)}))


if __name__ == '__main__':
    main()
