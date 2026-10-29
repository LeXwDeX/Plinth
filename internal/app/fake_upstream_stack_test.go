package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

// fakeAssetHost is the host the fake upstream writes into artifact URLs for a
// stack run. Tripo's native processing only reads source models from public
// HTTPS hosts, so the stack maps this host back to the fake. ".test" never
// resolves; the mapping below is the only way the name reaches a socket.
const fakeAssetHost = "fake-assets.example.test"

type fakeStackTransport func(*http.Request) (*http.Response, error)

func (f fakeStackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeStackHTTPTransport routes the fake artifact host of the Tripo
// connection (and its anonymous native-source client) to the fake upstream's
// plain-HTTP address. Everything else keeps the connection's own transport.
func fakeStackHTTPTransport(fake *url.URL) func(string, http.RoundTripper) http.RoundTripper {
	direct := &http.Transport{Proxy: nil}
	return func(connectionID string, base http.RoundTripper) http.RoundTripper {
		if connectionID != "tripo" && connectionID != "tripo.native-source" {
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

// TestFakeUpstreamStack runs a full Plinth application in live mode against the
// fake upstream and serves HTTP until interrupted (issue #96). It is only the
// host process of scripts/smoke/smoke-fake-stack.py: unset
// FAKE_UPSTREAM_STACK_URL skips it, and nothing here is reachable from a
// production binary (assemblyOverrides is private to test binaries).
//
// The test, not cmd/art-ai, hosts the stack because Tripo's native processing
// refuses non-public hosts and plain HTTP by design; the override maps one
// reserved ".test" host onto the loopback fake instead of loosening that rule.
func TestFakeUpstreamStack(t *testing.T) {
	raw := os.Getenv("FAKE_UPSTREAM_STACK_URL")
	if raw == "" {
		t.Skip("set FAKE_UPSTREAM_STACK_URL to host a stack against the fake upstream")
	}
	fake, err := url.Parse(raw)
	if err != nil || fake.Scheme != "http" {
		t.Fatalf("FAKE_UPSTREAM_STACK_URL must be the fake's http origin")
	}
	if host, _, splitErr := net.SplitHostPort(fake.Host); splitErr != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("the fake upstream must be on loopback")
	}
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	application, err := newWithDependencies(config, logger, assemblyOverrides{httpTransport: fakeStackHTTPTransport(fake)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := application.Run(ctx); err != nil {
		t.Fatalf("application stopped: %v", err)
	}
}
