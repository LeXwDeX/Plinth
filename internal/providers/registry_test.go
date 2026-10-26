package providers_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

func TestRegistryCanLimitOneProtocolAdapterByCapability(t *testing.T) {
	registry := providers.NewRegistry()
	if err := registry.RegisterCapabilities(
		"meshy.v2",
		mock.New(),
		generation.TextTo3D,
		generation.ImageTo3D,
	); err != nil {
		t.Fatal(err)
	}
	if _, available := registry.Provider(generation.Offering{
		AdapterKey: "meshy.v2",
		Capability: generation.TextTo3D,
	}); !available {
		t.Fatal("text-to-3d should be available")
	}
	if _, available := registry.Provider(generation.Offering{
		AdapterKey: "meshy.v2",
		Capability: generation.Remesh,
	}); available {
		t.Fatal("unimplemented remesh must not be advertised as available")
	}
}
