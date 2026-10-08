"""Explicit, synthetic API acceptance for a disposable self-host installation.

Run only on a disposable acceptance host after `install.sh` (as root, so the
one-time setup key in /var/lib/tofi/data/owner-bootstrap.secret is readable).
This does not install policies, purge data, or create a second account over
capacity.
The retained state contains generated fixture passwords; never export it.
"""
import argparse
import base64
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.request


def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()


def save(path, value):
    temporary = path.with_name(path.name + '.tmp')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, sort_keys=True)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


class Client:
    def __init__(self, url, session_path):
        self.url = url.rstrip('/')
        self.jar = http.cookiejar.LWPCookieJar(str(session_path))
        if session_path.exists():
            assert not session_path.is_symlink() and not session_path.stat().st_mode & 0o077
            self.jar.load(ignore_discard=True)
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(self.jar))

    def request(self, route, body=None, method=None, expected=200):
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(self.url + route, data=data, method=method,
                                        headers={'Content-Type': 'application/json', 'Origin': self.url})
        try:
            with self.opener.open(request, timeout=45) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
        self.jar.save(ignore_discard=True)
        if status not in (expected if isinstance(expected, tuple) else (expected,)):
            # Do not include response bodies: fixture credentials stay on-host.
            raise RuntimeError('Unexpected HTTP %d for %s' % (status, route))
        return json.loads(raw) if raw else None

    def login(self, state):
        session = self.request('/api/auth/session')
        if session.get('authenticated') and session['owner']['id'] == state['account_id']:
            return session
        self.request('/api/auth/login', {'identifier': state['username'], 'password': state['password']})
        session = self.request('/api/auth/session')
        assert session['authenticated'] and session['owner']['id'] == state['account_id']
        return session

    def ready(self):
        for _ in range(120):
            info = self.request('/api/computers/firecracker/info', expected=(200, 503))
            if info.get('state') == 'ready':
                assert info['vcpus'] == 1 and info['memory_mib'] == 1024
                return info
            time.sleep(1)
        raise RuntimeError('Guest not ready within 120 seconds')

    def action(self, state, name, args, expected=200):
        reply = self.request('/api/computers/firecracker/actions',
                             {'bot_id': state['bot_id'], 'action': name, 'args': args}, expected=expected)
        if expected == 200:
            assert reply['ok']
            return reply['result']
        return reply


def payload(state):
    return (state['marker'] + '\nUnicode: 雪 / café\n' + '0123456789abcdef' * 4096 + '\nAPPENDED\n')


