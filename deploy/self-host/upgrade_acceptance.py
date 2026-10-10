#!/usr/bin/env python3
"""Upgrade acceptance for the self-host installer (docs/agent-plan/safe-updates.md, A7).

Run as root on a DISPOSABLE test VM (nothing is purged, but the host is
updated, restored and rolled back for real). Synthetic data only: the admin,
password, Bot, skill, preference and key are generated here and the state file
that holds the password stays on the host (mode 0600); never export it.

    sudo python3 upgrade_acceptance.py --from v0.1.0-rc.4 --to v0.1.0-rc.5 \\
         --broken-manifest /root/broken-manifest.json

Steps (each is asserted; the first failure stops the run with exit 1, a full
pass exits 0 and writes the evidence JSON):

 1. install FROM fresh (install.sh --version FROM --yes --no-auto-update);
 2. through the HTTP API: create the admin, a Bot, a skill restricted to that
    Bot, a preference and an env var in Keys;
 3. add a custom line to /etc/tofi/tofi.env;
 4. `tofi update --version TO --yes`;
 5. all data and the custom line survived, `tofi status` is healthy at TO, and
    the pre-update backup exists;
 6. forced failure: `tofi update --manifest BROKEN --yes` (a manifest whose App
    image fails its health check; it must name a version newer than TO and be
    otherwise valid, so the images pull and the failure happens at start).
    Rollback must restore TO, the data written before the attempt, and the
    custom line;
 7. `tofi backups` lists the pre-update backups and `tofi restore` round-trips:
    restoring the FROM backup brings back FROM and drops what was written
    after it; restoring the automatic pre-restore backup brings TO back.

Data written while a rejected candidate was running is discarded by design (the
rollback restores the pre-update backup); this script writes nothing in that
window.
"""
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import time

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

ENV_FILE = Path('/etc/tofi/tofi.env')
SETUP_KEY_FILE = Path('/var/lib/tofi/data/owner-bootstrap.secret')
VERSION_RE = re.compile(r'^v\d+\.\d+\.\d+(-rc\.\d+)?$')
CUSTOM_KEY = 'TOFI_ACCEPTANCE_CUSTOM'


class Failure(Exception):
    """An acceptance assertion that did not hold."""


def check(condition, message):
    if not condition:
        raise Failure(message)


# --------------------------------------------------------------------------
# Pure helpers (unit-tested in test_upgrade_acceptance.py)


def env_lines(text):
    """tofi.env as {key: value}; comments and blanks ignored."""
    values = {}
    for line in text.splitlines():
        line = line.strip()
        if line and not line.startswith('#') and '=' in line:
            key, _, value = line.partition('=')
            values[key.strip()] = value.strip()
    return values


def snapshot_diff(before, after):
    """Human-readable differences between two data snapshots (empty when equal)."""
    problems = []
    for key in sorted(set(before) | set(after)):
        if before.get(key) != after.get(key):
            problems.append('%s: before %r, after %r' % (key, before.get(key), after.get(key)))
    return problems


def pick_backup(backups, version, reason):
    """The newest backup taken at `version` for `reason`, or None."""
    matching = [item for item in backups if item.get('version') == version and item.get('reason') == reason]
    matching.sort(key=lambda item: item.get('created_at') or '')
    return matching[-1] if matching else None


def validate_args(args):
    for name in ('from_version', 'to_version'):
        check(VERSION_RE.match(getattr(args, name) or ''), '--%s must look like v0.1.0 or v0.1.0-rc.1' % name.split('_')[0])
    check(args.from_version != args.to_version, '--from and --to must differ')
    if not args.skip_failure_scenario:
        check(args.broken_manifest, '--broken-manifest is required (or pass --skip-failure-scenario, which is '
              'not a pass for the release gate)')


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--from', dest='from_version', required=True, help='version to install first')
    parser.add_argument('--to', dest='to_version', required=True, help='version to update to')
    parser.add_argument('--broken-manifest', type=Path,
                        help='manifest.json of a release whose App fails its health check (step 6)')
    parser.add_argument('--skip-failure-scenario', action='store_true', help='skip step 6 (not a release-gate pass)')
    parser.add_argument('--install-script', type=Path, default=HERE.parent.parent / 'install.sh',
                        help='install.sh to run for step 1 (default: the one in this checkout)')
    parser.add_argument('--skip-install', action='store_true',
                        help='FROM is already installed and has no admin yet; do not run install.sh')
    parser.add_argument('--url', default='https://127.0.0.1:8321')
    parser.add_argument('--state-dir', type=Path, default=Path('/root/tofi-upgrade-acceptance'))
    parser.add_argument('--evidence', type=Path, help='evidence JSON (default: <state-dir>/evidence.json)')
    args = parser.parse_args(argv)
    validate_args(args)
    return args


