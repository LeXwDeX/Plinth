package tripov3

import (
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The rig and retarget contracts of #277 are constructed from the official
// pages read on rigDocsChecked (see rig.go); no real request backs them yet.

func (f *fixture) generated() string {
	f.t.Helper()
	gen := f.create("/generation/text-to-model", textBody(), key)
	f.finish(gen, key)
	return gen
}

// rigged creates a finished rig and returns its task id.
func (f *fixture) rigged(model, rigType string) string {
	f.t.Helper()
	body := map[string]any{"input": f.generated(), "model": model}
	if rigType != "" {
		body["rig_type"] = rigType
	}
	rig := f.create("/animations/rig", body, key)
	f.finish(rig, key)
	return rig
}

func TestRigAcceptsEverySpecTheDocumentationListsOnAV3BipedRig(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.generated()
	for _, spec := range []string{"tripo", "mixamo", "actorcore", "unity", "unreal", "vrm"} {
		rig := f.create("/animations/rig", map[string]any{"input": gen, "model": rigModelV3, "rig_type": "biped", "spec": spec, "out_format": "fbx"}, key)
		_, done := f.finish(rig, key)
		if field(t, done, "data", "status") != statusSuccess || field(t, done, "data", "type") != "animate_rig" {
			t.Fatalf("%s: %v", spec, done)
		}
		// The accepted request is observable in the poll, as the real poll
		// repeats it (and calls the rig type "topology").
		if field(t, done, "data", "input", "spec") != spec || field(t, done, "data", "input", "topology") != "biped" || field(t, done, "data", "input", "out_format") != "fbx" {
			t.Fatalf("%s: poll does not echo the request: %v", spec, field(t, done, "data", "input"))
		}
	}
}

func TestRigAcceptsEveryRigTypeTheDocumentationLists(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.generated()
	for _, rigType := range []string{"biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic", "others"} {
		rig := f.create("/animations/rig", map[string]any{"input": gen, "model": rigModelV3, "rig_type": rigType, "spec": "tripo"}, key)
		_, done := f.finish(rig, key)
		if field(t, done, "data", "input", "topology") != rigType {
			t.Fatalf("%s: %v", rigType, field(t, done, "data", "input"))
		}
	}
	// v2.5 rigs the non-humanoid types; a missing rig_type is the documented biped.
	for _, rigType := range []string{"quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic"} {
		rig := f.create("/animations/rig", map[string]any{"input": gen, "model": rigModelV25, "rig_type": rigType}, key)
		f.finish(rig, key)
	}
	rig := f.create("/animations/rig", map[string]any{"input": gen, "model": rigModelV3}, key)
	_, done := f.finish(rig, key)
	if field(t, done, "data", "input", "topology") != "biped" || field(t, done, "data", "input", "spec") != "tripo" || field(t, done, "data", "input", "out_format") != "glb" {
		t.Fatalf("defaults are not echoed: %v", field(t, done, "data", "input"))
	}
}

// A spec has no effect on a non-biped rig: the documentation does not refuse
// it, so neither does the fake.
func TestRigDoesNotRefuseASpecOnANonBipedRig(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	f.create("/animations/rig", map[string]any{"input": f.generated(), "model": rigModelV3, "rig_type": "quadruped", "spec": "unreal"}, key)
}

func TestRigRefusesWhatTheDocumentationDoesNotList(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := f.generated()
	for name, body := range map[string]map[string]any{
		"unknown rig type":         {"input": gen, "model": rigModelV3, "rig_type": "humanoid"},
		"empty rig type":           {"input": gen, "model": rigModelV3, "rig_type": ""},
		"unknown spec":             {"input": gen, "model": rigModelV3, "spec": "godot"},
		"unknown model":            {"input": gen, "model": "v9.0"},
		"unknown format":           {"input": gen, "model": rigModelV3, "out_format": "obj"},
		"others on v2.5":           {"input": gen, "model": rigModelV25, "rig_type": "others"},
		"others on v1.0":           {"input": gen, "model": rigModelV1, "rig_type": "others"},
		"quadruped on v1.0":        {"input": gen, "model": rigModelV1, "rig_type": "quadruped"},
		"unity on v2.5":            {"input": gen, "model": rigModelV25, "spec": "unity"},
		"unreal on v2.5":           {"input": gen, "model": rigModelV25, "spec": "unreal"},
		"actorcore on v1.0":        {"input": gen, "model": rigModelV1, "spec": "actorcore"},
		"vrm on v2.5":              {"input": gen, "model": rigModelV25, "spec": "vrm", "rig_type": "biped"},
		"spec is not a string":     {"input": gen, "model": rigModelV3, "spec": 3},
		"rig type is not a string": {"input": gen, "model": rigModelV3, "rig_type": true},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, response := f.do("POST", "/animations/rig", key, body)
			if status != http.StatusBadRequest || field(t, response, "code").(float64) != 1004 {
				t.Fatalf("status %d body %v", status, response)
			}
		})
	}
	// mixamo is documented for every model.
	for _, model := range []string{rigModelV1, rigModelV25, rigModelV3} {
		f.create("/animations/rig", map[string]any{"input": gen, "model": model, "spec": "mixamo"}, key)
	}
}

