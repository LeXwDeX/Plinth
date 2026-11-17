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

// take is allow with the wait of a refusal; exceeded asks without counting;
// record counts an event that already happened. All three read one window per
// key, a fixed minute from its first event.
func TestPerMinuteLimiterTakeExceededAndRecord(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	limiter := &perMinuteLimiter{now: func() time.Time { return now }}

	// Asking never counts, and never creates a window.
	for range 5 {
		if exceeded, wait := limiter.exceeded("a", 2); exceeded || wait != 0 {
			t.Fatalf("exceeded on an empty key = %v %v", exceeded, wait)
		}
	}
	if len(limiter.windows) != 0 {
		t.Fatalf("asking created %d windows", len(limiter.windows))
	}

	// take allows perMinute events, then refuses with the time the window has left.
	for index := range 2 {
		if allowed, wait := limiter.take("a", 2); !allowed || wait != 0 {
			t.Fatalf("take %d = %v %v", index+1, allowed, wait)
		}
		now = now.Add(10 * time.Second)
	}
	if allowed, wait := limiter.take("a", 2); allowed || wait != 40*time.Second {
		t.Fatalf("take over the limit = %v %v, want a refusal with 40s left", allowed, wait)
	}
	if exceeded, wait := limiter.exceeded("a", 2); !exceeded || wait != 40*time.Second {
		t.Fatalf("exceeded over the limit = %v %v", exceeded, wait)
	}
	// Another key has its own window.
	if allowed, _ := limiter.take("b", 2); !allowed {
		t.Fatal("another key was refused")
	}

	// record counts without asking: two failures reach a limit of two, and only then.
	limiter.record("c")
	if exceeded, _ := limiter.exceeded("c", 2); exceeded {
		t.Fatal("one recorded event reached a limit of two")
	}
	limiter.record("c")
	if exceeded, _ := limiter.exceeded("c", 2); !exceeded {
		t.Fatal("two recorded events did not reach a limit of two")
	}

	// A window ends a minute after its first event, whatever came after: a began
	// 60s ago, c 40s ago.
	now = now.Add(40 * time.Second)
	if allowed, _ := limiter.take("a", 2); !allowed {
		t.Fatal("the window of a had ended")
	}
	if exceeded, wait := limiter.exceeded("c", 2); !exceeded || wait != 20*time.Second {
		t.Fatalf("the window of c = %v %v, want it still running for 20s", exceeded, wait)
	}
	now = now.Add(20 * time.Second)
	if exceeded, _ := limiter.exceeded("c", 2); exceeded {
		t.Fatal("the window of c had ended")
	}
}

// A Retry-After value is whole seconds, rounded up and never zero.
func TestRetryAfterSecondsRoundsUpAndIsNeverZero(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		wait time.Duration
		want int
	}{
		{time.Minute, 60}, {59*time.Second + time.Nanosecond, 60}, {30 * time.Second, 30},
		{time.Second + time.Millisecond, 2}, {time.Millisecond, 1}, {0, 1}, {-time.Second, 1},
	} {
		if got := retryAfterSeconds(test.wait); got != test.want {
			t.Fatalf("retryAfterSeconds(%v) = %d, want %d", test.wait, got, test.want)
		}
	}
}
