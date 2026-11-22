package assetreview_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/assetreview"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/imagereview"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

func bound(value float64) *float64 { return &value }

type fakeModels struct {
	report modelinspect.Report
	err    error
	calls  int
}

func (fake *fakeModels) ModelReport(context.Context, generation.OwnerID,
	generation.TaskID, generation.AssetID,
) (modelinspect.Report, error) {
	fake.calls++
	return fake.report, fake.err
}

type fakeImages struct {
	inspection imagereview.Inspection
	err        error
	calls      int
}

func (fake *fakeImages) InspectOwned(context.Context, imagereview.InspectRequest) (imagereview.Inspection, error) {
	fake.calls++
	return fake.inspection, fake.err
}

type fakeSelfChecks struct {
	records []chat.SelfCheckRecord
}

func (fake *fakeSelfChecks) ListSelfChecks(context.Context, string, string) ([]chat.SelfCheckRecord, error) {
	return fake.records, nil
}

func snapshotInput(assets ...generation.Asset) assetlist.ReviewSnapshotInput {
	return assetlist.ReviewSnapshotInput{
		OwnerID: "owner-a", SessionID: "ses_1", ProjectID: "prj_1",
		ListID: "alist_1", ItemID: "aitem_1", Kind: "character",
		Brief: json.RawMessage(`{"purpose":"game"}`),
		Round: 1,
		Task: generation.Task{
			ID: "task_1", Capability: "text-to-3d", State: generation.TaskSucceeded,
			CreatedAt: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
			Assets:    assets,
		},
		Tasks: []generation.Task{{ID: "task_1", Capability: "text-to-3d"}},
		At:    time.Date(2026, 11, 18, 12, 1, 0, 0, time.UTC),
	}
}

func decode(t *testing.T, raw json.RawMessage) assetlist.ReviewSnapshot {
	t.Helper()
	var snapshot assetlist.ReviewSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snapshot
}

func TestSnapshotJudgesTheGlbAgainstThePurposeChecks(t *testing.T) {
	models := &fakeModels{report: modelinspect.Report{Triangles: 1500}}
	service := assetreview.New(assetreview.Config{
		Checks: func(context.Context, string, string, json.RawMessage) []budget.PlanCheck {
			return []budget.PlanCheck{{Check: "triangles", Maximum: bound(1000)}}
		},
		Models:  models,
		Counted: map[generation.Capability]bool{"text-to-3d": true},
	})
	raw, err := service.Snapshot(context.Background(), snapshotInput(
		generation.Asset{ID: "asset_model", Role: "model", MediaType: "model/gltf-binary",
			StorageKey: "k/model.glb", Size: 2048, SHA256: "bb"},
		generation.Asset{ID: "asset_img", Role: "preview", MediaType: "image/png",
			StorageKey: "k/preview.png", Size: 512, SHA256: "aa"},
	))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot := decode(t, raw)
	if snapshot.TaskID != "task_1" || snapshot.CoverAssetID != "asset_img" ||
		len(snapshot.Artifacts) != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Model3D == nil || snapshot.Model3D.AssetID != "asset_model" ||
		len(snapshot.Model3D.Checks) != 1 ||
		snapshot.Model3D.Checks[0].Status != "fail" ||
		models.calls != 1 {
		t.Fatalf("model judgment = %+v, calls = %d", snapshot.Model3D, models.calls)
	}
	if snapshot.Steps.Used != 1 || snapshot.Steps.Model3D != 1 {
		t.Fatalf("steps = %+v", snapshot.Steps)
	}
}

