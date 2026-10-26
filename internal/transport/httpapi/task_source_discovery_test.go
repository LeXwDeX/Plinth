package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestTaskAssetsSourceAppearsInExtendedInputDiscovery(t *testing.T) {
	t.Parallel()
	var parameter generation.Parameter
	if err := json.Unmarshal([]byte(`{"name":"source_task","type":"task","semantic":"source-task","task_source_kind":"assets","task_offerings":["local/forward"],"task_capabilities":["format-conversion"],"same_provider":true,"media_types":["model/gltf+json"],"extensions":[".gltf"],"max_bytes":1024}`), &parameter); err != nil {
		t.Fatal(err)
	}
	parameters := []generation.Parameter{{Name: "legacy_task", Type: "task"}, parameter}
	schemas := projectInputParameterSchemas(parameters)
	if len(schemas) != 1 {
		t.Fatalf("task-assets input schema missing or legacy shape changed: %+v", schemas)
	}
	var api catalog.ProviderDiscoveryAPISchema
	if err := json.Unmarshal([]byte(`{"input_fields":{"name":"name","label":"label","type":"type","media_types":"mime","extensions":"extensions","max_bytes":"bytes","task_source_kind":"source_kind","task_offerings":"origins","task_capabilities":"capabilities","same_provider":"same_vendor"}}`), &api); err != nil {
		t.Fatal(err)
	}
	wire := projectInputSchemaWire(schemas[0], api)
	encoded, _ := json.Marshal(wire)
	var got map[string]any
	_ = json.Unmarshal(encoded, &got)
	want := map[string]any{"name": "source_task", "label": "", "type": "task", "source_kind": "assets", "origins": []any{"local/forward"}, "capabilities": []any{"format-conversion"}, "same_vendor": true, "mime": []any{"model/gltf+json"}, "extensions": []any{".gltf"}, "bytes": float64(1024)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection=%s want=%v", encoded, want)
	}
	// Projection must not share mutable declaration slices.
	parameter.TaskOfferings[0] = "mutated"
	parameter.TaskCapabilities[0] = "mutated"
	if after, _ := json.Marshal(projectInputSchemaWire(schemas[0], api)); string(after) != string(encoded) {
		t.Fatalf("projection aliases source parameters: %s", after)
	}
	public, _ := json.Marshal(parameters[1])
	var native map[string]any
	_ = json.Unmarshal(public, &native)
	if native["task_source_kind"] != "assets" || native["task_offerings"] == nil {
		t.Fatalf("native parameter contract omitted: %s", public)
	}
}
