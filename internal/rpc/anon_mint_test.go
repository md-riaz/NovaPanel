package rpc

import (
	"novapanel/internal/agent"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConfiguredMintConcurrentAdmission(t *testing.T) {
	s := NewServer(agent.New())
	s.Agent.Cfg.AnonMintRPS = 2
	s.Agent.Cfg.AnonMintBurst = 3
	c := &attemptClock{}
	c.now.Store(1000)
	s.Agent.Clock = c
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.allowAuthentication(1001, "session.start", nil) {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 3 {
		t.Fatalf("admitted=%d", admitted.Load())
	}
	c.now.Add(499)
	if s.allowAuthentication(1001, "session.start", nil) {
		t.Fatal("early refill")
	}
	c.now.Add(1)
	if !s.allowAuthentication(1001, "session.start", nil) || s.allowAuthentication(1001, "session.start", nil) {
		t.Fatal("configured refill")
	}
	c.now.Add(100000)
	for i := 0; i < 3; i++ {
		if !s.allowAuthentication(1001, "session.start", nil) {
			t.Fatal("refill")
		}
	}
	if s.allowAuthentication(1001, "session.start", nil) {
		t.Fatal("burst exceeded")
	}
}
