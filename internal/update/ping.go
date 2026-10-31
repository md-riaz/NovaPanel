package update

import (
	"context"
	"time"

	"novapanel/internal/rpc"
	"novapanel/internal/rpcerr"
)

// Ping is the helper health seam, not helper launch or recovery integration.
// The existing RPC client enforces context cancellation and the reply envelope.
func Ping(ctx context.Context, socket string, deadlineMs int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadlineMs <= 0 {
		return rpcerr.Errf("invalid_argument", "invalid update health deadline")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(deadlineMs)*time.Millisecond)
	defer cancel()
	client, err := rpc.Dial(socket)
	if err != nil {
		return rpcerr.Errf("agent_error", "update health unavailable")
	}
	defer client.Close()
	reply, err := client.Call(ctx, "ping", struct{}{}, "")
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || !reply.OK {
		return rpcerr.Errf("agent_error", "update health unavailable")
	}
	return nil
}
