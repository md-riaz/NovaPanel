package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"strings"

	"novapanel/internal/rpcerr"
)

// Set only by the owner-authorized release build using -ldflags -X.
// No request, environment variable, or manifest can override this value.
var releasePublicKeyBase64 string

const maxManifestBytes = 1 << 20
const maxArtifactBytes = 512 << 20

type Manifest struct {
	Version     string     `json:"version"`
	SecurityFix bool       `json:"security_fix,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	Artifacts   []Artifact `json:"artifacts"`
}
type Artifact struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
}

func releaseKey() (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(releasePublicKeyBase64)
	if err != nil || len(key) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(key) != releasePublicKeyBase64 {
		return nil, rpcerr.Errf("unsupported", "update trust anchor unavailable")
	}
	return ed25519.PublicKey(key), nil
}

// Verify authenticates bounded exact bytes before parsing or selecting an artifact.
func Verify(body, signature []byte, installed string) (Manifest, Artifact, error) {
	key, err := releaseKey()
	if err != nil {
		return Manifest{}, Artifact{}, err
	}
	return verifyManifest(key, body, signature, installed, runtime.GOOS, runtime.GOARCH)
}

func verifyManifest(key ed25519.PublicKey, body, signature []byte, installed, goos, arch string) (Manifest, Artifact, error) {
	var m Manifest
	bad := func() (Manifest, Artifact, error) {
		return Manifest{}, Artifact{}, rpcerr.Errf("invalid_argument", "invalid update manifest")
	}
	if len(body) > maxManifestBytes || len(signature) > 1024 {
		return bad()
	}
	if err := verifySignature(key, body, signature); err != nil {
		return m, Artifact{}, err
	}
	// Reject duplicate object fields, including nested artifacts, before decoding.
	if err := uniqueJSON(json.NewDecoder(bytes.NewReader(body))); err != nil {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return bad()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return bad()
	}
	version, ok := parseVersion(m.Version)
	if !ok || version.pre != "" || len(m.Artifacts) == 0 || len(m.Artifacts) > 64 {
		return bad()
	}
	seen := map[string]bool{}
	var chosen Artifact
	for _, a := range m.Artifacts {
		u, e := url.Parse(a.URL)
		hash, herr := hex.DecodeString(a.SHA256)
		if a.OS == "" || a.Arch == "" || a.Filename == "" || a.Filename == "." || path.Base(a.Filename) != a.Filename || strings.ContainsAny(a.Filename, "\\\x00\r\n") || a.Size <= 0 || a.Size > maxArtifactBytes || herr != nil || len(hash) != 32 || len(a.SHA256) != 64 || e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
			return bad()
		}
		id := a.OS + "/" + a.Arch
		if seen[id] {
			return bad()
		}
		seen[id] = true
		if a.OS == goos && a.Arch == arch {
			chosen = a
		}
	}
	if previous, valid := parseVersion(installed); valid && compareRelease(version, previous) <= 0 {
		return m, Artifact{}, rpcerr.Errf("precondition_failed", "update version must be newer")
	}
	if chosen.OS == "" {
		return m, Artifact{}, rpcerr.Errf("unsupported", "no update artifact for platform")
	}
	return m, chosen, nil
}

// uniqueJSON requires exact schema field names and rejects decoded duplicate keys.
func uniqueJSON(d *json.Decoder) error {
	var value func(int) error
	value = func(depth int) error {
		if depth > 8 {
			return io.ErrUnexpectedEOF
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			// Optional fields may be absent, but explicit null is not a schema value.
			if token == nil {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return io.ErrUnexpectedEOF
				}
				// encoding/json otherwise accepts case-insensitive struct field aliases.
				switch depth {
				case 0:
					switch s {
					case "version", "security_fix", "notes", "artifacts":
					default:
						return io.ErrUnexpectedEOF
					}
				case 2:
					switch s {
					case "os", "arch", "filename", "sha256", "size", "url":
					default:
						return io.ErrUnexpectedEOF
					}
				default:
					return io.ErrUnexpectedEOF
				}
				seen[s] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e := value(depth + 1); e != nil {
					return e
				}
			}
		default:
			return io.ErrUnexpectedEOF
		}
		_, err = d.Token()
		return err
	}
	return value(0)
}

var versionGrammar = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type version struct {
	numbers [3]string
	pre     string
}

func parseVersion(s string) (version, bool) {
	m := versionGrammar.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	for _, id := range strings.Split(m[4], ".") {
		if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
			return version{}, false
		}
	}
	return version{[3]string{m[1], m[2], m[3]}, m[4]}, true
}

// The candidate is always final, so only core numbers and installed prerelease matter.
func compareRelease(a, b version) int {
	for i, x := range a.numbers {
		y := b.numbers[i]
		if len(x) < len(y) {
			return -1
		}
		if len(x) > len(y) {
			return 1
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	if b.pre != "" {
		return 1
	}
	return 0
}
