// panel-agent is the root-only NDJSON RPC daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"novapanel/internal/agent"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/validate"
	_ "novapanel/internal/verbs"
	"os"
	"os/signal"
)

var geteuid = os.Geteuid

func listenSocket(path string, gid uint32) (*net.UnixListener, error) {
	// Refuse existing paths here; recovery requires serve's lifetime reservations.
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("socket path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chown(path, 0, int(gid)); err != nil {
		l.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0660); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
func run(args []string) error {
	if handled, err := dispatchFirewallRevert(context.Background(), args, geteuid(), os.Getegid(), firewall.RecoverInstalledFirewall); handled {
		return err
	}
	if geteuid() != 0 {
		return fmt.Errorf("panel-agent must run as root")
	}
	f := flag.NewFlagSet("panel-agent", flag.ContinueOnError)
	conf := f.String("config", config.DefaultPath, "TOML configuration")
	socket := f.String("socket", "", "override socket path")
	webUser := f.String("web-user", "", "override web account")
	webUID := f.Int64("web-uid", -1, "explicit web uid for scratch tests")
	webGID := f.Int64("web-gid", -1, "explicit socket group for scratch tests")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	flags := map[string]string{}
	if *socket != "" {
		flags["socket_path"] = *socket
	}
	if *webUser != "" {
		flags["web_user"] = *webUser
	}
	cfg, err := config.Load(*conf, flags)
	if err != nil {
		return err
	}
	if *webUID < 0 {
		if err = cfg.ResolveWebUser(); err != nil {
			return err
		}
	} else {
		if *webUID > 1<<32-1 {
			return fmt.Errorf("invalid web uid")
		}
		cfg.WebUID = uint32(*webUID)
		cfg.WebGID = uint32(*webUID)
	}
	if *webGID >= 0 {
		if *webGID > 1<<32-1 {
			return fmt.Errorf("invalid web gid")
		}
		cfg.WebGID = uint32(*webGID)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, unix.SIGTERM)
	defer stop()
	return serve(ctx, agent.NewWithDependencies(cfg, exec.Real{}, &validate.V{}))
}

// serve shares the production lifecycle with scratch tests using an injected runner.
func serve(ctx context.Context, a *agent.Agent) (err error) {
	cleanup, err := a.OpenState(ctx)
	if err != nil {
		return err
	}
	var socketPath string
	var releaseSocket func() error
	defer func() {
		err = errors.Join(err, cleanup())
		if socketPath != "" {
			err = errors.Join(err, os.Remove(socketPath))
		}
		if releaseSocket != nil {
			err = errors.Join(err, releaseSocket())
		}
	}()
	releaseSocket, err = reserveSocket(a.Cfg.SocketPath)
	if err != nil {
		return err
	}
	if err = recoverSocket(a.Cfg.SocketPath); err != nil {
		return err
	}
	s := rpc.NewServer(a)
	s.WebUID = a.Cfg.WebUID
	s.DefaultDeadlineMs = a.Cfg.DeadlineDefaultMs
	s.ConfigureJobs()
	l, err := listenSocket(a.Cfg.SocketPath, a.Cfg.WebGID)
	if err != nil {
		return err
	}
	// Keep the socket pathname reserved until state has drained and unlocked.
	l.SetUnlinkOnClose(false)
	socketPath = a.Cfg.SocketPath
	defer l.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- a.Jobs.Run(ctx) }()
	go func() { results <- a.Scheduler.Run(ctx) }()
	serving := make(chan error, 1)
	go func() { serving <- s.ServeContext(ctx, l) }()
	served := false
	consumed := 0
running:
	for {
		select {
		case e := <-results:
			consumed++
			// A worker may stop on sticky audit failure; reads must remain available.
			if errors.Is(e, jobs.ErrRecoveryBlocked) {
				continue
			}
			if health := a.Audit.Healthy(); health != nil && errors.Is(e, health) {
				continue
			}
			err = e
			if ctx.Err() != nil && errors.Is(e, context.Canceled) {
				err = nil
			}
			break running
		case err = <-serving:
			served = true
			if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
				err = nil
			}
			break running
		case <-ctx.Done():
			break running
		}
	}
	cancel()
	if !served {
		<-serving
	}
	for consumed < 2 {
		e := <-results
		if e != nil && !errors.Is(e, context.Canceled) {
			if health := a.Audit.Healthy(); health == nil || !errors.Is(e, health) {
				err = errors.Join(err, e)
			}
		}
		consumed++
	}
	return err
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
