package catalog

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #186 route B (2026-10-20): Rig v3 is enabled alongside the accepted v2.5 Rig,
// pending its own dev-test public delivery acceptance (reverted if it fails).
func TestTripoRigV3EnabledKeepsAcceptedRigAvailable(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	accepted, ok := c.Get("tripo/processing/rigging")
	if !ok || !accepted.Enabled {
		t.Fatal("previously accepted v2.5 Rig must remain available")
	}
	candidate, ok := c.Get("tripo/processing/rigging-v3")
	if !ok {
		t.Fatal("missing explicit latest Rig candidate")
	}
	if !candidate.Enabled || candidate.Capability != schema.Rigging || candidate.Revision != "2026-10-20.1" || candidate.EstimateTotalCredits(json.RawMessage(`{"source_task":"source","output_formats":["FBX"],"authorize_extra_credits":true}`)) != 35 {
		t.Fatalf("Rig v3 availability or quote changed: %+v", candidate)
	}
	var wire struct {
		Model            string
		Endpoint         string
		ObserveEndpoint  string `json:"observe_endpoint"`
		ConvertEndpoint  string `json:"convert_endpoint"`
		NativeConversion struct {
			Formats []struct{ Format string }
		} `json:"native_conversion"`
	}
	if err := json.Unmarshal(candidate.ProviderOptions, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Model != "v3.0-20260909" || wire.Endpoint != "animations/rig" || wire.ObserveEndpoint != "tasks/{task_id}" || wire.ConvertEndpoint != "models/convert" || len(wire.NativeConversion.Formats) != 1 || wire.NativeConversion.Formats[0].Format != "FBX" {
		t.Fatalf("latest Rig route or export changed: %+v", wire)
	}
	source := candidate.Parameters[0]
	if !source.SameProvider || source.TaskSourceKind != schema.TaskSourceUpstreamJob || !slices.Equal(source.TaskOfferings, []schema.OfferingID{"tripo/tripo-v3.1-full/text-to-3d", "tripo/tripo-v3.1-full/image-to-3d"}) {
		t.Fatalf("latest Rig source widened: %+v", source)
	}
	for name, value := range map[string]string{"rig_type": "biped", "spec": "tripo", "out_format": "glb"} {
		p, ok := offeringParameter(candidate.Parameters, name)
		if !ok || !p.Fixed || p.Presentation == nil || p.Presentation.Surface != "readonly" || string(p.Default) != `"`+value+`"` {
			t.Fatalf("%s is not frozen to %s: %+v", name, value, p)
		}
	}
	if len(candidate.Outputs) != 3 || candidate.Outputs[0].Fidelity != "skeleton" || candidate.Outputs[1].Fidelity != "skeleton" || candidate.Outputs[1].DeliveryProfile != "binary-fbx-v1" {
		t.Fatalf("candidate lost required skeleton delivery: %+v", candidate.Outputs)
	}
	listed := 0
	for _, offering := range c.List(schema.Rigging) {
		if offering.ID == candidate.ID {
			listed++
		}
	}
	legacy := 0
	for _, model := range c.Application().ProviderDiscovery.LegacyModels {
		if model.ModelID == candidate.ModelID && model.ProviderKey == "tripo" {
			legacy++
		}
	}
	if listed != 1 || legacy != 1 {
		t.Fatalf("Rig v3 public listing=%d legacy registration=%d", listed, legacy)
	}
}
