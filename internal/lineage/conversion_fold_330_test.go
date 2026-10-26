package lineage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #330 second follow-ups, observation 2: the free refetch of a failed Meshy
// Convert became "3D v2" and previewed Meshy's re-exported GLB, while the
// failed Convert and local glTF attempts stayed as two "3D 失败" nodes. A
// format conversion does not change the model: its files belong to the source
// version (the 格式 tab and the ZIP already list them there), so a conversion
// Task of a session source is folded into that source. It takes no node and
// no version number; edges from it point at the source; the graph lists it so
// the client maps it to the source too.

var convertOffering = generation.Offering{ID: "convert", Parameters: []schema.Parameter{
	{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask},
	// Like Meshy Convert, the source may also be an uploaded model (#330: the
	// flag alone must not keep a conversion of a session Task unfolded).
	{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, UploadedModel: &schema.UploadedModel{}},
}}

// conversions stands in for the Product Schema's model_delivery conversions:
// the convert Offering, and the retexture Offering only with the fixed input
// convert_only (as Remesh's retired convert_format_only was).
type conversions struct{}

func (conversions) ConversionOffering(offering generation.OfferingID) bool {
	return offering == convertOffering.ID || offering == processOffering.ID
}

func (conversions) IsConversion(offering generation.OfferingID, input json.RawMessage) bool {
	if offering == convertOffering.ID {
		return true
	}
	var values map[string]any
	return offering == processOffering.ID && json.Unmarshal(input, &values) == nil && values["convert_only"] == true
}

var isConversion conversions

func TestFormatConversionsFoldIntoTheirSource330(t *testing.T) {
	source := task("source", 1, modelOffering, nil, modelAsset("source-a", "s"))
	failedLocal := task("gltf-failed", 2, convertOffering, input(t, map[string]any{"source_asset": "source-a"}))
	failedLocal.State, failedLocal.Failure = generation.TaskFailed, &generation.Failure{Class: generation.FailureIntegrity}
	failedConvert := task("convert-failed", 3, convertOffering, input(t, map[string]any{"source_task": "source"}))
	failedConvert.State, failedConvert.Failure = generation.TaskFailed, &generation.Failure{Class: generation.FailureIntegrity}
	refetch := task("convert-refetch", 4, convertOffering, input(t, map[string]any{"source_task": "source"}), modelAsset("refetch-glb", "r"))
	onlyFormat := task("convert-only", 5, processOffering, input(t, map[string]any{"source_task": "source", "convert_only": true}), modelAsset("only-a", "o"))
	retexture := task("retexture", 6, processOffering, input(t, map[string]any{"source_asset": "refetch-glb"}), modelAsset("retexture-a", "t"))
	tasks := []generation.Task{retexture, onlyFormat, refetch, failedConvert, failedLocal, source}
	svc, err := New(&fakeTasks{tasks: tasks}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 50, Conversions: isConversion})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := svc.SessionPage(context.Background(), "owner-a", "session-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, folded := range []string{"gltf-failed", "convert-failed", "convert-refetch", "convert-only"} {
		if _, ok := nodeOf(graph, folded); ok {
			t.Fatalf("conversion %s is a node of its own: %#v", folded, graph.Nodes)
		}
		if graph.Conversions[folded] != "source" {
			t.Fatalf("conversion %s is not mapped to its source: %#v", folded, graph.Conversions)
		}
	}
	first, _ := nodeOf(graph, "source")
	next, ok := nodeOf(graph, "retexture")
	if first.Version != 1 || !ok || next.Version != 2 {
		t.Fatalf("versions = %d, %d (%v)", first.Version, next.Version, ok)
	}
	// Work made from a conversion's file comes from the source version.
	if edges := edgeOf(graph, "retexture"); len(edges) != 1 || edges[0].From != "source" {
		t.Fatalf("retexture edges = %#v", edges)
	}
	for _, edge := range graph.Edges {
		if _, folded := graph.Conversions[edge.From]; folded {
			t.Fatalf("edge from a folded conversion: %#v", edge)
		}
		if _, folded := graph.Conversions[edge.To]; folded {
			t.Fatalf("edge into a folded conversion: %#v", edge)
		}
	}
	versions, err := svc.SessionVersions(context.Background(), "owner-a", "session-1")
	if err != nil || versions["source"] != 1 || versions["retexture"] != 2 || versions["convert-refetch"] != 0 || versions["convert-only"] != 0 {
		t.Fatalf("tool versions = %v, %v", versions, err)
	}
	legacy, err := svc.Session(context.Background(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeOf(legacy, "convert-refetch"); ok || legacy.Conversions["convert-refetch"] != "source" {
		t.Fatalf("legacy graph keeps the conversion: %#v", legacy)
	}

	// The same processing Offering without the conversion input is a version.
	plain := task("plain", 7, processOffering, input(t, map[string]any{"source_task": "source"}), modelAsset("plain-a", "p"))
	svc, _ = New(&fakeTasks{tasks: append(tasks, plain)}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 50, Conversions: isConversion})
	graph, _ = svc.SessionPage(context.Background(), "owner-a", "session-1", "", 10)
	if node, ok := nodeOf(graph, "plain"); !ok || node.Version != 3 {
		t.Fatalf("plain processing must stay a version: %#v %v", node, ok)
	}
}

