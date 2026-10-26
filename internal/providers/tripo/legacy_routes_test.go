package tripo

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyRouteCompletionRequiresMatchingOfferingIdentityAndPreservesPins(t *testing.T) {
	declared := generation.Offering{
		ID: "tripo/test", ProviderID: "tripo", AdapterKey: "tripo.v3", Capability: generation.TextTo3D,
		ProviderOptions: []byte(`{"model":"new-model","endpoint":"current/submit","observe_endpoint":"current/{task_id}"}`),
	}
	a := &Adapter{offeringRoutes: map[generation.OfferingID]generation.Offering{declared.ID: declared}}
	for _, change := range []func(*generation.Offering){
		func(o *generation.Offering) { o.ID = "other" },
		func(o *generation.Offering) { o.ProviderID = "other" },
		func(o *generation.Offering) { o.AdapterKey = "other" },
		func(o *generation.Offering) { o.Capability = generation.ImageTo3D },
	} {
		old := declared
		old.ProviderOptions = []byte(`{"model":"old-model"}`)
		change(&old)
		options, err := a.optionsFor(old)
		if err != nil || options.Endpoint != "" {
			t.Fatalf("mismatched identity used fallback: %#v %v", options, err)
		}
	}
	for _, endpoint := range []string{"pinned/submit", ""} {
		old := declared
		old.ProviderOptions = []byte(`{"model":"old-model","endpoint":"` + endpoint + `"}`)
		options, err := a.optionsFor(old)
		if err != nil || options.Endpoint != endpoint || options.Model != "old-model" || options.ObserveEndpoint != "current/{task_id}" {
			t.Fatalf("did not preserve pinned options: %#v %v", options, err)
		}
	}
}
