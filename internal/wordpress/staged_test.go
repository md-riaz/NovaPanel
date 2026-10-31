package wordpress

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
)

type stagedRunner struct {
	t      *testing.T
	calls  int
	status string
	fail   bool
}

func (r *stagedRunner) Run(ctx context.Context, program string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if program != "/usr/bin/gpgv" || len(args) != 7 || !reflect.DeepEqual(args[:3], []string{"--status-fd=1", "--homedir", "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strings.Split(args[2], "/fd/")[1]}) {
		r.t.Fatalf("unexpected invocation %q %q", program, args)
	}
	if args[3] != "--keyring" || args[5] == "" || args[6] == "" || opts.Stdin != nil || opts.Uid != 0 || opts.Gid != 0 || len(opts.Groups) != 0 || opts.MaxOutput != 16384 || !reflect.DeepEqual(opts.Env, []string{"LANG=C", "PATH=/usr/bin:/bin"}) {
		r.t.Fatalf("unsafe options: %+v %q", opts, args)
	}
	for _, p := range []string{args[4], args[5], args[6]} {
		f, e := os.Open(p)
		if e != nil {
			r.t.Fatal(e)
		}
		data, e := io.ReadAll(f)
		f.Close()
		if e != nil || len(data) == 0 {
			r.t.Fatal("missing held bytes")
		}
	}
	if r.fail {
		return []byte("secret raw output"), nil, 1, errors.New("secret failure")
	}
	return []byte(r.status), nil, 0, nil
}
func stageFixture(t *testing.T) (StagedPaths, Manifest) {
	t.Helper()
	dir := t.TempDir()
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	files := map[string]string{"wp-includes/version.php": "<?php\n$wp_version = '7.1.4';\n", "index.php": "<?php // fixture"}
	sums := map[string]string{}
	for name, raw := range files {
		w, e := z.Create("wordpress/" + name)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.WriteString(w, raw)
		sum := md5.Sum([]byte(raw))
		sums[name] = hex.EncodeToString(sum[:])
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	metadata, _ := json.Marshal(map[string]any{"checksums": sums})
	p := StagedPaths{CLI: filepath.Join(dir, "cli"), Signature: filepath.Join(dir, "sig"), Keyring: filepath.Join(dir, "key"), Archive: filepath.Join(dir, "archive"), Checksums: filepath.Join(dir, "checksums"), VerifierHome: filepath.Join(dir, "gnupg")}
	if e := os.Mkdir(p.VerifierHome, 0700); e != nil {
		t.Fatal(e)
	}
	m := ApprovedArtifacts()
	m.ChecksumEntries = len(files)
	for path, data := range map[string][]byte{p.CLI: []byte("fixture phar bytes, never executed"), p.Signature: []byte("fixture signature"), p.Keyring: []byte("fixture key"), p.Archive: archive.Bytes(), p.Checksums: metadata} {
		if e := os.WriteFile(path, data, 0644); e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(data)
		digest := hex.EncodeToString(h[:])
		switch path {
		case p.CLI:
			m.CLI.SHA256 = digest
		case p.Archive:
			m.WordPress.SHA256 = digest
		case p.Checksums:
			m.ChecksumsSHA256 = digest
		}
	}
	return p, m
}
func validStatus(m Manifest) string {
	return "[GNUPG:] VALIDSIG " + m.CLISignerFingerprint + " 2026-01-01 0 0 4 0 1 10 00\n"
}
func TestStagedPreflightHeldBytes(t *testing.T) {
	p, m := stageFixture(t)
	r := &stagedRunner{t: t, status: validStatus(m)}
	e, err := verifyStaged(context.Background(), p, r, m, uint32(os.Getuid()), true)
	if err != nil || r.calls != 1 || !e.SignatureVerified || e.CLISHA256 != m.CLI.SHA256 {
		t.Fatalf("preflight: %+v %v calls=%d", e, err, r.calls)
	}
}

func TestStagedPreflightRefusals(t *testing.T) {
	for _, name := range []string{"cli_tamper", "archive_tamper", "metadata_tamper", "leaf_symlink", "parent_symlink", "hardlink", "writable", "wrong_owner", "home_nonempty", "missing", "bad_signature", "wrong_signer", "duplicate_signature", "truncated_status", "cancel"} {
		t.Run(name, func(t *testing.T) {
			p, m := stageFixture(t)
			r := &stagedRunner{t: t, status: validStatus(m)}
			switch name {
			case "cli_tamper":
				_ = os.WriteFile(p.CLI, []byte("tamper"), 0644)
			case "archive_tamper":
				_ = os.WriteFile(p.Archive, []byte("tamper"), 0644)
			case "metadata_tamper":
				_ = os.WriteFile(p.Checksums, []byte("tamper"), 0644)
			case "leaf_symlink":
				_ = os.Rename(p.CLI, p.CLI+".old")
				_ = os.Symlink(p.CLI+".old", p.CLI)
			case "parent_symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(filepath.Dir(p.CLI), link); err != nil {
					t.Fatal(err)
				}
				p.CLI = filepath.Join(link, "cli")
			case "hardlink":
				if err := os.Link(p.CLI, p.CLI+".link"); err != nil {
					t.Fatal(err)
				}
			case "writable":
				_ = os.Chmod(p.CLI, 0664)
			case "wrong_owner":
				if os.Geteuid() != 0 {
					t.Skip("requires distinct ownership")
				}
				if err := os.Chown(p.CLI, 12345, 12345); err != nil {
					t.Fatal(err)
				}
			case "home_nonempty":
				_ = os.WriteFile(filepath.Join(p.VerifierHome, "gpgv.conf"), []byte("hostile"), 0600)
			case "missing":
				_ = os.Remove(p.Keyring)
			case "bad_signature":
				r.fail = true
			case "wrong_signer":
				r.status = strings.ReplaceAll(r.status, m.CLISignerFingerprint, strings.Repeat("A", 40))
			case "duplicate_signature":
				r.status += r.status
			case "truncated_status":
				r.status = strings.Repeat("X", 16384) + r.status
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "cancel" {
				cancel()
			}
			got, err := verifyStaged(ctx, p, r, m, uint32(os.Getuid()), true)
			if err == nil || got != (ArtifactEvidence{}) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe refusal: %+v %v", got, err)
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
}
func TestStagedProductionPinsNotFixtureAttestation(t *testing.T) {
	p, _ := stageFixture(t)
	r := &stagedRunner{t: t}
	if _, err := VerifyStaged(context.Background(), p, r); err == nil || r.calls != 0 {
		t.Fatal("exported verifier accepted fixture bytes or executed before pins")
	}
}

// Explicit opt-in consumes separately retained approved bytes. No PHP executes.
func TestStagedRetainedOfficialArtifacts(t *testing.T) {
	root := os.Getenv("NOVAPANEL_WP_STAGED_ARTIFACTS")
	if root == "" {
		t.Skip("approved staged artifacts not supplied")
	}
	if os.Geteuid() != 0 {
		t.Skip("exported production custody requires root")
	}
	p := StagedPaths{CLI: filepath.Join(root, "cli.phar"), Signature: filepath.Join(root, "cli.asc"), Keyring: filepath.Join(root, "key.gpg"), Archive: filepath.Join(root, "wordpress.zip"), Checksums: filepath.Join(root, "checksums.json"), VerifierHome: filepath.Join(root, "gnupg")}
	evidence, err := VerifyStaged(context.Background(), p, runner.Real{})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateArtifacts(evidence); err != nil {
		t.Fatal(err)
	}
}

func TestStagedArchiveContentRefusals(t *testing.T) {
	for _, kind := range []string{"checksum_mismatch", "extra", "missing", "traversal", "duplicate", "symlink", "wrong_version", "directory_collision"} {
		t.Run(kind, func(t *testing.T) {
			p, m := stageFixture(t)
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			files := []struct{ name, body string }{{"wordpress/index.php", "<?php // fixture"}, {"wordpress/wp-includes/version.php", "<?php\n$wp_version = '7.1.4';\n"}}
			switch kind {
			case "checksum_mismatch":
				files[0].body = "tampered"
			case "extra":
				files = append(files, struct{ name, body string }{"wordpress/extra.php", "extra"})
			case "missing":
				files = files[1:]
			case "traversal":
				files[0].name = "wordpress/../index.php"
			case "duplicate":
				files = append(files, files[0])
			case "wrong_version":
				files[1].body = "<?php $wp_version = '0';"
				raw, _ := os.ReadFile(p.Checksums)
				var d map[string]map[string]string
				_ = json.Unmarshal(raw, &d)
				h := md5.Sum([]byte(files[1].body))
				d["checksums"]["wp-includes/version.php"] = hex.EncodeToString(h[:])
				raw, _ = json.Marshal(d)
				_ = os.WriteFile(p.Checksums, raw, 0644)
				sh := sha256.Sum256(raw)
				m.ChecksumsSHA256 = hex.EncodeToString(sh[:])
			case "directory_collision":
				w, _ := z.Create("wordpress/index.php/")
				_, _ = w.Write(nil)
			}
			for i, f := range files {
				var w io.Writer
				var err error
				if kind == "symlink" && i == 0 {
					h := &zip.FileHeader{Name: f.name}
					h.SetMode(os.ModeSymlink | 0777)
					w, err = z.CreateHeader(h)
				} else {
					w, err = z.Create(f.name)
				}
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(w, f.body)
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p.Archive, b.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(b.Bytes())
			m.WordPress.SHA256 = hex.EncodeToString(hash[:])
			r := &stagedRunner{t: t, status: validStatus(m)}
			if _, err := verifyStaged(context.Background(), p, r, m, uint32(os.Getuid()), true); err == nil || r.calls != 0 {
				t.Fatalf("accepted %s or invoked verifier prematurely", kind)
			}
		})
	}
}

func TestStagedUnprivilegedRefusal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run with dropped UID for refusal proof")
	}
	r := &stagedRunner{t: t}
	got, err := VerifyStaged(context.Background(), StagedPaths{}, r)
	if err == nil || got != (ArtifactEvidence{}) || r.calls != 0 {
		t.Fatalf("unprivileged admission: %+v %v", got, err)
	}
}
