// novapanelctl is a thin client of the same agent socket used by panel-web.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"novapanel/internal/rpc"
	"os"
	"strings"
	"time"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("novapanelctl", flag.ContinueOnError)
	socket := f.String("socket", "/run/novapanel/agent.sock", "agent socket")
	timeout := f.Duration("timeout", 5*time.Second, "request timeout")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() < 1 {
		return fmt.Errorf("usage: novapanelctl [-socket path] ping|agent.info|service.status UNIT|service.restart UNIT")
	}
	var callArgs any = struct{}{}
	switch f.Arg(0) {
	case "service.status", "service.restart":
		if f.NArg() != 2 {
			return fmt.Errorf("service verb requires one unit")
		}
		unit := f.Arg(1)
		if !strings.HasSuffix(unit, ".service") {
			unit += ".service"
		}
		callArgs = struct {
			Unit string `json:"unit"`
		}{unit}
	default:
		if f.NArg() != 1 {
			return fmt.Errorf("unexpected arguments")
		}
	}
	c, err := rpc.Dial(*socket)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	reply, callErr := c.Call(ctx, f.Arg(0), callArgs, "")
	if reply.ID != "" {
		if err := rpc.Encode(out, reply); err != nil {
			return err
		}
	}
	return callErr
}
func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
