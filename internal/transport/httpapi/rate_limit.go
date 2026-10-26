package httpapi

import (
	"net/netip"
	"sync"
	"time"
)

// perMinuteLimiter allows a fixed number of events per key per minute in a
// fixed window. It backs the client error report limit (per owner) and the
// general API rate limit (per client address).
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

func (limiter *perMinuteLimiter) allow(key string, perMinute int) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
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
	if window.count >= perMinute {
		return false
	}
	window.count++
	limiter.windows[key] = window
	return true
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
