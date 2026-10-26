package generationtool_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #326 K (dev-test #321 note 99121, step 1): manual approval, the person has
// the 91.8 MB Bang result @model2 selected and says "用 Bang 再分割一次". The
// Agent remembered that Bang refused @model2 and called Bang on @model1 on
// its own. The tool stopped it for the switched model (right), but the
// question carried no reason, and the Agent offered "Bang on @model2" as a
// second option, which can never run. Whatever model the Agent tries, the
// person must learn why the named service cannot take the selected one, and
// every option the tool lists must be one that can be submitted.
//
// #326 L (same note, step 4): the reply called @model2 "3D v1" while the
// approval card and the version strip say "3D v5". Tool results carry the
// version label the page shows, so the Agent names models as the page does.

// smallOriginalHarness: @model1 is a 1 MiB Rodin result Bang's local-model
// path takes; @model2 is the 91.8 MB Bang result made from it.
func smallOriginalHarness(t *testing.T, mode string) *processingHarness {
	h := newProcessingHarness(t, mode, func(h *processingHarness) []generation.Task {
		return []generation.Task{rodinTask(h), bigBangResult(h)}
	})
	// The version strip numbers the Bang result 3D v5 (other 3D versions of
	// the session sit between them).
	h.executor.SetModelVersions(func(_ context.Context, owner generation.OwnerID, session string) (map[generation.TaskID]int, error) {
		if owner != "owner-a" || session != "session-a" {
			return nil, nil
		}
		return map[generation.TaskID]int{"rodin-1": 1, "bang-big": 5}, nil
	})
	return h
}

// unrunnable finds an option that runs Bang on the selected model.
var unrunnable = regexp.MustCompile(`Bang[^;,]*? on @model2`)

func assertSwitchQuestion(t *testing.T, h *processingHarness, result chat.ToolResult) (map[string]any, string) {
	t.Helper()
	if !result.NoSubmission || len(h.gateway.frozen) != 0 || h.gateway.submitCount != 0 {
		t.Fatalf("a paid step was frozen or submitted: %+v frozen=%d", result, len(h.gateway.frozen))
	}
	content := h.content(result)
	message, _ := content["error"].(string)
	if content["source_confirmation_required"] != true {
		t.Fatalf("result = %v", content)
	}
	for _, want := range []string{"@model2", "@model1", "10.48 MB", "91.8 MB", "Tripo Smart Segmentation"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the question does not say %q: %s", want, message)
		}
	}
	if found := unrunnable.FindString(message); found != "" {
		t.Fatalf("the question offers a combination that cannot run (%q): %s", found, message)
	}
	if found := offeringIDPattern.FindString(message); found != "" {
		t.Fatalf("the question names an internal ID %q: %s", found, message)
	}
	return content, message
}

func TestASwitchedSourceStillSaysWhyTheNamedServiceCannotTakeTheSelectedModel(t *testing.T) {
	for _, mode := range []string{"manual", "auto"} {
		h := smallOriginalHarness(t, mode)
		result, _ := h.runWith("owner-a", "session-a", "process_model",
			`{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/local-model"}`,
			namedHistory("再把这个模型分割成部件一次，用 Bang"), bigBangSelected)
		content, message := assertSwitchQuestion(t, h, result)
		// The named service on the proposed model is a real option.
		if !regexp.MustCompile(`Bang[^;,]*? on @model1`).MatchString(message) {
			t.Fatalf("the question does not offer Bang on @model1: %s", message)
		}
		selected, _ := content["selected_source"].(map[string]any)
		proposed, _ := content["proposed_source"].(map[string]any)
		if selected["handle"] != "@model2" || proposed["handle"] != "@model1" {
			t.Fatalf("selected = %v proposed = %v", selected, proposed)
		}
	}
}

// The Agent calls with no service at all; the person's own words named Bang.
func TestASwitchedSourceReadsTheServiceFromThePersonsWords(t *testing.T) {
	h := smallOriginalHarness(t, "manual")
	result, _ := h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model1"}`,
		namedHistory("再把这个模型分割成部件一次，用 Bang"), bigBangSelected)
	assertSwitchQuestion(t, h, result)
}

func TestAPlanOnASwitchedSourceSaysWhyTheNamedServiceCannotTakeTheSelectedModel(t *testing.T) {
	h := newAgentPlanHarness(t, "manual", false)
	h.gateway.tasks["rodin-1"] = rodinTask(h.processingHarness)
	h.gateway.tasks["bang-big"] = bigBangResult(h.processingHarness)
	command := h.command(`{"source_task_id":"@model1","steps":[{"capability":"mesh-segmentation","offering_id":"hyper3d/bang/local-model","parameters":{}}]}`)
	command.History, command.Options = namedHistory("用 Bang 再分割一次"), bigBangSelected
	result, err := h.executor.Execute(t.Context(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := h.content(result)
	message, _ := content["error"].(string)
	if content["source_confirmation_required"] != true || content["plan_id"] != nil {
		t.Fatalf("plan on another model = %v", content)
	}
	for _, want := range []string{"10.48 MB", "91.8 MB", "Tripo Smart Segmentation"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the plan question does not say %q: %s", want, message)
		}
	}
	if found := unrunnable.FindString(message); found != "" {
		t.Fatalf("the plan question offers a combination that cannot run (%q): %s", found, message)
	}
}

// L: list_session_models and the source views name each model by the
// version label the page shows.
func TestToolResultsNameModelsByThePagesVersionLabel(t *testing.T) {
	h := smallOriginalHarness(t, "manual")
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatal(err)
	}
	models, _ := content["models"].([]any)
	labels := map[string]any{}
	for _, raw := range models {
		model := raw.(map[string]any)
		labels[model["task_id"].(string)] = model["version"]
	}
	if labels["rodin-1"] != "3D v1" || labels["bang-big"] != "3D v5" {
		t.Fatalf("version labels = %v", labels)
	}
	// The question about a switched model names both versions.
	result, _ := h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/local-model"}`,
		namedHistory("再把这个模型分割成部件一次，用 Bang"), bigBangSelected)
	content = h.content(result)
	selected, _ := content["selected_source"].(map[string]any)
	proposed, _ := content["proposed_source"].(map[string]any)
	if selected["version"] != "3D v5" || proposed["version"] != "3D v1" {
		t.Fatalf("selected = %v proposed = %v", selected, proposed)
	}
	// A refusal of the named service on the selected model names it too.
	h = smallOriginalHarness(t, "manual")
	result, _ = h.runWith("owner-a", "session-a", "process_model",
		`{"step":"mesh-segmentation","source_task_id":"@model2","offering_id":"hyper3d/bang/local-model"}`,
		namedHistory("再把这个模型分割成部件一次，用 Bang"), bigBangSelected)
	content = h.content(result)
	source, _ := content["source"].(map[string]any)
	if source["version"] != "3D v5" {
		t.Fatalf("source = %v", content["source"])
	}
}
