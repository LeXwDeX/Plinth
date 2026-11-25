package meshyopenapiv1

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/core"
)

// #608: an action whose library rig_type is not the rig's answers 400. The
// real refusals of 2026-11-25 (meshy-openapi-v1-animation-rig-type-2026-11-25)
// sent quadruped action 1055 to a biped rig, alone and as action_ids [0, 1055].
func TestAnimationRefusesAnActionOfAnotherRigTypeWithTheRealText(t *testing.T) {
	var realTexts []string
	for _, exchange := range loadSamples(t).Find("POST /openapi/v1/animations", "") {
		if exchange.Response.Status != http.StatusBadRequest || exchange.Metadata.CapturedAt.Format("2006-01-02") != "2026-11-25" {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(exchange.Response.Body.JSON, &body); err != nil {
			t.Fatal(err)
		}
		realTexts = append(realTexts, body["message"].(string))
	}
	if len(realTexts) != 2 {
		t.Fatalf("rig_type samples: %v", realTexts)
	}

	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	biped := f.create("/rigging", map[string]any{"input_task_id": source, "height_meters": 1.7})
	f.finish("/rigging", biped, key)
	for i, body := range []map[string]any{
		{"rig_task_id": biped, "action_id": quadrupedAction},
		{"rig_task_id": biped, "action_ids": []int{0, quadrupedAction}},
	} {
		status, _, answer := f.Do("POST", f.v1("/animations"), key, body)
		if status != http.StatusBadRequest || answer["message"] != realTexts[i] {
			t.Errorf("%v = %d %v, want 400 %q", body, status, answer, realTexts[i])
		}
	}
	// The sample's request body is the one the fake was checked with.
	if status, _, _ := f.Do("POST", f.v1("/animations"), key, map[string]any{"rig_task_id": biped, "action_ids": []int{0, 1, 696}}); status != http.StatusAccepted {
		t.Errorf("biped actions on a biped rig = %d", status)
	}
}

// The library table holds the 2026-11-25 library: 678 biped and 127 quadruped
// actions; every id the Catalog offers is a biped action.
func TestAnimationLibraryRigTypes(t *testing.T) {
	counts := map[string]int{}
	for id := 0; id <= 2000; id++ {
		if rigType := core.ActionRigType(id); rigType != "" {
			counts[rigType]++
		}
	}
	if counts["biped"] != 678 || counts["quadruped"] != 127 {
		t.Errorf("library counts %v, want 678 biped and 127 quadruped", counts)
	}
	for id, want := range map[int]string{0: "biped", 332: "", 696: "biped", 697: "", 1055: "quadruped", 1264: "quadruped", 1069: ""} {
		if got := core.ActionRigType(id); got != want {
			t.Errorf("action %d rig_type %q, want %q", id, got, want)
		}
	}
}
