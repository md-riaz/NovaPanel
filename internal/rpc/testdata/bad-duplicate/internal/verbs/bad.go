package verbs

import "novapanel/internal/rpc"

func init() {
	rpc.Register(rpc.Verb{Name: "same", Run: nil})
	rpc.Register(rpc.Verb{Name: "same", Run: nil})
}
