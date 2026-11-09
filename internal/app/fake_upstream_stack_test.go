package app

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fakeAssetHost is the host the fake upstream writes into artifact URLs for a
// stack run. Tripo's native processing only reads source models from public
// HTTPS hosts, so the stack maps this host back to the fake. ".test" never
// resolves; the mapping below is the only way the name reaches a socket.
const fakeAssetHost = "fake-assets.example.test"

type fakeStackTransport func(*http.Request) (*http.Response, error)

func (f fakeStackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeStackHTTPTransport routes the fake artifact host of the Tripo, Meshy,
// Hyper3D and Jimeng image connections (and the anonymous native-source client
// of Tripo) to the fake upstream's plain-HTTP address. Everything else keeps the connection's own
// transport.
func fakeStackHTTPTransport(fake *url.URL) func(string, http.RoundTripper) http.RoundTripper {
	direct := &http.Transport{Proxy: nil}
	return func(connectionID string, base http.RoundTripper) http.RoundTripper {
		switch connectionID {
		case "tripo", "tripo.native-source", "meshy", "hyper3d", "jimeng-images":
		default:
			return base
		}
		return fakeStackTransport(func(r *http.Request) (*http.Response, error) {
			if !strings.EqualFold(r.URL.Hostname(), fakeAssetHost) {
				return base.RoundTrip(r)
			}
			routed := r.Clone(r.Context())
			routed.URL.Scheme, routed.URL.Host = fake.Scheme, fake.Host
			routed.Host = fake.Host
			return direct.RoundTrip(routed)
		})
	}
}

// fakeStackRefusalMarker starts the line the loopback guard writes for every refused
// request; scripts/smoke/smoke-fake-stack.py fails the run when a stack log holds it
// (#218: the gate proves it made zero real upstream requests).
const fakeStackRefusalMarker = "fake-stack: refused non-loopback request"

// fakeStackLoopbackOnly wraps every connection's transport so a fake stack can only
// reach loopback addresses and the reserved fake artifact host (which the inner
// mapping routes to the loopback fake). Any other destination is refused before a
// socket is opened and reported on log with the connection and host only (never the
// query, which may carry a signature).
func fakeStackLoopbackOnly(inner func(string, http.RoundTripper) http.RoundTripper, log io.Writer) func(string, http.RoundTripper) http.RoundTripper {
	return func(connectionID string, base http.RoundTripper) http.RoundTripper {
		next := inner(connectionID, base)
		return fakeStackTransport(func(r *http.Request) (*http.Response, error) {
			host := r.URL.Hostname()
			if strings.EqualFold(host, fakeAssetHost) || strings.EqualFold(host, "localhost") {
				return next.RoundTrip(r)
			}
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
				return next.RoundTrip(r)
			}
			fmt.Fprintf(log, "%s: connection %s host %s\n", fakeStackRefusalMarker, connectionID, host)
			return nil, fmt.Errorf("%s: %s", fakeStackRefusalMarker, host)
		})
	}
}

func TestFakeStackLoopbackOnlyRefusesNonLoopbackHosts(t *testing.T) {
	reached := 0
	inner := func(string, http.RoundTripper) http.RoundTripper {
		return fakeStackTransport(func(*http.Request) (*http.Response, error) {
			reached++
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
		})
	}
	var log strings.Builder
	transport := fakeStackLoopbackOnly(inner, &log)("tripo", nil)
	for _, target := range []string{"http://127.0.0.1:19410/x", "http://[::1]:19410/x", "http://localhost:19410/x", "https://" + fakeAssetHost + "/a.glb"} {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		if _, err := transport.RoundTrip(request); err != nil {
			t.Fatalf("%s refused: %v", target, err)
		}
	}
	for _, target := range []string{"https://api.tripo3d.ai/v2/openapi/task?sig=secret", "http://10.0.0.1/x", "https://example.com/"} {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		if _, err := transport.RoundTrip(request); err == nil || !strings.Contains(err.Error(), fakeStackRefusalMarker) {
			t.Fatalf("%s not refused: %v", target, err)
		}
	}
	if reached != 4 {
		t.Fatalf("inner transport reached %d times, want 4", reached)
	}
	if strings.Count(log.String(), fakeStackRefusalMarker) != 3 || strings.Contains(log.String(), "secret") {
		t.Fatalf("refusal log %q", log.String())
	}
}
