package schema_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var han = regexp.MustCompile(`\p{Han}`)

// #358: every reachable synchronous input reason has Schema copy, and the
// English text keeps the values the person can act on.
func TestInputRefusalLocalizesSchemaCopy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	messages := application.Runtime.GenerationMessages
	for _, test := range []struct {
		name    string
		reason  schema.InputRefusalReason
		values  schema.InputRefusalValues
		english []string
	}{
		{"mismatch", schema.InputRefusalMismatch, schema.InputRefusalValues{}, []string{"frozen input"}},
		{"unavailable", schema.InputRefusalUnavailable, schema.InputRefusalValues{}, []string{"temporarily"}},
		{"too large", schema.InputRefusalTooLarge, schema.InputRefusalValues{Size: "10.03 MB", Limit: "10 MB"}, []string{"10.03 MB", "10 MB"}},
		{"media type", schema.InputRefusalMediaType, schema.InputRefusalValues{MediaType: "image/gif", Formats: "image/png, image/jpeg"}, []string{"image/gif", "image/png, image/jpeg"}},
		{"dimensions", schema.InputRefusalDimensions, schema.InputRefusalValues{Width: 8192, Height: 8192, MinWidth: 256, MinHeight: 256, MaxPixels: 36000000}, []string{"8192×8192", "256×256", "36000000"}},
		{"invalid", schema.InputRefusalInvalid, schema.InputRefusalValues{}, []string{"required format"}},
		{"rig required", schema.InputRefusalRigRequired, schema.InputRefusalValues{}, []string{"same provider"}},
		{"pixels", schema.InputRefusalPixels, schema.InputRefusalValues{Width: 4096, Height: 4096, MaxPixels: 16_000_000}, []string{"4096×4096", "16000000"}},
		{"parameter", schema.InputRefusalParameter, schema.InputRefusalValues{Label: "Description"}, []string{"Description"}},
		{"parameters", schema.InputRefusalParameters, schema.InputRefusalValues{}, []string{"input parameters"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			refusal := messages.InputRefusal(test.reason, test.values)
			if refusal == nil || !refusal.UserFacing || refusal.Detail == "" ||
				refusal.DetailTemplate == "" || len(refusal.DetailValues)%2 != 0 {
				t.Fatalf("refusal = %+v", refusal)
			}
			english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
			if han.MatchString(english) {
				t.Fatalf("English refusal still has Chinese: %q", english)
			}
			for _, value := range test.english {
				if !strings.Contains(english, value) {
					t.Fatalf("English refusal %q lost %q", english, value)
				}
			}
			chinese := refusal.LocalizedDetail(func(text string) string { return texts.Text("zh-CN", text) })
			if chinese != refusal.Detail {
				t.Fatalf("default text changed: %q != %q", refusal.Detail, chinese)
			}
		})
	}
}

// A Catalog that declares no newer copy keeps the Provider diagnostic: the
// refusal helper returns nil instead of inventing a generic sentence.
func TestInputRefusalWithoutSchemaTextIsNil(t *testing.T) {
	if refusal := (schema.ExecutionMessages{}).InputRefusal(schema.InputRefusalInvalid, schema.InputRefusalValues{}); refusal != nil {
		t.Fatalf("refusal = %+v", refusal)
	}
	if refusal := (schema.ExecutionMessages{}).InputRefusal(schema.InputRefusalDimensions,
		schema.InputRefusalValues{Width: 1, Height: 1, MinWidth: 0, MinHeight: 1, MaxPixels: 1}); refusal != nil {
		t.Fatalf("incomplete dimensions produced %+v", refusal)
	}
	if refusal := (schema.ExecutionMessages{}).InputRefusal(schema.InputRefusalTooLarge,
		schema.InputRefusalValues{Size: "1 B"}); refusal != nil {
		t.Fatalf("missing limit produced %+v", refusal)
	}
	if refusal := (schema.ExecutionMessages{}).InputRefusal(schema.InputRefusalParameter,
		schema.InputRefusalValues{}); refusal != nil {
		t.Fatalf("missing parameter label produced %+v", refusal)
	}
}

func TestInputRefusalFormatsNumbers(t *testing.T) {
	messages := schema.ExecutionMessages{InputSourceDimensions: "{width}×{height} {min_width} {min_height} {max_pixels}"}
	refusal := messages.InputRefusal(schema.InputRefusalDimensions,
		schema.InputRefusalValues{Width: 12, Height: 34, MinWidth: 1, MinHeight: 2, MaxPixels: 56})
	if refusal == nil || refusal.Detail != "12×34 1 2 56" {
		t.Fatalf("refusal = %+v", refusal)
	}
}
