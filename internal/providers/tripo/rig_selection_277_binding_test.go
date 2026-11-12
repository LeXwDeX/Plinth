package tripo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The Retarget contract lists the Rig configurations it animates and the presets
// each takes (#277). A Rig the contract does not list is a source refusal; a
// listed Rig asked for a preset it does not take is a refusal of the preset, so
// the person is told which choice to change. Every refusal comes before any
// dispatch.
func TestRetargetSourceRigRulesNameWhatToChange(t *testing.T) {
	a := &Adapter{}
	validate := func(rigType, spec, action string) error {
		r := nativeProcessingFixture(generation.Animation)
		r.Offering = shippedOffering(t, "tripo/processing/animation")
		r.Input = json.RawMessage(`{"source_task":"source","animation_action":"` + action + `","out_format":"glb","export_with_geometry":true,"animate_in_place":false}`)
		d := r.TaskDependencies["source_task"]
		raw, _ := json.Marshal(nativeRigBinding{TaskID: d.TaskID, AttemptID: "rig-attempt", RemoteID: d.UpstreamJob.RemoteID, OfferingID: d.OfferingID, OfferingRevision: "2026-11-12.277.1", OfferingSHA256: testAssetSHA, Model: "v3.0-20260909", RigType: rigType, Spec: spec})
		d.ProviderBinding = raw
		r.TaskDependencies["source_task"] = d
		return a.ValidateInput(context.Background(), validation(r))
	}
	for name, tc := range map[string]struct {
		rigType, spec, action string
		refused               string // "", "source" or "action"
	}{
		"a biped takes a biped preset":                  {"biped", "tripo", "preset:jump", ""},
		"a quadruped takes its walk":                    {"quadruped", "tripo", "preset:quadruped:walk", ""},
		"an aquatic rig takes its march":                {"aquatic", "tripo", "preset:aquatic:march", ""},
		"a quadruped is asked for a biped preset":       {"quadruped", "tripo", "preset:idle", "action"},
		"a biped is asked for a creature preset":        {"biped", "tripo", "preset:quadruped:walk", "action"},
		"a hexapod is asked for a quadruped walk":       {"hexapod", "tripo", "preset:quadruped:walk", "action"},
		"an avian rig has no preset at all":             {"avian", "tripo", "preset:walk", "source"},
		"an others rig has no preset at all":            {"others", "tripo", "preset:walk", "source"},
		"a biped with an engine naming":                 {"biped", "unreal", "preset:walk", "source"},
		"a biped with another naming":                   {"biped", "mixamo", "preset:walk", "source"},
		"a skeleton type the contract does not know":    {"humanoid", "tripo", "preset:walk", "source"},
		"a creature rig with a naming other than Tripo": {"quadruped", "unity", "preset:quadruped:walk", "source"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validate(tc.rigType, tc.spec, tc.action)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			refusal := generation.UserFacingError(err)
			if refusal == nil {
				t.Fatalf("not refused with a Schema reason: %v", err)
			}
			if tc.refused == "action" && !strings.Contains(refusal.Detail, "动画动作") {
				t.Fatalf("the refusal does not name the action parameter: %q", refusal.Detail)
			}
			if tc.refused == "source" && !strings.Contains(refusal.Detail, "来源") {
				t.Fatalf("the refusal does not name the source: %q", refusal.Detail)
			}
		})
	}
}

// A Rig frozen before the skeleton choice was open carries it nowhere but in
// the values its Offering fixed: the binding reads the choice from the frozen
// input, or from the Offering's own fixed default when the input holds none.
func TestRigBindingReadsTheChoiceFromTheFrozenInputOrTheFixedDefault(t *testing.T) {
	a := &Adapter{}
	request := rigBindingRequest(t, rigV3)
	request.Input = json.RawMessage(`{"source_task":"source","rig_type":"hexapod","spec":"tripo","out_format":"glb"}`)
	raw, err := a.ResolveDependencyBinding(request)
	if err != nil || !strings.Contains(string(raw), `"rig_type":"hexapod"`) {
		t.Fatalf("binding = %s, %v", raw, err)
	}
	legacy := rigBindingRequest(t, rigV3)
	legacy.Input = nil
	for i := range legacy.Offering.Parameters {
		switch legacy.Offering.Parameters[i].Name {
		case "rig_type", "spec":
			legacy.Offering.Parameters[i].Fixed = true
		}
	}
	raw, err = a.ResolveDependencyBinding(legacy)
	if err != nil || !strings.Contains(string(raw), `"rig_type":"biped"`) || !strings.Contains(string(raw), `"spec":"tripo"`) {
		t.Fatalf("legacy binding = %s, %v", raw, err)
	}
}
