package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// #256: X-Forwarded-For names the client only when the TCP peer is a
// configured proxy; otherwise it is client input and ignored.
func TestClientAddressTrustsForwardingOnlyFromConfiguredProxies(t *testing.T) {
	t.Parallel()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.30.0.1/32")}
	for _, test := range []struct {
		name, peer, forwarded, want string
	}{
		{"direct client ignores the header", "203.0.113.9:5000", "198.51.100.1", "203.0.113.9"},
		{"trusted proxy names the client", "10.1.2.3:443", "198.51.100.7", "198.51.100.7"},
		{"rightmost untrusted hop wins over spoofed left entries", "172.30.0.1:80", "6.6.6.6, 198.51.100.8, 10.9.9.9", "198.51.100.8"},
		{"only proxies in the chain falls back to the peer", "10.1.2.3:443", "10.4.4.4", "10.1.2.3"},
		{"garbage header falls back to the peer", "10.1.2.3:443", "not-an-ip", "10.1.2.3"},
		{"no header", "10.1.2.3:443", "", "10.1.2.3"},
		{"rightmost hop with a port", "10.1.2.3:443", "198.51.100.9:5555", "198.51.100.9"},
		{"rightmost IPv6 hop with a port", "10.1.2.3:443", "[2001:db8::9]:5555", "2001:db8::9"},
		{"bracketed IPv6 hop without a port", "10.1.2.3:443", "[2001:db8::a]", "2001:db8::a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
			request.RemoteAddr = test.peer
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-For", test.forwarded)
			}
			if got := resolveClientAddress(request, trusted); got != test.want {
				t.Fatalf("client = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAPIRateLimitCapsEachClientPerMinute(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	limiter := &perMinuteLimiter{now: func() time.Time { return now }}
	handler := apiRateLimitMiddleware(limiter, "/api/", 3, "too many", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	send := func(path, peer string) int {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = peer
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	for range 3 {
		if code := send("/api/v1/tasks", "203.0.113.9:1"); code != http.StatusNoContent {
			t.Fatalf("within limit = %d", code)
		}
	}
	if code := send("/api/v1/tasks", "203.0.113.9:2"); code != http.StatusTooManyRequests {
		t.Fatalf("over limit = %d", code)
	}
	if code := send("/api/v1/tasks", "203.0.113.10:1"); code != http.StatusNoContent {
		t.Fatalf("another client = %d", code)
	}
	if code := send("/healthz", "203.0.113.9:3"); code != http.StatusNoContent {
		t.Fatalf("non-API path = %d", code)
	}
	now = now.Add(time.Minute)
	if code := send("/api/v1/tasks", "203.0.113.9:4"); code != http.StatusNoContent {
		t.Fatalf("next minute = %d", code)
	}
}
