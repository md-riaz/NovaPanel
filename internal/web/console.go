package web

import (
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
	"strings"
)

var consoleRoutes = []route{
	{"GET", "/databases", "user", "database.list"},
	{"POST", "/databases/create", "user", "database.create"},
	{"POST", "/databases/{dbID}/rotate", "user", "database.rotate_password"},
	{"POST", "/databases/{dbID}/delete", "user", "database.delete"},
	{"POST", "/jobs/{jobID}/claim", "user", "job.claim"},
	{"GET", "/sites", "user", "site.list"},
	{"GET", "/sites/{siteID}", "user", "site.get"},
	{"GET", "/sites/{siteID}/ssl", "user", "ssl.status"},
	{"GET", "/jobs/{jobID}", "user", "job.status"},
	{"POST", "/sites/create", "user", "site.create"},
	{"POST", "/sites/{siteID}/update", "user", "site.update"},
	{"POST", "/sites/{siteID}/delete", "user", "site.delete"},
	{"POST", "/sites/{siteID}/domains/add", "user", "domain.add"},
	{"POST", "/sites/{siteID}/domains/remove", "user", "domain.remove"},
	{"POST", "/sites/{siteID}/redirects/add", "user", "redirect.add"},
	{"POST", "/sites/{siteID}/redirects/remove", "user", "redirect.remove"},
	{"POST", "/sites/{siteID}/ssl/issue", "user", "ssl.issue"},
	{"POST", "/sites/{siteID}/ssl/renew", "user", "ssl.renew"},
	{"POST", "/sites/{siteID}/ssl/upload", "user", "ssl.upload"},
	{"POST", "/sites/{siteID}/ssl/auto", "user", "ssl.auto_set"},
}

type consoleView struct {
	Sites    []map[string]any
	Data     map[string]any
	SiteID   string
	Section  string
	Writable bool
}

func isConsole(path string) bool {
	return strings.HasPrefix(path, "/databases") || strings.HasPrefix(path, "/sites") || strings.HasPrefix(path, "/jobs/")
}
func (s *server) console(w http.ResponseWriter, r *http.Request, c RPCClient, rt route, token string, v view) {
	cv := &consoleView{SiteID: chi.URLParam(r, "siteID"), Section: rt.verb, Writable: v.Role == "owner" || v.Role == "admin"}
	v.Console = cv
	args := map[string]any{}
	if cv.SiteID != "" {
		args["site_id"] = cv.SiteID
	}
	if rt.verb == "job.status" {
		args["id"] = chi.URLParam(r, "jobID")
	}
	if r.Method == "POST" {
		if (rt.verb == "database.create" || rt.verb == "database.rotate_password") && r.Header.Get("HX-Request") != "true" {
			http.Error(w, "JavaScript is required to receive the one-time password", http.StatusBadRequest)
			return
		}
		if !cv.Writable {
			http.Error(w, "forbidden: read-only account", http.StatusForbidden)
			return
		}
		var err error
		if rt.verb == "job.claim" {
			args = map[string]any{"job_id": chi.URLParam(r, "jobID"), "claim": r.PostForm.Get("claim")}
		} else if rt.verb == "database.rotate_password" || rt.verb == "database.delete" {
			args = map[string]any{"db_id": chi.URLParam(r, "dbID")}
		} else {
			args, err = consoleArgs(rt.verb, cv.SiteID, r)
		}
		if err != nil {
			http.Error(w, "Invalid form: "+err.Error(), 400)
			return
		}
	}
	data, ok := call(w, r, c, rt.verb, args, token)
	if !ok {
		return
	}
	if rt.verb == "job.claim" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
		return
	}
	cv.Data = data
	status := 200
	if r.Method == "POST" {
		if rt.verb == "ssl.auto_set" {
			v.Message = "Auto-renew setting saved. Manual certificates are never auto-renewed."
			cv.Data, ok = call(w, r, c, "ssl.status", map[string]any{"site_id": cv.SiteID}, token)
			if !ok {
				return
			}
			cv.Section = "ssl.status"
		} else {
			job, valid := data["job"].(map[string]any)
			if !valid || job["id"] == nil || job["state"] == nil {
				http.Error(w, "Invalid agent job reply", http.StatusBadGateway)
				return
			}
			if rt.verb == "database.create" || rt.verb == "database.rotate_password" {
				claim, ok := data["claim"].(string)
				if !ok || claim == "" || r.Header.Get("HX-Request") != "true" {
					http.Error(w, "Claim requires JavaScript; rerun the operation", http.StatusBadGateway)
					return
				}
				w.Header().Set("X-Job-Claim", claim)
			}
			cv.Section = "job.status"
			cv.Data = job
			status = 202
		}
	}
	if items, ok := data["sites"].([]any); ok {
		for _, item := range items {
			if row, ok := item.(map[string]any); ok {
				cv.Sites = append(cv.Sites, row)
			}
		}
	}
	render(w, status, v)
}

