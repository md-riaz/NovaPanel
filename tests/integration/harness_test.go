package integration

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// The root integration launcher must never execute production initialization commands.
func TestScratchLauncherUsesInjectedDaemon(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	source, err := os.ReadFile(strings.TrimSuffix(file, "harness_test.go") + "agent_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"\"test\", \"-c\"", "-test.run=^TestSafeDaemonHelper$", "NOVAPANEL_SAFE_HELPER=1", "NOVAPANEL_SAFE_CONFIG="} {
		if !strings.Contains(string(source), required) {
			t.Errorf("scratch launcher must use the injected test-only daemon: missing %q", required)
		}
	}
	if strings.Contains(string(source), "[]string{\"--config\", config}") {
		t.Error("scratch launcher still invokes the production daemon")
	}
}
