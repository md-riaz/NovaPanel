package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectViolations(t *testing.T) {
	cases := []struct{ name, path, source, want string }{
		{"spawn", "internal/verbs/bad.go", `package verbs; import e "os/exec"; var _ = e.Command`, "os/exec"},
		{"shell", "internal/exec/bad.go", `package exec; func bad(){ Run("/bin/bash", []string{"-c", "echo bad"}) }`, "shell"},
		{"privileged", "cmd/panel-web/main.go", `package main; import _ "novapanel/internal/secret"`, "privileged"},
		{"unregistered", "internal/verbs/bad.go", `package verbs; var orphan = rpc.Verb{Name:"orphan", Run: run}`, "unregistered"},
		{"duplicate", "internal/verbs/bad.go", `package verbs; import "novapanel/internal/rpc"; func init(){rpc.Register(rpc.Verb{Name:"ping",Run:run});rpc.Register(rpc.Verb{Name:"ping",Run:run})}`, "duplicate"},
		{"orphan-handler", "internal/verbs/bad.go", `package verbs; func Forgotten(ctx context.Context, a *agent.Agent, args any)(any,error){return nil,nil}`, "unregistered handler"},
		{"root", "cmd/panel-web/main.go", `package main; func main(){}`, "root refusal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, tc.path)
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			problems := Check(root)
			found := false
			for _, p := range problems {
				if strings.Contains(p, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %q in %v", tc.want, problems)
			}
		})
	}
}
