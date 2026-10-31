package firewall

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestRuntimeRecoveryFailureReplay(t *testing.T) {
	for _, fault := range []string{"early", "wrong-id", "tamper", "command-first", "command-second", "marker"} {
		t.Run(fault, func(t *testing.T) {
			l, r, f := runtimeFixture(t, true)
			now := func() time.Time { return time.Unix(r.Deadline, 0) }
			selected := r.ID
			switch fault {
			case "early":
				now = func() time.Time { return time.Unix(r.Deadline-1, 0) }
			case "wrong-id":
				selected = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
			case "tamper":
				mustCustody(t, os.WriteFile(l.root+"/snapshots/"+r.ID+"/ufw/user.rules", []byte("tampered"), 0600))
			case "command-first":
				f.fail = true
			case "command-second":
				f.check = func() {
					if len(f.calls) == 1 {
						f.fail = true
					}
				}
			case "marker":
				f.check = func() { f.check = nil; mustCustody(t, os.Mkdir(l.root+"/restore/markers/"+r.ID+".json.next", 0700)) }
			}
			if recoverInstalled(t.Context(), selected, l, now, f) == nil {
				t.Fatal("fault succeeded")
			}
			j, e := OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			state, e := j.read()
			mustCustody(t, e)
			mustCustody(t, j.Close())
			expected := "reverting"
			if fault == "early" || fault == "wrong-id" {
				expected = "armed"
			}
			if state.State != expected || state.ID != r.ID || state.SnapshotSHA256 != r.SnapshotSHA256 {
				t.Fatal(state)
			}
			if fault == "tamper" {
				mustCustody(t, os.WriteFile(l.root+"/snapshots/"+r.ID+"/ufw/user.rules", custodyInput().Rules["user.rules"], 0600))
			}
			if fault == "marker" {
				mustCustody(t, os.Remove(l.root+"/restore/markers/"+r.ID+".json.next"))
			}
			f.fail = false
			f.check = nil
			now = func() time.Time { return time.Unix(r.Deadline+1, 0) }
			mustCustody(t, recoverInstalled(t.Context(), r.ID, l, now, f))
			b, e := os.ReadFile(l.rules + "/user.rules")
			mustCustody(t, e)
			if !bytes.Equal(b, custodyInput().Rules["user.rules"]) {
				t.Fatal("retry rules")
			}
			j, e = OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			state, e = j.read()
			mustCustody(t, e)
			mustCustody(t, j.Close())
			if state.State != "reverted" || state.ID != r.ID {
				t.Fatal(state)
			}
		})
	}
}
