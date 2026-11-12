package imagereview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// visualService is the test service with the shipped shape of the 3d_input
// visual items (#286): pose, perspective and cast shadows, judged by the Agent.
func visualService(t *testing.T) *Service {
	t.Helper()
	svc := service(t, fakeInputs{data: picture(t)}, nil)
	svc.review.VisualReviewArgument = "visual_review"
	svc.review.VisualNoteMaxCharacters = 40
	svc.review.Input3DVisualItems = []catalog.ChatImageVisualItem{
		{ID: "pose", Label: "姿态", Instruction: "look at the pose", FixHint: "redraw as an A pose"},
		{ID: "perspective", Label: "透视", Instruction: "look for perspective", FixHint: "redraw orthographic"},
		{ID: "cast_shadow", Label: "投影阴影", Instruction: "look for cast shadows", FixHint: "relight evenly"},
	}
	return svc
}

type visualPayload struct {
	UsageCheck struct {
		Checks []struct {
			Status string `json:"status"`
		} `json:"checks"`
		VisualItems []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Instruction string `json:"instruction"`
			FixHint     string `json:"fix_hint"`
		} `json:"visual_items"`
		VisualChecks []struct {
			Item    string `json:"item"`
			Verdict string `json:"verdict"`
			Note    string `json:"note"`
			FixHint string `json:"fix_hint"`
		} `json:"visual_checks"`
	} `json:"usage_check"`
}

