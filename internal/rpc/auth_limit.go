package rpc

import "encoding/json"

// Only fresh anonymous minting uses a per-peer admission throttle.
func (s *Server) allowAuthentication(peer uint32, verb string, _ json.RawMessage) bool {
	if verb != "session.start" {
		return true
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authRates == nil {
		s.authRates = make(map[uint32][3]authRate)
	}
	now := s.Agent.Now()
	rates := s.authRates[peer]
	r := &rates[0]
	if r.count == 0 || now < r.start || now-r.start >= 60000 {
		*r = authRate{start: now}
	}
	if r.count >= 10 {
		return false
	}
	r.count++
	s.authRates[peer] = rates
	return true
}
