package wordpress

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"novapanel/internal/rpcerr"
)

func TestInstallInputRefusals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*InstallInput)
	}{
		{"root uid", func(i *InstallInput) { i.UID = 0 }},
		{"root gid", func(i *InstallInput) { i.GID = 0 }},
		{"unsupported locale", func(i *InstallInput) { i.Locale = "fr_FR" }},
		{"missing locale", func(i *InstallInput) { i.Locale = "" }},
		{"bad database id", func(i *InstallInput) { i.DatabaseID = "d_arbitrary" }},
		{"bad user id", func(i *InstallInput) { i.DatabaseUserID = "01arz3ndektsv4rrffq69g5faw" }},
	}
	for _, s := range []string{"", "/", "relative", "/srv/../sites", "/srv//sites", "/srv/sites/", "/srv/sites\n", "/srv/sites\\bad", "/srv/sites\x00bad", "/srv/a b", "/srv/a--require=bad", "/srv/a;bad", "/srv/é"} {
		s := s
		tests = append(tests, struct {
			name   string
			change func(*InstallInput)
		}{"root path", func(i *InstallInput) { i.SitesRoot = s }})
	}
	for _, s := range []string{"root", "www-data", "../site", "--require=bad", "ab\n", "a", "Bad"} {
		s := s
		tests = append(tests, struct {
			name   string
			change func(*InstallInput)
		}{"site", func(i *InstallInput) { i.SiteName = s }})
	}
	for _, s := range []string{"", "https://example.test", "example.test/path", "example.test?x", "example.test#x", "user@example.test", "example.test:443", "--require=bad", "example.test\n", "a..test"} {
		s := s
		tests = append(tests, struct {
			name   string
			change func(*InstallInput)
		}{"domain", func(i *InstallInput) { i.PrimaryDomain = s }})
	}
	for _, s := range []string{"", strings.Repeat("x", 201), "bad\x00title", "bad\ntitle", "bad\x7ftitle", string([]byte{0xff})} {
		s := s
		tests = append(tests, struct {
			name   string
			change func(*InstallInput)
		}{"title", func(i *InstallInput) { i.Administrator.Title = s }})
	}
	for _, s := range []string{"", "-admin", "1admin", strings.Repeat("a", 61), "admin name", "admin\n", "é"} {
		s := s
		tests = append(tests, struct {
			name   string
			change func(*InstallInput)
		}{"username", func(i *InstallInput) { i.Administrator.Username = s }})
	}
	for _, s := range []string{"", "Name <a@example.test>", "a@example.test,b@example.test", "a@example.test\n", "a\x00@example.test", " a@example.test", strings.Repeat("x", 243) + "@example.test"} {
		s := s
		tests = append(tests, struct {
			name   string
			change func(*InstallInput)
		}{"email", func(i *InstallInput) { i.Administrator.Email = s }})
	}
	for n, tt := range tests {
		t.Run(fmt.Sprintf("%s_%d", tt.name, n), func(t *testing.T) {
			in := validInput()
			tt.change(&in)
			p, err := BuildInstall(in, artifactEvidence(t))
			if err == nil {
				t.Fatal("accepted invalid input")
			}
			if coded, ok := err.(*rpcerr.CodedError); !ok || coded.Code != "invalid_argument" || !reflect.DeepEqual(p, Plan{}) {
				t.Fatalf("invalid refusal: %v %#v", err, p)
			}
		})
	}
}

func TestAdministratorBoundaryValues(t *testing.T) {
	in := validInput()
	in.Administrator.Title = strings.Repeat("é", 100)
	in.Administrator.Username = "A" + strings.Repeat("x", 59)
	p, err := BuildInstall(in, artifactEvidence(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Steps[4].Args, "\x00"), "--title="+in.Administrator.Title) {
		t.Fatal("title changed")
	}
	// Option-shaped data remains exactly one named value, never another flag.
	in.Administrator.Title = "--require=/evil ' ; $(touch nope)"
	p, err = BuildInstall(in, artifactEvidence(t))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range p.Steps[4].Args {
		if a == "--title="+in.Administrator.Title {
			found = true
		}
	}
	if !found {
		t.Fatal("title was split or normalized")
	}
}
