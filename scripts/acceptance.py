#!/usr/bin/env python3
"""Run HTTP acceptance against a fresh, disposable native instance."""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

REPO = Path(__file__).resolve().parents[1]

class AcceptanceClient:
    def __init__(self, base, instance_id):
        self.base = base
        self.client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.instance_id = instance_id

    def verify(self):
        with self.client.open(self.base + '/health', timeout=5) as response:
            identity = json.load(response)
        if identity.get('environment') != 'acceptance' or identity.get('instance_id') != self.instance_id:
            raise RuntimeError('Refusing to test a personal or different instance')

    def call(self, method, path, body=None):
        # Recheck before each mutation so a restarted personal service cannot
        # inherit a test client's port and receive its remaining test requests.
        if method != 'GET':
            self.verify()
        req = urllib.request.Request(self.base + path, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Content-Type': 'application/json', 'Origin': self.base})
        with self.client.open(req, timeout=10) as response:
            return json.load(response)


def smoke(client):
    client.verify()
    a = client.call('POST', '/api/bots', {'name':'Acceptance A', 'instructions':'Test fixture'})
    b = client.call('POST', '/api/bots', {'name':'Acceptance B', 'instructions':'Test fixture'})
    group = client.call('POST', '/api/groups', {'name':'Acceptance group','bot_ids':[a['id'],b['id']]})
    assert len(group['bot_ids']) == 2
    memory = client.call('POST', f"/api/conversations/{a['dm_conversation_id']}/memories", {'content':'isolated fixture'})
    found = client.call('GET', f"/api/conversations/{a['dm_conversation_id']}/memories")
    assert any(m['id'] == memory['id'] for m in found['memories'])
    return a, group, memory


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, default=REPO / 'build/tofi-acceptance')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='tofi-acceptance-') as root:
        root = Path(root)
        data = root / 'data'
        with socket.socket() as sock:
            sock.bind(('127.0.0.1',0))
            port = sock.getsockname()[1]
        env = {k:v for k,v in os.environ.items() if not k.startswith('TOFI_')}
        env.update(TOFI_ENVIRONMENT='acceptance', TOFI_DATA_DIR=str(data),
                   TOFI_LISTEN=f'127.0.0.1:{port}', TOFI_UI_DIR=str(REPO/'ui/dist'))
        base = f'http://127.0.0.1:{port}'
        def start():
            process = subprocess.Popen([str(args.binary.resolve())], env=env, cwd=REPO,
                                       stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            for _ in range(100):
                if process.poll() is not None:
                    raise RuntimeError('Acceptance server exited: '+process.stderr.read().decode()[-1000:])
                try:
                    with opener.open(base+'/health',timeout=1) as response:
                        health = json.load(response)
                    identity = json.loads((data/'instance.json').read_text())
                    if health.get('instance_id') == identity['id']:
                        return process, AcceptanceClient(base,identity['id'])
                except (OSError, ValueError):
                    pass
                time.sleep(.1)
            process.terminate()
            process.wait(timeout=10)
            raise RuntimeError('Acceptance server did not start')
        def stop(process):
            process.terminate()
            try: process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        process, client = start()
        try:
            a, group, memory = smoke(client)
        finally:
            stop(process)
        process, reopened = start()
        try:
            assert reopened.instance_id == client.instance_id
            bots = reopened.call('GET','/api/bots')['bots']
            assert any(b['id']==a['id'] and b['dm_conversation_id']==a['dm_conversation_id'] for b in bots)
            assert any(c['id']==group['id'] for c in reopened.call('GET','/api/conversations')['conversations'])
            assert any(m['id']==memory['id'] for m in reopened.call('GET',f"/api/conversations/{a['dm_conversation_id']}/memories")['memories'])
        finally:
            stop(process)
    print('Isolated acceptance passed; disposable data removed, personal data untouched.')

if __name__ == '__main__':
    main()
