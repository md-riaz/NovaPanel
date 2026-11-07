package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

type markedAccount struct{ name, id string }

// A partial enumeration must never be used to authorize destructive work.
func markedAccounts(out []byte) ([]markedAccount, error) {
	if len(out) > 0 && out[len(out)-1] != '\n' {
		return nil, errors.New("incomplete passwd enumeration")
	}
	var accounts []markedAccount
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) != 7 {
			return nil, errors.New("invalid passwd enumeration")
		}
		const prefix = "novapanel-site="
		if !strings.HasPrefix(f[4], prefix) {
			continue
		}
		sid := strings.TrimPrefix(f[4], prefix)
		if !id.Valid(sid) || (&validate.V{}).SiteName(f[0]) != nil {
			continue
		}
		accounts = append(accounts, markedAccount{f[0], sid})
	}
	return accounts, nil
}

// Scan every page, including interrupted jobs, rather than assuming a bounded queue.
func siteJobLive(ctx context.Context, s *store.Store, sid, name string) (bool, error) {
	for _, state := range []string{"queued", "running", "failed"} {
		before := ""
		for {
			rows, err := s.ListJobs(ctx, store.JobFilter{State: state, Before: before, Limit: 200})
			if err != nil {
				return false, err
			}
			for _, j := range rows {
				if state == "failed" && j.Code != "interrupted" {
					continue
				}
				if j.ObjectKey == "site:"+sid || j.ObjectKey == "site:"+name {
					return true, nil
				}
				if j.Verb != "site.create" && j.Verb != "site.delete" && j.Verb != "php.set" {
					continue
				}
				var args struct {
					SiteID string `json:"site_id"`
					Name   string `json:"name"`
				}
				if json.Unmarshal(j.Args, &args) != nil || !id.Valid(args.SiteID) || (j.Verb == "site.create" && (&validate.V{}).SiteName(args.Name) != nil) {
					// An undecodable identity cannot prove that this job is unrelated.
					return true, nil
				}
				if args.SiteID == sid || args.Name == name {
					return true, nil
				}
			}
			if len(rows) < 200 {
				break
			}
			before = rows[len(rows)-1].ID
		}
	}
	return false, nil
}

func reconcilePHP(ctx context.Context, st *store.Store, q *jobs.Queue, runner exec.Runner, pool *php.PoolApplier, sitesRoot string) ([]string, error) {
	if st == nil || q == nil || runner == nil || pool == nil {
		return nil, errors.New("PHP reconcile dependencies unavailable")
	}
	sites, err := st.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, candidate := range sites {
		unlock, err := q.LockSiteOSUser(ctx, candidate.ID, candidate.Name)
		if err != nil {
			return changed, err
		}
		err = func() error {
			s, err := st.SiteByID(ctx, candidate.ID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if s.Name != candidate.Name || s.Status != "active" || s.Type == "static" {
				return nil
			}
			live, err := siteJobLive(ctx, st, s.ID, s.Name)
			if err != nil || live {
				return err
			}
			if err = php.VerifyUser(ctx, runner, sitesRoot, s.Name, s.ID); err != nil {
				return err
			}
			limits, err := st.SitePHPLimits(ctx, s.ID)
			if err != nil {
				return err
			}
			return pool.Recover(ctx, s.Name, php.PoolState{Exists: true, MaxChildren: 5, Limits: limits})
		}()
		unlock()
		if err != nil {
			return changed, err
		}
	}
	const capBytes = 1 << 20
	out, _, code, err := runner.Run(ctx, "/usr/bin/getent", []string{"passwd"}, exec.RunOpts{MaxOutput: capBytes, Stdin: strings.NewReader("")})
	if err != nil {
		return changed, err
	}
	if code != 0 || len(out) >= capBytes {
		return changed, errors.New("passwd enumeration failed or truncated")
	}
	accounts, err := markedAccounts(out)
	if err != nil {
		return changed, err
	}
	for _, account := range accounts {
		unlock, err := q.LockSiteOSUser(ctx, account.id, account.name)
		if err != nil {
			return changed, err
		}
		err = func() error {
			if _, err := st.SiteByID(ctx, account.id); err == nil {
				return nil
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err := st.SiteByName(ctx, account.name); err == nil {
				return nil
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			live, err := siteJobLive(ctx, st, account.id, account.name)
			if err != nil || live {
				return err
			}
			if err = php.DeleteOrphan(ctx, runner, sitesRoot, account.name, account.id, false); err != nil {
				return err
			}
			changed = append(changed, "osuser:"+account.name)
			return nil
		}()
		unlock()
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}
