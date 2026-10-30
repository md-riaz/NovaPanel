package integration

import (
	"os"
	"strings"
	"testing"
)

func TestPackagingContracts(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, e := os.ReadFile("../../packaging/" + path)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	agent := read("systemd/novapanel-agent.service")
	web := read("systemd/novapanel-web.service")
	for _, s := range []string{"NoNewPrivileges=no", "ProtectKernelTunables=yes", "SystemCallFilter=@system-service @privileged", "SystemCallFilter=~@obsolete @swap", "CAP_LINUX_IMMUTABLE", "TimeoutStopSec=30s"} {
		if !strings.Contains(agent, s) {
			t.Errorf("agent missing %s", s)
		}
	}
	for _, s := range []string{"RuntimeDirectory=", "StateDirectory=", "LogsDirectory="} {
		if strings.Contains(agent, s) {
			t.Errorf("forbidden %s", s)
		}
	}
	for _, s := range []string{"User=novapanel", "NoNewPrivileges=yes", "ProtectSystem=strict", "ReadWritePaths=/run/novapanel"} {
		if !strings.Contains(web, s) {
			t.Errorf("web missing %s", s)
		}
	}
	for _, unit := range []string{agent, web} {
		if !strings.Contains(strings.Split(unit, "[Service]")[0], "StartLimitBurst=20") {
			t.Error("start limit must be in Unit")
		}
	}
	tmp := read("tmpfiles/novapanel.conf")
	for _, s := range []string{"d /run/novapanel 0755 root root -", "d /var/lib/novapanel 0700 root root -", "d /var/log/novapanel 0750 root novapanel -", "d /var/www/acme 0755 root root -"} {
		if !strings.Contains(tmp, s) {
			t.Errorf("missing tmpfile %s", s)
		}
	}
	if !strings.Contains(read("sysusers/novapanel.conf"), `u novapanel - "NovaPanel web"`) {
		t.Error("missing web account")
	}
}
