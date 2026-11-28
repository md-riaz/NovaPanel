// Package config loads startup configuration once, with explicit precedence.
package config

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const DefaultPath = "/etc/novapanel/agent.toml"
const DefaultWebListen = "127.0.0.1:8080"
const WebListenPath = "/etc/novapanel/web-listen"

// WebListenState records how the shared panel bind was obtained, for logging and firewall pinning.
// Only valid is parsed. Absent and malformed both pin the default panel port conservatively.
type WebListenState string

const (
	WebListenValid     WebListenState = "valid"     // the file parses: WebListen is the configured bind
	WebListenAbsent    WebListenState = "absent"    // no file: WebListen is the loopback default
	WebListenMalformed WebListenState = "malformed" // unreadable or invalid: the panel bind is unknown
)

type Config struct {
	WebListen            string         `toml:"-"`
	WebListenState       WebListenState `toml:"-"`
	UploadsDir           string         `toml:"uploads_dir"`
	SocketPath           string         `toml:"socket_path"`
	DBPath               string         `toml:"db_path"`
	SealKeyPath          string         `toml:"seal_key_path"`
	LogDir               string         `toml:"log_dir"`
	WebUser              string         `toml:"web_user"`
	ACMEWebroot          string         `toml:"acme_webroot"`
	SitesRoot            string         `toml:"sites_root"`
	TLSDir               string         `toml:"tls_dir"`
	ACMEDirectoryProd    string         `toml:"acme_directory_prod"`
	ACMEDirectoryStaging string         `toml:"acme_directory_staging"`
	ACMEEmail            string         `toml:"acme_email"`
	ACMEUseStaging       bool           `toml:"acme_use_staging"`
	SSHPort              int            `toml:"ssh_port"`
	FirewallWindowMin    int            `toml:"firewall_window_min"`
	UpdateManifestURL    string         `toml:"update_manifest_url"`
	RateLimitRPS         int            `toml:"rate_limit_rps"`
	RateLimitBurst       int            `toml:"rate_limit_burst"`
	AnonMintRPS          int            `toml:"anon_mint_rps"`
	AnonMintBurst        int            `toml:"anon_mint_burst"`
	LoginMaxAttempts     int            `toml:"login_max_attempts"`
	LoginWindowSec       int            `toml:"login_window_sec"`
	TOTPMaxAttempts      int            `toml:"totp_max_attempts"`
	WebhookBodyMaxKB     int            `toml:"webhook_body_max_kb"`
	ReauthWindowSec      int            `toml:"reauth_window_sec"`
	IdleSec              int            `toml:"idle_sec"`
	AbsSec               int            `toml:"abs_sec"`
	DeadlineDefaultMs    int            `toml:"deadline_default_ms"`
	WebUID               uint32         `toml:"-"`
	WebGID               uint32         `toml:"-"`
}

func Defaults() Config {
	return Config{
		WebListen: DefaultWebListen, WebListenState: WebListenAbsent,
		UploadsDir: "/var/spool/novapanel/uploads", SocketPath: "/run/novapanel/agent.sock", DBPath: "/var/lib/novapanel/panel.db", SealKeyPath: "/var/lib/novapanel/seal.key", LogDir: "/var/log/novapanel", WebUser: "novapanel", ACMEWebroot: "/var/www/acme", SitesRoot: "/srv/sites", TLSDir: "/etc/novapanel/tls",
		ACMEDirectoryProd: "https://acme-v02.api.letsencrypt.org/directory", ACMEDirectoryStaging: "https://acme-staging-v02.api.letsencrypt.org/directory", SSHPort: 22, FirewallWindowMin: 15, UpdateManifestURL: "https://releases.example/novapanel/stable/manifest.json", RateLimitRPS: 5, RateLimitBurst: 20, AnonMintRPS: 20, AnonMintBurst: 100, LoginMaxAttempts: 5, LoginWindowSec: 900, TOTPMaxAttempts: 5, WebhookBodyMaxKB: 512, ReauthWindowSec: 300, IdleSec: 600, AbsSec: 43200, DeadlineDefaultMs: 120000,
	}
}

// Load overlays defaults, an optional TOML file, NOVAPANEL_KEY environment,
// then explicitly supplied snake_case flag values. It does not resolve accounts.
func Load(path string, flags map[string]string) (Config, error) {
	return load(path, WebListenPath, flags)
}

