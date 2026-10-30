package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/config"
	certs "novapanel/internal/tls"
)

func startupConfig(t *testing.T) config.Config {
	t.Helper()
	d := t.TempDir()
	c := config.Defaults()
	c.DBPath = filepath.Join(d, "state", "panel.db")
	c.SealKeyPath = filepath.Join(d, "state", "seal.key")
	c.LogDir = filepath.Join(d, "log")
	c.SocketPath = filepath.Join(d, "run", "agent.sock")
	c.SitesRoot = filepath.Join(d, "sites")
	c.ACMEWebroot = filepath.Join(d, "acme")
	c.TLSDir = filepath.Join(d, "configured", "tls")
	return c
}

func TestTLSRootRejectsUnsafeExistingPaths(t *testing.T) {
	for _, kind := range []string{"public", "special", "file", "symlink", "parent-symlink", "writable-parent", "foreign-parent", "foreign-root", "unclean"} {
		t.Run(kind, func(t *testing.T) {
			c := startupConfig(t)
			parent := filepath.Dir(c.TLSDir)
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(c.TLSDir, 0700); err != nil {
				t.Fatal(err)
			}
			target := c.TLSDir
			switch kind {
			case "public":
				if err := os.Chmod(target, 0755); err != nil {
					t.Fatal(err)
				}
			case "special":
				if err := os.Chmod(target, os.ModeSetgid|0700); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink", "parent-symlink":
				link := target
				if kind == "parent-symlink" {
					link = parent
				}
				moved := link + "-original"
				if err := os.Rename(link, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, link); err != nil {
					t.Fatal(err)
				}
			case "writable-parent":
				target = parent
				if err := os.Chmod(target, 0777); err != nil {
					t.Fatal(err)
				}
			case "foreign-root", "foreign-parent":
				if os.Geteuid() != 0 {
					t.Skip("chown negative control requires root")
				}
				if kind == "foreign-parent" {
					target = parent
				}
				if err := os.Chown(target, 65534, 65534); err != nil {
					t.Fatal(err)
				}
			case "unclean":
				c.TLSDir = parent + "/./tls"
			}
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			var ownerBefore unix.Stat_t
			if err := unix.Lstat(target, &ownerBefore); err != nil {
				t.Fatal(err)
			}
			r := &startupRunner{}
			a := NewWithDependencies(c, r, nil)
			if close, err := a.OpenState(context.Background()); err == nil {
				close()
				t.Fatal("unsafe TLS path accepted")
			}
			after, err := os.Lstat(target)
			if err != nil || before.Mode() != after.Mode() || !os.SameFile(before, after) {
				t.Fatal("unsafe existing path changed", err)
			}
			var ownerAfter unix.Stat_t
			if err := unix.Lstat(target, &ownerAfter); err != nil || ownerBefore.Uid != ownerAfter.Uid {
				t.Fatal("unsafe owner changed", err)
			}
			if r.calls != 0 {
				t.Fatal("unsafe startup reached privileged command")
			}
		})
	}
}

func TestStartupProvisionsTLSRoot(t *testing.T) {
	c := startupConfig(t)
	a := NewWithDependencies(c, &startupRunner{}, nil)
	close, err := a.OpenState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	info, err := os.Lstat(c.TLSDir)
	if err != nil {
		t.Fatalf("startup omitted TLS root: %v", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("TLS root must be private")
	}
	var st unix.Stat_t
	if err := unix.Lstat(c.TLSDir, &st); err != nil || st.Uid != uint32(os.Geteuid()) {
		t.Fatal("TLS root owner", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"example.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := certs.Validate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), []string{"example.test"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := certs.Install(c.TLSDir, "example.test", pair); err != nil {
		t.Fatal("install after actual startup", err)
	}
	if _, err := certs.Load(c.TLSDir, "example.test", []string{"example.test"}, now); err != nil {
		t.Fatal("load after actual startup", err)
	}
}