// Explicit allowlists prevent forged forms from extending the agent contract.
func consoleArgs(verb, site string, r *http.Request) (map[string]any, error) {
	args := map[string]any{}
	if site != "" {
		args["site_id"] = site
	}
	required := func(names ...string) error {
		for _, name := range names {
			value := r.PostForm.Get(name)
			if value == "" {
				return fmt.Errorf("%s required", name)
			}
			args[name] = value
		}
		return nil
	}
	boolean := func(name string, optional bool) error {
		value, exists := r.PostForm[name]
		if !exists && optional {
			return nil
		}
		if len(value) != 1 || (value[0] != "true" && value[0] != "false") {
			return fmt.Errorf("%s must be true or false", name)
		}
		args[name] = value[0] == "true"
		return nil
	}
	switch verb {
	case "database.create":
		for _, name := range []string{"name", "site_id"} {
			if value := r.PostForm.Get(name); value != "" {
				args[name] = value
			}
		}
	case "site.create":
		if err := required("type", "primary_domain"); err != nil {
			return nil, err
		}
		if r.PostForm.Get("type") != "static" {
			return nil, fmt.Errorf("only static sites are available in Phase 3")
		}
		if name := r.PostForm.Get("name"); name != "" {
			args["name"] = name
		}
	case "site.update":
		if value, ok := r.PostForm["primary_domain_id"]; ok {
			if len(value) != 1 || value[0] == "" {
				return nil, fmt.Errorf("primary domain ID required")
			}
			args["primary_domain_id"] = value[0]
		}
		for _, name := range []string{"maintenance", "force_https"} {
			if err := boolean(name, true); err != nil {
				return nil, err
			}
		}
		if value, ok := r.PostForm["hsts_max_age"]; ok {
			n, err := strconv.Atoi(value[0])
			if err != nil || n < 0 || len(value) != 1 {
				return nil, fmt.Errorf("invalid HSTS max age")
			}
			args["hsts_max_age"] = n
		}
		if len(args) == 1 {
			return nil, fmt.Errorf("setting required")
		}
	case "site.delete":
		if err := boolean("purge_files", true); err != nil {
			return nil, err
		}
	case "domain.add":
		if err := required("fqdn"); err != nil {
			return nil, err
		}
	case "domain.remove", "redirect.remove":
		delete(args, "site_id")
		name := "domain_id"
		if verb == "redirect.remove" {
			name = "redirect_id"
		}
		if err := required(name); err != nil {
			return nil, err
		}
	case "redirect.add":
		if err := required("source", "target"); err != nil {
			return nil, err
		}
		code, err := strconv.Atoi(r.PostForm.Get("code"))
		if err != nil || (code != 301 && code != 302) {
			return nil, fmt.Errorf("redirect code must be 301 or 302")
		}
		args["code"] = code
	case "ssl.issue":
		if err := boolean("staging", true); err != nil {
			return nil, err
		}
	case "ssl.upload":
		if err := required("cert_pem", "key_pem"); err != nil {
			return nil, err
		}
		if len(r.PostForm.Get("cert_pem")) > 128<<10 || len(r.PostForm.Get("key_pem")) > 128<<10 {
			return nil, fmt.Errorf("PEM exceeds limit")
		}
	case "ssl.auto_set":
		if err := boolean("enabled", false); err != nil {
			return nil, err
		}
	case "ssl.renew":
	default:
		return nil, fmt.Errorf("unsupported operation")
	}
	return args, nil
}
