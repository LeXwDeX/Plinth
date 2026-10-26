package generationtool_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330: Meshy Convert is an ordinary format-conversion Offering. The Agent's
// convert_model_format reaches it through the same Schema route the 格式 tab
// uses (model_delivery.conversions), only for a source the Offering binds
// (generation.BindModelSource), and never sends the requested format as an
// argument it does not declare: one run converts to all six formats.

const meshyConvertOffering = generation.OfferingID("meshy/processing/convert")

// acceptingValidator is the kernel's read-only input check accepting every
// bound source; it records what was checked.
type acceptingValidator struct {
	*processingGateway
	validated []generation.OfferingID
}

func (g *acceptingValidator) ValidateOfferingInput(_ context.Context, _ generation.OwnerID, offering generation.OfferingID, _ string, _ json.RawMessage) error {
	g.validated = append(g.validated, offering)
	return nil
}

func convertHarness(t *testing.T, product *catalog.Catalog) (*processingHarness, *acceptingValidator) {
	t.Helper()
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	if product != nil {
		h.gateway.product = product
		h.gateway.grantGateway.offering = product.Get
	}
	h.gateway.normalize = true
	for _, id := range []generation.TaskID{"model-1", "tripo-1"} {
		task := h.gateway.tasks[id]
		task.Assets[0].MediaType = "model/gltf-binary"
		h.gateway.tasks[id] = task
	}
	gateway := &acceptingValidator{processingGateway: h.gateway}
	config := h.config
	config.Gateway = gateway
	var err error
	if h.executor, err = generationtool.New(config); err != nil {
		t.Fatal(err)
	}
	return h, gateway
}

func TestConvertModelFormatRunsMeshyConvertForTheFormatsItProvides(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, source, format string
		want                 map[string]any
	}{
		// A Meshy result goes by its original task while Meshy keeps it.
		{"meshy task", "model-1", "fbx", map[string]any{"source_task": "model-1"}},
		{"format named in capitals", "model-1", "USDZ", map[string]any{"source_task": "model-1"}},
		// Another provider's result goes as its saved model file.
		{"tripo result file", "tripo-1", "stl", map[string]any{"source_asset": "tripo-1-glb"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, gateway := convertHarness(t, product)
			result, _ := h.run("convert_model_format", `{"source_task_id":"`+test.source+`","format":"`+test.format+`"}`)
			if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != meshyConvertOffering {
				t.Fatalf("result = %+v frozen = %+v", result, h.gateway.frozen)
			}
			input := h.frozenInput(0)
			for name, value := range test.want {
				if input[name] != value {
					t.Fatalf("frozen input = %v, want %s=%v", input, name, value)
				}
			}
			formats, _ := input["target_formats"].([]any)
			if _, sent := input["format"]; sent || len(formats) != 6 || input["convert_format_only"] != nil {
				t.Fatalf("frozen input = %v: one run asks for all six formats and nothing else", input)
			}
			if content := h.content(result); content["success"] != true || content["estimated_credits"] != float64(1) {
				t.Fatalf("result = %v", content)
			}
			if !slices.Contains(gateway.validated, meshyConvertOffering) {
				t.Fatalf("the kernel input check was skipped: %v", gateway.validated)
			}
		})
	}
}

// The free local routes stay first for what they provide: glTF never goes to
// Meshy (it does not provide it), and nothing else changes order.
func TestConvertModelFormatKeepsTheLocalGltfRoute(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	h, gateway := convertHarness(t, product)
	result, _ := h.run("convert_model_format", `{"source_task_id":"model-1","format":"gltf"}`)
	if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "local/generated-glb/to-gltf" {
		t.Fatalf("result = %+v frozen = %+v", result, h.gateway.frozen)
	}
	if slices.Contains(gateway.validated, meshyConvertOffering) {
		t.Fatalf("a glTF request checked Meshy Convert: %v", gateway.validated)
	}
}

// The person's choice first: with the legacy Tripo conversion enabled, an STL
// for a Tripo result goes to Tripo (the existing default) unless the person
// named Meshy for it.
func TestConvertModelFormatPrefersTheServiceThePersonNamed(t *testing.T) {
	h, _ := convertHarness(t, nil) // the harness Catalog enables Tripo conversion
	result, _ := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"STL"}`)
	if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "tripo/tripo-v3.1/format-conversion" {
		t.Fatalf("default route = %+v frozen = %+v", result, h.gateway.frozen)
	}

	h, _ = convertHarness(t, nil)
	history := []chat.Message{{ID: "u-1", Role: "user", Content: "用 Meshy 把它转成 STL"}}
	result, _ = h.runAs("owner-a", "session-a", "convert_model_format", `{"source_task_id":"tripo-1","format":"STL"}`, history)
	if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != meshyConvertOffering {
		t.Fatalf("named route = %+v frozen = %+v", result, h.gateway.frozen)
	}
}

// Listed means it can be submitted: list_session_models names Meshy Convert
// for a model it binds, and not for a model over its 70 MiB file limit that it
// cannot take by task either.
func TestSessionModelsListMeshyConvertOnlyWhereItCanRun(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := convertHarness(t, product)
	big := h.gateway.tasks["tripo-1"]
	big.ID = "tripo-big"
	big.Assets = []generation.Asset{big.Assets[0]}
	big.Assets[0].ID, big.Assets[0].TaskID, big.Assets[0].Size = "tripo-big-glb", "tripo-big", 71<<20
	h.gateway.tasks[big.ID] = big
	listed, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Models []struct {
			TaskID    string `json:"task_id"`
			NextSteps []struct {
				Tool     string `json:"tool"`
				Services []struct {
					OfferingID string `json:"offering_id"`
				} `json:"services"`
			} `json:"next_steps"`
		} `json:"models"`
	}
	if err := json.Unmarshal(listed, &view); err != nil {
		t.Fatal(err)
	}
	offers := map[string]bool{}
	for _, model := range view.Models {
		for _, step := range model.NextSteps {
			for _, service := range step.Services {
				if step.Tool == "convert_model_format" && service.OfferingID == string(meshyConvertOffering) {
					offers[model.TaskID] = true
				}
			}
		}
	}
	if !offers["model-1"] || !offers["tripo-1"] || offers["tripo-big"] {
		t.Fatalf("Meshy Convert offered for %v; want model-1 and tripo-1, not tripo-big", offers)
	}
}
