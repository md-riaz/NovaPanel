// Package web is a stateless renderer. Session authority stays in the agent (F4).
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"novapanel/internal/config"

	"github.com/go-chi/chi/v5"
	"novapanel/internal/rpc"
)

const CookieName = "__Host-novapanel"

//go:embed templates/*.html assets/*
var files embed.FS
var pages = template.Must(template.ParseFS(files, "templates/*.html"))

type RPCClient interface {
	Call(context.Context, string, any, string) (rpc.Reply, error)
	Close() error
}
type Options struct {
	Origin                       string
	Dial                         func() (RPCClient, error)
	RateLimitRPS, RateLimitBurst int
	Now                          func() time.Time
	// LogOnlyOrigin never disables synchronizer-token validation.
	LogOnlyOrigin   bool
	OriginViolation func()
}
type server struct {
	options Options
	origin  string
	limiter transportLimiter
}
type view struct {
	Page, CSRF, User, Role, Message, URI string
	Codes                                []string
	Status, Metrics                      any
}

func New(o Options) (http.Handler, error) {
	u, e := url.Parse(o.Origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || o.Dial == nil {
		return nil, fmt.Errorf("valid HTTPS origin and RPC dialer required")
	}
	defaults := config.Defaults()
	if o.RateLimitRPS == 0 {
		o.RateLimitRPS = defaults.RateLimitRPS
	}
	if o.RateLimitBurst == 0 {
		o.RateLimitBurst = defaults.RateLimitBurst
	}
	if o.RateLimitRPS < 0 || o.RateLimitBurst < 0 {
		return nil, fmt.Errorf("rate limits must be positive")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &server{options: o, origin: "https://" + u.Host, limiter: transportLimiter{buckets: make(map[[32]byte]transportBucket), rps: float64(o.RateLimitRPS), burst: float64(o.RateLimitBurst)}}
	r := chi.NewRouter()
	assets, _ := fs.Sub(files, "assets")
	r.HandleFunc("/assets/*", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch name {
		case "app.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case "app.js", "htmx.min.js", "qrcode.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	for _, rt := range routes {
		route := rt
		r.MethodFunc(route.method, route.path, func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, route) })
	}
	return headers(r), nil
}

type route struct{ method, path, auth, verb string }

// All routes declare a policy; unknown paths/methods never reach RPC.
var routes = []route{
	{"GET", "/", "user", ""}, {"GET", "/login", "bootstrap", ""}, {"POST", "/login", "anonymous", "session.login"},
	{"POST", "/logout", "user", "session.logout"}, {"GET", "/reauth", "user", ""}, {"POST", "/reauth", "user", "session.reauth"},
	{"GET", "/mfa", "user", ""}, {"POST", "/mfa/enroll", "user", "mfa.enroll"}, {"POST", "/mfa/confirm", "user", "mfa.confirm"}, {"POST", "/mfa/disable", "user", "mfa.disable"},
}

func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
func setCookie(w http.ResponseWriter, value string, remove bool) {
	c := &http.Cookie{Name: CookieName, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
	if remove {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// HTMX only processes HX-Redirect on non-3xx responses.
func redirect(w http.ResponseWriter, r *http.Request, target string) {
	w.Header().Set("HX-Redirect", target)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func render(w http.ResponseWriter, status int, v view) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.ExecuteTemplate(w, "page", v)
}
func (s *server) serve(w http.ResponseWriter, r *http.Request, rt route) {
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == CookieName {
			count++
		}
	}
	if count > 1 {
		http.Error(w, "Ambiguous session cookie", 400)
		return
	}
	if r.Method != "GET" && !s.sameOrigin(r) {
		if s.options.OriginViolation != nil {
			s.options.OriginViolation()
		}
		if !s.options.LogOnlyOrigin {
			http.Error(w, "Cross-site request refused", http.StatusForbidden)
			return
		}
	}
	token := ""
	if cookie, e := r.Cookie(CookieName); e == nil {
		token = cookie.Value
	}
	if token != "" && !s.limiter.allow(token, s.options.Now()) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	c, e := s.options.Dial()
	if e != nil {
		http.Error(w, "Agent unavailable", http.StatusServiceUnavailable)
		return
	}
	defer c.Close()
	v := view{Page: rt.path}
	if rt.auth == "bootstrap" {
		// F4: a live anonymous cookie reuses its row and synchronizer token.
		data, ok := call(w, r, c, "session.start", map[string]any{}, token)
		if !ok {
			return
		}
		session, _ := data["session"].(string)
		v.CSRF, _ = data["csrf_token"].(string)
		if session == "" || v.CSRF == "" {
			http.Error(w, "Invalid agent reply", http.StatusBadGateway)
			return
		}
		setCookie(w, session, false)
		render(w, 200, v)
		return
	}
	if token == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	identityVerb := "session.whoami"
	if rt.auth == "anonymous" {
		identityVerb = "session.start"
	}
	data, ok := call(w, r, c, identityVerb, map[string]any{}, token)
	if !ok {
		return
	}
	v.CSRF, _ = data["csrf_token"].(string)
	v.User, _ = data["user"].(string)
	v.Role, _ = data["role"].(string)
	if v.CSRF == "" || (rt.auth != "anonymous" && v.Role != "owner" && v.Role != "admin" && v.Role != "read_only") {
		http.Error(w, "Invalid agent identity", http.StatusBadGateway)
		return
	}
	if r.Method != "GET" {
		values := r.Header.Values("X-CSRF-Token")
		if len(values) != 1 || values[0] == "" || subtle.ConstantTimeCompare([]byte(values[0]), []byte(v.CSRF)) != 1 {
			http.Error(w, "Invalid CSRF token", http.StatusForbidden)
			return
		}
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/x-www-form-urlencoded" {
			http.Error(w, "Form content type required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if e := r.ParseForm(); e != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		args := map[string]any{}
		if rt.verb == "session.login" {
			user, password, proof := r.PostForm.Get("user"), r.PostForm.Get("password"), r.PostForm.Get("totp")
			if user == "" || len(user) > 128 || password == "" || len(password) > 1024 || len(proof) > 128 {
				http.Error(w, "Invalid login form", http.StatusBadRequest)
				return
			}
			args["user"], args["password"] = user, password
			if proof != "" {
				args["totp"] = proof
			}
		}
		if rt.verb == "session.reauth" || rt.verb == "mfa.confirm" || rt.verb == "mfa.disable" {
			code := r.PostForm.Get("totp")
			if code == "" || len(code) > 128 {
				http.Error(w, "Authenticator or backup code required", 400)
				return
			}
			args["totp"] = code
		}
		data, ok = call(w, r, c, rt.verb, args, token)
		if !ok {
			return
		}
		switch rt.verb {
		case "session.logout":
			setCookie(w, "", true)
			redirect(w, r, "/login")
			return
		case "session.login", "session.reauth":
			session, _ := data["session"].(string)
			csrf, _ := data["csrf_token"].(string)
			if session == "" || csrf == "" {
				http.Error(w, "Invalid agent reply", http.StatusBadGateway)
				return
			}
			setCookie(w, session, false)
			if rt.verb == "session.login" {
				redirect(w, r, "/")
				return
			}
			v.CSRF = csrf
			v.Message = "Session reauthenticated"
		case "mfa.enroll":
			v.URI, _ = data["otpauth_uri"].(string)
			v.Page = "/mfa"
		case "mfa.confirm":
			raw, _ := json.Marshal(data["backup_codes"])
			_ = json.Unmarshal(raw, &v.Codes)
			v.Page = "/mfa"
		case "mfa.disable":
			v.Message = "MFA disabled"
			v.Page = "/mfa"
		}
	}
	if rt.path == "/" {
		v.Status = readDashboard(r, c, "system.status", token)
		v.Metrics = readDashboard(r, c, "system.metrics", token)
	}
	render(w, 200, v)
}
func readDashboard(r *http.Request, c RPCClient, verb, token string) any {
	reply, e := c.Call(r.Context(), verb, map[string]any{}, token)
	if e != nil || !reply.OK {
		return "Unavailable in this phase"
	}
	return reply.Data
}
func call(w http.ResponseWriter, r *http.Request, c RPCClient, verb string, args any, token string) (map[string]any, bool) {
	reply, e := c.Call(r.Context(), verb, args, token)
	if e != nil || !reply.OK {
		data, _ := reply.Data.(map[string]any)
		if hint, _ := data["reauth_required"].(bool); hint {
			render(w, 403, view{Page: "/reauth-required", Message: "Reauthentication required"})
			return nil, false
		}
		status := http.StatusBadGateway
		switch reply.Code {
		case "unauthenticated":
			status = 401
		case "forbidden":
			status = 403
		case "bad_request", "invalid_argument":
			status = 400
		case "conflict", "precondition_failed":
			status = 409
		case "unsupported":
			status = http.StatusNotImplemented
		case "interrupted":
			status = http.StatusConflict
		}
		http.Error(w, http.StatusText(status), status)
		return nil, false
	}
	raw, e := json.Marshal(reply.Data)
	if e != nil {
		http.Error(w, "Invalid agent reply", http.StatusBadGateway)
		return nil, false
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		http.Error(w, "Invalid agent reply", http.StatusBadGateway)
		return nil, false
	}
	return data, true
}
func (s *server) sameOrigin(r *http.Request) bool {
	fetch := r.Header.Get("Sec-Fetch-Site")
	if fetch != "" && fetch != "same-origin" && fetch != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == s.origin
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, e := url.Parse(ref)
		return e == nil && u.User == nil && u.Scheme+"://"+u.Host == s.origin
	}
	return fetch == "same-origin" || fetch == "none"
}

// Only hashed transport keys and counters survive requests, never session authority or secrets.
