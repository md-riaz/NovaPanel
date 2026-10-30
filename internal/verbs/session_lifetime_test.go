package verbs_test

import (
	"context"
	"novapanel/internal/config"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredSessionLifetimes(t *testing.T) {
	for _, policy := range []struct{ name, idle, absolute string }{
		{"override", "7", "19"},
		{"absolute_before_idle", "30", "19"},
		{"defaults", "600", "43200"},
	} {
		t.Run(policy.name, func(t *testing.T) {
			t.Setenv("NOVAPANEL_IDLE_SEC", policy.idle)
			t.Setenv("NOVAPANEL_ABS_SEC", policy.absolute)
			cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.toml"), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, boundary := range []string{"idle", "absolute"} {
				t.Run(boundary, func(t *testing.T) {
					s, c, call := sessionHarness(t)
					s.Agent.Cfg = cfg
					start := c.now
					anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
					row, err := s.Agent.Store.GetSession(context.Background(), strings.Split(anon, "_")[1])
					if err != nil || row.IdleDeadline != start+600000 || row.AbsDeadline != start+600000 {
						t.Fatal("anonymous short lifetime changed")
					}
					token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
					if token == anon {
						t.Fatal("login did not rotate")
					}
					if r := call("session.whoami", anon, `{}`); r.Code != "unauthenticated" {
						t.Fatal("old anonymous credential accepted")
					}
					idle, absolute := int64(cfg.IdleSec)*1000, start+int64(cfg.AbsSec)*1000
					deadline := min(start+idle, absolute)
					row, err = s.Agent.Store.GetSession(context.Background(), strings.Split(token, "_")[1])
					if err != nil || row.IdleDeadline != deadline || row.AbsDeadline != absolute {
						t.Fatal("configured creation deadlines ignored")
					}
					c.now = start + 1000
					sessionData(t, call("session.whoami", token, `{}`))
					deadline = min(c.now+idle, absolute)
					row, err = s.Agent.Store.GetSession(context.Background(), strings.Split(token, "_")[1])
					if err != nil || row.IdleDeadline != deadline || row.AbsDeadline != absolute {
						t.Fatal("configured resolver idle extension ignored")
					}
					if boundary == "absolute" {
						for c.now+idle/2 < absolute {
							c.now += idle / 2
							sessionData(t, call("session.whoami", token, `{}`))
						}
						c.now = absolute - 1
						sessionData(t, call("session.whoami", token, `{}`))
						deadline = absolute
					}
					// Resolve without touching to prove the last live millisecond and exact expiry.
					c.now = deadline - 1
					row, err = s.Agent.Store.GetSession(context.Background(), strings.Split(token, "_")[1])
					if err != nil {
						t.Fatal("session expired before boundary")
					}
					c.now = deadline
					if r := call("session.whoami", token, `{}`); r.Code != "unauthenticated" {
						t.Fatal("session accepted at expiry boundary")
					}
				})
			}
		})
	}
}

func TestSessionLifetimeOverridesRejectInvalidConfig(t *testing.T) {
	for _, key := range []string{"NOVAPANEL_IDLE_SEC", "NOVAPANEL_ABS_SEC"} {
		for _, value := range []string{"0", "-1", "invalid"} {
			t.Run(key+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := config.Load(filepath.Join(t.TempDir(), "missing.toml"), nil); err == nil {
					t.Fatal("invalid lifetime accepted")
				}
			})
		}
	}
}
