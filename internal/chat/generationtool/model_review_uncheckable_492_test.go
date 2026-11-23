package generationtool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// fbxTask is a succeeded 3D result whose one saved model file is an FBX: a quad
// P2 task is exactly this (the fake's P2 quad output, #294), and no read-only
// review tool can read it.
func fbxTask(id, owner, session string, data []byte) generation.Task {
	task := reviewTask(id, owner, session, data, generation.TaskSucceeded)
	task.Assets[1].ID = generation.AssetID(id + "-fbx")
	task.Assets[1].Variant = "fbx"
	task.Assets[1].MediaType = "model/fbx"
	return task
}

// #492: every read-only review tool refuses an FBX with the Schema's not-GLB
// text, and the outward core reports the stable not_glb kind, so the Agent can
// record not_inspectable from that real refusal instead of paying for a
// conversion.
func TestModelReviewToolsRefuseAnUnreadableModelFormat(t *testing.T) {
	task := fbxTask("task-a", "owner-a", "session-a", []byte("Kaydara FBX Binary  \x00"))
	gateway := newReviewGateway(task)
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-fbx": []byte("Kaydara FBX Binary  \x00")})
	executor, schema := reviewExecutor(t, gateway, assets, nil)
	want := strings.NewReplacer("{asset}", "task-a-fbx", "{media}", "model/fbx").
		Replace(schema.ModelReview.Source.SourceNotGLB)

	t.Run("inspect_model", func(t *testing.T) {
		result, err := executor.InspectModel(context.Background(),
			reviewCommand("inspect_model", `{"model":"task-a"}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.Error != want || !result.NoSubmission || result.TaskID != "" {
			t.Fatalf("platform refusal = %+v, want %q", result, want)
		}
	})
	t.Run("render_model_views", func(t *testing.T) {
		result, err := executor.RenderModelViews(context.Background(),
			reviewCommand("render_model_views", `{"model":"task-a","views":["front"]}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.Error != want || !result.NoSubmission || result.TaskID != "" {
			t.Fatalf("platform refusal = %+v, want %q", result, want)
		}
	})
	t.Run("compare_models", func(t *testing.T) {
		result, err := executor.CompareModels(context.Background(),
			reviewCommand("compare_models", `{"models":["task-a","task-a"],"views":["front"]}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.Error != want || !result.NoSubmission || result.TaskID != "" {
			t.Fatalf("platform refusal = %+v, want %q", result, want)
		}
	})
	t.Run("the outward core reports not_glb", func(t *testing.T) {
		_, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
			Owner: "owner-a", TaskID: "task-a", AssetID: "task-a-fbx",
		})
		var refusal *generationtool.ModelInspectRefusal
		if !errors.As(err, &refusal) {
			t.Fatalf("core inspection = %v, want a refusal", err)
		}
		if refusal.Kind != generationtool.ModelInspectNotGLB || refusal.Message != want {
			t.Fatalf("core refusal = %+v, want kind %s and %q", refusal, generationtool.ModelInspectNotGLB, want)
		}
		// not_glb is the refusal the inspect tool keeps as failed evidence: it
		// is what makes a later not_inspectable conclusion provable.
		if !executor.ModelInspectReadRefusalForTest(refusal.Kind) {
			t.Fatalf("kind %s is not a read refusal", refusal.Kind)
		}
	})
}

// #492: compare_model_reference reaches the same loader, so a model whose format
// it cannot read is refused there too, with the same kind of failed evidence.
func TestCompareModelReferenceRefusesAnUnreadableModelFormat(t *testing.T) {
	data := []byte("Kaydara FBX Binary  \x00")
	task := referenceTask("task-a", "owner-a", "session-a", data)
	task.Assets[1].ID = generation.AssetID("task-a-fbx")
	task.Assets[1].Variant = "fbx"
	task.Assets[1].MediaType = "model/fbx"
	gateway := newReviewGateway(task)
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-fbx": data})
	pictures := &reviewPictures{pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32)}}
	executor := referenceExecutor(t, gateway, assets, pictures, newReviewSelfCheckStore(), nil)
	schema, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("{asset}", "task-a-fbx", "{media}", "model/fbx").
		Replace(schema.Application().Chat.ModelReview.Source.SourceNotGLB)

	result, err := executor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != want || !result.NoSubmission || result.TaskID != "" {
		t.Fatalf("platform refusal = %+v, want %q", result, want)
	}
	if gateway.submitted != 0 || len(pictures.opened) != 0 {
		t.Fatalf("submitted=%d pictures=%v, want no submission and no picture read",
			gateway.submitted, pictures.opened)
	}
}
