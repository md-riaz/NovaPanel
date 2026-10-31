package firewall

import (
	"reflect"
	"testing"
)

func TestRenderRejectsHostileFields(t *testing.T) {
	base := Rule{Action: "allow", Proto: "tcp", Port: 443}
	for _, mutate := range []func(*Rule){
		func(r *Rule) { r.Action = "reset" }, func(r *Rule) { r.Proto = "tcp;disable" },
		func(r *Rule) { r.Port = 0 }, func(r *Rule) { r.Port = 65536 },
		func(r *Rule) { r.Source = "--force" }, func(r *Rule) { r.Source = "1.2.3.4/32\n" },
		func(r *Rule) { r.Comment = "$(reboot)" }, func(r *Rule) { r.Comment = "line\nnext" },
		func(r *Rule) { r.Action = "limit"; r.Proto = "udp" },
	} {
		r := base
		mutate(&r)
		if p, err := Render(Desired{Rules: []Rule{r}}, 22, ""); err == nil || p != nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	for _, d := range []Desired{{DefaultIncoming: "reset"}, {Rules: make([]Rule, 257)}} {
		if _, err := Render(d, 22, ""); err == nil {
			t.Fatal("accepted invalid desired")
		}
	}
	if _, err := Render(Desired{}, 0, ""); err == nil {
		t.Fatal("accepted invalid SSH port")
	}
	if _, err := Render(Desired{}, 22, "any"); err == nil {
		t.Fatal("accepted invalid pinned source")
	}
	if _, err := Render(Desired{Rules: []Rule{{Action: "allow", Proto: "udp", Port: 53, Source: "2001:db8::/32", Comment: "DNS service"}}}, 22, "192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
}

func TestRenderPinsSSHBeforeDeny(t *testing.T) {
	p, err := Render(Desired{Rules: []Rule{{Action: "deny", Proto: "tcp", Port: 2222}}}, 2222, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"insert", "1", "allow", "in", "proto", "tcp", "from", "any", "to", "any", "port", "2222"}
	if !reflect.DeepEqual(p[0], want) {
		t.Fatalf("SSH first: %v", p)
	}
	if !reflect.DeepEqual(p[1], []string{"default", "deny", "incoming"}) {
		t.Fatal(p)
	}
}
