package imagereview

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The Agent-judged items of a checklist (#286). The deterministic report
// cannot see pose, perspective or cast shadows, so the Schema declares them as
// visual items: inspect_image lists them with the checklist, the Agent looks at
// the picture, and its verdicts come back in the declared argument. The server
// records each verdict beside the deterministic checks and counts them in the
// summary, which is what the work ledger reads, so a picture the Agent judged
// to have the wrong pose is flagged in the self-check record instead of
// surfacing in the 3D result. A verdict is the Agent's own claim: nothing here
// looks at pixels.

// visualVerdict is one Agent verdict on a declared visual item.
type visualVerdict struct {
	Item    string `json:"item"`
	Verdict string `json:"verdict"`
	Note    string `json:"note"`
}

// visualCheck is a verdict as the result and the summary record it. Verdict is
// the Agent's wording; FixHint is the Schema's, kept only for a failed item.
type visualCheck struct {
	Item    string `json:"item"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
	FixHint string `json:"fix_hint,omitempty"`
}

// visualOutcome is what a call adds to the result: the items it offered, the
// verdicts it recorded and how many of them passed or failed. A verdict of
// not_applicable is recorded but counts as neither.
type visualOutcome struct {
	Items  []map[string]string
	Checks []visualCheck
	Passed int
	Failed int
}

// visualItems are the Agent-judged items a checklist declares.
func (service *Service) visualItems(checklistName string) []catalog.ChatImageVisualItem {
	if checklistName == "3d_input" {
		return service.review.Input3DVisualItems
	}
	return nil
}

// visualVerdicts reads the declared verdict argument of one call. An absent or
// null argument is no verdict. Anything else must be a list of verdicts on
// items the checklist declares, each at most once, with a known verdict and a
// note within the Schema's bound; a call that breaks that is refused whole, so
// a typo in an item id never records half a review.
func (service *Service) visualVerdicts(raw map[string]json.RawMessage, items []catalog.ChatImageVisualItem) ([]visualVerdict, bool) {
	name := service.review.VisualReviewArgument
	value, present := raw[name]
	if name == "" || !present || jsonEqualNull(value) {
		return nil, true
	}
	declared := make(map[string]bool, len(items))
	for _, item := range items {
		declared[item.ID] = true
	}
	var verdicts []visualVerdict
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&verdicts) != nil || len(verdicts) > len(items) {
		return nil, false
	}
	seen := make(map[string]bool, len(verdicts))
	for index := range verdicts {
		verdict := &verdicts[index]
		verdict.Note = strings.TrimSpace(verdict.Note)
		if !declared[verdict.Item] || seen[verdict.Item] ||
			utf8.RuneCountInString(verdict.Note) > service.review.VisualNoteMaxCharacters {
			return nil, false
		}
		switch verdict.Verdict {
		case catalog.ImageVisualPass, catalog.ImageVisualFail, catalog.ImageVisualNotApplicable:
		default:
			return nil, false
		}
		seen[verdict.Item] = true
	}
	return verdicts, true
}

// newVisualOutcome pairs the Agent's verdicts with the declared items. The
// items are listed in Schema order every time, so the Agent always sees what
// is left to judge; the verdicts are recorded in the order they were given.
func newVisualOutcome(items []catalog.ChatImageVisualItem, verdicts []visualVerdict) visualOutcome {
	outcome := visualOutcome{}
	hints := make(map[string]string, len(items))
	for _, item := range items {
		hints[item.ID] = item.FixHint
		outcome.Items = append(outcome.Items, map[string]string{
			"id": item.ID, "label": item.Label, "instruction": item.Instruction,
		})
	}
	for _, verdict := range verdicts {
		check := visualCheck{Item: verdict.Item, Verdict: verdict.Verdict, Note: verdict.Note}
		switch verdict.Verdict {
		case catalog.ImageVisualPass:
			outcome.Passed++
		case catalog.ImageVisualFail:
			outcome.Failed++
			check.FixHint = hints[verdict.Item]
		}
		outcome.Checks = append(outcome.Checks, check)
	}
	return outcome
}

// attach adds the outcome to a usage_check object. Nothing is added for a
// checklist that declares no visual items.
func (outcome visualOutcome) attach(usageCheck map[string]any) {
	if len(outcome.Items) == 0 {
		return
	}
	usageCheck["visual_items"] = outcome.Items
	if len(outcome.Checks) > 0 {
		usageCheck["visual_checks"] = outcome.Checks
	}
}
