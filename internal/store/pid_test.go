package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPidfileLifetimeAndStaleRecovery(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	if e := os.WriteFile(p+".pid", []byte("2147483647\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(p + ".pid")
	if e != nil || strings.TrimSpace(string(raw)) != strconv.Itoa(os.Getpid()) {
		t.Fatal(string(raw), e)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Tx(context.Background(), func(*sql.Tx) error { close(entered); <-release; return nil })
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	if _, e = os.Stat(p + ".pid"); e != nil {
		t.Fatal(e)
	}
	close(release)
	<-done
	if e = <-closed; e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(p + ".pid"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}
