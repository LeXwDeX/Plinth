package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #468: the default Schema declares the pool's policy values (limit, queue
// poll, idle TTL, wait hint) with defaults and environment overrides.
func TestDefaultCatalogDeclaresUpstreamPoolSettings(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var tripo catalog.ProviderConnectionSchema
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID == "tripo" {
			tripo = connection
		}
	}
	if tripo.Concurrency == nil {
		t.Fatal("tripo connection declares no concurrency")
	}
	pool, ok := tripo.Concurrency.Pools["model-processing"]
	if !ok {
		t.Fatalf("tripo connection has no model-processing pool: %+v", tripo.Concurrency)
	}
	if pool.LimitEnv != "TRIPO_CONVERSION_CONCURRENCY" || pool.Default != 5 {
		t.Fatalf("limit = %d/%q, want 5/TRIPO_CONVERSION_CONCURRENCY", pool.Default, pool.LimitEnv)
	}
	if pool.QueuePollSeconds == nil || pool.QueuePollSeconds.Default != 2 ||
		pool.QueuePollSeconds.Env != "TRIPO_POOL_QUEUE_POLL_SECONDS" {
		t.Fatalf("queue_poll_seconds = %+v", pool.QueuePollSeconds)
	}
	if pool.IdleTTLSeconds == nil || pool.IdleTTLSeconds.Default != 600 ||
		pool.IdleTTLSeconds.Env != "TRIPO_POOL_IDLE_TTL_SECONDS" {
		t.Fatalf("idle_ttl_seconds = %+v", pool.IdleTTLSeconds)
	}
	if pool.WaitHintSeconds == nil || pool.WaitHintSeconds.Default != 2 ||
		pool.WaitHintSeconds.Env != "TRIPO_POOL_WAIT_HINT_SECONDS" {
		t.Fatalf("wait_hint_seconds = %+v", pool.WaitHintSeconds)
	}
}

// #468: each policy value is range-checked, its environment variable must be a
// valid name, and the idle TTL must cover the largest poll interval.
func TestUpstreamPoolSettingsRejectInvalidValues(t *testing.T) {
	for name, test := range map[string]struct {
		edit func(pool map[string]any)
		want string
	}{
		"queue poll zero": {func(pool map[string]any) {
			pool["queue_poll_seconds"] = map[string]any{"default": 0}
		}, "queue_poll_seconds"},
		"queue poll too large": {func(pool map[string]any) {
			pool["queue_poll_seconds"] = map[string]any{"default": 301}
		}, "queue_poll_seconds"},
		"idle ttl below the poll interval": {func(pool map[string]any) {
			pool["idle_ttl_seconds"] = map[string]any{"default": 1}
		}, "idle_ttl_seconds"},
		"wait hint zero": {func(pool map[string]any) {
			pool["wait_hint_seconds"] = map[string]any{"default": 0}
		}, "wait_hint_seconds"},
		"invalid environment name": {func(pool map[string]any) {
			pool["wait_hint_seconds"] = map[string]any{"env": "lower-case", "default": 2}
		}, "环境变量名"},
		"negative limit": {func(pool map[string]any) {
			pool["default"] = -1
		}, "default 不能为负数"},
	} {
		t.Run(name, func(t *testing.T) {
			document := defaultCatalogDocument(t)
			test.edit(tripoPoolMap(t, document))
			if _, err := loadDocument(t, document); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

// A valid idle TTL that covers the poll interval loads, and the pool keeps the
// declared values.
func TestUpstreamPoolSettingsAcceptAValidIdleTTL(t *testing.T) {
	document := defaultCatalogDocument(t)
	pool := tripoPoolMap(t, document)
	pool["idle_ttl_seconds"] = map[string]any{"default": 3}
	product, err := loadDocument(t, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID != "tripo" || connection.Concurrency == nil {
			continue
		}
		if got := connection.Concurrency.Pools["model-processing"].IdleTTLSeconds; got == nil || got.Default != 3 {
			t.Fatalf("idle_ttl_seconds = %+v", got)
		}
	}
}

func tripoPoolMap(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	connection := connectionMap(t, document, "tripo")
	concurrency, ok := connection["concurrency"].(map[string]any)
	if !ok {
		t.Fatal("tripo connection has no concurrency")
	}
	pools, ok := concurrency["pools"].(map[string]any)
	if !ok {
		t.Fatal("tripo concurrency has no pools")
	}
	pool, ok := pools["model-processing"].(map[string]any)
	if !ok {
		t.Fatal("tripo has no model-processing pool")
	}
	return pool
}
