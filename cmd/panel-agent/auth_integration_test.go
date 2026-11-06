package main

import (
	"bytes"
	"context"
	"encoding/json"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionDaemonStoredSessionResolver(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch peer gate requires root")
	}
	a := scratchAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveScratch(ctx, a) }()
	waitAgent(t, a, done)
	root, e := rpc.Dial(a.Cfg.SocketPath)
	if e != nil {
		t.Fatal(e)
	}
	_, e = root.Call(ctx, "user.create", map[string]string{"username": "scratchreader", "email": "scratch@example.test", "role": "read_only", "password": "scratch-password"}, "")
	root.Close()
	if e != nil {
		t.Fatal(e)
	}
	probe := `import base64,hashlib,hmac,json,os,socket,struct,sys,time,urllib.parse
os.setgroups([]);os.setgid(0);os.setuid(65534)
s=socket.socket(socket.AF_UNIX);s.settimeout(5);s.connect("agent.sock");f=s.makefile("rb")
def call(verb,args,token="",code=None):
 s.sendall((json.dumps({"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":verb,"args":args,"session":token})+"\n").encode())
 r=json.loads(f.readline())
 if code: assert not r["ok"] and r["code"]==code,(verb,r);return
 assert r["ok"],(verb,r)
 return r["data"]
call("agent.info",{},code="unauthenticated")
anon=call("session.start",{})["session"]
token=call("session.login",{"user":"scratchreader","password":"scratch-password"},anon)["session"]
call("session.whoami",{},anon,"unauthenticated")
assert call("session.whoami",{},token)["role"]=="read_only"
call("service.restart",{"unit":"nginx.service"},token,"forbidden")
uri=call("mfa.enroll",{},token)["otpauth_uri"]
secret=urllib.parse.parse_qs(urllib.parse.urlparse(uri).query)["secret"][0]
key=base64.b32decode(secret+"="*((-len(secret))%8))
step=int(time.time())//30
v=hmac.new(key,struct.pack(">Q",step),hashlib.sha1).digest();offset=v[-1]&15
proof=str((struct.unpack(">I",v[offset:offset+4])[0]&0x7fffffff)%1000000).zfill(6)
codes=call("mfa.confirm",{"totp":proof},token)["backup_codes"]
call("session.reauth",{"totp":proof},token,"unauthenticated")
fresh=call("session.reauth",{"totp":codes[0]},token)["session"]
call("session.whoami",{},token,"unauthenticated")
call("mfa.disable",{"totp":codes[0]},fresh,"unauthenticated")
call("mfa.disable",{"totp":codes[1]},fresh)
call("session.logout",{},fresh)
call("session.whoami",{},fresh,"unauthenticated")
print("REAL_WEB_PEER_AUTH_MFA_OK")
`
	out, stderr, code, e := (exec.Real{}).Run(ctx, "/usr/bin/python3", []string{"-"}, exec.RunOpts{Dir: filepath.Dir(a.Cfg.SocketPath), Stdin: bytes.NewBufferString(probe)})
	if e != nil || code != 0 || !strings.Contains(string(out), "REAL_WEB_PEER_AUTH_MFA_OK") {
		t.Fatalf("peer probe: %s %s %d %v", out, stderr, code, e)
	}
	events, e := os.ReadFile(filepath.Join(a.Cfg.LogDir, "audit.log"))
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"scratch-password", "otpauth://", "backup_codes", "nps_"} {
		if strings.Contains(string(events), s) {
			t.Fatalf("audit leaked %q", s)
		}
	}
	var lines int
	for _, line := range bytes.Split(bytes.TrimSpace(events), []byte("\n")) {
		var v map[string]any
		if json.Unmarshal(line, &v) != nil {
			t.Fatal("audit format")
		}
		lines++
	}
	if lines == 0 {
		t.Fatal("no durable audit")
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
}
