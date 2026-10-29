//go:build fakestack

package app

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

// TestFakeUpstreamStack runs a full Plinth application in live mode against the
// fake upstream and serves HTTP until interrupted (issue #96). It is only the
// host process of scripts/smoke/smoke-fake-stack.py, which compiles it with
// `go test -c -tags fakestack`. The build tag keeps this interactive entry point
// out of every ordinary `go test` run, so no run reports a permanent skip, and
// nothing here is reachable from a production binary (assemblyOverrides is
// private to test binaries).
//
// The test, not cmd/art-ai, hosts the stack because Tripo's native processing
// refuses non-public hosts and plain HTTP by design; the override maps one
// reserved ".test" host onto the loopback fake instead of loosening that rule.
func TestFakeUpstreamStack(t *testing.T) {
	raw := os.Getenv("FAKE_UPSTREAM_STACK_URL")
	if raw == "" {
		t.Fatal("set FAKE_UPSTREAM_STACK_URL to the fake upstream's http origin")
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
