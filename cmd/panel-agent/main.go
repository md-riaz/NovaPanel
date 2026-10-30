// panel-agent is the root-only NDJSON RPC daemon.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"novapanel/internal/agent"
	"novapanel/internal/config"
	"novapanel/internal/rpc"
	_ "novapanel/internal/verbs"
	"os"
	"os/signal"
)

var geteuid = os.Geteuid

func listenSocket(path string, gid uint32) (*net.UnixListener, error) {
	// Refuse stale sockets too. Never unlink a path that another agent may own.
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
	l, err := listenSocket(cfg.SocketPath, cfg.WebGID)
	if err != nil {
		return err
	}
	defer l.Close()
	a := agent.New()
	s := rpc.NewServer(a)
	s.WebUID = cfg.WebUID
	s.DefaultDeadlineMs = cfg.DeadlineDefaultMs
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sig:
			_ = l.Close()
		case <-done:
		}
	}()
	err = s.Serve(l)
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
