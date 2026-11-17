package httpapi

import "time"

// SetMCPLimitClock gives the external MCP listener's two counters (requests per
// key, failed authentications per client address) a time source a test can
// move. The counters are private to the package; the harness that assembles
// the listener lives in the external test package, which reaches them here.
func (s *Server) SetMCPLimitClock(now func() time.Time) {
	for _, limiter := range []*perMinuteLimiter{&s.mcpKeyRequests, &s.mcpAuthFailures} {
		limiter.mu.Lock()
		limiter.now = now
		limiter.mu.Unlock()
	}
}