func TestSnapshotMarksEveryCheckUnknownWithoutAGlb(t *testing.T) {
	models := &fakeModels{}
	service := assetreview.New(assetreview.Config{
		Checks: func(context.Context, string, string, json.RawMessage) []budget.PlanCheck {
			return []budget.PlanCheck{
				{Check: "triangles", Maximum: bound(1000)},
				{Check: "uv", Required: true},
			}
		},
		Models: models,
	})
	raw, err := service.Snapshot(context.Background(), snapshotInput(
		generation.Asset{ID: "asset_img", Role: "image", MediaType: "image/png",
			StorageKey: "k/image.png"},
	))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot := decode(t, raw)
	if snapshot.Model3D == nil || snapshot.Model3D.AssetID != "" ||
		len(snapshot.Model3D.Checks) != 2 {
		t.Fatalf("model judgment = %+v", snapshot.Model3D)
	}
	for _, check := range snapshot.Model3D.Checks {
		if check.Status != "unknown" || check.Detail != "no_glb" {
			t.Fatalf("check = %+v; want unknown/no_glb", check)
		}
	}
	if models.calls != 0 {
		t.Fatalf("model inspections = %d; want none", models.calls)
	}
}

func TestSnapshotJudgesTheCoverPictureWithTheDeliveryChecklist(t *testing.T) {
	images := &fakeImages{inspection: imagereview.Inspection{
		Report: imageinspect.Report{Width: 512, Height: 512},
		Checks: []imageinspect.Check{
			{Name: "alpha", Status: "pass", Actual: "transparent"},
			{Name: "subject_ratio", Status: "fail", Actual: "4%", Expected: ">= 10%"},
		},
	}}
	service := assetreview.New(assetreview.Config{Images: images})
	raw, err := service.Snapshot(context.Background(), snapshotInput(
		generation.Asset{ID: "asset_img", Role: "image", MediaType: "image/png",
			StorageKey: "k/image.png"},
	))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot := decode(t, raw)
	if images.calls != 1 {
		t.Fatalf("image inspections = %d; want 1", images.calls)
	}
	if snapshot.Image2D == nil || snapshot.Image2D.AssetID != "asset_img" ||
		snapshot.Image2D.Width != 512 || len(snapshot.Image2D.Checks) != 2 ||
		snapshot.Image2D.Checks[1].Status != "fail" ||
		snapshot.Image2D.Checks[1].Budget != ">= 10%" {
		t.Fatalf("image judgment = %+v", snapshot.Image2D)
	}
	if snapshot.Model3D != nil {
		t.Fatalf("model judgment = %+v; want none for a 2D item", snapshot.Model3D)
	}
}

func TestSnapshotJudgesTheModelRoleGlbBeforeConvertedModels(t *testing.T) {
	models := &fakeModels{report: modelinspect.Report{Triangles: 1200}}
	service := assetreview.New(assetreview.Config{
		Checks: func(context.Context, string, string, json.RawMessage) []budget.PlanCheck {
			return []budget.PlanCheck{{Check: "triangles", Maximum: bound(10000)}}
		},
		Models: models,
	})
	// Stored assets come back ordered by role, so converted_model (FBX/OBJ)
	// precedes the task's own model. The GLB is the judged file either way.
	raw, err := service.Snapshot(context.Background(), snapshotInput(
		generation.Asset{ID: "asset_fbx", Role: "converted_model", Variant: "converted.fbx",
			MediaType: "application/octet-stream", StorageKey: "k/model.fbx"},
		generation.Asset{ID: "asset_obj", Role: "converted_model", Variant: "converted.obj",
			MediaType: "model/obj", StorageKey: "k/model.obj"},
		generation.Asset{ID: "asset_glb", Role: "model", Variant: "glb",
			MediaType: "model/gltf-binary", StorageKey: "k/model.glb"},
	))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot := decode(t, raw)
	if snapshot.Model3D == nil || snapshot.Model3D.AssetID != "asset_glb" ||
		snapshot.Model3D.Checks[0].Status != "pass" || models.calls != 1 {
		t.Fatalf("model judgment = %+v, calls = %d; want the model role's GLB", snapshot.Model3D, models.calls)
	}
	if snapshot.CoverAssetID != "asset_glb" {
		t.Fatalf("cover = %q; want the GLB when the item has no picture", snapshot.CoverAssetID)
	}
}

