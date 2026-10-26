package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #329 notes 99868 and 99888: two refusals that are safe to explain told the
// person nothing they could act on. Meshy 7.1 Multiview takes at most four
// reference pictures; a fifth is refused (the sequence is preserved, not cut)
// and the answer must say which public input, how many were given and how many
// fit. Rodin Gen-2.5 always needs explicit extra-credit consent; a request
// without it was a generic invalid-input answer with no next step.
func TestMultiviewReferenceLimitRefusalNamesThePublicLabelAndCounts(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// The canary IDs must never appear in a requester-facing reason.
	input := `{"reference_assets":["asset-canary-1","asset-canary-2","asset-canary-3","asset-canary-4","asset-canary-5"]}`
	_, normalized, err := product.Normalize(
		schema.ImageTo3D, "meshy/meshy-7.1-multiview/image-to-3d", json.RawMessage(input))
	if err == nil || normalized != nil {
		t.Fatalf("five pictures normalized = %s, err=%v", normalized, err)
	}
	reason := schema.UserFacingError(err)
	if reason == nil || reason.DetailTemplate == "" || len(reason.DetailValues) == 0 {
		t.Fatalf("refusal has no Schema template: %#v (err=%v)", reason, err)
	}
	for _, fact := range []string{"参考图", "5", "4", "没有提交"} {
		if !strings.Contains(reason.Detail, fact) {
			t.Fatalf("Chinese reason %q must state %q", reason.Detail, fact)
		}
	}
	for _, private := range []string{"reference_assets", "asset-canary", "authorize_extra_credits"} {
		if strings.Contains(reason.Detail, private) {
			t.Fatalf("reason leaked %q: %s", private, reason.Detail)
		}
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	english := reason.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	for _, fact := range []string{"Reference images", "at most 4 pictures", "5 were provided"} {
		if !strings.Contains(english, fact) {
			t.Fatalf("English reason %q must state %q", english, fact)
		}
	}
	for _, chinese := range []string{"参考图", "最多", "没有提交"} {
		if strings.Contains(english, chinese) {
			t.Fatalf("English reason still says %q: %s", chinese, english)
		}
	}

	// The boundary the Schema declares still prepares: four pictures, with the
	// extra-credit approval its own native quote requires.
	boundary := `{"reference_assets":["asset-canary-1","asset-canary-2","asset-canary-3","asset-canary-4"],"authorize_extra_credits":true}`
	_, normalized, err = product.Normalize(
		schema.ImageTo3D, "meshy/meshy-7.1-multiview/image-to-3d", json.RawMessage(boundary))
	if err != nil {
		t.Fatalf("four pictures refused: %v", err)
	}
	var frozen struct {
		References []string `json:"reference_assets"`
	}
	if err := json.Unmarshal(normalized, &frozen); err != nil || len(frozen.References) != 4 {
		t.Fatalf("four pictures froze %s", normalized)
	}
}

func TestRodinMissingExtraCreditConsentNamesTheNextStep(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, normalized, err := product.Normalize(
		schema.ImageTo3D, "hyper3d/rodin-gen-2.5/image-to-3d",
		json.RawMessage(`{"reference_assets":["asset-canary"]}`))
	if err == nil || normalized != nil {
		t.Fatalf("request without consent normalized = %s, err=%v", normalized, err)
	}
	reason := schema.UserFacingError(err)
	if reason == nil || reason.DetailTemplate == "" {
		t.Fatalf("consent refusal has no Schema template: %#v (err=%v)", reason, err)
	}
	for _, fact := range []string{"额外积分", "同意"} {
		if !strings.Contains(reason.Detail, fact) {
			t.Fatalf("Chinese reason %q must state %q", reason.Detail, fact)
		}
	}
	if strings.Contains(reason.Detail, "authorize_extra_credits") {
		t.Fatalf("reason leaked the internal parameter name: %s", reason.Detail)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	english := reason.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	if !strings.Contains(strings.ToLower(english), "extra credits") || !strings.Contains(strings.ToLower(english), "approve") {
		t.Fatalf("English reason = %q", english)
	}

	// The same prepared step succeeds once the person gives that consent.
	_, normalized, err = product.Normalize(
		schema.ImageTo3D, "hyper3d/rodin-gen-2.5/image-to-3d",
		json.RawMessage(`{"reference_assets":["asset-canary"],"authorize_extra_credits":true}`))
	if err != nil || normalized == nil {
		t.Fatalf("consent not accepted: %s, err=%v", normalized, err)
	}
}
