#!/usr/bin/env python3
"""Run retained official artifacts only inside an owned network-none container."""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import signal
import time
import tarfile
import tempfile
import uuid
import zipfile

ROOT = Path(__file__).resolve().parents[1]
IMAGE = 'php@sha256:92ff31bbd049fd199bf3700719374e4594b2baa7324eefc459bda26d622c3ea9'
ARTIFACTS = Path(os.environ['NOVAPANEL_WP_STAGED_ARTIFACTS'])
EVIDENCE = Path(tempfile.mkdtemp(prefix='wp-offline-', dir=os.environ['TMPDIR']))
NAME = 'novapanel-wp-' + uuid.uuid4().hex[:12]

OWNER_LABEL = 'org.novapanel.wp-offline-owner'
CANCEL_SIGNAL = 0
CLEANING = False
COMMAND_TIMEOUT = 180
CLEANUP_TIMEOUT = 20
CLEANUP_DEADLINE = None


class Cancelled(Exception):
    pass


def check_cancelled():
    if CANCEL_SIGNAL and not CLEANING:
        raise Cancelled()


def cancellation(signum, _frame):
    # Do not raise into Popen, filesystem writes or cleanup. Drain mutations so
    # daemon-side build/create completion cannot race pre-ID cleanup.
    global CANCEL_SIGNAL
    if not CANCEL_SIGNAL:
        CANCEL_SIGNAL = signum


def cmd(*args, data=None, check=True):
    check_cancelled()
    mutation = args[:2] in (('docker', 'build'), ('docker', 'create'), ('docker', 'start'))
    timeout = COMMAND_TIMEOUT
    if CLEANUP_DEADLINE is not None:
        timeout = max(.01, CLEANUP_DEADLINE - time.monotonic())
    with subprocess.Popen(args, stdin=subprocess.PIPE if data is not None else subprocess.DEVNULL,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=ROOT) as child:
        deadline = time.monotonic() + timeout
        first = True
        try:
            while True:
                try:
                    out, err = child.communicate(input=data if first else None, timeout=min(.1, max(.01, deadline - time.monotonic())))
                    break
                except subprocess.TimeoutExpired:
                    first = False
                    if time.monotonic() >= deadline or (CANCEL_SIGNAL and not CLEANING and not mutation):
                        child.kill()
                        out, err = child.communicate()
                        raise RuntimeError(f'command interrupted or timed out; evidence={EVIDENCE}')
        finally:
            if child.poll() is None:
                child.kill()
                child.communicate()
    p = subprocess.CompletedProcess(args, child.returncode, out, err)
    with (EVIDENCE / 'commands.log').open('ab') as log:
        log.write((' '.join(args) + '\n').encode() + out + err)
    if check and p.returncode:
        raise RuntimeError(f'command failed; evidence={EVIDENCE}')
    check_cancelled()
    return p