func TestSnapshotFallsBackToAConvertedGlb(t *testing.T) {
	models := &fakeModels{report: modelinspect.Report{Triangles: 1200}}
	service := assetreview.New(assetreview.Config{
		Checks: func(context.Context, string, string, json.RawMessage) []budget.PlanCheck {
			return []budget.PlanCheck{{Check: "triangles", Maximum: bound(10000)}}
		},
		Models: models,
	})
	// No model-role GLB: a converted GLB is still a GLB the inspector can read.
	raw, err := service.Snapshot(context.Background(), snapshotInput(
		generation.Asset{ID: "asset_fbx", Role: "converted_model", Variant: "fbx",
			MediaType: "model/fbx", StorageKey: "k/model.fbx"},
		generation.Asset{ID: "asset_glb", Role: "converted_model", Variant: "glb",
			MediaType: "model/gltf-binary", StorageKey: "k/model.glb"},
	))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot := decode(t, raw)
	if snapshot.Model3D == nil || snapshot.Model3D.AssetID != "asset_glb" {
		t.Fatalf("model judgment = %+v; want the converted GLB", snapshot.Model3D)
	}
}

func TestSnapshotCopiesSelfCheckAndUnreadableModel(t *testing.T) {
	models := &fakeModels{err: errors.New("cannot read")}
	selfChecks := &fakeSelfChecks{records: []chat.SelfCheckRecord{{
		TaskID: "task_1",
		Conclusion: &chat.SelfCheckConclusion{
			Status: "passed", Summary: "looks right", Remaining: []string{"texture seam"},
		},
	}}}
	service := assetreview.New(assetreview.Config{
		Checks: func(context.Context, string, string, json.RawMessage) []budget.PlanCheck {
			return []budget.PlanCheck{{Check: "triangles", Maximum: bound(1000)}}
		},
		Models:          models,
		SelfChecks:      selfChecks,
		SelfCheckSchema: catalog.ChatSelfCheckSchema{},
	})
	raw, err := service.Snapshot(context.Background(), snapshotInput(
		generation.Asset{ID: "asset_model", Role: "model", MediaType: "model/gltf-binary",
			StorageKey: "k/model.glb"},
	))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot := decode(t, raw)
	if snapshot.Model3D == nil || len(snapshot.Model3D.Checks) != 1 ||
		snapshot.Model3D.Checks[0].Status != "unknown" ||
		snapshot.Model3D.Checks[0].Detail != "unreadable" {
		t.Fatalf("unreadable model judgment = %+v", snapshot.Model3D)
	}
	if snapshot.SelfCheck == nil || snapshot.SelfCheck.Summary != "looks right" ||
		len(snapshot.SelfCheck.Remaining) != 1 {
		t.Fatalf("self check = %+v", snapshot.SelfCheck)
	}
}

// #425: a snapshot's frozen threshold is worded from the Schema in the UI
// locale of the turn that entered review, so the board shows it in the
// reader's language.
func TestSnapshotBudgetsFollowTheRequestLanguage(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	checks := func(context.Context, string, string, json.RawMessage) []budget.PlanCheck {
		return []budget.PlanCheck{{
			Check: "triangles", Maximum: bound(30000),
		}, {
			Check: "uv", Required: true,
		}}
	}
	for _, test := range []struct {
		locale string
		want   []string
	}{
		{"", []string{"最多 30000", "必填"}},
		{"en", []string{"max 30000", "required"}},
	} {
		test := test
		t.Run(test.locale, func(t *testing.T) {
			service := assetreview.New(assetreview.Config{
				Checks: checks,
				Review: application.AssetList.Review,
				Texts:  texts,
			})
			ctx := context.Background()
			if test.locale != "" {
				ctx = catalog.WithLocale(ctx, test.locale)
			}
			raw, err := service.Snapshot(ctx, snapshotInput(
				generation.Asset{ID: "asset_img", Role: "image", MediaType: "image/png",
					StorageKey: "k/image.png"},
			))
			if err != nil {
				t.Fatalf("snapshot: %v", err)
			}
			snapshot := decode(t, raw)
			if snapshot.Model3D == nil || len(snapshot.Model3D.Checks) != 2 {
				t.Fatalf("model judgment = %+v", snapshot.Model3D)
			}
			for index, want := range test.want {
				if got := snapshot.Model3D.Checks[index].Budget; got != want {
					t.Fatalf("%s budget[%d] = %q; want %q", test.locale, index, got, want)
				}
			}
		})
	}
}