# --------------------------------------------------------------------------
# The host and the App as seen by the steps (faked in the unit tests)


class Host:
    """Real commands on the test VM."""

    def __init__(self, args):
        self.args = args
        self.client = None
        self.state = {}

    def run(self, argv, timeout=3600):
        result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        return result.returncode, result.stdout, result.stderr

    def tofi(self, *argv, timeout=3600):
        return self.run(['tofi'] + list(argv), timeout=timeout)

    def install(self, version):
        script = self.args.install_script
        check(script.is_file(), 'install script %s not found' % script)
        return self.run(['bash', str(script), '--version', version, '--yes', '--no-auto-update'])

    def read_env(self):
        return ENV_FILE.read_text()

    def append_env(self, line):
        with open(ENV_FILE, 'a') as stream:
            stream.write(line + '\n')

    def api(self):
        from acceptance_api import Client
        if self.client is None:
            self.args.state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
            self.client = Client(self.args.url, self.args.state_dir / 'session.cookies', insecure=True)
        return self.client

    def setup_key(self):
        return SETUP_KEY_FILE.read_text().strip()


class Acceptance:
    def __init__(self, host, args, log=print):
        self.host, self.args, self.log = host, args, log
        self.evidence = {'from': args.from_version, 'to': args.to_version, 'steps': []}

    # -- helpers

    def step(self, name):
        self.log('==> %s' % name)
        self.evidence['steps'].append(name)

    def tofi_json(self, *argv):
        code, out, err = self.host.tofi(*argv)
        check(code == 0, '`tofi %s` exited %d: %s' % (' '.join(argv), code, (err or out).strip()[-400:]))
        try:
            return json.loads(out)
        except ValueError:
            raise Failure('`tofi %s` printed no JSON: %r' % (' '.join(argv), out[:200]))

    def status(self):
        return self.tofi_json('status', '--json')

    def assert_healthy_at(self, version, label):
        status = self.status()
        check(status.get('phase') == 'installed', '%s: phase is %r, not installed' % (label, status.get('phase')))
        check(status.get('version') == version, '%s: version is %r, expected %s' % (label, status.get('version'), version))
        check(status.get('healthy') is True, '%s: status is not healthy' % label)
        services = status.get('services')
        if isinstance(services, dict):
            check('starting' not in services.values(), '%s: a service is still starting' % label)
        return status

    def backups(self):
        return self.tofi_json('backups', '--json')

    def assert_custom_line(self, label):
        values = env_lines(self.host.read_env())
        check(values.get(CUSTOM_KEY) == self.host.state['custom_value'],
              '%s: custom line %s was lost from tofi.env' % (label, CUSTOM_KEY))

    # -- the App fixture

    def ensure_login(self):
        client = self.host.api()
        state = self.host.state['api']
        session = client.request('/api/auth/session')
        if not session.get('authenticated'):
            client.request('/api/auth/login', {'identifier': state['username'], 'password': state['password']})
        return client

    def create_fixture(self):
        client = self.host.api()
        suffix = secrets.token_hex(4)
        state = {'username': 'upgrade-synthetic-' + suffix, 'password': secrets.token_urlsafe(32),
                 'bot_name': 'Upgrade fixture bot ' + suffix, 'skill': 'upgrade-fixture-' + suffix,
                 'timezone': 'America/Los_Angeles', 'key_name': 'upgrade-fixture-key-' + suffix,
                 'key_target': 'UPGRADE_FIXTURE_KEY_' + suffix.upper()}
        self.host.state['api'] = state
        check(client.request('/api/auth/session').get('setup_required'), 'FROM is not fresh: setup is closed')
        client.request('/api/auth/setup', {'username': state['username'], 'email': state['username'] + '@example.invalid',
                                           'password': state['password'], 'bootstrap_secret': self.host.setup_key()})
        bot = client.request('/api/bots', {'name': state['bot_name'], 'instructions': 'Synthetic upgrade fixture only.'},
                             expected=201)
        state['bot_id'] = bot['id']
        client.request('/api/extensions/skills', {'name': state['skill'], 'files': {
            'SKILL.md': '---\nname: %s\ndescription: synthetic upgrade fixture\n---\nSynthetic body.' % state['skill']}},
            expected=201)
        client.request('/api/extensions/skills/%s/access' % state['skill'],
                       {'mode': 'selected', 'bot_ids': [bot['id']]}, method='PUT')
        client.request('/api/preferences', {'timezone': state['timezone']}, method='PUT')
        client.request('/api/computer/credentials', {'name': state['key_name'], 'kind': 'env',
                                                     'target': state['key_target'], 'value': secrets.token_urlsafe(24)},
                       expected=(200, 201))
        return state

    def collect(self):
        """Everything the fixture created, as comparable plain values."""
        client = self.ensure_login()
        state = self.host.state['api']
        bots = client.request('/api/bots')
        bots = bots if isinstance(bots, list) else bots.get('bots', [])
        skills = client.request('/api/extensions/skills')['skills']
        skill = next((s for s in skills if s['name'] == state['skill']), None)
        preferences = client.request('/api/preferences')
        keys = client.request('/api/computer/credentials')
        keys = keys if isinstance(keys, list) else keys.get('credentials', keys.get('items', []))
        return {
            'bots': json.dumps(sorted(b['name'] for b in bots)),
            'skill_present': skill is not None,
            'skill_access': json.dumps((skill or {}).get('access'), sort_keys=True),
            'timezone': preferences.get('timezone'),
            'key_names': json.dumps(sorted(k.get('name') for k in keys)),
        }

    def add_bot(self, name):
        self.ensure_login().request('/api/bots', {'name': name, 'instructions': 'Synthetic marker.'}, expected=201)

    # -- the steps

    def run(self):
        args = self.args
        self.step('1. install %s' % args.from_version)
        if not args.skip_install:
            check(not Path('/etc/tofi/install-state.json').exists(), 'TOFI is already installed here; use a fresh VM '
                  '(or --skip-install if %s is installed without an admin)' % args.from_version)
            code, out, err = self.host.install(args.from_version)
            check(code == 0, 'install.sh failed: %s' % (err or out).strip()[-400:])
        self.assert_healthy_at(args.from_version, 'after install')

        self.step('2. admin and synthetic data through the API')
        self.create_fixture()
        before_update = self.collect()
        check(before_update['skill_present'] and 'selected' in before_update['skill_access'], 'fixture skill not restricted')

        self.step('3. custom line in tofi.env')
        self.host.state['custom_value'] = secrets.token_hex(6)
        self.host.append_env('%s=%s' % (CUSTOM_KEY, self.host.state['custom_value']))

        self.step('4. tofi update --version %s --yes' % args.to_version)
        code, out, err = self.host.tofi('update', '--version', args.to_version, '--yes', '--wait', '0')
        check(code == 0, 'update failed (exit %d): %s' % (code, (err or out).strip()[-600:]))

        self.step('5. data, custom line, health and backup after the update')
        self.assert_healthy_at(args.to_version, 'after update')
        problems = snapshot_diff(before_update, self.collect())
        check(not problems, 'data changed across the update: %s' % '; '.join(problems))
        self.assert_custom_line('after update')
        from_backup = pick_backup(self.backups(), args.from_version, 'pre-update')
        check(from_backup, 'no pre-update backup of %s is listed by `tofi backups`' % args.from_version)
        self.evidence['from_backup'] = from_backup['id']

        if args.skip_failure_scenario:
            self.log('==> 6. SKIPPED (--skip-failure-scenario): this run is NOT a release-gate pass')
            self.evidence['failure_scenario'] = 'skipped'
        else:
            self.failure_scenario()

        self.step('7. backups listed and tofi restore round-trips')
        self.restore_round_trip(from_backup)
        self.evidence['passed'] = True
        return self.evidence

    def failure_scenario(self):
        args = self.args
        self.step('6. forced failure: update to %s must roll back' % args.broken_manifest)
        broken = json.loads(Path(args.broken_manifest).read_text())
        check(broken.get('version') not in (args.from_version, args.to_version),
              'the broken manifest must name a different version than FROM and TO')
        self.add_bot('Written before the failing update')
        before_failure = self.collect()
        backups_before = {item['id'] for item in self.backups()}
        code, out, err = self.host.tofi('update', '--manifest', str(args.broken_manifest), '--yes', '--wait', '0')
        check(code != 0, 'the update to the broken manifest unexpectedly succeeded')
        check('update rejected; previous version restored' in err,
              'rollback message missing; tofi said: %s' % (err or out).strip()[-400:])
        self.assert_healthy_at(args.to_version, 'after rollback')
        problems = snapshot_diff(before_failure, self.collect())
        check(not problems, 'data written before the failed update is not intact: %s' % '; '.join(problems))
        self.assert_custom_line('after rollback')
        created = [item for item in self.backups() if item['id'] not in backups_before]
        check(any(item.get('reason') == 'pre-update' and item.get('version') == args.to_version for item in created),
              'the failed update left no pre-update backup of %s' % args.to_version)
        self.evidence['rollback'] = 'restored %s' % args.to_version

    def restore_round_trip(self, from_backup):
        args = self.args
        listed = self.backups()
        check(len(listed) >= 1 and all(item.get('reason') for item in listed), '`tofi backups` lists nothing usable')
        code, out, err = self.host.tofi('backups')
        check(code == 0 and from_backup['id'] in out, '`tofi backups` does not show %s' % from_backup['id'])
        baseline = self.collect()
        self.add_bot('Written after the backup')
        with_marker = self.collect()
        check(snapshot_diff(baseline, with_marker), 'marker Bot was not written')
        # Back to FROM: the marker (and the failure-scenario Bot) did not exist then.
        code, out, err = self.host.tofi('restore', from_backup['id'], '--yes', '--wait', '0')
        check(code == 0, 'restore of %s failed (exit %d): %s' % (from_backup['id'], code, (err or out).strip()[-600:]))
        self.assert_healthy_at(args.from_version, 'after restore')
        restored = self.collect()
        self.verify_fixture_only(restored)
        self.assert_custom_line('after restore')
        pre_restore = pick_backup(self.backups(), args.to_version, 'pre-restore')
        check(pre_restore, 'restore took no pre-restore backup of %s' % args.to_version)
        # And forward again: the pre-restore backup holds everything as it was.
        code, out, err = self.host.tofi('restore', pre_restore['id'], '--yes', '--wait', '0')
        check(code == 0, 'restore of %s failed (exit %d): %s' % (pre_restore['id'], code, (err or out).strip()[-600:]))
        self.assert_healthy_at(args.to_version, 'after restoring the pre-restore backup')
        problems = snapshot_diff(with_marker, self.collect())
        check(not problems, 'the restore round trip lost data: %s' % '; '.join(problems))
        self.assert_custom_line('after round trip')
        self.evidence['restore_round_trip'] = [from_backup['id'], pre_restore['id']]

    def verify_fixture_only(self, snapshot):
        state = self.host.state['api']
        bots = json.loads(snapshot['bots'])
        check(state['bot_name'] in bots, 'the fixture Bot is missing after restoring the FROM backup')
        check('Written after the backup' not in bots, 'data written after the backup survived the restore')
        check(snapshot['skill_present'] and snapshot['timezone'] == state['timezone'], 'fixture settings missing after restore')
        check(state['key_name'] in json.loads(snapshot['key_names']), 'fixture key missing after restore')


def main(argv=None):
    try:
        args = parse_args(argv)
    except Failure as error:
        print('upgrade_acceptance: %s' % error, file=sys.stderr)
        return 2
    if os.geteuid() != 0:
        print('upgrade_acceptance: run as root on the test VM (sudo).', file=sys.stderr)
        return 2
    host = Host(args)
    started = time.time()
    try:
        evidence = Acceptance(host, args).run()
    except (Failure, RuntimeError, OSError, subprocess.SubprocessError) as error:
        print('FAIL: %s' % error, file=sys.stderr)
        return 1
    evidence['seconds'] = round(time.time() - started)
    path = args.evidence or args.state_dir / 'evidence.json'
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, indent=2, sort_keys=True) + '\n')
    print('PASS: %s' % json.dumps(evidence, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