class OwnedResources:
    def __enter__(self):
        self.handlers = {s: signal.signal(s, cancellation) for s in (signal.SIGTERM, signal.SIGINT)}
        # Retained even on SIGKILL. Reap only this explicit record, never scan
        # other launches, tags, images or containers.
        with (EVIDENCE / 'owner.json').open('x') as record:
            record.write(json.dumps({'name': NAME, 'label': OWNER_LABEL}) + '\n')
            record.flush()
            os.fsync(record.fileno())
        directory = os.open(EVIDENCE, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
        return self

    def cleanup(self):
        global CLEANING, CLEANUP_DEADLINE
        CLEANING = True
        CLEANUP_DEADLINE = time.monotonic() + CLEANUP_TIMEOUT
        errors = []
        removed = []
        try:
            for kind, target in (('container', NAME), ('image', NAME + ':local')):
                try:
                    p = cmd('docker', kind, 'inspect', target, check=False)
                    if p.returncode:
                        if b'No such' in p.stderr:
                            continue
                        raise RuntimeError('inspect failed, absence not established')
                    item = json.loads(p.stdout)[0]
                    if item['Config'].get('Labels', {}).get(OWNER_LABEL) != NAME:
                        raise RuntimeError('ownership label mismatch')
                    identity = item['Id']
                    args = ('docker', kind, 'rm', '-f', identity) if kind == 'container' else ('docker', kind, 'rm', identity)
                    cmd(*args)
                    # Immutable-ID readback, not just disappearance of a tag.
                    p = cmd('docker', kind, 'inspect', identity, check=False)
                    if not p.returncode or b'No such' not in p.stderr:
                        raise RuntimeError('resource absence not established')
                    removed.append({'kind': kind, 'id': identity})
                except Exception as exc:
                    errors.append(f'{kind} {target}: {exc}')
            (EVIDENCE / 'cleanup.json').write_text(json.dumps({'removed': removed, 'errors': errors}) + '\n')
            if errors:
                (EVIDENCE / 'cleanup-errors.log').write_text('\n'.join(errors) + '\n')
                raise RuntimeError(f'cleanup failed; evidence={EVIDENCE}')
        finally:
            CLEANING = False
            CLEANUP_DEADLINE = None

    def __exit__(self, exc_type, exc, tb):
        try:
            self.cleanup()
        finally:
            for s, handler in self.handlers.items():
                signal.signal(s, handler)
            print('Evidence:', EVIDENCE, flush=True)
        check_cancelled()


def reap(record):
    global NAME
    import re
    item = json.loads(Path(record).read_text())
    if item.get('label') != OWNER_LABEL or not re.fullmatch(r'novapanel-(?:wp|reviewcancel)-[a-f0-9]{12}', item.get('name', '')):
        raise ValueError('invalid explicit ownership record')
    NAME = item['name']
    with OwnedResources():
        pass

def inspect():
    raw = cmd('docker', 'inspect', NAME).stdout
    (EVIDENCE / 'inspect.json').write_bytes(raw)
    item = json.loads(raw)[0]
    h = item['HostConfig']
    assert item['Config']['User'] == '23041:23041'
    assert h['ReadonlyRootfs'] and h['NetworkMode'] == 'none'
    assert not h['Privileged'] and not h['Binds'] and not h['Devices'] and not h['CapAdd']
    assert not h['PortBindings'] and h['CapDrop'] == ['ALL']
    assert 'no-new-privileges:true' in h['SecurityOpt']
    assert h['Memory'] == h['MemorySwap'] == 536870912 and h['PidsLimit'] == 64
    assert h['NanoCpus'] == 1000000000
    assert set(h['Tmpfs']) == {'/scratch'}

def run_lab():
    sha = cmd('git', 'rev-parse', 'HEAD').stdout.decode().strip()
    assert not cmd('git', 'status', '--porcelain').stdout, 'commit inputs before exact-source lab'
    (EVIDENCE / 'source-sha.txt').write_text(sha + '\n')
    # Verify signature, full ZIP and checksum inventory through production custody.
    cmd('go', 'test', './internal/wordpress', '-run', '^TestStagedRetainedOfficialArtifacts$', '-count=1', '-v')
    pins = {'cli.phar': 'ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c',
            'wordpress.zip': 'a147146c159362778ecbbbfb07bb20ddf4e85b130f971c824e656a8f938c5f87',
            'checksums.json': 'bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7'}
    blobs = {name: (ARTIFACTS / name).read_bytes() for name in pins}
    for name, expected in pins.items():
        assert hashlib.sha256(blobs[name]).hexdigest() == expected
    cmd('go', 'build', '-o', str(EVIDENCE / 'probe'), './tools/phase4wpoffline')
    pack = io.BytesIO()
    with tarfile.open(fileobj=pack, mode='w') as tar:
        def add(name, data=b'', directory=False, uid=0, mode=0o444):
            t = tarfile.TarInfo(name)
            t.uid = t.gid = uid
            t.mode = mode
            t.type = tarfile.DIRTYPE if directory else tarfile.REGTYPE
            t.size = 0 if directory else len(data)
            tar.addfile(t, None if directory else io.BytesIO(data))
        for d in ['lab', 'lab/cwd', 'lab/home', 'lab/packages', 'lab/empty-ini']:
            add(d, directory=True, mode=0o755)
        add('lab/cli.phar', blobs['cli.phar'])
        add('lab/checksums.json', blobs['checksums.json'])
        # Missing bootstrap intentionally gives the first real runtime RED.
        bootstrap = ROOT / 'tools/phase4wpoffline/bootstrap.php'
        if bootstrap.exists():
            add('lab/bootstrap.php', bootstrap.read_bytes())
            add('lab/transport.php', (bootstrap.parent / 'transport.php').read_bytes())
        add('lab/probe', (EVIDENCE / 'probe').read_bytes(), mode=0o555)
        add('lab/hostile.ini', b'auto_prepend_file=/lab/hostile.php\n')
        add('lab/hostile.php', b'<?php file_put_contents("/scratch/INJECTED", "bad"); exit(91);')
        add('Dockerfile', ('FROM ' + IMAGE + '\nCOPY lab /lab\n').encode())
    # --pull=false alone still permits fetching a missing base. Require local custody.
    cmd('docker', 'image', 'inspect', IMAGE)
    image = NAME + ':local'
    cmd('docker', 'build', '--network=none', '--pull=false', '--force-rm', '--label', OWNER_LABEL + '=' + NAME, '-t', image, '-', data=pack.getvalue())
    cmd('docker', 'create', '--name', NAME, '--label', OWNER_LABEL + '=' + NAME, '--user', '23041:23041', '--network', 'none',
        '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
        '--memory', '512m', '--memory-swap', '512m', '--cpus', '1', '--pids-limit', '64',
        '--tmpfs', '/scratch:rw,nosuid,nodev,noexec,size=256m,mode=0700,uid=23041,gid=23041',
        '--entrypoint', '/bin/sleep', image, '180')
    inspect()
    cmd('docker', 'start', NAME)
    # Write scratch as the numeric site user, never root and never host PHP.
    files = io.BytesIO()
    with tarfile.open(fileobj=files, mode='w') as tar:
        with zipfile.ZipFile(io.BytesIO(blobs['wordpress.zip'])) as z:
            for ent in z.infolist():
                if ent.is_dir():
                    continue
                name = 'public/' + ent.filename.removeprefix('wordpress/')
                data = z.read(ent)
                t = tarfile.TarInfo(name); t.mode = 0o600; t.size = len(data)
                tar.addfile(t, io.BytesIO(data))
        for name, data in {'public/wp-config.php': b'<?php file_put_contents("/scratch/INJECTED", "bad"); exit(91);', 'public/wp-content/mu-plugins/hostile.php': b'<?php exit(91);', 'public/wp-cli.yml': b'require: /lab/hostile.php\n', 'public/.user.ini': b'auto_prepend_file=/lab/hostile.php\n'}.items():
            t = tarfile.TarInfo(name); t.mode = 0o600; t.size = len(data)
            tar.addfile(t, io.BytesIO(data))
    cmd('docker', 'exec', '-i', NAME, 'tar', '-x', '-C', '/scratch', data=files.getvalue())
    cmd('docker', 'exec', NAME, '/usr/local/bin/php', '-n', '-v')
    p = cmd('docker', 'exec', '-e', 'PHPRC=/lab/hostile.ini', '-e', 'WP_CLI_EARLY_REQUIRE=/lab/hostile.php', NAME, '/lab/probe', check=False)
    if p.returncode:
        raise RuntimeError(f'runtime failed; evidence={EVIDENCE}')
    assert cmd('docker', 'exec', NAME, 'test', '-e', '/scratch/INJECTED', check=False).returncode == 1
    inspect()
    (EVIDENCE / 'result.json').write_text(json.dumps({'source': sha, 'result': 'PASS', 'pins': pins}) + '\n')
    print('PASS', EVIDENCE)

def main():
    import sys
    try:
        if len(sys.argv) == 3 and sys.argv[1] == '--reap':
            reap(sys.argv[2])
        else:
            with OwnedResources():
                run_lab()
    except Exception:
        if CANCEL_SIGNAL:
            raise SystemExit(128 + CANCEL_SIGNAL)
        raise


if __name__ == '__main__':
    main()
