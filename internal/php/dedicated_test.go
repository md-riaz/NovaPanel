package php

import (
	"strings"
	"testing"
)

func TestDedicatedMasterSinglePool(t *testing.T) {
	a, err := DedicatedMaster("siteone")
	if err != nil {
		t.Fatal(err)
	}
	b, err := DedicatedMaster("sitetwo")
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) || strings.Count(string(a), "include = ") != 1 || !strings.Contains(string(a), "/siteone.conf\n") || strings.ContainsAny(string(a), "*?") {
		t.Fatal("master must use exactly one site pool")
	}
	for _, bad := range []string{"../other", "foo\ninclude=/tmp/evil", ""} {
		if _, err := DedicatedMaster(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestDedicatedPoolIsolationConfiguration(t *testing.T) {
	for _, name := range []string{"siteone", "sitetwo"} {
		pool, err := RenderPool(name, 2)
		if err != nil {
			t.Fatal(err)
		}
		text := string(pool)
		for _, line := range []string{"[" + name + "]\n", "user = " + name + "\n", "group = " + name + "\n", "listen = /run/php/novapanel/" + name + ".sock\n", "listen.owner = www-data\n", "listen.group = www-data\n", "listen.mode = 0660\n"} {
			if !strings.Contains(text, line) {
				t.Fatalf("%s: missing %q", name, line)
			}
		}
		if strings.Contains(text, "listen.acl_users") || strings.Contains(text, "listen.acl_groups") {
			t.Fatal("socket ACL directives forbidden")
		}
	}
}
