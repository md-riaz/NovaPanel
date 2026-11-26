package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCronPersistence(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	phase4Exec(t, s.db.raw, "INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site','example','static','/srv/sites/example/public',1,1)")
	row := CronJob{ID: "cron", SiteID: "site", Schedule: "daily", ArgvJSON: `["/bin/echo","hello"]`, UnitName: "unit", Enabled: true, Created: 1}
	if err = s.Tx(ctx, func(tx *sql.Tx) error { return s.InsertCronTx(ctx, tx, row) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.CronByID(ctx, row.ID)
	if err != nil || got.Enabled {
		t.Fatalf("write-ahead: %+v %v", got, err)
	}
	if err = s.SetCronEnabled(ctx, row.ID, true); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListCronBySite(ctx, row.SiteID, 100)
	if err != nil || len(rows) != 1 || !rows[0].Enabled || rows[0].ArgvJSON != row.ArgvJSON {
		t.Fatalf("list: %+v %v", rows, err)
	}
	if err = s.SetSiteStatus(ctx, row.SiteID, "deleting"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSite(ctx, row.SiteID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CronByID(ctx, row.ID); err != sql.ErrNoRows {
		t.Fatalf("cascade: %v", err)
	}
}
