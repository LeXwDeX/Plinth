package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientAddressKey struct{}

// resolveClientAddress names the client of a request (#256). X-Forwarded-For
// is client input unless the TCP peer is one of the configured trusted proxies;
// then the chain is read from the right and the first hop that is not itself a
// trusted proxy is the client. Entries left of it may be forged and are ignored.
func resolveClientAddress(request *http.Request, trusted []netip.Prefix) string {
	peer := peerAddress(request)
	address, err := netip.ParseAddr(peer)
	if err != nil || !addressTrusted(address, trusted) {
		return peer
	}
	hops := strings.Split(strings.Join(request.Header.Values("X-Forwarded-For"), ","), ",")
	for index := len(hops) - 1; index >= 0; index-- {
		hop, err := parseForwardedHop(hops[index])
		if err != nil {
			return peer
		}
		hop = hop.Unmap()
		if !addressTrusted(hop, trusted) {
			return hop.String()
		}
	}
	return peer
}

// parseForwardedHop reads one X-Forwarded-For entry. Some proxies append the
// client port ("1.2.3.4:5555", "[2001:db8::1]:5555"); it is not part of the
// address, and without stripping it the whole chain fell back to the proxy.
func parseForwardedHop(raw string) (netip.Addr, error) {
	hop := strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(hop); err == nil {
		hop = host
	}
	return netip.ParseAddr(strings.Trim(hop, "[]"))
}

func addressTrusted(address netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// clientAddressMiddleware resolves the client once so the login throttle, the
// API rate limit and the access log agree on who is asking.
func clientAddressMiddleware(trusted []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		client := resolveClientAddress(request, trusted)
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), clientAddressKey{}, client)))
	})
}

// apiRateLimitMiddleware caps requests per client address per minute under
// prefix. A non-positive limit or an empty prefix disables it.
func apiRateLimitMiddleware(limiter *perMinuteLimiter, prefix string, perMinute int, detail string, next http.Handler) http.Handler {
	if perMinute <= 0 || prefix == "" {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, prefix) && !limiter.allow(rateLimitClientKey(requestPeerAddress(request)), perMinute) {
			writer.Header().Set("Retry-After", "60")
			writeProblem(writer, http.StatusTooManyRequests, "rate_limited", detail)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// requestIsSecureContext reports whether the browser treats the page that made the
// request as a secure context (#134): HTTPS, or a loopback host. HTTPS is seen as
// TLS on this connection, or X-Forwarded-Proto from a configured trusted proxy
// (the proxy appends its own entry last); from anyone else the header is client
// input and ignored, the same rule as X-Forwarded-For.
func requestIsSecureContext(request *http.Request, trusted []netip.Prefix) bool {
	if request.TLS != nil {
		return true
	}
	if hostIsLoopback(request.Host) {
		return true
	}
	address, err := netip.ParseAddr(peerAddress(request))
	if err != nil || !addressTrusted(address, trusted) {
		return false
	}
	protocols := strings.Split(strings.Join(request.Header.Values("X-Forwarded-Proto"), ","), ",")
	return strings.EqualFold(strings.TrimSpace(protocols[len(protocols)-1]), "https")
}

// hostIsLoopback accepts the hosts browsers treat as potentially trustworthy
// without TLS: localhost (and *.localhost) and loopback addresses, with or without a port.
func hostIsLoopback(host string) bool {
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.Unmap().IsLoopback()
}
