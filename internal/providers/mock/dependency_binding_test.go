package mock

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Edit Multiview's provider-bound source must be usable on the local mock
// stack. The mock projects the same bounded, read-only evidence as the Tripo
// Adapter, only for a complete four-view image-to-multiview source, and does
// not unlock other provider-bound sources whose real Adapters check more.
func TestMockProjectsProviderBindingOnlyForACompleteFourViewSource(t *testing.T) {
	var resolver generation.DependencyBindingResolver = New()
	views := func(variants ...string) []generation.Asset {
		assets := make([]generation.Asset, 0, len(variants))
		for _, variant := range variants {
			assets = append(assets, generation.Asset{Role: "image", Variant: variant, Required: true, Size: 3})
		}
		return assets
	}
	request := generation.DependencyBindingRequest{
		Offering: generation.Offering{ProviderID: "tripo", Capability: schema.ImageToMultiview}, TaskID: "task-a", AttemptID: "attempt-a",
		Job:    generation.UpstreamJob{ProviderID: "tripo", RemoteID: "mock-remote"},
		Assets: views("front", "left", "back", "right"),
	}
	raw, err := resolver.ResolveDependencyBinding(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generation.CanonicalDependencyBinding(raw); err != nil {
		t.Fatalf("binding is not canonical: %s", raw)
	}
	var binding map[string]string
	if err := json.Unmarshal(raw, &binding); err != nil || binding["task_id"] != "task-a" || binding["attempt_id"] != "attempt-a" || binding["remote_id"] != "mock-remote" {
		t.Fatalf("binding = %s", raw)
	}
	for name, broken := range map[string]func(*generation.DependencyBindingRequest){
		"no task":          func(r *generation.DependencyBindingRequest) { r.TaskID = "" },
		"no attempt":       func(r *generation.DependencyBindingRequest) { r.AttemptID = "" },
		"no remote job":    func(r *generation.DependencyBindingRequest) { r.Job.RemoteID = "" },
		"other provider":   func(r *generation.DependencyBindingRequest) { r.Job.ProviderID = "meshy" },
		"rigging source":   func(r *generation.DependencyBindingRequest) { r.Offering.Capability = schema.Rigging },
		"keycap prototype": func(r *generation.DependencyBindingRequest) { r.Offering.Capability = "creative-lab-keycap-prototype" },
		"three views":      func(r *generation.DependencyBindingRequest) { r.Assets = views("front", "left", "back") },
		"a view twice":     func(r *generation.DependencyBindingRequest) { r.Assets = views("front", "left", "back", "back") },
		"an unknown view":  func(r *generation.DependencyBindingRequest) { r.Assets = views("front", "left", "back", "top") },
		"an optional view": func(r *generation.DependencyBindingRequest) { r.Assets[3].Required = false },
		"an empty view":    func(r *generation.DependencyBindingRequest) { r.Assets[0].Size = 0 },
		"no saved output":  func(r *generation.DependencyBindingRequest) { r.Assets = nil },
	} {
		changed := request
		changed.Assets = append([]generation.Asset(nil), request.Assets...)
		broken(&changed)
		if _, err := resolver.ResolveDependencyBinding(changed); err == nil {
			t.Fatalf("%s: binding accepted", name)
		}
	}
}

// An output that is required only under an input condition (an edited view)
// is pinned to minimum 0 in the frozen Offering when the condition is unmet;
// the mock then delivers only the views the request asked for, like Tripo.
func TestMockDeliversOnlyConditionallyRequiredOutputsThatApply(t *testing.T) {
	one, zero := 1, 0
	condition := func(parameter string) *schema.ParameterCondition {
		return &schema.ParameterCondition{Parameter: parameter, Operator: "present"}
	}
	offering := generation.Offering{ProviderID: "tripo", Outputs: []generation.AssetRequirement{
		{Role: "image", Variant: "front", Delivery: generation.AssetDeliveryRequired, Minimum: &zero, RequiredWhen: condition("front_prompt")},
		{Role: "image", Variant: "back", Delivery: generation.AssetDeliveryRequired, Minimum: &one, RequiredWhen: condition("back_prompt")},
		{Role: "image", Variant: "left", Delivery: generation.AssetDeliveryRequired, Minimum: &one},
	}}
	var variants []string
	for _, plan := range planClaims(offering, map[string]any{"back_prompt": "green"}) {
		variants = append(variants, plan.variant)
	}
	if len(variants) != 2 || variants[0] != "back" || variants[1] != "left" {
		t.Fatalf("delivered variants = %v", variants)
	}
}
