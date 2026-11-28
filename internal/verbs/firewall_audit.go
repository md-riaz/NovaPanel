package verbs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
)

func AuditFirewallReverts(a *agent.Agent) error {
	dir := filepath.Dir(a.Cfg.DBPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "fw-reverted-") {
			continue
		}
		pending := strings.TrimPrefix(entry.Name(), "fw-reverted-")
		if !id.Valid(pending) || !entry.Type().IsRegular() {
			return errors.New("invalid firewall revert marker")
		}
		path := filepath.Join(dir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(b) != pending+"\n" {
			return errors.New("invalid firewall revert marker")
		}
		if err = a.Audit.Record(audit.Event{Actor: "agent", Role: "owner", Verb: "firewall.revert", ObjectKey: "firewall", Outcome: "ok", Detail: map[string]any{"pending_id": pending, "source": "deadman"}}); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
