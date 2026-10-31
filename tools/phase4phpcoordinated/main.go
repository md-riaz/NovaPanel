// Command phase4phpcoordinated exercises the real coordinator inside the owned lab container.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/render/nginx"
	"novapanel/internal/render/php"
)

const base = "/tmp/coordinated"

var errInjected = errors.New("errInjected failure")

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func write(path string, b []byte) { must(os.WriteFile(path, b, 0600)) }

type lab struct {
	phase  string
	failed bool
	events []string
	cancel context.CancelFunc
}

func (l *lab) command(ctx context.Context, prog string, args ...string) ([]byte, []byte, int, error) {
	return (runner.Real{}).Run(ctx, prog, args, runner.RunOpts{Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}})
}
func (l *lab) hit(event string) error {
	l.events = append(l.events, event)
	if l.phase == event && !l.failed {
		l.failed = true
		l.cancel()
		return errInjected
	}
	return nil
}
func (l *lab) Run(ctx context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	event := "nginx.validate"
	if strings.Contains(prog, "php-fpm") {
		event = "fpm.validate"
	}
	if args[0] == "reload" {
		event = "nginx.reload"
		if args[1] == "php8.3-fpm" {
			event = "fpm.reload"
		}
	}
	var out, stderr []byte
	var code int
	var err error
	switch event {
	case "fpm.validate":
		out, stderr, code, err = l.command(ctx, "/usr/local/sbin/php-fpm", "-t", "-y", base+"/fpm.ini")
	case "nginx.validate":
		b, e := os.ReadFile(base + "/nginx/sites-enabled/lab-site.conf")
		if e != nil {
			return nil, nil, 1, e
		}
		s := strings.ReplaceAll(string(b), "listen 80;", "listen 127.0.0.1:8081;")
		s = strings.ReplaceAll(s, "    listen [::]:80;\n", "")
		write(base+"/nginx.ini", []byte("pid "+base+"/nginx.pid; error_log "+base+"/nginx.log; events {} http { access_log off; client_body_temp_path /tmp/client; fastcgi_temp_path /tmp/fastcgi; proxy_temp_path /tmp/proxy; uwsgi_temp_path /tmp/uwsgi; scgi_temp_path /tmp/scgi;\n"+s+"\n}\n"))
		out, stderr, code, err = l.command(ctx, "/tmp/nginx/usr/sbin/nginx", "-t", "-c", base+"/nginx.ini", "-p", "/tmp/")
	case "fpm.reload":
		if b, e := os.ReadFile(base + "/fpm.pid"); e == nil {
			out, stderr, code, err = l.command(ctx, "/bin/kill", "-USR2", strings.TrimSpace(string(b)))
		} else {
			out, stderr, code, err = l.command(ctx, "/usr/local/sbin/php-fpm", "-D", "-y", base+"/fpm.ini")
		}
	case "nginx.reload":
		argv := []string{"-c", base + "/nginx.ini", "-p", "/tmp/"}
		if _, e := os.Stat(base + "/nginx.pid"); e == nil {
			argv = append(argv, "-s", "reload")
		}
		out, stderr, code, err = l.command(ctx, "/tmp/nginx/usr/sbin/nginx", argv...)
	}
	if err != nil || code != 0 {
		fmt.Fprintf(os.Stderr, "%s: %s %v\n", event, stderr, err)
		return out, stderr, code, err
	}
	if err = l.hit(event); err != nil {
		return out, stderr, 1, err
	}
	return out, stderr, code, nil
}
func health(ctx context.Context, want string) error {
	client := http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:8081/index.php", nil)
		req.Host = "lab.invalid"
		res, err := client.Do(req)
		if err == nil {
			b, e := io.ReadAll(res.Body)
			res.Body.Close()
			if e == nil && res.StatusCode == 200 && string(b) == want {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("service did not serve %q", want)
}
func main() {
	if os.Geteuid() != 33 {
		panic("lab requires UID 33")
	}
	for _, dir := range []string{"/pool", "/nginx/sites-available", "/nginx/sites-enabled", "/old", "/new"} {
		must(os.MkdirAll(base+dir, 0700))
	}
	write(base+"/old/index.php", []byte("<?php echo 'old:'.ini_get('memory_limit');"))
	write(base+"/new/index.php", []byte("<?php echo 'new:'.ini_get('memory_limit');"))
	write(base+"/fpm.ini", []byte("[global]\nerror_log="+base+"/fpm.log\npid="+base+"/fpm.pid\ninclude="+base+"/pool/*.conf\n"))
	l := &lab{}
	p := &php.Applier{Root: base + "/pool", Runner: l, Probe: func(ctx context.Context, d php.Desired) error { return l.hit("fpm.probe") }}
	a := &nginx.Applier{Root: base + "/nginx", Runner: l, Probe: func(ctx context.Context, d nginx.Desired) error {
		want := "old:256M"
		if strings.HasSuffix(d.Docroot, "/new") {
			want = "new:512M"
		}
		if err := health(ctx, want); err != nil {
			return err
		}
		return l.hit("nginx.probe")
	}}
	old := nginx.Desired{Name: "lab-site", Domains: []string{"lab.invalid"}, Docroot: base + "/old", ACMERoot: "/tmp/lab/acme", PHP: &php.Desired{Name: "lab-site", Type: "php", Version: "8.3", UID: 33, GID: 33}}
	must(a.ApplyPHP(context.Background(), p, old, func(context.Context) error { return nil }, func(context.Context) error { panic("unexpected restored probe") }))
	files := []string{base + "/pool/lab-site.conf", base + "/pool/lab-site.conf.lkg", base + "/nginx/sites-available/lab-site.conf", base + "/nginx/sites-available/lab-site.conf.lkg"}
	before := map[string][]byte{}
	for _, f := range files {
		b, e := os.ReadFile(f)
		must(e)
		before[f] = b
	}
	results := map[string]any{}
	for _, phase := range []string{"fpm.validate", "nginx.validate", "fpm.reload", "fpm.probe", "nginx.reload", "nginx.probe", "commit"} {
		ctx, cancel := context.WithCancel(context.Background())
		l.cancel = cancel
		l.phase = phase
		l.failed = false
		l.events = nil
		next := old
		pool := *old.PHP
		pool.Limits = &php.Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}
		next.PHP = &pool
		next.Docroot = base + "/new"
		restored := 0
		err := a.ApplyPHP(ctx, p, next, func(context.Context) error { return l.hit("commit") }, func(c context.Context) error {
			restored++
			if c.Err() != nil {
				return c.Err()
			}
			deadline, ok := c.Deadline()
			if !ok || time.Until(deadline) > 10*time.Second {
				return errors.New("unbounded recovery")
			}
			return health(c, "old:256M")
		})
		cancel()
		if err == nil || !l.failed {
			panic(fmt.Sprint("failure not exercised: ", phase, err))
		}
		want := 1
		if strings.HasSuffix(phase, "validate") {
			want = 0
		}
		if restored != want {
			panic("restored probe count " + strconv.Itoa(restored))
		}
		for _, f := range files {
			b, e := os.ReadFile(f)
			must(e)
			if !bytes.Equal(b, before[f]) {
				panic("old bytes lost: " + f)
			}
		}
		target, e := os.Readlink(base + "/nginx/sites-enabled/lab-site.conf")
		must(e)
		if target != "../sites-available/lab-site.conf" {
			panic("enabled target changed")
		}
		must(health(context.Background(), "old:256M"))
		results[phase] = map[string]any{"status": "PASS", "restored_probes": restored, "events": append([]string{}, l.events...), "old_bytes_and_health": "PASS"}
	}
	// Keep command paths confined; no production service manager is called.
	must(json.NewEncoder(os.Stdout).Encode(results))
}