func TestRetargetAcceptsEveryDocumentedPresetOnTheRigItFits(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	biped := f.rigged(rigModelV3, "biped")
	for _, preset := range []string{"preset:idle", "preset:walk", "preset:run", "preset:dive", "preset:climb", "preset:jump", "preset:slash", "preset:shoot", "preset:hurt", "preset:fall", "preset:turn"} {
		retarget := f.create("/animations/retarget", map[string]any{"input": biped, "animation": preset, "out_format": "glb"}, key)
		statuses, done := f.finish(retarget, key)
		if statuses[len(statuses)-1] != statusSuccess || field(t, done, "data", "input", "animation") != preset {
			t.Fatalf("%s: %v %v", preset, statuses, done)
		}
	}
	for _, creature := range []struct{ rigType, preset string }{
		{"quadruped", "preset:quadruped:walk"}, {"hexapod", "preset:hexapod:walk"}, {"octopod", "preset:octopod:walk"},
		{"serpentine", "preset:serpentine:march"}, {"aquatic", "preset:aquatic:march"},
	} {
		// The retarget page lists the creature presets for rigs built with v2.5 or v3.0.
		for _, model := range []string{rigModelV25, rigModelV3} {
			rig := f.rigged(model, creature.rigType)
			retarget := f.create("/animations/retarget", map[string]any{"input": rig, "animation": creature.preset}, key)
			if statuses, done := f.finish(retarget, key); statuses[len(statuses)-1] != statusSuccess {
				t.Fatalf("%s on %s: %v", creature.preset, model, done)
			}
		}
	}
	// The v1.0 names exist on a v1.0 biped rig only.
	v1 := f.rigged(rigModelV1, "biped")
	retarget := f.create("/animations/retarget", map[string]any{"input": v1, "animation": "preset:biped:wave_goodbye_01"}, key)
	if statuses, _ := f.finish(retarget, key); statuses[len(statuses)-1] != statusSuccess {
		t.Fatalf("v1.0 preset on a v1.0 rig: %v", statuses)
	}
}

// How the real API fails a preset on a rig it does not fit is unverified; the
// fake reuses the failure captured for a v2 rig.
func TestRetargetFailsWhenThePresetDoesNotFitTheRig(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	biped, quadruped, v25Quadruped := f.rigged(rigModelV3, "biped"), f.rigged(rigModelV3, "quadruped"), f.rigged(rigModelV25, "quadruped")
	for name, tc := range map[string]struct{ rig, preset string }{
		"a biped preset on a quadruped rig":      {quadruped, "preset:run"},
		"a biped preset on a v2.5 quadruped rig": {v25Quadruped, "preset:idle"},
		"a quadruped preset on a biped rig":      {biped, "preset:quadruped:walk"},
		"a hexapod preset on a quadruped rig":    {quadruped, "preset:hexapod:walk"},
		"a v1.0 name on a v3.0 biped rig":        {biped, "preset:biped:wave_goodbye_01"},
		"a v3.0 name on a v1.0 biped rig":        {f.rigged(rigModelV1, "biped"), "preset:walk"},
		"a creature preset on an avian rig":      {f.rigged(rigModelV3, "avian"), "preset:quadruped:walk"},
		"a creature preset on an 'others' rig":   {f.rigged(rigModelV3, "others"), "preset:aquatic:march"},
	} {
		t.Run(name, func(t *testing.T) {
			retarget := f.create("/animations/retarget", map[string]any{"input": tc.rig, "animation": tc.preset}, key)
			statuses, body := f.finish(retarget, key)
			if statuses[len(statuses)-1] != statusFailed || field(t, body, "data", "error_code").(float64) != 1004 {
				t.Fatalf("flow %v body %v", statuses, body)
			}
		})
	}
	// One preset that does not fit fails the whole list.
	retarget := f.create("/animations/retarget", map[string]any{"input": quadruped, "animations": []string{"preset:quadruped:walk", "preset:run"}}, key)
	if statuses, _ := f.finish(retarget, key); statuses[len(statuses)-1] != statusFailed {
		t.Fatalf("a list holding a preset that does not fit succeeded: %v", statuses)
	}
}