type visualSummary struct {
	Checks struct {
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"checks"`
	Visual []struct {
		Item    string `json:"item"`
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
		FixHint string `json:"fix_hint"`
	} `json:"visual"`
}

func inspectVisual(t *testing.T, svc *Service, arguments string) (visualPayload, visualSummary, string) {
	t.Helper()
	result, err := svc.Inspect(context.Background(), command(arguments, nil))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		return visualPayload{}, visualSummary{}, result.Error
	}
	var payload visualPayload
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	var summary visualSummary
	if err := json.Unmarshal(result.Summary, &summary); err != nil {
		t.Fatal(err)
	}
	return payload, summary, ""
}

// deterministicCounts is how many of the deterministic checks passed and failed.
func deterministicCounts(payload visualPayload) (passed, failed int) {
	for _, check := range payload.UsageCheck.Checks {
		if check.Status == "pass" {
			passed++
		} else {
			failed++
		}
	}
	return passed, failed
}

// #286: the 3d_input checklist lists the visual items for the Agent to judge,
// and a call without verdicts changes nothing else in the result or the counts.
func TestInspectListsTheVisualItemsOfTheChecklist(t *testing.T) {
	payload, summary, refused := inspectVisual(t, visualService(t), `{"image":"@img1","checklist":"3d_input"}`)
	if refused != "" {
		t.Fatalf("refused: %s", refused)
	}
	items := payload.UsageCheck.VisualItems
	if len(items) != 3 || items[0].ID != "pose" || items[1].ID != "perspective" || items[2].ID != "cast_shadow" ||
		items[0].Label != "姿态" || items[1].Instruction != "look for perspective" {
		t.Fatalf("visual items = %+v", items)
	}
	if len(payload.UsageCheck.VisualChecks) != 0 || len(summary.Visual) != 0 {
		t.Fatalf("verdicts were recorded without being given: %+v %+v", payload.UsageCheck.VisualChecks, summary.Visual)
	}
	passed, failed := deterministicCounts(payload)
	if summary.Checks.Passed != passed || summary.Checks.Failed != failed {
		t.Fatalf("summary counts %+v differ from the deterministic %d/%d", summary.Checks, passed, failed)
	}
}

// #286: the delivery checklist declares no visual items, so it lists none.
func TestInspectDeliveryChecklistHasNoVisualItems(t *testing.T) {
	payload, _, refused := inspectVisual(t, visualService(t), `{"image":"@img1"}`)
	if refused != "" {
		t.Fatalf("refused: %s", refused)
	}
	if payload.UsageCheck.VisualItems != nil || payload.UsageCheck.VisualChecks != nil {
		t.Fatalf("delivery checklist carries visual data: %+v", payload.UsageCheck)
	}
}

// #286: the Agent's verdicts are recorded beside the deterministic checks and
// counted in the summary the self-check record reads: a fail is a failed check
// with the Schema's fix hint, a pass is a passed one, not_applicable is neither.
func TestInspectRecordsTheAgentsVisualVerdicts(t *testing.T) {
	arguments := `{"image":"@img1","checklist":"3d_input","visual_review":[` +
		`{"item":"pose","verdict":"pass","note":" A pose, arms away from the torso "},` +
		`{"item":"perspective","verdict":"fail","note":"shot from above"},` +
		`{"item":"cast_shadow","verdict":"not_applicable"}]}`
	payload, summary, refused := inspectVisual(t, visualService(t), arguments)
	if refused != "" {
		t.Fatalf("refused: %s", refused)
	}
	checks := payload.UsageCheck.VisualChecks
	if len(checks) != 3 {
		t.Fatalf("visual checks = %+v", checks)
	}
	if checks[0].Item != "pose" || checks[0].Verdict != "pass" || checks[0].Note != "A pose, arms away from the torso" || checks[0].FixHint != "" {
		t.Fatalf("passed item = %+v", checks[0])
	}
	if checks[1].Item != "perspective" || checks[1].Verdict != "fail" || checks[1].FixHint != "redraw orthographic" {
		t.Fatalf("failed item = %+v", checks[1])
	}
	if checks[2].Verdict != "not_applicable" || checks[2].FixHint != "" {
		t.Fatalf("inapplicable item = %+v", checks[2])
	}
	if len(payload.UsageCheck.VisualItems) != 3 {
		t.Fatal("the items must stay listed beside the verdicts")
	}
	passed, failed := deterministicCounts(payload)
	if summary.Checks.Passed != passed+1 || summary.Checks.Failed != failed+1 {
		t.Fatalf("summary counts %+v, want the deterministic %d/%d plus one pass and one fail", summary.Checks, passed, failed)
	}
	if len(summary.Visual) != 3 || summary.Visual[1].Item != "perspective" || summary.Visual[1].FixHint != "redraw orthographic" {
		t.Fatalf("summary verdicts = %+v", summary.Visual)
	}
}

// #286: a review that cannot be recorded faithfully is refused whole, so a typo
// in an item id never records half of it and never reads as a pass.
func TestInspectRefusesAnInvalidVisualReviewWhole(t *testing.T) {
	for name, arguments := range map[string]string{
		"an undeclared item":                    `{"image":"@img1","checklist":"3d_input","visual_review":[{"item":"lighting","verdict":"pass"}]}`,
		"the same item twice":                   `{"image":"@img1","checklist":"3d_input","visual_review":[{"item":"pose","verdict":"pass"},{"item":"pose","verdict":"fail"}]}`,
		"an unknown verdict":                    `{"image":"@img1","checklist":"3d_input","visual_review":[{"item":"pose","verdict":"maybe"}]}`,
		"no verdict":                            `{"image":"@img1","checklist":"3d_input","visual_review":[{"item":"pose"}]}`,
		"a note over the bound":                 `{"image":"@img1","checklist":"3d_input","visual_review":[{"item":"pose","verdict":"pass","note":"` + strings.Repeat("长", 41) + `"}]}`,
		"an extra member":                       `{"image":"@img1","checklist":"3d_input","visual_review":[{"item":"pose","verdict":"pass","score":5}]}`,
		"not a list":                            `{"image":"@img1","checklist":"3d_input","visual_review":{"item":"pose","verdict":"pass"}}`,
		"a null element":                        `{"image":"@img1","checklist":"3d_input","visual_review":[null]}`,
		"verdicts on a checklist without items": `{"image":"@img1","checklist":"delivery","visual_review":[{"item":"pose","verdict":"pass"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, refused := inspectVisual(t, visualService(t), arguments)
			if refused != "invalid visual review" {
				t.Fatalf("error = %q, want the visual review refusal", refused)
			}
		})
	}
}

// #286: a missing, null or empty review is no review.
func TestInspectTreatsAnEmptyVisualReviewAsNone(t *testing.T) {
	for _, value := range []string{`null`, `[]`} {
		payload, summary, refused := inspectVisual(t, visualService(t),
			`{"image":"@img1","checklist":"3d_input","visual_review":`+value+`}`)
		if refused != "" || len(payload.UsageCheck.VisualChecks) != 0 || len(summary.Visual) != 0 {
			t.Fatalf("%s: refused=%q checks=%+v summary=%+v", value, refused, payload.UsageCheck.VisualChecks, summary.Visual)
		}
	}
	// A checklist without items accepts the empty forms too.
	if _, _, refused := inspectVisual(t, visualService(t), `{"image":"@img1","visual_review":[]}`); refused != "" {
		t.Fatalf("an empty review on the delivery checklist was refused: %s", refused)
	}
}

// #286: a Schema that declares no visual items behaves as before.
func TestInspectWithoutDeclaredVisualItemsIsUnchanged(t *testing.T) {
	svc := service(t, fakeInputs{data: picture(t)}, nil)
	payload, summary, refused := inspectVisual(t, svc, `{"image":"@img1","checklist":"3d_input"}`)
	if refused != "" {
		t.Fatalf("refused: %s", refused)
	}
	if payload.UsageCheck.VisualItems != nil || summary.Visual != nil {
		t.Fatalf("visual data without declared items: %+v %+v", payload.UsageCheck, summary)
	}
	// With no argument declared, a visual_review member is just an unknown
	// argument: ignored, like any other the Schema does not declare.
	payload, summary, refused = inspectVisual(t, svc,
		`{"image":"@img1","checklist":"3d_input","visual_review":[{"item":"pose","verdict":"pass"}]}`)
	if refused != "" || payload.UsageCheck.VisualChecks != nil || summary.Visual != nil {
		t.Fatalf("an undeclared argument was acted on: refused=%q %+v %+v", refused, payload.UsageCheck, summary)
	}
}
