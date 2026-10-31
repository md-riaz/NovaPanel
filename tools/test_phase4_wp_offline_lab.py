"""Owned Docker lifecycle regressions. Opt in with NOVAPANEL_WP_DOCKER_TEST=1."""
import importlib.util
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest

MODULE = Path(__file__).with_name('phase4_wp_offline_lab.py')


def load():
    spec = importlib.util.spec_from_file_location('lab', MODULE)
    lab = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(lab)
    return lab


class LifecycleTests(unittest.TestCase):
    def test_cleanup_failure_is_logged_and_image_cleanup_still_attempted(self):
        lab = load()
        calls = []
        def command(*args, **kwargs):
            calls.append(args)
            if args[1:3] == ('container', 'inspect'):
                return subprocess.CompletedProcess(args, 0, json.dumps([{
                    'Id': 'c' * 64, 'Config': {'Labels': {lab.OWNER_LABEL: lab.NAME}}
                }]).encode(), b'')
            if args[1:3] == ('image', 'inspect'):
                return subprocess.CompletedProcess(args, 0, json.dumps([{
                    'Id': 'sha256:' + 'a' * 64, 'Config': {'Labels': {lab.OWNER_LABEL: lab.NAME}}
                }]).encode(), b'')
            raise RuntimeError('injected removal failure')
        lab.cmd = command
        owner = lab.OwnedResources()
        with self.assertRaisesRegex(RuntimeError, 'cleanup failed'):
            owner.cleanup()
        self.assertTrue(any(c[1:3] == ('image', 'rm') for c in calls))
        log = (lab.EVIDENCE / 'cleanup-errors.log').read_text()
        self.assertIn('container', log)
        self.assertIn('image', log)

    def test_foreign_label_is_never_removed(self):
        lab = load()
        calls = []
        def command(*args, **kwargs):
            calls.append(args)
            return subprocess.CompletedProcess(args, 0, json.dumps([{
                'Id': 'c' * 64, 'Config': {'Labels': {lab.OWNER_LABEL: 'foreign'}}
            }]).encode(), b'')
        lab.cmd = command
        with self.assertRaisesRegex(RuntimeError, 'cleanup failed'):
            lab.OwnedResources().cleanup()
        self.assertFalse(any('rm' in c for c in calls))

    def test_cancelled_command_is_drained_and_bounded(self):
        lab = load()
        lab.CANCEL_SIGNAL = signal.SIGTERM
        with self.assertRaises(lab.Cancelled):
            lab.cmd(sys.executable, '-c', 'raise SystemExit(99)')
        lab.CANCEL_SIGNAL = 0
        lab.COMMAND_TIMEOUT = .15
        started = time.monotonic()
        with self.assertRaisesRegex(RuntimeError, 'timed out'):
            lab.cmd(sys.executable, '-c', 'import time; time.sleep(30)')
        self.assertLess(time.monotonic() - started, 2)

    def test_reap_record_validation(self):
        lab = load()
        record = lab.EVIDENCE / 'invalid.json'
        record.write_text(json.dumps({'name': 'unrelated', 'label': lab.OWNER_LABEL}))
        with self.assertRaises(ValueError):
            lab.reap(record)

    @unittest.skipUnless(os.environ.get('NOVAPANEL_WP_DOCKER_TEST') == '1', 'local Docker opt-in')
    def test_launcher_signals_at_build_create_and_running(self):
        for phase in ('build', 'create', 'start', 'running'):
            for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGKILL):
                with self.subTest(phase=phase, signal=sig):
                    self.run_signal(phase, sig)

    def run_signal(self, phase, sig):
        # COPY-only build from the exact locally retained PHP digest. No pull or network.
        fixture = load()
        subprocess.run(['docker', 'image', 'inspect', fixture.IMAGE], check=True, capture_output=True)
        with tempfile.TemporaryDirectory(prefix='wp-signal-', dir=os.environ['TMPDIR']) as td:
            ready = Path(td) / 'ready.json'
            code = '''
import importlib.util,io,json,os,signal,sys,tarfile,time
from pathlib import Path
spec=importlib.util.spec_from_file_location('lab',sys.argv[1]);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
phase=sys.argv[2];ready=Path(sys.argv[3]); original=m.cmd
# Stop after the daemon has committed the effect but before its caller receives the ID.
def command(*args,**kwargs):
    result=original(*args,**kwargs)
    if args[:2] == ('docker',phase) and phase in ('build','create','start'):
        ready.write_text(json.dumps({'name':m.NAME,'evidence':str(m.EVIDENCE)}))
        while not m.CANCEL_SIGNAL: time.sleep(.01)
    return result
m.cmd=command
try:
    with m.OwnedResources():
        pack=io.BytesIO()
        with tarfile.open(fileobj=pack,mode='w') as tar:
            data=('FROM '+m.IMAGE+'\\nLABEL fixture='+m.NAME+'\\n').encode();t=tarfile.TarInfo('Dockerfile');t.size=len(data);tar.addfile(t,io.BytesIO(data))
        m.cmd('docker','build','--network=none','--pull=false','--force-rm','--label',m.OWNER_LABEL+'='+m.NAME,'-t',m.NAME+':local','-',data=pack.getvalue())
        m.check_cancelled()
        m.cmd('docker','create','--name',m.NAME,'--label',m.OWNER_LABEL+'='+m.NAME,'--network=none','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges:true','--user=23041:23041','--entrypoint=/bin/sleep',m.NAME+':local','180')
        m.check_cancelled()
        m.cmd('docker','start',m.NAME)
        ready.write_text(json.dumps({'name':m.NAME,'evidence':str(m.EVIDENCE)}))
        while True:
            time.sleep(.01);m.check_cancelled()
except m.Cancelled:
    sys.exit(128+m.CANCEL_SIGNAL)
'''
            p = subprocess.Popen([sys.executable, '-c', code, str(MODULE), phase, str(ready)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            name = None
            try:
                deadline = time.monotonic() + 30
                while not ready.exists() and time.monotonic() < deadline and p.poll() is None:
                    time.sleep(.02)
                self.assertTrue(ready.exists(), p.communicate(timeout=1)[0].decode() if p.poll() is not None else 'readiness timeout')
                record = json.loads(ready.read_text()); name = record['name']
                if phase != 'build':
                    item = json.loads(subprocess.check_output(['docker', 'container', 'inspect', name]))[0]
                    self.assertEqual(item['Config']['Labels'][fixture.OWNER_LABEL], name)
                    if phase == 'running': self.assertTrue(item['State']['Running'])
                p.send_signal(sig)
                # Repeated cancellation must not interrupt cleanup.
                p.send_signal(sig)
                output = p.communicate(timeout=15)[0].decode()
                if sig == signal.SIGKILL:
                    self.assertEqual(p.returncode, -sig, output)
                    # No finally claim. The next explicit invocation reaps only
                    # this durable record after the original mutation drained.
                    subprocess.run([sys.executable, str(MODULE), '--reap', str(Path(record['evidence']) / 'owner.json')], check=True, capture_output=True, timeout=25)
                else:
                    self.assertEqual(p.returncode, 128 + sig, output)
                for kind, target in (('container', name), ('image', name + ':local')):
                    result = subprocess.run(['docker', kind, 'inspect', target], capture_output=True)
                    self.assertNotEqual(result.returncode, 0, (kind, output))
                if sig != signal.SIGKILL:
                    self.assertTrue((Path(record['evidence']) / 'cleanup.json').exists())
            finally:
                if p.poll() is None: p.kill(); p.wait()
                # Emergency test teardown refuses foreign resources too.
                if name:
                    for kind, target in (('container', name), ('image', name + ':local')):
                        result = subprocess.run(['docker', kind, 'inspect', target], capture_output=True)
                        if result.returncode == 0:
                            item = json.loads(result.stdout)[0]
                            self.assertEqual(item['Config']['Labels'][fixture.OWNER_LABEL], name)
                            subprocess.run(['docker', kind, 'rm', '-f', item['Id']], check=True, capture_output=True)


if __name__ == '__main__':
    unittest.main()
