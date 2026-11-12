package meshy_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #277: Meshy Rigging's skeleton template and finger bones, exercised with the
// shipped Product Schema and the real adapter against the fake meshy/openapi-v1
// upstream. The fake checks the values the Rigging page (read 2026-11-12)
// documents; no real request backs them yet.

const meshyRigging = "meshy/processing/rigging"

// riggingWire normalizes an input the way the kernel freezes it, starts the
// shipped Offering on the fake and returns the body the adapter sent.
func riggingWire(t *testing.T, upstream *recordedMeshy, source, input string) map[string]any {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, normalized, err := product.Normalize(generation.Rigging, meshyRigging, json.RawMessage(input))
	if err != nil {
		t.Fatalf("%s: %v", input, err)
	}
	started, err := upstream.adapter(t, nil).Start(context.Background(), generation.StartRequest{
		OwnerID: "owner-a", TaskID: "task", AttemptID: "attempt", Offering: offering, Input: normalized,
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {
			TaskID: "task-local", Capability: generation.ImageTo3D, ProviderID: "meshy",
			UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: source},
		}},
	})
	if err != nil || started.Job == nil || started.Job.RemoteID == "" {
		t.Fatalf("%s: the adapter or the fake refused the Rigging: %+v, %v", input, started, err)
	}
	return upstream.body("rigging")
}

func TestMeshyRiggingDefaultsKeepTheHistoricalWire(t *testing.T) {
	upstream := newRecordedMeshy(t)
	source := upstream.finishedSource("image-to-3d", map[string]any{"image_url": "https://example.test/a.png", "target_polycount": 10000})
	// The frozen input holds the declared defaults, the wire holds neither: the
	// request of a default Rigging is the one that was accepted before #277.
	body := riggingWire(t, upstream, source, `{"source_task":"task-local","height_meters":1.8}`)
	if len(body) != 2 || body["input_task_id"] != source || body["height_meters"] != 1.8 {
		t.Fatalf("default Rigging wire = %v", body)
	}
	for _, input := range []string{
		`{"source_task":"task-local","skeleton_template":"meshyrig"}`,
		`{"source_task":"task-local","include_fingers":false}`,
	} {
		if body = riggingWire(t, upstream, source, input); len(body) != 1 || body["input_task_id"] != source {
			t.Fatalf("%s: wire = %v", input, body)
		}
	}
}

func TestMeshyRiggingSendsEveryDocumentedSkeletonTemplate(t *testing.T) {
	upstream := newRecordedMeshy(t)
	source := upstream.finishedSource("image-to-3d", map[string]any{"image_url": "https://example.test/a.png", "target_polycount": 10000})
	for _, template := range []string{"mixamo", "ue5_mannequin", "max_biped", "unity_humanoid", "maya_human_ik", "blender_rigify_basic_human", "cc5_hd_neutral_base", "daz_genesis9"} {
		body := riggingWire(t, upstream, source, `{"source_task":"task-local","skeleton_template":"`+template+`"}`)
		if len(body) != 2 || body["skeleton_template"] != template || body["input_task_id"] != source {
			t.Fatalf("%s: wire = %v", template, body)
		}
	}
	body := riggingWire(t, upstream, source, `{"source_task":"task-local","skeleton_template":"unity_humanoid","include_fingers":true,"height_meters":1.7}`)
	if body["skeleton_template"] != "unity_humanoid" || body["include_fingers"] != true || body["height_meters"] != 1.7 || len(body) != 4 {
		t.Fatalf("template with fingers wire = %v", body)
	}
	if body = riggingWire(t, upstream, source, `{"source_task":"task-local","include_fingers":true}`); body["include_fingers"] != true || len(body) != 2 {
		t.Fatalf("fingers wire = %v", body)
	}
}

func TestMeshyRiggingRefusesWhatTheSchemaDoesNotDeclare(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"an unknown template":        `{"source_task":"task-local","skeleton_template":"godot_humanoid"}`,
		"fingers that are no switch": `{"source_task":"task-local","include_fingers":"yes"}`,
		"fingers with a texture":     `{"source_task":"task-local","include_fingers":true,"texture_image_url":"https://example.test/skin.png"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, normalized, err := product.Normalize(generation.Rigging, meshyRigging, json.RawMessage(input)); err == nil {
				t.Fatalf("accepted %s: %s", input, normalized)
			} else if name == "fingers with a texture" && !strings.Contains(err.Error(), "手指骨骼") {
				t.Fatalf("the refusal does not say why: %v", err)
			}
		})
	}
	// Without fingers a texture image stays allowed.
	if _, _, err := product.Normalize(generation.Rigging, meshyRigging, json.RawMessage(`{"source_task":"task-local","include_fingers":false,"texture_image_url":"https://example.test/skin.png"}`)); err != nil {
		t.Fatal(err)
	}
}
