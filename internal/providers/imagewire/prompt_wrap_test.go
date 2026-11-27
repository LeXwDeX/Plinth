package imagewire_test

import (
	"context"
	"encoding/json"
	"mime"
	"mime/multipart"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var maskWrap = schema.PromptWrap{
	When:     schema.ParameterCondition{Parameter: "mask", Operator: "present"},
	Template: "Edit only the masked area: {prompt}. Keep everything outside the masked area exactly unchanged.",
}

func wrappedOffering(t *testing.T, encoding string) generation.Offering {
	t.Helper()
	offering := multipartOffering()
	encoded, err := json.Marshal(generation.ImageProtocol{
		Endpoint: "images/edits", RequestEncoding: encoding,
		RequestTemplate: json.RawMessage(`{"model":"$model"}`),
		PromptWraps:     []schema.PromptWrap{maskWrap},
	})
	if err != nil {
		t.Fatal(err)
	}
	offering.ProviderOptions = encoded
	offering.Parameters[0].Semantic = schema.SemanticPrompt
	return offering
}

func multipartPrompt(t *testing.T, offering generation.Offering, input string, inputs map[string][]generation.InputAsset) string {
	t.Helper()
	reader, contentType, err := imagewire.EncodeMultipart(context.Background(), generation.StartRequest{
		Offering: offering, Input: json.RawMessage(input), InputAssets: inputs,
	}, &fixtureAssets{data: []byte("\x89PNG-fixture")})
	if err != nil {
		t.Fatal(err)
	}
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(reader, parameters["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Value["prompt"]) != 1 {
		t.Fatalf("prompt fields = %v", form.Value["prompt"])
	}
	return form.Value["prompt"][0]
}

// #687: GPT Image follows a mask only when the prompt names the masked area, so
// the declared wrap frames the user's words (Chinese included) whenever a mask
// is sent, and leaves an edit without a mask as the user wrote it.
func TestPromptWrapFramesTheMaskedEditOnTheWire(t *testing.T) {
	offering := wrappedOffering(t, "multipart")
	references := map[string][]generation.InputAsset{"reference_assets": {{ID: "a", MediaType: "image/png"}}}
	masked := map[string][]generation.InputAsset{"reference_assets": references["reference_assets"], "mask": {{ID: "b", MediaType: "image/png"}}}

	got := multipartPrompt(t, offering, `{"prompt":"  换个颜色 ","reference_assets":["a"],"mask":"b"}`, masked)
	want := "Edit only the masked area: 换个颜色. Keep everything outside the masked area exactly unchanged."
	if got != want {
		t.Fatalf("masked prompt = %q, want %q", got, want)
	}
	if got := multipartPrompt(t, offering, `{"prompt":"换个颜色","reference_assets":["a"]}`, references); got != "换个颜色" {
		t.Fatalf("prompt without a mask = %q, want it unchanged", got)
	}
	// Only the prompt parameter is wrapped; no declaration sends the prompt as is.
	offering.Parameters[0].Semantic = ""
	if got := multipartPrompt(t, offering, `{"prompt":"换个颜色","reference_assets":["a"],"mask":"b"}`, masked); got != "换个颜色" {
		t.Fatalf("a parameter that is not the prompt was wrapped: %q", got)
	}
}

func TestPromptWrapAppliesToTheJSONProjection(t *testing.T) {
	offering := wrappedOffering(t, "json")
	offering.Parameters = offering.Parameters[:1]
	offering.Parameters = append(offering.Parameters, generation.Parameter{Name: "mask", Type: "text"})
	for input, want := range map[string]string{
		`{"prompt":"red","mask":"m"}`: "Edit only the masked area: red. Keep everything outside the masked area exactly unchanged.",
		`{"prompt":"red"}`:            "red",
	} {
		body, err := imagewire.Build(context.Background(), generation.StartRequest{Offering: offering, Input: json.RawMessage(input)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if body["prompt"] != want {
			t.Fatalf("%s: prompt = %v, want %q", input, body["prompt"], want)
		}
	}
}
