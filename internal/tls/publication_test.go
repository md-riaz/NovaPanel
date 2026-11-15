package tls

import (
	"encoding/json"
	"errors"
	"fmt"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicationRejectsUnsafeTokensAndPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	token, err := Stage(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Recovery){
		func(r *Recovery) { r.Version = "../escape" },
		func(r *Recovery) { r.Version = "/absolute" },
		func(r *Recovery) { r.Previous = r.Version + "/" },
		func(r *Recovery) { r.Primary = "../escape" },
		func(r *Recovery) { r.Root += "/.." },
	} {
		bad := token
		mutate(&bad)
		if err := Publish(bad); err == nil {
			t.Fatal("unsafe publication accepted")
		}
		if err := Restore(bad); err == nil {
			t.Fatal("unsafe restore accepted")
		}
	}
	file := filepath.Join(root, token.Primary, token.Version, "privkey.pem")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("fullchain.pem", file); err != nil {
		t.Fatal(err)
	}
	if err := Publish(token); err == nil {
		t.Fatal("symlinked key accepted")
	}
	if _, err := os.Lstat(filepath.Join(root, token.Primary, "current")); !os.IsNotExist(err) {
		t.Fatal("invalid publication changed pointer", err)
	}
}

func TestConcurrentPublicationsConflict(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Stage(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Stage(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for _, token := range []Recovery{first, second} {
		go func(token Recovery) { <-start; results <- Publish(token) }(token)
	}
	close(start)
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		var coded *rpcerr.CodedError
		if err == nil {
			successes++
		} else if errors.As(err, &coded) && coded.Code == "conflict" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("publication lost comparison", successes, conflicts)
	}
}

func TestStageRejectsUnsafePriorPair(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, "example.test", c); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "example.test", "current", "privkey.pem"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(root, "example.test", c); err == nil {
		t.Fatal("staged with unsafe rollback pair")
	}
}

func TestPublicationConflictsAndFirstInstall(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Stage(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := Stage(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(first); err != nil {
		t.Fatal(err)
	}
	if err := Publish(first); err != nil {
		t.Fatal("repeat publish", err)
	}
	var coded *rpcerr.CodedError
	if err := Publish(stale); !errors.As(err, &coded) || coded.Code != "conflict" {
		t.Fatal("stale publication", err)
	}
	newer, err := Stage(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(newer); err != nil {
		t.Fatal(err)
	}
	if err := Restore(first); !errors.As(err, &coded) || coded.Code != "conflict" {
		t.Fatal("stale restore", err)
	}
	if err := Restore(newer); err != nil {
		t.Fatal(err)
	}
	if err := Restore(first); err != nil {
		t.Fatal(err)
	}
	if err := Restore(first); err != nil {
		t.Fatal("repeat restore", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "example.test", "current")); !os.IsNotExist(err) {
		t.Fatal("first restore left pointer", err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var recovered Recovery
	if err := json.Unmarshal(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	if err := Publish(recovered); err != nil {
		t.Fatal("recovered token", err)
	}
	for _, output := range []string{string(encoded), fmt.Sprintf("%v %+v %#v %#v", first, first, first, &first)} {
		if strings.Contains(output, string(key)) || strings.Contains(output, "PRIVATE KEY") {
			t.Fatal("key leaked")
		}
	}
}

func TestStagePublishRestorePair(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := Install(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	nextCert, nextKey := fixture(t, nil)
	c, err = Validate(nextCert, nextKey, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	token, err := Stage(root, "example.test", c)
	if err != nil {
		t.Fatal(err)
	}
	if token.Previous != prior || token.Version == prior {
		t.Fatal("incorrect recovery versions")
	}
	current := filepath.Join(root, "example.test", "current")
	if got, err := os.Readlink(current); err != nil || got != prior {
		t.Fatal("stage changed current", err)
	}
	if err := Publish(token); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(current); err != nil || got != token.Version {
		t.Fatal("publish", err)
	}
	if _, err := Load(root, "example.test", []string{"example.test"}, testNow); err != nil {
		t.Fatal(err)
	}
	if err := Restore(token); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(current); err != nil || got != prior {
		t.Fatal("restore", err)
	}
	loaded, err := Load(root, "example.test", []string{"example.test"}, testNow)
	if err != nil || string(loaded.certPEM) != string(cert) || string(loaded.keyPEM) != string(key) {
		t.Fatal("did not restore original pair", err)
	}
	for _, version := range []string{prior, token.Version} {
		if _, err := os.Stat(filepath.Join(root, "example.test", version, "privkey.pem")); err != nil {
			t.Fatal("lost retained pair", err)
		}
	}
}
