package hyper3d_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
)

// #460 B21 / Hyper3D check-status: a non-terminal poll honors Retry-After.
// Hyper3D otherwise backs off 5, 10, 15 ... 30 seconds; the header wins.
func TestHyper3DPollHonoursRetryAfter(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{PollRetryAfter: 7 * time.Second}, hyper3dapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/hyper3d/api/v2", server.Client(), nil)
	started, err := port.Start(context.Background(), generation.StartRequest{
		Offering: offering(generation.TextTo3D),
		Input:    []byte(`{"prompt":"a small robot"}`),
	})
	if err != nil || started.Job == nil {
		t.Fatalf("start = %+v, %v", started, err)
	}
	observed, err := port.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: offering(generation.TextTo3D),
		Job: *started.Job, Opaque: started.Opaque,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.PollAfter != 7*time.Second {
		t.Fatalf("PollAfter = %v, want the Retry-After 7s", observed.PollAfter)
	}
}

// #460 C30: the Bang and Texture material sets accept Hybrid.
func TestHyper3DProcessingMaterialAcceptsHybrid(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []generation.OfferingID{"hyper3d/bang/local-model", "hyper3d/texture/local-model"} {
		base, ok := taskCatalog.Get(id)
		if !ok {
			t.Fatalf("offering %s is missing", id)
		}
		clone := withMaterial(t, base, "Hybrid")
		if err := hyper3d.ValidateOffering(clone); err != nil {
			t.Fatalf("%s must accept material Hybrid: %v", id, err)
		}
		if err := hyper3d.ValidateOffering(withMaterial(t, base, "Plaster")); err == nil {
			t.Fatalf("%s must reject an unknown material", id)
		}
	}
}

func withMaterial(t *testing.T, base generation.Offering, material string) generation.Offering {
	t.Helper()
	clone := base
	var options map[string]any
	if err := json.Unmarshal(clone.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	template, ok := options["request_template"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no request_template", clone.ID)
	}
	template["material"] = material
	options["request_template"] = template
	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	clone.ProviderOptions = encoded
	return clone
}

var _ = http.StatusOK
