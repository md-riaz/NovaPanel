package firewall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryServiceMembership(t *testing.T) {
	for _, s := range []string{"0::/system.slice/novapanel-fwrevert-" + custodyID + ".service\n", "0::/system.slice/novapanel-agent.service\n", "0::/user.slice/user-0.slice/novapanel-fwrevert-" + custodyID + ".service\n", "1:name=systemd:/system.slice/novapanel-fwrevert-" + custodyID + ".service\n", "0::/system.slice/novapanel-fwrevert-" + custodyID + ".service\n0::/other\n"} {
		p, e := recoveryMembership([]byte(s), custodyID)
		want := s == "0::/system.slice/novapanel-fwrevert-"+custodyID+".service\n"
		if (e == nil) != want || (want && p != "/system.slice/novapanel-fwrevert-"+custodyID+".service") {
			t.Fatalf("%q: %q %v", s, p, e)
		}
	}
}
func TestRuntimeLayoutRefusesUnsafe(t *testing.T) {
	for _, attack := range []string{"mode", "symlink", "lock-gid", "lock-size", "directory-gid"} {
		t.Run(attack, func(t *testing.T) {
			l, r, f := runtimeFixture(t, true)
			switch attack {
			case "mode":
				mustCustody(t, os.Chmod(l.root, 0770))
			case "symlink":
				mustCustody(t, os.Rename(l.root+"/snapshots", l.root+"/moved"))
				mustCustody(t, os.Symlink("moved", l.root+"/snapshots"))
			case "lock-gid":
				mustCustody(t, os.Chown(l.root+"/journal/lock", 0, 1))
			case "directory-gid":
				mustCustody(t, os.Chown(l.root+"/snapshots", 0, 1))
			case "lock-size":
				mustCustody(t, os.WriteFile(filepath.Join(l.root, "journal/lock"), []byte("x"), 0600))
			}
			if recoverInstalled(t.Context(), r.ID, l, func() time.Time { return time.Unix(r.Deadline, 0) }, f) == nil || len(f.calls) != 0 {
				t.Fatal("unsafe layout admitted")
			}
		})
	}
}
