package generationtool_test

import (
	"reflect"
	"strings"
	"testing"
)

// #433: the multiview tool takes several views of one object whether the person
// pressed the button, the Agent names a group, or the Agent gives the pictures
// itself. Handles (and this turn's or the selected pictures) therefore carry
// views too, so the person's locked model resolves to its multiview shape and a
// model that takes no several views refuses the step instead of running one
// picture as a single-image 3D step.

const (
	meshyImageTo3D          = "meshy/meshy-7.1/image-to-3d"
	meshyMultiviewImageTo3D = "meshy/meshy-7.1-multiview/image-to-3d"
)

func fourViewPictures() []string {
	return []string{"front-url", "left-url", "back-url", "right-url"}
}

func threeViewPictures() []string {
	return []string{"front-url", "left-url", "back-url"}
}

func referenceAssets(run multiviewRun) []any {
	assets, _ := run.input["reference_assets"].([]any)
	return assets
}

// A session lock on Meshy 7.1 and a multiview call that gives four handles: the
// person's model is the family, so the step runs Meshy 7.1 multiview with all
// four pictures. Before #433 the views were unknown here, the lock resolved to
// the single-image shape and its one-picture limit cut the list to the front.
func TestMultiviewHandlesRunTheLockFamilysMultiviewShape(t *testing.T) {
	for _, test := range []struct {
		name     string
		pictures []string
		handles  string
	}{
		{"four views", fourViewPictures(), `["@img1","@img2","@img3","@img4"]`},
		{"three views", threeViewPictures(), `["@img1","@img2","@img3"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := runMultiviewIn(t, multiviewScenario{
				locked:   "meshy-7.1",
				also:     []string{meshyImageTo3D, meshyMultiviewImageTo3D},
				pictures: test.pictures,
			}, nil, `{"reference_handles":`+test.handles+`}`, nil)
			if run.err != nil || run.result.NoSubmission {
				t.Fatalf("handles under the lock's family: %+v %v", run.result, run.err)
			}
			if run.gateway.submitted.OfferingID != meshyMultiviewImageTo3D {
				t.Fatalf("offering = %q, want the locked model's multiview shape (submitted %q)",
					meshyMultiviewImageTo3D, run.gateway.submitted.OfferingID)
			}
			assets := referenceAssets(run)
			if len(assets) != len(test.pictures) {
				t.Fatalf("reference_assets = %v, want all %d pictures", assets, len(test.pictures))
			}
			for index, url := range test.pictures {
				if assets[index] != "asset-for-"+url {
					t.Fatalf("reference_assets = %v, want the handles in their order", assets)
				}
			}
			if _, written := run.input["reference_views"]; written {
				// Meshy reads the pictures in view order; it declares no view labels.
				t.Fatalf("an order-only Offering must get no view labels: %v", run.input["reference_views"])
			}
			if choice, _ := run.content["model_choice"].(map[string]any); choice["source"] != "user" {
				t.Fatalf("model_choice = %v, want the locked model's family", run.content["model_choice"])
			}
			if note, _ := run.content["reference_note"].(string); !strings.Contains(note, "front") {
				t.Fatalf("reference_note = %q, want the views the step used", note)
			}
		})
	}
}

// The lock has no multiview shape (Rodin is a plain multi-reference model): the
// four pictures cannot run as they are, so the step refuses with the Schema's
// wording and submits nothing. It must not submit the front picture alone.
func TestMultiviewHandlesUnderASinglePictureLockRefuse(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		locked:   "rodin-gen-2.5",
		also:     []string{rodinImageTo3D},
		pictures: fourViewPictures(),
	}, nil, `{"reference_handles":["@img1","@img2","@img3","@img4"]}`, nil)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" || len(run.resolver.urls) != 0 {
		t.Fatalf("a lock without a multiview shape must refuse: %+v (submitted %q, resolved %v)",
			run.result, run.gateway.submitted.OfferingID, run.resolver.urls)
	}
	want := strings.ReplaceAll(run.chat.ThreeView.Messages.ViewsUnsupported, "{model}", "Rodin Gen-2.5")
	if !strings.Contains(run.result.AgentContent, want) {
		t.Fatalf("refusal = %q, want the Schema's views_unsupported copy %q", run.result.AgentContent, want)
	}
}

// Without a lock the call is not the person's model: the multiview tool's own
// model choice runs, and it takes every picture the handles name.
func TestMultiviewHandlesWithoutALockRunAMultiviewModel(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		also:     []string{meshyImageTo3D, meshyMultiviewImageTo3D},
		pictures: threeViewPictures(),
	}, nil, `{"reference_handles":["@img1","@img2","@img3"]}`, nil)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("handles without a lock: %+v %v", run.result, run.err)
	}
	if run.gateway.submitted.OfferingID != multiviewOffering {
		t.Fatalf("offering = %q, want the first multiview model the purpose and Catalog offer", run.gateway.submitted.OfferingID)
	}
	if assets := referenceAssets(run); len(assets) != 3 {
		t.Fatalf("reference_assets = %v, want all three pictures", assets)
	}
	// Tripo labels each picture's view: the position the tool describes is
	// frozen with the pictures, as a three-view group's views are.
	if got := run.input["reference_views"]; !reflect.DeepEqual(got, []any{"front", "left", "back"}) {
		t.Fatalf("reference_views = %v, want the platform's view order by position", got)
	}
}

// The Agent may say which view each handle is. A labelled Offering keeps the
// Agent's labels in their own order, and refuses a list that names no view of
// each picture, repeats one, or leaves out the front view.
func TestMultiviewHandlesKeepTheCallsViewLabels(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{pictures: fourViewPictures()}, nil,
		`{"reference_handles":["@img1","@img2"],"reference_views":["front","back"]}`, nil)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("labelled handles: %+v %v", run.result, run.err)
	}
	if got := run.input["reference_views"]; !reflect.DeepEqual(got, []any{"front", "back"}) {
		t.Fatalf("reference_views = %v, want the labels the Agent gave", got)
	}
	if assets := referenceAssets(run); len(assets) != 2 {
		t.Fatalf("reference_assets = %v, want both labelled pictures", assets)
	}

	for _, arguments := range []string{
		// One label fewer than pictures.
		`{"reference_handles":["@img1","@img2","@img3"],"reference_views":["front","left"]}`,
		// The same view twice.
		`{"reference_handles":["@img1","@img2"],"reference_views":["front","front"]}`,
		// No front view.
		`{"reference_handles":["@img1","@img2"],"reference_views":["left","back"]}`,
		// A view the platform does not declare.
		`{"reference_handles":["@img1","@img2"],"reference_views":["front","top"]}`,
	} {
		bad := runMultiviewIn(t, multiviewScenario{pictures: fourViewPictures()}, nil, arguments, nil)
		if bad.err != nil || !bad.result.NoSubmission || bad.gateway.submitted.OfferingID != "" ||
			!strings.Contains(bad.result.AgentContent, bad.chat.ThreeView.Messages.ViewsInvalid) {
			t.Fatalf("%s: an invalid view list must refuse: %+v %v", arguments, bad.result, bad.err)
		}
	}
}

// An order-only Offering reads pictures by position, so a label list that would
// make it read the back picture as the left one refuses instead of shifting the
// angles (#243).
func TestMultiviewHandlesRefuseANonContiguousPrefixForAnOrderOnlyModel(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		locked:   "meshy-7.1",
		also:     []string{meshyImageTo3D, meshyMultiviewImageTo3D},
		pictures: fourViewPictures(),
	}, nil, `{"reference_handles":["@img1","@img2"],"reference_views":["front","back"]}`, nil)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if !run.result.NoSubmission || run.gateway.submitted.OfferingID != "" {
		t.Fatalf("front plus back must refuse an order-only model: %+v (submitted %q)",
			run.result, run.gateway.submitted.OfferingID)
	}
	if !strings.Contains(run.result.AgentContent, run.chat.ThreeView.Messages.ViewsInvalid) {
		t.Fatalf("refusal = %q, want the view check's words", run.result.AgentContent)
	}
}
