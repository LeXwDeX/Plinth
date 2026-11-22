package generationtool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #418: asset CDNs label model files from the host's mime table, so a saved
// GLB can carry application/x-tgif or application/octet-stream. The read-only
// inspection judges the saved bytes, not that label.
func TestModelReviewInspectsAGLBWithAnUninformativeStoredMediaType(t *testing.T) {
	glb := reviewModelGLB(t, 1, 3)
	for _, media := range []string{"application/x-tgif", "application/octet-stream", ""} {
		t.Run("media="+media, func(t *testing.T) {
			task := reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded)
			task.Assets[1].MediaType = media
			gateway := newReviewGateway(task)
			assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
			executor, _ := reviewExecutor(t, gateway, assets, nil)

			platform, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
			if err != nil || platform.Error != "" {
				t.Fatalf("platform inspection = %+v err=%v", platform, err)
			}
			payload := reviewPayload(t, platform)
			if payload["ok"] != true || reviewNumber(t, payload, "report", "triangles") != 6 {
				t.Fatalf("platform inspection = %v", payload)
			}

			inspected, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
				Owner: "owner-a", TaskID: "task-a", AssetID: "task-a-glb",
			})
			if err != nil {
				t.Fatalf("core inspection: %v", err)
			}
			if inspected.AssetID != "task-a-glb" || inspected.Report.Triangles != 6 {
				t.Fatalf("core inspection = %+v", inspected)
			}
		})
	}
}

// The stored label decides nothing once the bytes are read: a file that is
// The stored label decides nothing once the bytes are read: a file that is
// not binary glTF is refused with the Schema's bytes-not-GLB text, including
// when the label claims model/gltf-binary (#424), and the outward kind stays
// model_not_glb.
func TestModelReviewRefusesNonModelBytesWithAnUninformativeStoredMediaType(t *testing.T) {
	notModel := []byte("v 0 0 0\nf 1 1 1\n")
	for _, media := range []string{"application/x-tgif", "application/octet-stream", "model/gltf-binary"} {
		t.Run("media="+media, func(t *testing.T) {
			task := reviewTask("task-a", "owner-a", "session-a", notModel, generation.TaskSucceeded)
			task.Assets[1].MediaType = media
			gateway := newReviewGateway(task)
			assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": notModel})
			executor, schema := reviewExecutor(t, gateway, assets, nil)

			platform, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
			if err != nil {
				t.Fatal(err)
			}
			want := strings.NewReplacer("{asset}", "task-a-glb", "{media}", media).
				Replace(schema.ModelReview.Source.SourceBytesNotGLB)
			if platform.Error != want || !platform.NoSubmission {
				t.Fatalf("platform refusal = %+v, want %q", platform, want)
			}

			inspected, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
				Owner: "owner-a", TaskID: "task-a", AssetID: "task-a-glb",
			})
			var refusal *generationtool.ModelInspectRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("core inspection = %+v err=%v, want a refusal", inspected, err)
			}
			if refusal.Kind != generationtool.ModelInspectNotGLB || refusal.Message != want {
				t.Fatalf("core refusal = %+v, want kind %s and %q", refusal, generationtool.ModelInspectNotGLB, want)
			}
		})
	}
}
