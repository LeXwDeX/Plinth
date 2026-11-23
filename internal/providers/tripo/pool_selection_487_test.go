package tripo

// pool_selection_487_test.go 锁定 #487：上游并发按 task category 分池，每个
// Offering 声明自己派发走哪个池，一类占满不影响另一类（rate-limits 页，
// https://developers.tripo3d.ai/en/docs/rate-limits，读取 2026-11-23）。

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/upstreampool"
)

func poolOffering(id string, pool string) generation.Offering {
	return generation.Offering{
		ID:              generation.OfferingID(id),
		AdapterKey:      "tripo.v3",
		ProviderID:      "tripo",
		Capability:      generation.TextTo3D,
		ProviderOptions: []byte(`{"endpoint":"generation/text-to-model","pool":"` + pool + `"}`),
	}
}

func TestOfferingDrawsSlotsFromItsOwnDeclaredPool487(t *testing.T) {
	pool := upstreampool.New(upstreampool.Options{WaitHint: time.Second})
	pool.Register("tripo.v3", "generation-h", upstreampool.PoolConfig{Limit: 1})
	pool.Register("tripo.v3", "mesh-operations", upstreampool.PoolConfig{Limit: 1})
	h := poolOffering("tripo/tripo-v3.1/text-to-3d", "generation-h")
	mesh := poolOffering("tripo/geometry/decimate-triangle", "mesh-operations")
	adapter, err := New(Config{
		BaseURL: "https://openapi.tripo3d.ai/v3", APIKey: "tripo-key",
		OfferingRoutes: []generation.Offering{h, mesh}, Pool: pool,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.poolIDFor(h); got != "generation-h" {
		t.Fatalf("H series pool = %q, want generation-h", got)
	}
	if got := adapter.poolIDFor(mesh); got != "mesh-operations" {
		t.Fatalf("decimate pool = %q, want mesh-operations", got)
	}
	ctx := context.Background()
	// One slot per category: both dispatches are admitted at the same time.
	if _, ok, _ := adapter.acquirePool(ctx, h, "h-1"); !ok {
		t.Fatal("generation-h refused its own free slot")
	}
	if _, ok, _ := adapter.acquirePool(ctx, mesh, "mesh-1"); !ok {
		t.Fatal("a busy generation-h pool must not block mesh operations")
	}
	// The same category is still bounded: a second H-series dispatch queues.
	if _, ok, _ := adapter.acquirePool(ctx, h, "h-2"); ok {
		t.Fatal("generation-h admitted more than its limit")
	}
	if _, ok, _ := adapter.acquirePool(ctx, mesh, "mesh-2"); ok {
		t.Fatal("mesh-operations admitted more than its limit")
	}
}

// An Offering that declares no pool keeps no queueing at all: a name nothing
// registered would silently queue nothing, so the declaration has to exist.
func TestOfferingWithoutPoolDeclarationIsNeverQueued487(t *testing.T) {
	pool := upstreampool.New(upstreampool.Options{WaitHint: time.Second})
	pool.Register("tripo.v3", "mesh-operations", upstreampool.PoolConfig{Limit: 1})
	plain := generation.Offering{ID: "tripo/image-to-multiview", AdapterKey: "tripo.v3", Capability: generation.ImageToMultiview}
	adapter, err := New(Config{
		BaseURL: "https://openapi.tripo3d.ai/v3", APIKey: "tripo-key",
		OfferingRoutes: []generation.Offering{plain}, Pool: pool,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.poolIDFor(plain); got != "" {
		t.Fatalf("undeclared pool = %q, want empty", got)
	}
	if adapter.poolEnabled(plain) {
		t.Fatal("an Offering without a pool must not be queued")
	}
}

// A declared pool the connection never registered is not really enabled, which
// is the #478 rule kept for the per-Offering declaration.
func TestOfferingOnUnregisteredPoolIsNotQueued487(t *testing.T) {
	pool := upstreampool.New(upstreampool.Options{WaitHint: time.Second})
	pool.Register("tripo.v3", "generation-h", upstreampool.PoolConfig{Limit: 1})
	mesh := poolOffering("tripo/geometry/decimate-triangle", "mesh-operations")
	adapter, err := New(Config{
		BaseURL: "https://openapi.tripo3d.ai/v3", APIKey: "tripo-key",
		OfferingRoutes: []generation.Offering{mesh}, Pool: pool,
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.poolEnabled(mesh) {
		t.Fatal("an unregistered pool must not queue")
	}
}
