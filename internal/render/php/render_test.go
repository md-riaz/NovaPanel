package php_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"novapanel/internal/rpcerr"

	"novapanel/internal/render/php"
)

func TestPhase4PHPRender(t *testing.T) {
	t.Run("custom limits", func(t *testing.T) {
		limits := php.Limits{MemoryMB: 512, UploadMB: 128, MaxExecSec: 300}
		got, err := php.Render(php.Desired{Name: "custom-site", Type: "php", Version: "8.3", UID: 1003, GID: 1004, Limits: &limits})
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range []string{"php_admin_value[memory_limit] = 512M\n", "php_admin_value[post_max_size] = 128M\n", "php_admin_value[upload_max_filesize] = 128M\n", "php_admin_value[max_execution_time] = 300\n"} {
			if !strings.Contains(string(got), line) {
				t.Fatalf("missing %q in %s", line, got)
			}
		}
		limits.UploadMB = 513
		got, err = php.Render(php.Desired{Name: "custom-site", Type: "wordpress", Version: "8.3", UID: 1003, GID: 1004, Limits: &limits})
		var coded *rpcerr.CodedError
		if !errors.As(err, &coded) || coded.Code != "invalid_argument" || len(got) != 0 {
			t.Fatalf("invalid render limits: %s, %v", got, err)
		}
	})
	for _, kind := range []string{"php", "wordpress"} {
		t.Run(kind, func(t *testing.T) {
			d := php.Desired{Name: "example-site", Type: kind, Version: "8.3", UID: 1001, GID: 1002}
			got, err := php.Render(d)
			if err != nil {
				t.Fatal(err)
			}
			const want = `[example-site]
user = 1001
group = 1002
listen = /run/php/novapanel/example-site.sock
listen.owner = www-data
listen.group = www-data
listen.mode = 0660
pm = ondemand
pm.max_children = 2
pm.process_idle_timeout = 10s
pm.max_requests = 500
php_admin_value[cgi.fix_pathinfo] = 0
php_admin_value[error_log] = /var/log/novapanel/example-site/php-error.log
php_admin_flag[log_errors] = on
php_admin_value[memory_limit] = 256M
php_admin_value[post_max_size] = 64M
php_admin_value[upload_max_filesize] = 64M
php_admin_value[max_execution_time] = 60
`
			if string(got) != want {
				t.Fatalf("pool mismatch:\n%s\nwant:\n%s", got, want)
			}
			again, err := php.Render(d)
			if err != nil || !bytes.Equal(got, again) {
				t.Fatalf("non-deterministic render: %v", err)
			}
		})
	}
}

func TestPhase4PHPRenderRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*php.Desired)
	}{
		{"static", func(d *php.Desired) { d.Type = "static" }},
		{"unknown type", func(d *php.Desired) { d.Type = "PHP" }},
		{"empty type", func(d *php.Desired) { d.Type = "" }},
		{"empty version", func(d *php.Desired) { d.Version = "" }},
		{"unsupported version", func(d *php.Desired) { d.Version = "8.4" }},
		{"version injection", func(d *php.Desired) { d.Version = "8.3\nlisten.acl_users = attacker" }},
		{"root uid", func(d *php.Desired) { d.UID = 0 }},
		{"root gid", func(d *php.Desired) { d.GID = 0 }},
		{"invalid uid sentinel", func(d *php.Desired) { d.UID = ^uint32(0) }},
		{"invalid gid sentinel", func(d *php.Desired) { d.GID = ^uint32(0) }},
	}
	for _, name := range []string{"", "a", "root", "www-data", "novapanel", "../escape", "/etc/php", "other/site", "site_name", "Site", strings.Repeat("a", 32), "site\x00", "site\n[other]", "site;user=root", "site$USER", "site\rlisten=evil", "site\\escape", "site\""} {
		cases = append(cases, struct {
			name   string
			change func(*php.Desired)
		}{"name " + name, func(d *php.Desired) { d.Name = name }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := php.Desired{Name: "example-site", Type: "php", Version: "8.3", UID: 1001, GID: 1002}
			tc.change(&d)
			got, err := php.Render(d)
			var coded *rpcerr.CodedError
			if !errors.As(err, &coded) || coded.Code != "invalid_argument" || len(got) != 0 {
				t.Fatalf("got %q, %v; want no bytes and invalid_argument", got, err)
			}
		})
	}
}
