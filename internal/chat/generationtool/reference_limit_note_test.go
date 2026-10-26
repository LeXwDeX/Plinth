package generationtool_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #329: a model that takes fewer pictures than the turn gave (Meshy 7.1 and
// T2.5 Image to 3D take one) uses the first ones, as before, and the result
// says so in the Schema's words, so the Agent can tell the person and point
// at a multi-image model.
func TestModelFromMorePicturesThanTheModelTakesSaysWhichWereUsed(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// One Schema text says it everywhere (#329 note 99647, observation 9):
	// the Offering's runtime.generation_messages pair, the one-picture form
	// for a model that takes one.
	note := product.Application().Runtime.GenerationMessages.ReferenceLimitOne
	if !strings.Contains(note, "{model}") || !strings.Contains(note, "{total}") {
		t.Fatalf("no one-picture reference limit text: %q", note)
	}
	single := func(offering *generation.Offering) {
		offering.Parameters[1].MinItems, offering.Parameters[1].MaxItems = 1, 1
	}
	views := &fakeThreeViews{members: map[string]bool{}}

	run := runModelFromImagesWith(t, views, []string{"/api/v1/assets/own-a", "/api/v1/assets/own-b", "/api/v1/assets/own-c"}, single)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("model from three pictures: %+v %v", run.result, run.err)
	}
	if len(run.resolver.urls) != 1 || run.resolver.urls[0] != "/api/v1/assets/own-a" {
		t.Fatalf("sent %v, want the first picture only", run.resolver.urls)
	}
	want := strings.NewReplacer("{model}", "configured-model", "{count}", strconv.Itoa(1), "{total}", strconv.Itoa(3)).Replace(note)
	if got := run.content["reference_note"]; got != want {
		t.Fatalf("reference_note = %v, want %q", got, want)
	}
	// Observation 7: what the Agent is given is the facts — which picture is
	// used, how many were given, and which kind of model takes several.
	for _, fact := range []string{"configured-model", "第 1 张", "3 张", "支持多图的模型"} {
		if !strings.Contains(want, fact) {
			t.Fatalf("reference_note %q does not state %q", want, fact)
		}
	}

	// One picture, or a model without a count limit: no note.
	for name, run := range map[string]threeViewModelRun{
		"one picture": runModelFromImagesWith(t, views, []string{"/api/v1/assets/own-a"}, single),
		"no limit":    runModelFromImagesWith(t, views, []string{"/api/v1/assets/own-a", "/api/v1/assets/own-b"}, func(*generation.Offering) {}),
	} {
		if run.err != nil {
			t.Fatalf("%s: %v", name, run.err)
		}
		if _, noted := run.content["reference_note"]; noted {
			t.Fatalf("%s got a note: %v", name, run.content)
		}
	}
}
