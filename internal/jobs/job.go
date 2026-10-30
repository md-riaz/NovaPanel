// Package jobs defines plain job data for the future worker boundary.
// It must not import agent or rpc. Queueing and persistence are not implemented.
package jobs

import "encoding/json"

// Job carries the operation and its validated arguments to a Verb.Exec worker.
// Lifecycle and persistence fields belong to the phase that implements jobs.
type Job struct {
	ID   string
	Verb string
	Args json.RawMessage
}
