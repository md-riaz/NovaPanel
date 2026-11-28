package firewall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"novapanel/internal/exec"
)

var ruleFiles = []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"}

// Snapshot captures the installed rules and diagnostic output into a new private directory.
// The caller supplies trusted, private directories and serializes firewall mutations.
func Snapshot(ctx context.Context, runner exec.Runner, rulesDir, snapshotDir string) error {
	if runner == nil {
		return errors.New("missing firewall runner")
	}
	if err := os.Mkdir(snapshotDir, 0700); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(snapshotDir, "ufw"), 0700); err != nil {
		return err
	}
	names, err := snapshotRuleNames(rulesDir)
	if err != nil {
		return err
	}
	for _, name := range names {
		b, err := readRegular(filepath.Join(rulesDir, name))
		if err != nil {
			return err
		}
		if err = writeSnapshot(filepath.Join(snapshotDir, "ufw", name), b); err != nil {
			return err
		}
	}
	defaults, err := readRegular(filepath.Join(filepath.Dir(rulesDir), "default", "ufw"))
	if err != nil {
		return err
	}
	if err = writeSnapshot(filepath.Join(snapshotDir, "default-ufw"), defaults); err != nil {
		return err
	}
	status, err := firewallCommand(ctx, runner, "/usr/sbin/ufw", "status", "numbered")
	if err != nil {
		return err
	}
	first, _, _ := strings.Cut(string(status), "\n")
	enabled := "0\n"
	switch first {
	case "Status: active":
		enabled = "1\n"
	case "Status: inactive":
	default:
		return errors.New("invalid ufw status")
	}
	for _, item := range []struct {
		name, program string
		args          []string
	}{
		{"iptables-save", "/usr/sbin/iptables-save", nil}, {"ip6tables-save", "/usr/sbin/ip6tables-save", nil},
	} {
		b, e := firewallCommand(ctx, runner, item.program, item.args...)
		if e != nil {
			return e
		}
		if e = writeSnapshot(filepath.Join(snapshotDir, item.name), b); e != nil {
			return e
		}
	}
	if err = writeSnapshot(filepath.Join(snapshotDir, "ufw-status-numbered"), status); err != nil {
		return err
	}
	if err = writeSnapshot(filepath.Join(snapshotDir, "enabled"), []byte(enabled)); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(snapshotDir, "ufw"), snapshotDir, filepath.Dir(snapshotDir)} {
		d, e := os.Open(path)
		if e != nil {
			return e
		}
		e = d.Sync()
		d.Close()
		if e != nil {
			return e
		}
	}
	return nil
}

func snapshotRuleNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".rules") {
			if !entry.Type().IsRegular() {
				return nil, errors.New("unsafe firewall file")
			}
			names = append(names, entry.Name())
		}
	}
	for _, required := range ruleFiles {
		found := false
		for _, name := range names {
			if name == required {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("missing firewall rule file")
		}
	}
	return names, nil
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, errors.New("unsafe firewall file")
	}
	return os.ReadFile(path)
}
func writeSnapshot(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}
func firewallCommand(ctx context.Context, runner exec.Runner, program string, args ...string) ([]byte, error) {
	out, _, code, err := runner.Run(ctx, program, args, exec.RunOpts{Env: []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}, MaxOutput: 4<<20 + 1})
	if err != nil {
		return nil, err
	}
	if code != 0 || len(out) > 4<<20 {
		return nil, fmt.Errorf("firewall command failed: %s", program)
	}
	return out, nil
}

// Restore writes the captured rules before restoring the prior enabled state.
func Restore(ctx context.Context, runner exec.Runner, rulesDir, snapshotDir string) error {
	if runner == nil {
		return errors.New("missing firewall runner")
	}
	enabled, err := readRegular(filepath.Join(snapshotDir, "enabled"))
	if err != nil {
		return err
	}
	if string(enabled) != "0\n" && string(enabled) != "1\n" {
		return errors.New("invalid firewall snapshot")
	}
	names, err := snapshotRuleNames(filepath.Join(snapshotDir, "ufw"))
	if err != nil {
		return err
	}
	data := make([][]byte, len(names))
	for i, name := range names {
		data[i], err = readRegular(filepath.Join(snapshotDir, "ufw", name))
		if err != nil {
			return err
		}
	}
	for i, name := range names {
		tmp, e := os.CreateTemp(rulesDir, ".novapanel-fw-")
		if e != nil {
			return e
		}
		path := tmp.Name()
		_, e = tmp.Write(data[i])
		if e == nil {
			e = tmp.Chmod(0640)
		}
		if e == nil {
			e = tmp.Sync()
		}
		e = errors.Join(e, tmp.Close())
		if e == nil {
			e = os.Rename(path, filepath.Join(rulesDir, name))
		}
		if e != nil {
			os.Remove(path)
			return e
		}
	}
	d, err := os.Open(rulesDir)
	if err != nil {
		return err
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return err
	}
	defaults, err := readRegular(filepath.Join(snapshotDir, "default-ufw"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		dir := filepath.Join(filepath.Dir(rulesDir), "default")
		info, e := os.Lstat(filepath.Join(dir, "ufw"))
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("unsafe firewall default file")
		}
		tmp, e := os.CreateTemp(dir, ".novapanel-fw-default-")
		if e != nil {
			return e
		}
		path := tmp.Name()
		_, e = tmp.Write(defaults)
		if e == nil {
			e = tmp.Chmod(info.Mode().Perm())
		}
		if e == nil {
			e = tmp.Sync()
		}
		e = errors.Join(e, tmp.Close())
		if e == nil {
			e = os.Rename(path, filepath.Join(dir, "ufw"))
		}
		if e != nil {
			os.Remove(path)
			return e
		}
		d, e := os.Open(dir)
		if e != nil {
			return e
		}
		e = errors.Join(d.Sync(), d.Close())
		if e != nil {
			return e
		}
	}
	if string(enabled) == "0\n" {
		_, err = firewallCommand(ctx, runner, "/usr/sbin/ufw", "--force", "disable")
		return err
	}
	if _, err = firewallCommand(ctx, runner, "/usr/sbin/ufw", "--force", "enable"); err != nil {
		return err
	}
	_, err = firewallCommand(ctx, runner, "/usr/sbin/ufw", "reload")
	return err
}
