package verbs

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/sshkey"
	"novapanel/internal/store"
)

type sshkeyRunner struct{ root, site string }

func (r sshkeyRunner) Run(_ context.Context, prog string, _ []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" {
		return []byte(fmt.Sprintf("demo:x:1234:1234:novapanel-site=%s:%s:/usr/sbin/nologin\n", r.site, filepath.Join(r.root, "demo"))), nil, 0, nil
	}
	if opts.Uid != 1234 || opts.Gid != 1234 || len(opts.Groups) != 0 {
		return nil, nil, 1, fmt.Errorf("wrong credentials")
	}
	var req sshkey.Request
	if err := json.NewDecoder(opts.Stdin).Decode(&req); err != nil {
		return nil, nil, 1, err
	}
	res, err := sshkey.Apply(req)
	if err != nil {
		return nil, nil, 1, err
	}
	b, err := json.Marshal(res)
	return b, nil, 0, err
}

func TestSSHKeyConcurrentAdds(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "demo"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: filepath.Join(root, "demo", "public")}, store.Domain{ID: did, FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	a.Jobs = jobs.NewQueue(st, nil, nil)
	a.Exec = sshkeyRunner{root: root, site: sid}
	a.Cfg.SitesRoot = root
	keys := make([]string, 2)
	for i := range keys {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key, err := ssh.NewPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		keys[i] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, key := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			_, e := sshkeyAdd(ctx, a, &sshkeyAddArgs{SiteID: sid, PublicKey: k})
			errs <- e
		}(key)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	res, err := (sshkeyService{a}).run(ctx, sid, "list", "")
	if err != nil || len(res.Keys) != 2 {
		t.Fatalf("lost key: %+v %v", res, err)
	}
}
