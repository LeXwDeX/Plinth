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

// #134: Clear-Site-Data is only honoured in a secure context, so the header is sent
// for HTTPS (directly or via a trusted proxy) and loopback hosts, never for plain HTTP
// on an IP address, and X-Forwarded-Proto from an untrusted peer is ignored.
func TestRequestIsSecureContext(t *testing.T) {
	t.Parallel()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, test := range []struct {
		name, target, peer, proto string
		want                      bool
	}{
		{"direct TLS", "https://203.0.113.5/api/auth/session", "203.0.113.9:5000", "", true},
		{"plain HTTP on an IP address", "http://203.0.113.5:8080/api/auth/session", "203.0.113.9:5000", "", false},
		{"plain HTTP on a name", "http://plinth.example.test/api/auth/session", "203.0.113.9:5000", "", false},
		{"localhost", "http://localhost:3000/api/auth/session", "203.0.113.9:5000", "", true},
		{"subdomain of localhost", "http://app.localhost/api/auth/session", "203.0.113.9:5000", "", true},
		{"IPv4 loopback", "http://127.0.0.1:8080/api/auth/session", "203.0.113.9:5000", "", true},
		{"IPv6 loopback", "http://[::1]:8080/api/auth/session", "203.0.113.9:5000", "", true},
		{"a host that only starts with localhost", "http://localhost.evil.test/api/auth/session", "203.0.113.9:5000", "", false},
		{"trusted proxy forwards HTTPS", "http://plinth.example.test/api/auth/session", "10.1.2.3:443", "https", true},
		{"trusted proxy forwards HTTP", "http://plinth.example.test/api/auth/session", "10.1.2.3:443", "http", false},
		{"trusted proxy appended HTTPS after a client-supplied value", "http://plinth.example.test/api/auth/session", "10.1.2.3:443", "http, https", true},
		{"trusted proxy appended HTTP after a client-supplied HTTPS", "http://plinth.example.test/api/auth/session", "10.1.2.3:443", "https, http", false},
		{"untrusted peer cannot claim HTTPS", "http://plinth.example.test/api/auth/session", "203.0.113.9:5000", "https", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, test.target, nil)
			request.RemoteAddr = test.peer
			if test.proto != "" {
				request.Header.Set("X-Forwarded-Proto", test.proto)
			}
			if got := requestIsSecureContext(request, trusted); got != test.want {
				t.Fatalf("secure context = %v, want %v", got, test.want)
			}
		})
	}
}
