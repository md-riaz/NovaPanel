// Package jobs implements durable, audit-gated background work.
package jobs

import (
	"context"
	"encoding/json"
	"novapanel/internal/store"
)

// Job preserves the dispatch boundary while the store owns the persisted shape.
type Job = store.Job

type Metadata struct {
	Actor, Role, CreatedBy string
	Scopes                 []string
	Effectful, Automation  bool
}
type metadataKey struct{}

// WithMetadata is for trusted dispatcher wiring, never request JSON.
func WithMetadata(ctx context.Context, m Metadata) context.Context {
	m.Scopes = append([]string(nil), m.Scopes...)
	return context.WithValue(ctx, metadataKey{}, m)
}
func MetadataFrom(ctx context.Context) (Metadata, bool) {
	m, ok := ctx.Value(metadataKey{}).(Metadata)
	m.Scopes = append([]string(nil), m.Scopes...)
	return m, ok
}
func metadata(j Job) Metadata {
	return Metadata{Actor: j.Actor, Role: j.Role, CreatedBy: j.CreatedBy, Scopes: j.Scopes, Effectful: j.Effectful, Automation: j.Automation}
}

type EnqueueResult struct {
	Job        Job    `json:"job"`
	ClaimToken string `json:"claim,omitempty"`
}

// Clock returns UTC Unix milliseconds. Set it before starting queue/scheduler.
type Clock interface{ Now() int64 }

// Persistence consists of A's frozen typed store contract.
type Persistence interface {
	CreateJob(context.Context, store.Job) error
	CreateJobWithClaim(context.Context, store.Job, store.JobClaim) error
	GetJob(context.Context, string) (store.Job, error)
	ListJobs(context.Context, store.JobFilter) ([]store.Job, error)
	StartJob(context.Context, string, int64) (store.Job, error)
	NextQueuedJob(context.Context) (store.Job, error)
	UpdateJob(context.Context, store.Job) error
	RecoverJobs(context.Context, int64) error
	AppendJobEvent(context.Context, store.JobEvent) error
	PutClaim(context.Context, store.JobClaim) error
	PublishClaim(context.Context, string, []byte, []byte) error
	ConsumeClaim(context.Context, string, string, string) (store.JobClaim, error)
	DeleteExpiredClaims(context.Context) error
	PutSchedule(context.Context, store.Schedule) error
	GetSchedule(context.Context, string) (store.Schedule, error)
	ListSchedules(context.Context) ([]store.Schedule, error)
	DueSchedules(context.Context, int64) ([]store.Schedule, error)
	SetScheduleEnabled(context.Context, string, bool) error
	EnqueueScheduledJob(context.Context, string, int64, int64, int64, json.RawMessage, store.Job, *store.JobClaim) (bool, error)
}
