// novapanelctl is a thin client of the same agent socket used by panel-web.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"novapanel/internal/rpc"
	"os"
	"time"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("novapanelctl", flag.ContinueOnError)
	socket := f.String("socket", "/run/novapanel/agent.sock", "agent socket")
	timeout := f.Duration("timeout", 5*time.Second, "request timeout")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("usage: novapanelctl [-socket path] ping|agent.info")
	}
	c, err := rpc.Dial(*socket)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	reply, callErr := c.Call(ctx, f.Arg(0), struct{}{}, "")
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
