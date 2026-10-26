package meshy

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func nestedInputOffering() generation.Offering {
	return generation.Offering{
		ProviderID:      "meshy",
		ProviderOptions: json.RawMessage(`{"endpoint":"keychain/v1/build","api_version":"creative-lab","input_paths":{"size":["options","size_mm"],"quality":["options","export_resolution"],"format":["output","format"],"absent":["options","absent"]},"result_assets":[{"path":"model_urls","mode":"map","role":"model"}]}`),
		Parameters: []generation.Parameter{
			{Name: "source", Type: "task", WireName: "input_task_id"},
			{Name: "size", Type: "number"},
			{Name: "quality", Type: "select", WireName: "resolution", WireValues: map[string]json.RawMessage{"highest": json.RawMessage(`2048`)}},
			{Name: "format", Type: "select"},
			{Name: "absent", Type: "number"},
		},
	}
}

func TestInputPathsProjectResolvedValuesAndKeepAbsentFieldsAbsent(t *testing.T) {
	offering := nestedInputOffering()
	if err := ValidateOffering(offering); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"source": "public-task", "size": float64(40), "quality": "highest", "format": "glb"}
	request := generation.StartRequest{Offering: offering, TaskDependencies: map[string]generation.TaskDependency{"source": {UpstreamJob: generation.UpstreamJob{RemoteID: "upstream-prototype"}}}}
	if err := (&Adapter{}).projectInput(context.Background(), request, body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"input_task_id": "upstream-prototype", "options": map[string]any{"size_mm": float64(40), "export_resolution": float64(2048)}, "output": map[string]any{"format": "glb"}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v; want %#v", body, want)
	}
}

func TestInputPathsRejectInvalidDeclarationsAtStartupAndExecution(t *testing.T) {
	for _, tc := range []struct{ name, paths string }{
		{"unknown", `{"unknown":["options","size"]}`},
		{"empty", `{"size":[]}`},
		{"flat", `{"size":["size"]}`},
		{"empty-segment", `{"size":["options",""]}`},
		{"dotted-segment", `{"size":["options","size.mm"]}`},
		{"duplicate", `{"size":["options","size"],"quality":["options","size"]}`},
		{"prefix", `{"size":["options","size"],"quality":["options","size","value"]}`},
		{"flat-prefix", `{"size":["format","size"]}`},
		{"depth", `{"size":["a","b","c","d","e","f","g","h","i"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offering := nestedInputOffering()
			offering.ProviderOptions = json.RawMessage(`{"endpoint":"build","api_version":"v1","result_assets":[{"path":"model_urls","role":"model","mode":"map"}],"input_paths":` + tc.paths + `}`)
			if err := ValidateOffering(offering); err == nil {
				t.Fatal("startup accepted invalid declaration")
			}
			if err := (&Adapter{}).projectInput(context.Background(), generation.StartRequest{Offering: offering}, map[string]any{}); err == nil {
				t.Fatal("frozen execution accepted invalid declaration")
			}
		})
	}
}

func TestInputPathsRejectRawContainerAndAliasing(t *testing.T) {
	offering := nestedInputOffering()
	if err := (&Adapter{}).projectInput(context.Background(), generation.StartRequest{Offering: offering}, map[string]any{"size": float64(40), "options": map[string]any{"size_mm": float64(1)}}); err == nil {
		t.Fatal("accepted undeclared container alongside public input")
	}
	if err := (&Adapter{}).projectInput(context.Background(), generation.StartRequest{Offering: offering}, map[string]any{"options": map[string]any{"size_mm": float64(1)}}); err == nil {
		t.Fatal("accepted undeclared container when all mapped public parameters are absent")
	}
	for _, wireName := range []string{"size", "input_task_id"} {
		offering.Parameters[2].WireName = wireName
		if err := ValidateOffering(offering); err == nil {
			t.Fatalf("accepted alias %q that can overwrite another input", wireName)
		}
	}
}

func TestInputPathsLegacyFlatProjectionUnchanged(t *testing.T) {
	offering := generation.Offering{ProviderOptions: json.RawMessage(`{}`), Parameters: []generation.Parameter{{Name: "size", Type: "number", WireName: "size_mm"}}}
	body := map[string]any{"size": float64(40), "legacy": true}
	if err := (&Adapter{}).projectInput(context.Background(), generation.StartRequest{Offering: offering}, body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, map[string]any{"size_mm": float64(40), "legacy": true}) {
		t.Fatalf("legacy projection changed: %#v", body)
	}
}
