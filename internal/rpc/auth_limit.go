package rpc

import "encoding/json"

// Bootstrap, password login and MFA proofs have independent per-peer budgets.
// Peers come from SO_PEERCRED, never from request labels or session IDs.
func (s *Server) allowAuthentication(peer uint32, verb string, args json.RawMessage) bool {
	limits := [3]int{10, s.Agent.Cfg.LoginMaxAttempts, s.Agent.Cfg.TOTPMaxAttempts}
	windows := [3]int64{60000, int64(s.Agent.Cfg.LoginWindowSec) * 1000, int64(s.Agent.Cfg.LoginWindowSec) * 1000}
	var charge [3]bool
	switch verb {
	case "session.start":
		charge[0] = true
	case "session.login":
		charge[1] = true
		var proof struct {
			TOTP string `json:"totp"`
		}
		if json.Unmarshal(args, &proof) == nil && proof.TOTP != "" {
			charge[2] = true
		}
	case "session.reauth", "mfa.confirm", "mfa.disable":
		charge[2] = true
	default:
		return true
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authRates == nil {
		s.authRates = make(map[uint32][3]authRate)
	}
	now := s.Agent.Now()
	rates := s.authRates[peer]
	for i, needed := range charge {
		if !needed {
			continue
		}
		r := &rates[i]
		if r.count == 0 || now < r.start || now-r.start >= windows[i] {
			*r = authRate{start: now}
		}
		if r.count >= limits[i] {
			return false
		}
	}
	for i, needed := range charge {
		if needed {
			rates[i].count++
		}
	}
	s.authRates[peer] = rates
	return true
}
