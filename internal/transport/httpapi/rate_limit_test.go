package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// New keys used to start with a zero window, so every first request from a new
// client scanned the whole map: n clients cost O(n²). Expired windows are now
// swept at most once per minute.
func TestPerMinuteLimiterSweepsExpiredWindowsOncePerMinute(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	limiter := &perMinuteLimiter{now: func() time.Time { return now }}
	limiter.allow("first", 5)
	swept := limiter.lastSweep
	for index := range 2000 {
		now = now.Add(time.Millisecond)
		limiter.allow(netip.AddrFrom4([4]byte{10, 0, byte(index >> 8), byte(index)}).String(), 5)
	}
	if !limiter.lastSweep.Equal(swept) || len(limiter.windows) != 2001 {
		t.Fatalf("swept again within the window: last sweep %s (first %s), %d windows", limiter.lastSweep, swept, len(limiter.windows))
	}
	now = now.Add(time.Minute)
	limiter.allow("later", 5)
	if len(limiter.windows) != 1 || !limiter.lastSweep.Equal(now) {
		t.Fatalf("expired windows kept: %d windows, last sweep %s", len(limiter.windows), limiter.lastSweep)
	}
}

// One IPv6 subscriber usually holds a whole /64, so the API rate limit and
// the login throttle count it as one client; IPv4 stays per address.
func TestRateLimitKeyGroupsIPv6ByPrefix64(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ client, want string }{
		{"2001:db8:1:2:aaaa::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"},
		{"203.0.113.9", "203.0.113.9"},
		{"not-an-ip", "not-an-ip"},
	} {
		if got := rateLimitClientKey(test.client); got != test.want {
			t.Fatalf("key(%q) = %q, want %q", test.client, got, test.want)
		}
	}
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	limiter := &perMinuteLimiter{now: func() time.Time { return now }}
	handler := apiRateLimitMiddleware(limiter, "/api/", 2, "too many", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	codes := []int{}
	for _, peer := range []string{"[2001:db8::1]:1", "[2001:db8::2]:1", "[2001:db8::3]:1", "[2001:db8:0:1::1]:1"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		request.RemoteAddr = peer
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		codes = append(codes, response.Code)
	}
	if codes[0] != http.StatusNoContent || codes[1] != http.StatusNoContent ||
		codes[2] != http.StatusTooManyRequests || codes[3] != http.StatusNoContent {
		t.Fatalf("codes = %v", codes)
	}
}