def verify(client, state):
    info = client.ready()
    marker = client.action(state, 'files.read', {'path': 'installer-marker.txt'})
    assert marker['content'] == state['marker'] and marker['sha256'] == digest(state['marker'])
    text = payload(state)
    result = client.action(state, 'files.read', {'path': state['integrity_path']})
    assert result['sha256'] == digest(text) and result['content'] == text.rstrip('\n')
    listing = client.action(state, 'files.list', {'path': state['integrity_path'].rsplit('/', 1)[0]})
    assert any(row['name'] == 'integrity.txt' and row['size'] == len(text.encode()) for row in listing)
    chunks, offset, version = [], 0, ''
    while True:
        chunk = client.action(state, 'files.export_chunk',
                              {'path': state['integrity_path'], 'offset': offset, 'limit': 4096, 'version': version})
        chunks.append(base64.b64decode(chunk['data_base64'], validate=True))
        assert chunk['next_offset'] > offset and chunk['size'] == len(text.encode())
        offset, version = chunk['next_offset'], chunk['version']
        if chunk['eof']:
            break
    assert b''.join(chunks) == text.encode()
    shell = client.action(state, 'shell.exec', {'command': 'printf synthetic-tool-ok'})
    assert 'synthetic-tool-ok' in json.dumps(shell)
    return {'guest_state': info['state'], 'vcpus': info['vcpus'], 'memory_mib': info['memory_mib'],
            'file_bytes': len(text.encode()), 'file_sha256': digest(text), 'export_chunks': len(chunks)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['initial', 'files', 'verify', 'auth-capacity', 'restart-refusal'])
    parser.add_argument('--url', default='http://127.0.0.1:8321')
    parser.add_argument('--setup-key-file', type=Path, default=Path('/var/lib/tofi/data/owner-bootstrap.secret'))
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    client = Client(args.url, args.state.with_name(args.state.name + '.cookies'))
    checks = []
    if args.mode == 'initial':
        assert not args.state.exists(), 'fresh synthetic state path required'
        assert client.request('/api/auth/session')['setup_required']
        state = {'username': 'installer-synthetic-' + secrets.token_hex(4),
                 'password': secrets.token_urlsafe(32), 'marker': 'synthetic-persist-' + secrets.token_hex(12)}
        save(args.state, state)
        body = {'username': state['username'], 'email': state['username'] + '@example.invalid',
                'password': state['password']}
        client.request('/api/auth/setup', dict(body, bootstrap_secret='wrong-' + secrets.token_urlsafe(16)),
                       expected=401)
        body['bootstrap_secret'] = args.setup_key_file.read_text().strip()
        client.request('/api/auth/setup', body)
        assert not args.setup_key_file.exists(), 'setup key must be consumed by the first Admin'
        session = client.request('/api/auth/session')
        assert session['authenticated'] and session['owner']['role'] == 'admin' and not session['setup_required']
        state['account_id'] = session['owner']['id']
        save(args.state, state)
        client.request('/api/auth/setup', body, expected=(401, 409))
        state['bot_id'] = client.request('/api/bots', {'name': 'Installer synthetic fixture',
            'instructions': 'Use this isolated synthetic test workspace only.'}, expected=201)['id']
        save(args.state, state)
        client.ready()
        client.action(state, 'files.write', {'path': 'installer-marker.txt', 'content': state['marker']})
        checks += ['wrong_setup_key_refused', 'first_admin_setup', 'setup_key_consumed', 'repeat_setup_refused']
    else:
        state = json.loads(args.state.read_text())
        assert state['username'].startswith('installer-synthetic'), 'only synthetic acceptance fixtures are allowed'
        assert not args.state.stat().st_mode & 0o077, 'private fixture state required'
        client.login(state)
    if args.mode in ('files', 'initial'):
        assert not state.get('integrity_initialized'), 'fixture already initialized; use verify'
        client.ready()
        if 'integrity_path' in state:
            state.setdefault('prior_integrity_paths', []).append(state['integrity_path'])
        state['integrity_path'] = 'installer-acceptance-' + secrets.token_hex(8) + '/integrity.txt'
        save(args.state, state)
        initial = payload(state).removesuffix('APPENDED\n')
        written = client.action(state, 'files.write', {'path': state['integrity_path'], 'content': initial})
        assert written['sha256'] == digest(initial) and written['bytes'] == len(initial.encode())
        appended = client.action(state, 'files.write', {'path': state['integrity_path'], 'content': 'APPENDED\n',
                                                       'append': True, 'expected_sha256': digest(initial)})
        assert appended['sha256'] == digest(payload(state))
        # The App's action facade maps Guest failures to HTTP 502. Require the
        # actual conflict reason and verify the original bytes below.
        conflict = client.action(state, 'files.write', {'path': state['integrity_path'], 'content': 'must not overwrite',
                                                      'expected_sha256': digest(initial)}, expected=502)
        assert 'file_conflict' in json.dumps(conflict)
        state['integrity_initialized'] = True
        save(args.state, state)
        checks += ['nested_unicode_write', 'append_with_matching_hash', 'stale_hash_refused']
    elif args.mode == 'auth-capacity':
        accounts = client.request('/api/admin/accounts')
        capacity = client.request('/api/admin/capacity')
        assert len(accounts) == 1 and accounts[0]['id'] == state['account_id']
        assert len(capacity['accounts']) == 1 and capacity['accounts'][0]['quota_bytes'] == 8 << 30
        assert capacity['per_account_internal_reserved_bytes'] == 8 << 30
        assert capacity['admission_remaining_bytes'] < 16 << 30
        client.request('/api/admin/accounts', {'username': 'installer-synthetic-overcapacity',
                       'email': 'overcapacity@example.invalid', 'password': secrets.token_urlsafe(32)}, expected=400)
        assert client.request('/api/admin/accounts') == accounts
        client.request('/api/admin/accounts/' + state['account_id'] + '/quota', {'quota_gib': 1024}, method='PATCH', expected=409)
        assert client.request('/api/admin/capacity')['accounts'][0]['quota_bytes'] == 8 << 30
        checks += ['second_account_capacity_refused_without_row', 'quota_growth_refused_without_change']
        # A synthetic Admin can reset its own initial password without another
        # disk promise. Retain both generated passwords before session revocation.
        state['reset_password'] = secrets.token_urlsafe(32)
        state['updated_password'] = secrets.token_urlsafe(32)
        save(args.state, state)
        client.request('/api/admin/accounts/' + state['account_id'],
                       {'initial_password': state['reset_password']}, method='PATCH')
        assert not client.request('/api/auth/session')['authenticated']
        state['password'] = state['reset_password']
        save(args.state, state)
        assert client.login(state)['owner']['must_change_password']
        client.request('/api/bots', expected=403)
        client.request('/api/auth/password', {'current_password': state['password'], 'password': state['updated_password']})
        state['password'] = state['updated_password']
        save(args.state, state)
        assert not client.login(state)['owner']['must_change_password']
        checks += ['reset_revokes_session', 'forced_password_blocks_workspace', 'updated_password_restores_access']
    elif args.mode == 'restart-refusal':
        verify(client, state)
        refusal = client.request('/api/computer/resources/apply', {'confirm': True}, expected=403)
        assert 'quota_management_required' in json.dumps(refusal)
        checks += ['account_resource_restart_requires_central_management']
    result = verify(client, state)
    result.update(passed=True, mode=args.mode, account_id=state['account_id'], bot_id=state['bot_id'],
                  checks=checks + ['read_and_list_integrity', 'chunk_export_integrity', 'shell_action'])
    save(args.evidence, result)
    print(json.dumps(result, sort_keys=True), flush=True)


if __name__ == '__main__':
    main()
