package openaiv1

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// GatewayMuteMarker, [[fake:gateway-mute]] in the last user message of a Chat
// Completions or Responses request, plays the gateway (llms-proxy) muting a
// target after its upstream refused with a used-up quota (#640), as
// formal-test showed on 2026-11-26 (sanitized structure from the gateway's
// and Plinth's logs, no committed sample). Requests carrying the marker are
// counted per API key: the first is answered with the 429 of
// GatewayWeeklyQuotaBody; every later one with a 503 the gateway writes
// itself, alternately "all targets muted until <RFC3339>" (the mute lasts 1,
// 2 and then 4 minutes, as observed) and "all targets recovering (half-open
// probe in flight)". Observed: the 429 body's code, message wording and quota
// dimension, group and limit; the 503 status and the two message fragments;
// the growing mute. Constructed: the 503 envelope (an error object with
// message and type upstream_unavailable), the strict alternation, the 4-minute
// cap, the group name and reset time, and that only requests with the marker
// see the mute (a real gateway mutes every request for the target). The count
// lives in the fake process and is never reset, so each test starts its own
// fake.
const GatewayMuteMarker = "[[fake:gateway-mute]]"

// The fragments of the gateway's own 503 answers.
const (
	GatewayMutedPrefix       = "all targets muted until "
	GatewayRecoveringMessage = "all targets recovering (half-open probe in flight)"
)

// GatewayWeeklyQuotaBody is the 429 the first GatewayMuteMarker request gets:
// the weekly group quota of formal-test's default chat connection, in the
// envelope of the 2026-11-20 sample (quotaExceededBody).
func GatewayWeeklyQuotaBody(resetsAt time.Time) map[string]any {
	resets := resetsAt.UTC().Format(time.RFC3339)
	return map[string]any{"error": map[string]any{
		"code": "quota_exceeded", "type": "quota_exceeded",
		"message": "Quota exceeded (weekly). Limit: $20.00, used: $20.24. Resets at " + resets,
		"quota": map[string]any{
			"dimension": "weekly", "group_name": "fake-group", "limit_usd": 20,
			"resets_at": resets, "scope": "model_group", "used_usd": 20.24,
		},
	}}
}

// gatewayMute counts the GatewayMuteMarker requests of each API key.
type gatewayMute struct {
	mu   sync.Mutex
	seen map[string]int
}

// next is the 1-based number of this key's marker request.
func (m *gatewayMute) next(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == nil {
		m.seen = map[string]int{}
	}
	m.seen[key]++
	return m.seen[key]
}

// GatewayMuteMinutes is how long the mute of the n-th marker request lasts
// (n of 2, 4, 6...: 1, 2, then 4 minutes); 0 for a request answered with the
// 429 or the recovering 503.
func GatewayMuteMinutes(n int) int {
	if n < 2 || n%2 != 0 {
		return 0
	}
	return min(1<<((n-2)/2), 4)
}

func (r chatRequest) gatewayMute() bool {
	return strings.Contains(r.lastText(), GatewayMuteMarker)
}

// writeGatewayMute answers a GatewayMuteMarker request.
func (h *handler) writeGatewayMute(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC().Truncate(time.Second)
	n := h.mute.next(fakeupstream.BearerKey(r))
	if n == 1 {
		writeJSON(w, http.StatusTooManyRequests, GatewayWeeklyQuotaBody(now.Add(72*time.Hour)))
		return
	}
	message := GatewayRecoveringMessage
	if minutes := GatewayMuteMinutes(n); minutes > 0 {
		message = GatewayMutedPrefix + now.Add(time.Duration(minutes)*time.Minute).Format(time.RFC3339)
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{
		"message": message, "type": "upstream_unavailable",
	}})
}
