package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The traffic limits of the external MCP listener (#332), declared by
// application.access_keys.rate_limit. Two counters guard it:
//
//   - requests per access key per minute (mcpKeyRequests), counted after the key
//     has authenticated and before any tool or download runs, so the JSON-RPC
//     endpoint and the download route draw from one window;
//   - failed authentications per client address per minute (mcpAuthFailures),
//     asked before the key is looked up. An address that reached the limit gets
//     429 until its minute ends, so a throttled address never makes the server
//     look up a key, and sequential guessing is capped per address. Only failures are
//     counted, and a success does not clear the count: holding one valid key must
//     not make guessing free. An address shared by several callers (a NAT, a
//     proxy that is not a trusted one) is throttled as one.
//
// Both are fixed one-minute windows in the memory of this process
// (perMinuteLimiter): a restart starts every window clean and several instances
// each allow the full count. Persisting them is left for when the listener runs
// as more than one instance. The key count is exact (one atomic step). The
// failure limit holds for attempts made one after another: the check and the
// count are two steps around the key lookup, so attempts in flight at once
// overshoot it by their number. Reserving a slot per attempt would close that,
// but would throttle valid callers sharing an address while their keys are
// checked; the secret (256 bits) stays unguessable either way. A Catalog that declares no limits
// limits nothing.

// The JSON-RPC codes of the two refusals, outside every code the protocol and
// the rest of the listener use (-32001, -32003, -32020, -32022, ...).
const (
	mcpCodeRateLimited   = -32090
	mcpCodeAuthThrottled = -32091
)

// mcpRateLimit is the Schema's limits of the listener, nil when it declares none.
func (s *Server) mcpRateLimit() *catalog.AccessKeysRateLimitSchema {
	if s.application.AccessKeys == nil {
		return nil
	}
	return s.application.AccessKeys.RateLimit
}

// mcpClientBucket is the bucket the failed authentications of a request count
// against: the TCP peer address (the listener trusts no proxy, see
// MCPPublicHandler), an IPv6 address as its /64. It is never what the request says about itself, which is
// all mcpClient.address reads: that one is for the audit.
func mcpClientBucket(request *http.Request) string {
	return rateLimitClientKey(requestPeerAddress(request))
}

// writeMCPAuthThrottled answers a client address that failed to authenticate
// as often as the Schema allows this minute. No key was looked up.
func (s *Server) writeMCPAuthThrottled(writer http.ResponseWriter, request *http.Request, wait time.Duration, limit int) {
	s.writeMCPLimited(writer, request, mcpCodeAuthThrottled, "auth_throttled", "Authentication throttled",
		s.application.MCPPublic.Messages.AuthThrottled, wait,
		fmt.Errorf("failed authentications of the client address reached the per-minute limit of %d", limit))
}

// writeMCPKeyLimited answers an authenticated key that made as many requests as
// the Schema allows this minute. The access log names the key by its public ID,
// which identifies a key and opens nothing.
func (s *Server) writeMCPKeyLimited(writer http.ResponseWriter, request *http.Request, wait time.Duration, keyID string, limit int) {
	s.writeMCPLimited(writer, request, mcpCodeRateLimited, "rate_limited", "Rate limited",
		s.application.MCPPublic.Messages.RateLimited, wait,
		fmt.Errorf("access key %s reached the per-minute request limit of %d", keyID, limit))
}

// writeMCPLimited answers 429: Retry-After and the JSON-RPC problem carry the
// wait in whole seconds (rounded up, never zero), worded in the request's
// language; the cause goes to the access log only.
func (s *Server) writeMCPLimited(writer http.ResponseWriter, request *http.Request, rpcCode int, code, rpcMessage, message string, wait time.Duration, cause error) {
	seconds := retryAfterSeconds(wait)
	detail := strings.ReplaceAll(s.localizedText(requestLocale(request), message), "{seconds}", strconv.Itoa(seconds))
	writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	s.writeMCPProblemData(writer, request, http.StatusTooManyRequests, rpcCode, code, rpcMessage, detail,
		map[string]any{"retry_after_seconds": seconds}, cause)
}