func load(path, webListenPath string, flags map[string]string) (Config, error) {
	c := Defaults()
	listen, present, err := ReadWebListen(webListenPath)
	switch {
	case err != nil:
		// The bind file only informs firewall pinning. A malformed or unreadable file must not block recovery
		// or revert, so Load succeeds with an uncertain bind, and the agent pins the default panel port.
		fmt.Fprintf(os.Stderr, "web-listen %s is malformed, the agent conservatively pins the default panel port %s: %v\n", webListenPath, DefaultWebListen, err)
		c.WebListen, c.WebListenState = DefaultWebListen, WebListenMalformed
	case !present:
		c.WebListen, c.WebListenState = listen, WebListenAbsent
	default:
		c.WebListen, c.WebListenState = listen, WebListenValid
	}
	if path == "" {
		path = DefaultPath
	}
	data, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return c, e
	}
	if e == nil {
		if e = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&c); e != nil {
			return c, e
		}
	}
	val := reflect.ValueOf(&c).Elem()
	typ := val.Type()
	known := map[string]int{}
	for i := 0; i < typ.NumField(); i++ {
		key := typ.Field(i).Tag.Get("toml")
		if key == "-" {
			continue
		}
		known[key] = i
		if s, ok := os.LookupEnv("NOVAPANEL_" + strings.ToUpper(key)); ok {
			if e = set(val.Field(i), s); e != nil {
				return c, fmt.Errorf("%s: %w", key, e)
			}
		}
	}
	for key, s := range flags {
		i, ok := known[key]
		if !ok {
			return c, fmt.Errorf("unknown configuration key %q", key)
		}
		if e = set(val.Field(i), s); e != nil {
			return c, fmt.Errorf("%s: %w", key, e)
		}
	}
	return c, c.Validate()
}
func set(v reflect.Value, s string) error {
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, e := strconv.ParseBool(s)
		if e != nil {
			return e
		}
		v.SetBool(b)
	case reflect.Int:
		n, e := strconv.Atoi(s)
		if e != nil {
			return e
		}
		v.SetInt(int64(n))
	default:
		return fmt.Errorf("unsupported configuration type")
	}
	return nil
}
func (c Config) Validate() error {
	for _, p := range []string{c.UploadsDir, c.SocketPath, c.DBPath, c.SealKeyPath, c.LogDir, c.ACMEWebroot, c.SitesRoot, c.TLSDir} {
		if !filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\r\n") {
			return fmt.Errorf("configuration path must be absolute: %q", p)
		}
	}
	if c.WebUser == "" {
		return fmt.Errorf("web_user is required")
	}
	val := reflect.ValueOf(c)
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		if val.Field(i).Kind() == reflect.Int && val.Field(i).Int() <= 0 {
			return fmt.Errorf("%s must be positive", typ.Field(i).Tag.Get("toml"))
		}
	}
	if c.SSHPort > 65535 {
		return fmt.Errorf("ssh_port exceeds 65535")
	}
	return nil
}

// ResolveWebUser resolves the configured account for the socket group and peer gate.
func (c *Config) ResolveWebUser() error {
	u, e := user.Lookup(c.WebUser)
	if e != nil {
		return fmt.Errorf("web_user: %w", e)
	}
	uid, e := strconv.ParseUint(u.Uid, 10, 32)
	if e != nil {
		return e
	}
	gid, e := strconv.ParseUint(u.Gid, 10, 32)
	if e != nil {
		return e
	}
	c.WebUID = uint32(uid)
	c.WebGID = uint32(gid)
	return nil
}

// WebListenKnown reports whether the panel bind was parsed from a file. Absent and malformed binds are
// uncertain, so firewall changes pin the default panel port for them.
func (c Config) WebListenKnown() bool {
	return c.WebListenState == WebListenValid
}

// ReadWebListen returns the bind from a valid file (present true), the loopback default for an absent
// file (present false), or an error when the file is malformed or unreadable.
func ReadWebListen(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultWebListen, false, nil
	}
	if err != nil {
		return "", false, err
	}
	listen := strings.TrimSpace(string(b))
	host, port, err := net.SplitHostPort(listen)
	if err != nil || strings.ContainsAny(host, "\x00\r\n") {
		return "", false, fmt.Errorf("invalid web listen address")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", false, fmt.Errorf("invalid web listen port")
	}
	return listen, true, nil
}