// A conversion whose source is not a Task of the session (an uploaded model)
// is the first version of that model and stays a node.
func TestConversionWithoutASessionSourceStaysAVersion330(t *testing.T) {
	orphan := task("orphan", 1, convertOffering, input(t, map[string]any{"source_asset": "uploaded-asset"}), modelAsset("orphan-a", "o"))
	svc, err := New(&fakeTasks{tasks: []generation.Task{orphan}}, fakeCaptures{}, fakeInputs{}, Config{MaxTasks: 50, Conversions: isConversion})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := svc.SessionPage(context.Background(), "owner-a", "session-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if node, ok := nodeOf(graph, "orphan"); !ok || node.Version != 1 || len(graph.Conversions) != 0 {
		t.Fatalf("orphan = %#v %v conversions = %v", node, ok, graph.Conversions)
	}
}

// A failed conversion that was completed locally after the fact must not
// insert a node into the historical chain: it never had a number while it was
// failed, and adding one now would renumber every later version. Its Task is
// still persisted, successful and has its own assets; only the version strip
// folds it into the source it converted.
func TestLocallyCompletedConversionDoesNotInsertAHistoricalVersion330(t *testing.T) {
	source := task("source", 1, modelOffering, nil, modelAsset("source-a", "s"))
	completed := task("convert-completed", 2, convertOffering, input(t, map[string]any{"source_task": "source"}), modelAsset("completed-a", "c"))
	completed.State = generation.TaskSucceeded
	completed.DeliveryCompleted = true
	later := task("later", 3, modelOffering, nil, modelAsset("later-a", "l"))
	since := start.Add(10 * time.Minute)
	svc, err := New(&fakeTasks{tasks: []generation.Task{later, completed, source}}, fakeCaptures{}, fakeInputs{}, Config{
		MaxTasks: 50, Conversions: isConversion, FoldConversionsSince: since,
	})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := svc.SessionPage(context.Background(), "owner-a", "session-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeOf(graph, "convert-completed"); ok {
		t.Fatalf("completed conversion became a version: %#v", graph.Nodes)
	}
	if graph.Conversions["convert-completed"] != "source" {
		t.Fatalf("completed conversion is not mapped to its source: %#v", graph.Conversions)
	}
	first, _ := nodeOf(graph, "source")
	next, ok := nodeOf(graph, "later")
	if first.Version != 1 || !ok || next.Version != 2 {
		t.Fatalf("versions = %d, %d (%v)", first.Version, next.Version, ok)
	}
}
