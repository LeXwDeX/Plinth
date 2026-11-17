package httpapi

import (
	"net/netip"
	"sync"
	"time"
)

// perMinuteLimiter allows a fixed number of events per key per minute in a
// fixed window. It backs the client error report limit (per owner), the
// general API rate limit (per client address) and the external MCP listener's
// two limits (per access key, and failed authentications per client address).
//
// The counts live in the memory of this process: a restart starts every window
// clean, and several instances each allow the full count.
type perMinuteLimiter struct {
	mu        sync.Mutex
	windows   map[string]perMinuteWindow
	lastSweep time.Time
	now       func() time.Time
}

type perMinuteWindow struct {
	start time.Time
	count int
}

// retryAfter is how long the window still runs at now: the earliest moment a
// refused caller can succeed.
func (window perMinuteWindow) retryAfter(now time.Time) time.Duration {
	return window.start.Add(time.Minute).Sub(now)
}

// current returns the live window of key as of now (a fresh, empty one when the
// key has none or its minute ended). The caller holds limiter.mu; the returned
// window is stored only by the callers that count.
func (limiter *perMinuteLimiter) current(key string) (perMinuteWindow, time.Time) {
	clock := limiter.now
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	if limiter.windows == nil {
		limiter.windows = map[string]perMinuteWindow{}
	}
	// Expired windows are dropped at most once per minute, so a burst of new
	// keys costs O(1) each instead of a full map scan per new key.
	if now.Sub(limiter.lastSweep) >= time.Minute {
		for stale, window := range limiter.windows {
			if now.Sub(window.start) >= time.Minute {
				delete(limiter.windows, stale)
			}
		}
		limiter.lastSweep = now
	}
	window, exists := limiter.windows[key]
	if !exists || now.Sub(window.start) >= time.Minute {
		window = perMinuteWindow{start: now}
	}
	return window, now
}

func (limiter *perMinuteLimiter) allow(key string, perMinute int) bool {
	allowed, _ := limiter.take(key, perMinute)
	return allowed
}

// take counts one event of key unless its window already holds perMinute
// events. A refused event also reports how long the window still runs.
func (limiter *perMinuteLimiter) take(key string, perMinute int) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	window, now := limiter.current(key)
	if window.count >= perMinute {
		return false, window.retryAfter(now)
	}
	window.count++
	limiter.windows[key] = window
	return true, 0
}

// exceeded reports, without counting anything, whether the window of key
// already holds perMinute events, and if so how long it still runs.
func (limiter *perMinuteLimiter) exceeded(key string, perMinute int) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	window, now := limiter.current(key)
	if window.count >= perMinute {
		return true, window.retryAfter(now)
	}
	return false, 0
}

// record counts one event of key that already happened, whatever the count.
func (limiter *perMinuteLimiter) record(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	window, _ := limiter.current(key)
	window.count++
	limiter.windows[key] = window
}

// retryAfterSeconds is a wait as the whole seconds of a Retry-After header:
// rounded up, never zero.
func retryAfterSeconds(wait time.Duration) int {
	return max(1, int((wait+time.Second-1)/time.Second))
}

// rateLimitClientKey is the bucket a client address counts against. An IPv6
// subscriber is usually delegated a whole /64 and can rotate addresses inside
// it freely, so IPv6 addresses count per /64; IPv4 stays per address.
func rateLimitClientKey(client string) string {
	address, err := netip.ParseAddr(client)
	if err != nil {
		return client
	}
	address = address.Unmap()
	if address.Is4() {
		return address.String()
	}
	return netip.PrefixFrom(address.WithZone(""), 64).Masked().String()
}
