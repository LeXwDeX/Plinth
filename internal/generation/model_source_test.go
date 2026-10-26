package generation

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func modelSourceOffering(parameters ...Parameter) Offering {
	return Offering{ID: "vendor/process", ProviderID: "vendor", Parameters: parameters}
}

func modelSourceTask(provider string, capability Capability, assets ...Asset) Task {
	return Task{ID: "task-1", Capability: capability, State: TaskSucceeded,
		Offering: Offering{ProviderID: provider}, Assets: assets}
}

func savedModel(id, variant string) Asset {
	return Asset{ID: AssetID(id), Role: "model", Variant: variant, StorageKey: "key/" + id, MediaType: "model/" + variant, Size: 100}
}

func TestBindModelSourcePrefersTheTaskRouteForTheSameProvider(t *testing.T) {
	offering := modelSourceOffering(
		Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask,
			SameProvider: true, TaskCapabilities: []Capability{"text-to-3d"}},
		Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset})
	binding, err := BindModelSource(offering, modelSourceTask("vendor", "text-to-3d", savedModel("a1", "glb")), "", nil)
	if err != nil || binding.Parameter != "source_task" || binding.Value != "task-1" {
		t.Fatalf("binding = %+v, %v", binding, err)
	}
	// Another provider's job cannot be continued, but its saved file can.
	binding, err = BindModelSource(offering, modelSourceTask("other", "text-to-3d", savedModel("a1", "glb")), "", nil)
	if err != nil || binding.Parameter != "source_asset" || binding.Value != "a1" {
		t.Fatalf("foreign binding = %+v, %v", binding, err)
	}
	// A disallowed task capability falls back to the saved file as well.
	binding, err = BindModelSource(offering, modelSourceTask("vendor", "retexture", savedModel("a1", "glb")), "", nil)
	if err != nil || binding.Parameter != "source_asset" {
		t.Fatalf("capability binding = %+v, %v", binding, err)
	}
}

func TestBindModelSourceOrdersAssetVariantsByPreference(t *testing.T) {
	offering := modelSourceOffering(Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset,
		MediaTypes: []string{"model/*"}, MaxBytes: 1000})
	task := modelSourceTask("vendor", "text-to-3d", savedModel("a-obj", "obj"), savedModel("a-fbx", "fbx"), savedModel("a-glb", "glb"))
	binding, err := BindModelSource(offering, task, "", []string{"glb", "fbx"})
	if err != nil || binding.Value != "a-glb" {
		t.Fatalf("preferred variant = %+v, %v", binding, err)
	}
	binding, err = BindModelSource(offering, task, "a-obj", []string{"glb"})
	if err != nil || binding.Value != "a-obj" {
		t.Fatalf("explicit asset = %+v, %v", binding, err)
	}
	huge := savedModel("a-huge", "glb")
	huge.Size = 5000
	if _, err := BindModelSource(offering, modelSourceTask("vendor", "text-to-3d", huge), "", nil); err == nil {
		t.Fatal("an oversize model was bound")
	}
}

// #330 third follow-ups: a Task completed from a local delivery has no Provider
// job of its own, so only its saved assets may feed a step. The kernel already
// rejects an upstream dependency on it; the binder must not emit one either.
func TestBindModelSourceOnlyUsesSavedAssetsForLocalDeliveryCompletion(t *testing.T) {
	model := savedModel("a1", "glb")
	model.TaskID = "task-1"
	model.SHA256 = strings.Repeat("a", 64)
	source := modelSourceTask("vendor", "text-to-3d", model)
	source.Offering.ID = "source/forward"
	source.DeliveryCompleted = true

	assetParameter := Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask,
		TaskSourceKind: schema.TaskSourceAssets, TaskOfferings: []OfferingID{"source/forward"},
		TaskCapabilities: []Capability{"text-to-3d"}, MediaTypes: []string{"model/glb"},
		Extensions: []string{".glb"}, MaxBytes: 1000}
	binding, err := BindModelSource(modelSourceOffering(assetParameter), source, "", nil)
	if err != nil || binding.Parameter != "source_task" || binding.Value != "task-1" || binding.AssetID != "a1" {
		t.Fatalf("asset route = %+v, %v", binding, err)
	}

	upstream := modelSourceOffering(Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask,
		SameProvider: true, TaskCapabilities: []Capability{"text-to-3d"}})
	if _, err := BindModelSource(upstream, source, "", nil); !IsCode(err, ErrInvalidInput) {
		t.Fatalf("upstream route err = %v; want ErrInvalidInput", err)
	}

	mixed := modelSourceOffering(
		Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask,
			SameProvider: true, TaskCapabilities: []Capability{"text-to-3d"}},
		assetParameter)
	binding, err = BindModelSource(mixed, source, "", nil)
	if err != nil || binding.AssetID != "a1" || binding.Value != "task-1" {
		t.Fatalf("mixed route = %+v, %v; want the task-assets route", binding, err)
	}
}

func TestBindModelSourceRejectsUnusableSources(t *testing.T) {
	taskOnly := modelSourceOffering(Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask,
		SameProvider: true, TaskCapabilities: []Capability{"rigging"}})
	ready := savedModel("a1", "glb")
	running := modelSourceTask("vendor", "rigging", ready)
	running.State = TaskRunning
	unsaved := modelSourceTask("vendor", "rigging", Asset{ID: "a2", Role: "model", Variant: "glb"})
	for name, source := range map[string]Task{
		"not finished":      running,
		"no saved model":    unsaved,
		"wrong capability":  modelSourceTask("vendor", "text-to-3d", ready),
		"foreign provider":  modelSourceTask("other", "rigging", ready),
		"no model for task": modelSourceTask("vendor", "rigging"),
	} {
		_, err := BindModelSource(taskOnly, source, "", nil)
		var domain *Error
		if !errors.As(err, &domain) || domain.Code != ErrInvalidInput {
			t.Errorf("%s: err = %v; want ErrInvalidInput", name, err)
		}
	}
	// A source-url parameter is never a binding target.
	urlOnly := modelSourceOffering(Parameter{Name: "source_url", Type: "text", Semantic: schema.SemanticSourceURL})
	if _, err := BindModelSource(urlOnly, modelSourceTask("vendor", "text-to-3d", ready), "", nil); err == nil {
		t.Fatal("a URL-only offering accepted a platform model")
	}
}
