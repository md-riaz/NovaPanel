package rpc

import (
	"bufio"
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
)

func PeerAllowed(uid, webUID uint32) bool { return uid == 0 || uid == webUID }
func peerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var sockErr error
	if err = raw.Control(func(fd uintptr) { cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, sockErr
	}
	if cred == nil {
		return 0, fmt.Errorf("missing peer credentials")
	}
	return cred.Uid, nil
}
func (s *Server) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		uc, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			continue
		}
		uid, err := peerUID(uc)
		if err != nil || !PeerAllowed(uid, s.WebUID) {
			_ = conn.Close()
			continue
		}
		go s.serveConn(uc, uid)
	}
}
func (s *Server) serveConn(c *net.UnixConn, uid uint32) {
	defer c.Close()
	caller := RootCaller(uid)
	ctx := WithCaller(context.Background(), caller)
	scanner := bufio.NewScanner(c)
	scanner.Buffer(make([]byte, 4096), MaxRequestBytes+1)
	scanner.Split(splitNDJSON)
	for scanner.Scan() {
		req, err := DecodeLine(scanner.Bytes())
		if err != nil {
			return
		}
		if err = Encode(c, s.Dispatch(ctx, req)); err != nil {
			return
		}
	}
}
