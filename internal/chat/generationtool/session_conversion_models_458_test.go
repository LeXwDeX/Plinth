package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
)

// #458: a format conversion delivers files of the model it converted, so it is
// never a new model of the session. list_session_models and the self-check
// queue leave conversion Tasks out (the converted files show on the source
// model's export_formats), while the source resolution paths still follow a
// converted file back to its Task.

// localGltfConversionOffering is a declared format conversion of the default
// Product Schema (model_delivery.conversions): it provides glTF and names no
// fixed input beyond its model source.
const localGltfConversionOffering = generation.OfferingID("local/generated-glb/to-gltf")

// conversionTask is a succeeded format-conversion Task delivering a saved
// glTF model file, as the local glTF package and Meshy Convert do.
func conversionTask(id, owner, session string, offering generation.OfferingID, source string) generation.Task {
	return generation.Task{
		ID: generation.TaskID(id), OwnerID: generation.OwnerID(owner), SessionID: session,
		Capability: generation.FormatConversion, State: generation.TaskSucceeded,
		Offering: generation.Offering{ID: offering, ProviderID: "local", ProviderName: "Local", ModelName: "glTF"},
		Input:    json.RawMessage(`{"source_task":"` + source + `"}`),
		// A Provider still keeps its upstream job (#325 judges that by age).
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second),
		Assets: []generation.Asset{
			{ID: generation.AssetID(id + "-gltf"), TaskID: generation.TaskID(id), Role: "model",
				Variant: "gltf", StorageKey: "key/gltf", MediaType: "model/gltf+json", Size: 1024},
		},
	}
}

func conversionHarness458(t *testing.T, limit int) *processingHarness {
	t.Helper()
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{
			modelTask("model-1", "owner-a", "session-a", h.meshy, "text-to-3d", "glb"),
			conversionTask("conv-1", "owner-a", "session-a", localGltfConversionOffering, "model-1"),
		}
	})
	if limit > 0 {
		h.config.Chat.Agent.MaxListedModels = limit
		var err error
		if h.executor, err = generationtool.New(h.config); err != nil {
			t.Fatal(err)
		}
	}
	// The App injects the same rule the version chain folds conversions with.
	h.executor.SetConversionRule(modeldelivery.NewSchemaConversionRule(h.app.ModelDelivery).IsConversion)
	return h
}

// TestListSessionModelsSkipsFormatConversionTasks is the #458 regression: a
// session with one generated model and one successful local glTF conversion
// lists exactly one model, whose export_formats show glTF available.
func TestListSessionModelsSkipsFormatConversionTasks(t *testing.T) {
	h := conversionHarness458(t, 0)
	h.executor.SetModelFormats(func(_ context.Context, _ generation.OwnerID, task generation.TaskID) (map[string]string, error) {
		if task != "model-1" {
			t.Errorf("format coverage read for the excluded conversion %s", task)
		}
		return map[string]string{"glb": "available", "gltf": "available"}, nil
	})
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Models []struct {
			TaskID        string            `json:"task_id"`
			ExportFormats map[string]string `json:"export_formats"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Models) != 1 || listed.Models[0].TaskID != "model-1" {
		t.Fatalf("models = %s; want only model-1", raw)
	}
	if got := listed.Models[0].ExportFormats["gltf"]; got != "available" {
		t.Fatalf("export_formats.gltf = %q; want available (%s)", got, raw)
	}
}

// TestSessionModelTasksSkipsFormatConversionTasks: the self-check queue never
// lists a conversion as a result to check.
func TestSessionModelTasksSkipsFormatConversionTasks(t *testing.T) {
	h := conversionHarness458(t, 0)
	tasks, err := h.executor.SessionModelTasks(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].TaskID != "model-1" {
		t.Fatalf("self-check tasks = %+v; want only model-1", tasks)
	}
}

// TestListSessionModelsCountsTheLimitAfterExcludingConversions: with a one-model
// limit and the conversion first in the read order, the listing still returns
// the generated model. The limit is applied after the conversion is excluded,
// so a conversion never takes the slot.
func TestListSessionModelsCountsTheLimitAfterExcludingConversions(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{
			modelTask("model-1", "owner-a", "session-a", h.meshy, "text-to-3d", "glb"),
			conversionTask("conv-1", "owner-a", "session-a", localGltfConversionOffering, "model-1"),
		}
	})
	h.config.Chat.Agent.MaxListedModels = 1
	// The conversion is read first, so a limit applied before the exclusion
	// would keep it and drop the model.
	h.config.Gateway = &orderedListGateway{processingGateway: h.gateway,
		order: map[generation.TaskID]int{"conv-1": 0, "model-1": 1}}
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetConversionRule(modeldelivery.NewSchemaConversionRule(h.app.ModelDelivery).IsConversion)
	h.executor = executor

	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Models []struct {
			TaskID string `json:"task_id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Models) != 1 || listed.Models[0].TaskID != "model-1" {
		t.Fatalf("models = %s; want only model-1", raw)
	}
}

// TestSessionModelsKeepConversionsWithoutARule: without an injected conversion
// rule the listing is unchanged, so an application that declares no
// model_delivery conversions keeps its previous behaviour.
func TestSessionModelsKeepConversionsWithoutARule(t *testing.T) {
	h := newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{
			modelTask("model-1", "owner-a", "session-a", h.meshy, "text-to-3d", "glb"),
			conversionTask("conv-1", "owner-a", "session-a", localGltfConversionOffering, "model-1"),
		}
	})
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Models []struct {
			TaskID string `json:"task_id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Models) != 2 {
		t.Fatalf("models = %s; want both tasks without a rule", raw)
	}
	tasks, err := h.executor.SessionModelTasks(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("self-check tasks = %+v; want both tasks without a rule", tasks)
	}
}
