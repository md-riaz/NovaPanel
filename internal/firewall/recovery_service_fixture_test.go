package firewall

import (
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"testing"
)

func TestRecoveryServiceFilesystemGate(t *testing.T) {
	for _, attack := range []string{"valid", "daemon", "user", "wrong-id", "forged-env", "membership-symlink", "membership-fifo", "membership-mode", "membership-gid", "oversized", "type", "missing-pid", "root-replaced", "group-replaced", "manager-replaced", "proc-magic", "cgroup-magic", "membership-changed"} {
		t.Run(attack, func(t *testing.T) {
			if os.Geteuid() != 0 || os.Getegid() != 0 {
				t.Skip("service metadata fixtures require root UID/GID")
			}
			base := custodyTempDir(t)
			paths := map[string]string{"/run/systemd/system": base + "/manager", "/proc/" + strconv.Itoa(os.Getpid()): base + "/proc", "/sys/fs/cgroup": base + "/root", "/sys/fs/cgroup/system.slice/novapanel-fwrevert-" + custodyID + ".service": base + "/group"}
			for _, p := range paths {
				mustCustody(t, os.Mkdir(p, 0700))
			}
			proc := paths["/proc/"+strconv.Itoa(os.Getpid())]
			group := paths["/sys/fs/cgroup/system.slice/novapanel-fwrevert-"+custodyID+".service"]
			membership := "0::/system.slice/novapanel-fwrevert-" + custodyID + ".service\n"
			switch attack {
			case "daemon":
				membership = "0::/system.slice/novapanel-agent.service\n"
			case "user":
				membership = "0::/user.slice/x\n"
			case "wrong-id":
				membership = "0::/system.slice/novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAW.service\n"
			case "forged-env":
				t.Setenv("INVOCATION_ID", "forged")
				membership = "0::/\n"
			case "oversized":
				membership = string(make([]byte, 4097))
			}
			mustCustody(t, os.WriteFile(proc+"/cgroup", []byte(membership), 0600))
			mustCustody(t, os.WriteFile(group+"/cgroup.type", []byte("domain\n"), 0600))
			mustCustody(t, os.WriteFile(group+"/cgroup.procs", []byte(strconv.Itoa(os.Getpid())+"\n"), 0600))
			switch attack {
			case "membership-symlink":
				mustCustody(t, os.Rename(proc+"/cgroup", proc+"/target"))
				mustCustody(t, os.Symlink("target", proc+"/cgroup"))
			case "membership-fifo":
				mustCustody(t, os.Remove(proc+"/cgroup"))
				mustCustody(t, unix.Mkfifo(proc+"/cgroup", 0600))
			case "membership-mode":
				mustCustody(t, os.Chmod(proc+"/cgroup", 0660))
			case "membership-gid":
				mustCustody(t, os.Chown(proc+"/cgroup", 0, 1))
			case "type":
				mustCustody(t, os.WriteFile(group+"/cgroup.type", []byte("threaded\n"), 0600))
			case "missing-pid":
				mustCustody(t, os.WriteFile(group+"/cgroup.procs", nil, 0600))
			}
			calls := map[string]int{}
			open := func(p string, private bool) (*os.File, error) {
				calls[p]++
				if calls[p] == 2 && ((attack == "root-replaced" && p == "/sys/fs/cgroup") || (attack == "group-replaced" && paths[p] == group) || (attack == "manager-replaced" && p == "/run/systemd/system")) {
					mustCustody(t, os.Rename(paths[p], paths[p]+".old"))
					mustCustody(t, os.Mkdir(paths[p], 0700))
				}
				return strictRuntimeDir(paths[p], private)
			}
			// Fixture filesystem magic replaces only the kernel filesystem-type probe.
			n := 0
			statfs := func(_ int, s *unix.Statfs_t) error {
				n++
				s.Type = unix.PROC_SUPER_MAGIC
				if n == 2 {
					s.Type = unix.CGROUP2_SUPER_MAGIC
				}
				if attack == "proc-magic" && n == 1 || attack == "cgroup-magic" && n == 2 {
					s.Type = 0
				}
				if attack == "membership-changed" && n == 2 {
					mustCustody(t, os.WriteFile(proc+"/cgroup", []byte("0::/other\n"), 0600))
				}
				return nil
			}
			e := admitRecoveryServiceWith(custodyID, open, statfs)
			if (e == nil) != (attack == "valid") {
				t.Fatalf("gate result %v", e)
			}
		})
	}
}
