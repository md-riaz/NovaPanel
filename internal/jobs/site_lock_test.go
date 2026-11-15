package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSiteOSUserLocks(t *testing.T) {
	q, _, _, ctx := fixture(t)
	const a = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const b = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	unlock, err := q.LockSiteOSUser(ctx, a, "example")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for _, pair := range [][2]string{{a, "other"}, {b, "example"}} {
		wait, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		release, err := q.LockSiteOSUser(wait, pair[0], pair[1])
		cancel()
		if release != nil {
			release()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("contention %v: %v", pair, err)
		}
	}
	// A failed child acquisition must not retain the parent.
	release, err := q.LockSiteOSUser(ctx, b, "other")
	if err != nil {
		t.Fatal(err)
	}
	release()
	// Database namespaces are independent even for the same name.
	db, err := q.LockDatabase(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	db()
}

func TestSiteCreateOSUserLocks(t *testing.T) {
	q, _, _, ctx := fixture(t)
	parent, ok := q.locks.tryLock("site:example")
	if !ok {
		t.Fatal("parent")
	}
	defer parent()
	worker := context.WithValue(ctx, heldLocksKey{}, map[string]bool{"site:example": true})
	release, err := q.LockSiteCreateOSUser(worker, "example")
	if err != nil {
		t.Fatal(err)
	}
	if unlocked, ok := q.locks.tryLock("osuser:example"); ok {
		unlocked()
		t.Fatal("OS username not locked")
	}
	const siteID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if unlocked, ok := q.locks.tryLock("site:" + siteID); !ok {
		t.Fatal("create acquired an unrelated site ID lock")
	} else {
		unlocked()
	}
	if _, err := q.LockSiteCreateOSUser(worker, "example"); !errors.Is(err, ErrRetry) {
		t.Fatalf("expected retry: %v", err)
	}
	release()
	if _, err := q.LockSiteCreateOSUser(ctx, "root"); err == nil {
		t.Fatal("accepted reserved site name")
	}
}

func TestSiteOSUserWorkerYieldsAndReusesParent(t *testing.T) {
	q, _, _, ctx := fixture(t)
	const site = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	parent, ok := q.locks.tryLock("site:" + site)
	if !ok {
		t.Fatal("parent")
	}
	defer parent()
	worker := context.WithValue(ctx, heldLocksKey{}, map[string]bool{"site:" + site: true})
	child, err := q.LockSiteOSUser(worker, site, "example")
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.LockSiteOSUser(worker, site, "example")
	if !errors.Is(err, ErrRetry) {
		t.Fatalf("expected retry: %v", err)
	}
	child()
	if _, err = q.LockSiteOSUser(ctx, "bad", "example"); err == nil {
		t.Fatal("accepted invalid site ID")
	}
	if _, err = q.LockSiteOSUser(ctx, site, "root"); err == nil {
		t.Fatal("accepted invalid user")
	}
}
