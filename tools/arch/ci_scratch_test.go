package arch

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCIUnitScratchSocketBudget(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/go.yml")
	if err != nil {
		t.Fatal(err)
	}
	unit := strings.Split(string(workflow), "\n  integration:")[0]
	match := regexp.MustCompile(`scratch=\$\(mktemp -d "\$HOME/([^"/]+)"\)`).FindStringSubmatch(unit)
	if len(match) != 2 || !strings.HasSuffix(match[1], "XXXXXX") {
		t.Fatal("unit scratch must use a private mktemp directory directly under HOME")
	}
	// Linux sockaddr_un permits 107 pathname bytes. Budget Go's longest
	// affected test name, a maximum uint32 TempDir suffix, and its socket.
	for _, name := range []string{
		"TestCtlServiceUnitservice.restartphp8.3-fpm.service",
		"TestRealRPCRequestConnectionClosesOnMalformedReply",
	} {
		path := "/home/runner/" + match[1] + "/" + name + "4294967295/001/agent.sock"
		if len(path) > 107 {
			t.Errorf("unit scratch template %q produces a %d-byte socket path, limit 107", match[1], len(path))
		}
	}
	prefix := strings.TrimSuffix(match[1], "XXXXXX")
	if !strings.Contains(unit, `"$HOME"/`+prefix+`*) rm -rf -- "$TMPDIR" ;;`) {
		t.Fatal("unit scratch cleanup must match the provisioned HOME prefix")
	}
}
