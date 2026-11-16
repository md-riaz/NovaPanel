package jobs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"novapanel/internal/store"
)

func provisionWordPressFixture(t *testing.T, q *Queue, r *store.WordPressReservation) {
	t.Helper()
	s, ok := q.store.(*store.Store)
	if wrapped, wrappedOK := q.store.(*stageLifetimeStore); wrappedOK {
		s, ok = wrapped.Store, true
	}
	if !ok {
		t.Fatal("fixture requires store")
	}
	r.SiteID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	r.DomainID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO sites(id,name,type,docroot,php_version,status,created,updated) VALUES(?,?,'wordpress',?,'8.3','active',0,0)`, r.SiteID, r.Input.SiteName, filepath.Join(r.Input.SitesRoot, r.Input.SiteName, "public"))
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO domains(id,site_id,fqdn,is_primary,created) VALUES(?,?,?,1,0)`, r.DomainID, r.SiteID, r.Input.PrimaryDomain)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