func TestRetargetTakesAListOfAtMostFivePresets(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	biped := f.rigged(rigModelV3, "biped")
	list := []string{"preset:idle", "preset:walk", "preset:run", "preset:jump", "preset:turn"}
	retarget := f.create("/animations/retarget", map[string]any{"input": biped, "animations": list}, key)
	statuses, done := f.finish(retarget, key)
	if statuses[len(statuses)-1] != statusSuccess {
		t.Fatalf("flow %v", statuses)
	}
	echoed, _ := field(t, done, "data", "input", "animations").([]any)
	if len(echoed) != 5 {
		t.Fatalf("the list is not echoed: %v", field(t, done, "data", "input"))
	}
	if _, single := fakeupstream.Dig(done, "data", "input", "animation"); single {
		t.Fatalf("a list request must not echo a single animation: %v", field(t, done, "data", "input"))
	}
	for name, body := range map[string]map[string]any{
		"six presets":            {"input": biped, "animations": append(list, "preset:fall")},
		"both forms":             {"input": biped, "animation": "preset:walk", "animations": []string{"preset:run"}},
		"neither form":           {"input": biped},
		"empty list":             {"input": biped, "animations": []string{}},
		"unknown preset":         {"input": biped, "animation": "preset:moonwalk"},
		"unknown in list":        {"input": biped, "animations": []string{"preset:walk", "preset:moonwalk"}},
		"a bare name":            {"input": biped, "animation": "walk"},
		"an unknown biped name":  {"input": biped, "animation": "preset:biped:moonwalk"},
		"unknown format":         {"input": biped, "animation": "preset:walk", "out_format": "obj"},
		"geometry not a boolean": {"input": biped, "animation": "preset:walk", "export_with_geometry": "yes"},
		"in place not a boolean": {"input": biped, "animation": "preset:walk", "animate_in_place": 1},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, response := f.do("POST", "/animations/retarget", key, body)
			if status != http.StatusBadRequest || field(t, response, "code").(float64) != 1004 {
				t.Fatalf("status %d body %v", status, response)
			}
		})
	}
}

func TestRetargetEchoesItsSwitches(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	biped := f.rigged(rigModelV3, "biped")
	retarget := f.create("/animations/retarget", map[string]any{
		"input": biped, "animation": "preset:run", "out_format": "fbx", "export_with_geometry": false, "animate_in_place": true,
	}, key)
	_, done := f.finish(retarget, key)
	input, _ := field(t, done, "data", "input").(map[string]any)
	if input["animation"] != "preset:run" || input["out_format"] != "fbx" || input["export_with_geometry"] != false || input["animate_in_place"] != true {
		t.Fatalf("poll does not echo the request: %v", input)
	}
	// An omitted switch reads as its documented default.
	retarget = f.create("/animations/retarget", map[string]any{"input": biped, "animation": "preset:walk"}, key)
	_, done = f.finish(retarget, key)
	input, _ = field(t, done, "data", "input").(map[string]any)
	if input["out_format"] != "glb" || input["export_with_geometry"] != true || input["animate_in_place"] != false {
		t.Fatalf("defaults are not echoed: %v", input)
	}
}
