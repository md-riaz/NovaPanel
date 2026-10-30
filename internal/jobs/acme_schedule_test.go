package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"novapanel/internal/id"
	"novapanel/internal/store"
	"testing"
	"time"
)

func TestAutomaticACMERenewalDailyPersisted(t *testing.T) {
	q, st, _, ctx := fixture(t)
	clock := &fakeClock{time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).UnixMilli()}
	q.SetClock(clock)
	dueID := ""
	for _, v := range []struct {
		name, source  string
		auto, staging bool
		days          int
	}{
		{"due", "letsencrypt", true, false, 30}, {"later", "letsencrypt", true, true, 31},
		{"manual", "manual", true, false, 1}, {"disabled", "letsencrypt", false, false, 1},
	} {
		sid, err := id.New()
		if err != nil {
			t.Fatal(err)
		}
		if v.name == "due" {
			dueID = sid
		}
		err = st.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO sites(id,name,type,stack_driver,docroot,status,cert_source,ssl_auto,created,updated) VALUES(?,?,'static','nginx','/scratch','active',?,?,1,1)`, sid, v.name, v.source, v.auto)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO domains(id,site_id,fqdn,is_primary,cert_state,cert_staging,cert_not_after,created) VALUES(?,?,?,1,'issued',?,?,1)`, v.name, sid, v.name+".example", v.staging, clock.now+int64(v.days)*24*60*60*1000)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	scheduler := NewScheduler(q, clock, nil)
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("daily due jobs=%+v err=%v", rows, err)
	}
	j := rows[0]
	var args struct {
		SiteID  string `json:"site_id"`
		Staging *bool  `json:"staging"`
	}
	if json.Unmarshal(j.Args, &args) != nil || args.SiteID != dueID || args.Staging == nil || *args.Staging || j.Verb != "ssl.renew" || j.CreatedBy != "local-root" || j.Role != "owner" || !j.Effectful {
		t.Fatalf("authority/provenance: %+v %s", j, j.Args)
	}
	// A reconstructed scheduler must not fire again before the persisted cadence.
	scheduler = NewScheduler(q, clock, nil)
	if err = scheduler.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = st.ListJobs(ctx, store.JobFilter{})
	if len(rows) != 1 {
		t.Fatalf("duplicate jobs: %d", len(rows))
	}
	clock.now += 24 * 60 * 60 * 1000
	if err = scheduler.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = st.ListJobs(ctx, store.JobFilter{})
	// Pending work is not duplicated; the staging certificate has now reached 30 days.
	if len(rows) != 2 {
		t.Fatalf("next daily jobs: %+v", rows)
	}
	// Complete the prior job, then add an uncovered alias outside the renewal window.
	if _, err = st.StartJob(ctx, j.ID, clock.now); err != nil {
		t.Fatal(err)
	}
	j.State = "done"
	if err = st.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sites SET force_https=1 WHERE id=?", dueID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE domains SET cert_not_after=? WHERE site_id=?", clock.now+90*24*60*60*1000, dueID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO domains(id,site_id,fqdn,created) VALUES('alias',?,'alias.example',1)", dueID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = scheduler.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = st.ListJobs(ctx, store.JobFilter{Verb: "ssl.issue"})
	if err != nil || len(rows) != 1 || rows[0].ObjectKey != "site:"+dueID {
		t.Fatalf("expansion=%+v err=%v", rows, err)
	}
}
