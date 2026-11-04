package chat

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func reviewHandleOrchestrator() *Orchestrator {
	return &Orchestrator{schema: catalog.ChatSchema{
		Context:     catalog.ChatContextSchema{ResultHandlesField: "handles"},
		ModelReview: catalog.ChatModelReviewSchema{ResultViewsField: "views"},
	}}
}

// #103 review F9: the compact summary carries the handles too, under the
// Schema's key, so the frontend can bind each picture to its view without
// guessing by position.
func TestWithSummaryHandlesAddsTheSchemaKeyAndKeepsTheSummary(t *testing.T) {
	orchestrator := reviewHandleOrchestrator()
	summary := json.RawMessage(`{"tool":"render","views":["front","back"]}`)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(orchestrator.withSummaryHandles(summary, []string{"@img1", "@img2"}), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["handles"]) != `["@img1","@img2"]` || string(got["views"]) != `["front","back"]` || string(got["tool"]) != `"render"` {
		t.Fatalf("summary = %v", got)
	}

	// Nothing to add, nothing changes.
	if got := string(orchestrator.withSummaryHandles(summary, nil)); got != string(summary) {
		t.Fatalf("summary without handles = %s", got)
	}
	if got := string(orchestrator.withSummaryHandles(nil, []string{"@img1"})); got != "" {
		t.Fatalf("absent summary = %q", got)
	}
	withoutKey := &Orchestrator{schema: catalog.ChatSchema{}}
	if got := string(withoutKey.withSummaryHandles(summary, []string{"@img1"})); got != string(summary) {
		t.Fatalf("summary without a Schema key = %s", got)
	}
	// A summary that is not a JSON object is left as is.
	if got := string(orchestrator.withSummaryHandles(json.RawMessage(`"text"`), []string{"@img1"})); got != `"text"` {
		t.Fatalf("scalar summary = %s", got)
	}
}

func TestWithResultHandlesPairsAViewsArrayOnlyWhenItLinesUp(t *testing.T) {
	orchestrator := reviewHandleOrchestrator()
	handles := []string{"@img1", "@img2"}

	content := orchestrator.withResultHandles(`{"ok":true,"views":[{"view":"front"},{"view":"back"}]}`, handles)
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	var views []map[string]any
	if err := json.Unmarshal(result["views"], &views); err != nil {
		t.Fatal(err)
	}
	if string(result["handles"]) != `["@img1","@img2"]` || views[0]["handle"] != "@img1" || views[1]["handle"] != "@img2" {
		t.Fatalf("result = %s", content)
	}

	// A result with fewer views than pictures keeps its views as they were:
	// pairing a subset could name the wrong picture. The handles key is still
	// written, since the pictures exist.
	var mismatched map[string]json.RawMessage
	content = orchestrator.withResultHandles(`{"ok":true,"views":[{"view":"front"}]}`, handles)
	if err := json.Unmarshal([]byte(content), &mismatched); err != nil {
		t.Fatal(err)
	}
	if string(mismatched["views"]) != `[{"view":"front"}]` || string(mismatched["handles"]) != `["@img1","@img2"]` {
		t.Fatalf("mismatched views were changed: %s", content)
	}
	// A result without a views array gains no empty field.
	withoutViews := `{"ok":true}`
	var plain map[string]json.RawMessage
	if err := json.Unmarshal([]byte(orchestrator.withResultHandles(withoutViews, handles)), &plain); err != nil {
		t.Fatal(err)
	}
	if _, present := plain["views"]; present {
		t.Fatalf("a result without views gained one: %v", plain)
	}
}
