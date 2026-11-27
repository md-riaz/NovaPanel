// panel-web is an unprivileged stateless HTTP renderer behind a TLS proxy.
package main

import (
	"fmt"
	"log"
	"net/http"
	"novapanel/internal/config"
	"novapanel/internal/rpc"
	"novapanel/internal/web"
	"os"
	"time"
)

var geteuid = os.Geteuid
var serveHTTP = func(s *http.Server) error { return s.ListenAndServe() }

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func run() error {
	if geteuid() == 0 {
		return fmt.Errorf("panel-web refuses to run as root")
	}
	cfg, err := config.Load(os.Getenv("NOVAPANEL_CONFIG"), nil)
	if err != nil {
		return err
	}
	socket := cfg.SocketPath
	handler, err := web.New(web.Options{UploadsDir: cfg.UploadsDir, RateLimitRPS: cfg.RateLimitRPS, RateLimitBurst: cfg.RateLimitBurst, Origin: env("NOVAPANEL_WEB_ORIGIN", "https://localhost"), Dial: func() (web.RPCClient, error) { return rpc.Dial(socket) }, LogOnlyOrigin: os.Getenv("NOVAPANEL_WEB_ORIGIN_LOG_ONLY") == "true", OriginViolation: func() { log.Print("request origin policy violation") }})
	if err != nil {
		return err
	}
	timed := http.TimeoutHandler(handler, 15*time.Second, "Request timed out")
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/uploads" && r.Method == http.MethodPost {
			handler.ServeHTTP(w, r)
			return
		}
		timed.ServeHTTP(w, r)
	})
	return serveHTTP(&http.Server{Addr: cfg.WebListen, Handler: dispatch, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
