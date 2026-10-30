package verbs

import "context"

type Agent struct{}
type Verb struct {
	Name string
	Run  func(context.Context, *Agent, any) (any, error)
}

var orphan = Verb{Name: "orphan", Run: func(context.Context, *Agent, any) (any, error) { return nil, nil }}
