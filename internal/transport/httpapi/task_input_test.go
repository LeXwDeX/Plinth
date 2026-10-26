package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestPublicTaskInputKeepsOnlyDeclaredPublicParameters(t *testing.T) {
	t.Parallel()
	task := generation.Task{
		Input: json.RawMessage(`{"prompt":"银色机械猫","size":"1024x1024","n":2,"image":"asset-secret","image_url":"https://x/y.png","mask":"m","seed":7,"undeclared":"x","source_task":"t0"}`),
		Offering: generation.Offering{Parameters: []schema.Parameter{
			{Name: "prompt", Type: "text", Semantic: schema.SemanticPrompt},
			{Name: "size", Type: "text", Semantic: schema.SemanticOutputSize},
			{Name: "n", Type: "number"},
			{Name: "image", Type: "asset"},
			{Name: "image_url", Type: "text", Semantic: schema.SemanticSourceURL},
			{Name: "mask", Type: "asset", Semantic: schema.SemanticMask},
			{Name: "seed", Type: "number", Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceHidden}},
			{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask},
		}},
	}
	got := publicTaskInput(task)
	if got == nil || got.Prompt != "银色机械猫" {
		t.Fatalf("input = %#v", got)
	}
	if len(got.Parameters) != 2 || string(got.Parameters["size"]) != `"1024x1024"` || string(got.Parameters["n"]) != `2` {
		t.Fatalf("parameters = %v", got.Parameters)
	}
	encoded, _ := json.Marshal(got)
	for _, leaked := range []string{"asset-secret", "https://", "undeclared", "seed", "source_task", `"mask"`} {
		if containsString(string(encoded), leaked) {
			t.Fatalf("%q leaked in %s", leaked, encoded)
		}
	}
}

func TestPublicTaskInputIsAbsentWhenNothingIsPublic(t *testing.T) {
	t.Parallel()
	if publicTaskInput(generation.Task{}) != nil {
		t.Fatal("empty input should project to nil")
	}
	task := generation.Task{Input: json.RawMessage(`{"image":"a"}`), Offering: generation.Offering{Parameters: []schema.Parameter{{Name: "image", Type: "asset"}}}}
	if publicTaskInput(task) != nil {
		t.Fatal("asset-only input should project to nil")
	}
}

func containsString(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestPublicTaskInputWithholdsUndeclaredSemanticURLParameters(t *testing.T) {
	t.Parallel()
	task := generation.Task{
		Input: json.RawMessage(`{"prompt":"猫","texture_image_url":"https://cdn.example/tex.png","art_style":"realistic"}`),
		Offering: generation.Offering{Parameters: []schema.Parameter{
			{Name: "prompt", Type: "text", Semantic: schema.SemanticPrompt},
			{Name: "texture_image_url", Type: "text"},
			{Name: "art_style", Type: "text"},
		}},
	}
	got := publicTaskInput(task)
	if got == nil || len(got.Parameters) != 1 || string(got.Parameters["art_style"]) != `"realistic"` {
		t.Fatalf("parameters = %#v", got)
	}
}

// Every credits-metered Offering of the default catalog (generation and post-processing alike)
// must report credits for a succeeded task, including the explicitly free metered analysis,
// and nothing for an unfinished task or an unmetered Offering.
func TestPublicCreditsChargedCoversEveryMeteredOffering(t *testing.T) {
	t.Parallel()
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	metered := 0
	analyzeFound := false
	rigCheckFound := false
	freeMetered := map[string]bool{"meshy/processing/analyze-printability": true, "tripo/processing/rig-check": true}
	for _, offering := range cat.List("") {
		if offering.ID == "meshy/processing/analyze-printability" {
			analyzeFound = true
			if !offering.CreditsMetered || offering.EstimatedCredits != 0 {
				t.Fatalf("analysis must remain explicitly credits-metered with a zero estimate: %+v", offering)
			}
		}
		if offering.ID == "tripo/processing/rig-check" {
			rigCheckFound = true
			if !offering.CreditsMetered || offering.EstimatedCredits != 0 {
				t.Fatalf("rig check must remain explicitly credits-metered with a zero estimate: %+v", offering)
			}
		}
		if !offering.CreditsMetered {
			if got := publicCreditsCharged(generation.Task{State: schema.TaskSucceeded, Offering: offering, Input: json.RawMessage(`{}`)}); got != nil {
				t.Errorf("%s: unmetered offering reported credits %d", offering.ID, *got)
			}
			continue
		}
		metered++
		if offering.NativePricing != nil {
			// A per-selection price (edit-multiview charges each edited view) quotes zero for the empty
			// default input, which input rules reject anyway; its upper quote still carries the unit.
			quote := defaultNativePrice(offering)
			if quote != nil && quote.TotalAmount == "0" {
				quote = upperNativePrice(offering)
			}
			task := generation.Task{State: schema.TaskSucceeded, Offering: offering, NativeQuote: quote}
			if publicCreditsCharged(task) != nil || task.NativeQuote == nil || task.NativeQuote.TotalAmount == "0" {
				t.Fatalf("%s: native charge must retain its unit rather than integer zero", offering.ID)
			}
			continue
		}
		got := publicCreditsCharged(generation.Task{State: schema.TaskSucceeded, Offering: offering, Input: json.RawMessage(`{}`)})
		if freeMetered[string(offering.ID)] {
			if got == nil || *got != 0 {
				t.Errorf("%s: free metered offering must report an explicit zero, got %v", offering.ID, got)
			}
		} else if got == nil || *got <= 0 {
			t.Errorf("%s: paid metered offering must report positive credits", offering.ID)
		}
		if publicCreditsCharged(generation.Task{State: schema.TaskFailed, Offering: offering}) != nil {
			t.Errorf("%s: failed task reports credits", offering.ID)
		}
	}
	if !analyzeFound {
		t.Fatal("default catalog is missing the free metered analysis Offering")
	}
	if !rigCheckFound {
		t.Fatal("default catalog is missing the free metered Rig Check Offering")
	}
	if metered < 10 {
		t.Fatalf("only %d metered offerings checked", metered)
	}
}
