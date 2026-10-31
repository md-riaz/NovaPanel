package rpc

import (
	"encoding/json"
	"math"
)

// Fresh anonymous minting has a high per-peer backstop, not a per-visitor budget.
func (s *Server) allowAuthentication(peer uint32, verb string, _ json.RawMessage) bool {
	if verb != "session.start" {
		return true
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authRates == nil {
		s.authRates = make(map[uint32]authRate)
	}
	now := s.Agent.Now()
	burst := float64(s.Agent.Cfg.AnonMintBurst)
	r, exists := s.authRates[peer]
	if !exists {
		r = authRate{updated: now, tokens: burst}
	}
	if now > r.updated {
		r.tokens = math.Min(burst, r.tokens+(float64(now)-float64(r.updated))/1000*float64(s.Agent.Cfg.AnonMintRPS))
		r.updated = now
	}
	allowed := r.tokens >= 1
	if allowed {
		r.tokens--
	}
	s.authRates[peer] = r
	return allowed
}
