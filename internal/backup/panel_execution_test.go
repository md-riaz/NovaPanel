package backup

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestSnapshotExecutionRequiresPanelAndLiveSnapshot(t *testing.T) {
	// No live DB or key is used by this invocation test.
	scratch := t.TempDir()
	os.Chmod(scratch, 0700)
	source := scratchDB(t)
	snap, err := CreateSnapshot(context.Background(), source, scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	p := &Password{plain: []byte("fixture-password")}
	defer p.Close()
	r := Repository{ID: "r", Name: "r", Role: "panel", Kind: "local", Location: "/srv/recovery"}
	f := &fakeRestic{}
	if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err != nil {
		t.Fatal(err)
	}
	want := []string{"-r", "/srv/recovery", "--retry-lock", "30s", "backup", "--tag", "panel", "--", snap.Path()}
	if !reflect.DeepEqual(f.args, want) {
		t.Fatal(f.args)
	}
	for _, code := range []int{3, 11, 12, 99} {
		f.code = code
		if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err == nil {
			t.Fatal("nonzero exit accepted", code)
		}
	}
	f.code = 0
	r.Role = "site"
	if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err == nil {
		t.Fatal("site repo accepted")
	}
	r.Role = "panel"
	snap.Close()
	if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err == nil {
		t.Fatal("closed snapshot accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = BackupPanelSnapshot(ctx, f, r, p, snap); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRepositoryLocationsRejectInjection(t *testing.T) {
	r := Repository{ID: "r", Name: "r", Role: "panel"}
	for _, v := range []struct{ kind, location string }{
		{"local", "--password=secret"}, {"local", "/a/../b"}, {"local", "relative"},
		{"sftp", "sftp://user:password@host/repo"}, {"sftp", "sftp:host:/repo"},
		{"s3", "s3:http://host/bucket"}, {"s3", "s3:https://key:secret@host/bucket"},
		{"s3", "s3:https://host/bucket?token=secret"}, {"b2", "b2:secret"},
	} {
		r.Kind = v.kind
		r.Location = v.location
		if ValidateRepository(r) == nil {
			t.Fatal("accepted", v)
		}
	}
	for _, v := range []struct{ kind, location string }{{"local", "/srv/repo"}, {"sftp", "sftp://user@host/repo"}, {"s3", "s3:https://host/bucket/prefix"}} {
		r.Kind = v.kind
		r.Location = v.location
		if err := ValidateRepository(r); err != nil {
			t.Fatal(v, err)
		}
	}
}
