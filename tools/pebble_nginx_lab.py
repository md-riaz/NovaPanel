#!/usr/bin/env python3
"""Run production ACME jobs with real Pebble and nginx, loopback-only."""
import hashlib,json,os,subprocess,tempfile,uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
E=Path(tempfile.mkdtemp(prefix='phase3-pebble-',dir=os.environ['TMPDIR']))
NAME='novapanel-pebble-'+uuid.uuid4().hex[:12]
PEBBLE=Path(os.environ['NOVAPANEL_PEBBLE_BINARY'])
IMAGE='nginx@sha256:1d13701a5f9f3fb01aaa88cef2344d65b6b5bf6b7d9fa4cf0dca557a8d7702ba'
def run(*args,check=True,**kw):
 p=subprocess.run(args,cwd=ROOT,capture_output=True,**kw)
 with (E/'commands.log').open('ab') as f:f.write(('$ '+' '.join(args)+'\n').encode()+p.stdout+p.stderr+f'\nexit={p.returncode}\n'.encode())
 if check and p.returncode:raise RuntimeError(f'{args} exit={p.returncode}')
 return p

def inspect(stage):
 p=run('docker','inspect',NAME);(E/(stage+'.json')).write_bytes(p.stdout)
 d=json.loads(p.stdout)[0];h=d['HostConfig']
 assert not d['Mounts'] and not h['Privileged'] and h['NetworkMode']=='none' and not h['PortBindings']
 assert not h['CapAdd'] and h['CapDrop']==['ALL'] and 'no-new-privileges:true' in h['SecurityOpt']
 assert d['Config']['User']=='65534:65534' and h['Memory']==536870912 and h['PidsLimit']==64 and h['NanoCpus']==1000000000
 assert d['Config']['Labels']['novapanel.lab']==NAME

created=False
try:
 sha=run('git','rev-parse','HEAD').stdout.decode().strip()
 run('git','diff','--exit-code','HEAD')
 (E/'source.tar').write_bytes(run('git','archive','--format=tar',sha).stdout)
 (E/'pebble').write_bytes(PEBBLE.read_bytes())
 run('go','test','-c','-o',str(E/'acceptance.test'),'./tests/integration',env=dict(os.environ,CGO_ENABLED='0'))
 run('openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(E/'server.key'),'-out',str(E/'server.pem'),'-days','2','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost,IP:127.0.0.1')
 conf={'pebble':{'listenAddress':'127.0.0.1:14000','managementListenAddress':'127.0.0.1:15000','certificate':'/tmp/server.pem','privateKey':'/tmp/server.key','httpPort':8080,'tlsPort':8443,'externalAccountBindingRequired':False,'retryAfter':{'authz':1,'order':1},'profiles':{'default':{'description':'isolated acceptance','validityPeriod':518400}}}}
 (E/'pebble.json').write_text(json.dumps(conf,indent=2))
 run('docker','create','--name',NAME,'--label','novapanel.lab='+NAME,'--user','65534:65534','--cap-drop','ALL','--security-opt','no-new-privileges:true','--memory','512m','--memory-swap','512m','--cpus','1','--pids-limit','64','--network','none','--add-host','demo.test:127.0.0.1','--add-host','alias.test:127.0.0.1','--entrypoint','/bin/sh',IMAGE,'-c','sleep 600');created=True
 inspect('before');run('docker','start',NAME)
 run('docker','cp',str(E/'source.tar'),NAME+':/tmp/source.tar')
 run('docker','exec',NAME,'tar','-xf','/tmp/source.tar','-C','/tmp')
 for name in ['pebble','pebble.json','server.pem','server.key','acceptance.test']:
  (E/name).chmod(0o755 if name in ['pebble','acceptance.test'] else 0o644)
  run('docker','cp',str(E/name),NAME+':/tmp/'+name)
 run('docker','exec','-d','-e','PEBBLE_VA_NOSLEEP=1','-e','PEBBLE_AUTHZREUSE=0',NAME,'/bin/sh','-c','exec /tmp/pebble -config /tmp/pebble.json > /tmp/pebble.log 2>&1')
 run('docker','exec',NAME,'/bin/sh','-c','for i in $(seq 1 40); do wget -q --no-check-certificate -O /tmp/directory.json https://127.0.0.1:14000/dir && exit 0; sleep 0.25; done; exit 1')
 p=run('docker','exec','-e','NOVAPANEL_REAL_NGINX=1','-e','NOVAPANEL_PEBBLE=1','-e','SSL_CERT_FILE=/tmp/server.pem','-w','/tmp/tests/integration',NAME,'/tmp/acceptance.test','-test.run','^TestRealPebbleNginxJobs$','-test.v','-test.timeout=240s',check=False)
 (E/'acceptance.log').write_bytes(p.stdout+p.stderr)
 run('docker','cp',NAME+':/tmp/pebble.log',str(E/'pebble.log'))
 inspect('after')
 (E/'manifest.json').write_text(json.dumps({'sha':sha,'base_source_archive_sha256':hashlib.sha256((E/'source.tar').read_bytes()).hexdigest(),'acceptance_binary_sha256':hashlib.sha256((E/'acceptance.test').read_bytes()).hexdigest(),'pebble_image':'ghcr.io/letsencrypt/pebble@sha256:ddf230642b1a584f519f32e347de1b05a6e4c1f6c35c1863b33effeab5f78199','exit':p.returncode,'network':'none; loopback only; HTTP01 validation port override 8080','scope':'no host mounts, services, public CA or ports; test-only additions to pinned source'},indent=2))
 print(p.stdout.decode()+p.stderr.decode());assert p.returncode==0
 assert run('git','rev-parse','HEAD').stdout.decode().strip()==sha
 run('git','diff','--exit-code','HEAD')
finally:
 if created:
  run('docker','cp',NAME+':/tmp/pebble.log',str(E/'pebble.log'),check=False)
  inspect('teardown');run('docker','rm','-f',NAME)
  assert run('docker','inspect',NAME,check=False).returncode!=0
  (E/'teardown.txt').write_text('Owned container absent after removal\n')
 print('Evidence: '+str(E))
