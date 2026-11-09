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

// fakeStackHTTPTransport is the fake shape's artifact-host routing
// (fakeShapeHTTPTransport, #219) for the test-hosted stacks of #96.
func fakeStackHTTPTransport(fake *url.URL) func(string, http.RoundTripper) http.RoundTripper {
	return fakeShapeHTTPTransport(fake)
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
		return fakeShapeTransport(func(r *http.Request) (*http.Response, error) {
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
		return fakeShapeTransport(func(*http.Request) (*http.Response, error) {
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
