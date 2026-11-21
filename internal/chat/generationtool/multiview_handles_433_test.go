package generationtool_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
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

// wantReferenceOrder checks the frozen pictures are the given ones, in that
// order: an order-only Offering reads a picture's position as its view, so a
// reordered list would make it read another angle as the front.
func wantReferenceOrder(t *testing.T, run multiviewRun, urls []string) {
	t.Helper()
	assets := referenceAssets(run)
	if len(assets) != len(urls) {
		t.Fatalf("reference_assets = %v, want %d pictures", assets, len(urls))
	}
	for index, url := range urls {
		if assets[index] != "asset-for-"+url {
			t.Fatalf("reference_assets = %v, want %v in that order", assets, urls)
		}
	}
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

// The frozen picture list is the order the views were bound in: a picture the
// person marked "input" does not move ahead of them (#433). An order-only model
// reads positions, so a moved picture would make it take the back view as the
// front.
func TestMultiviewHandlesKeepTheirOrderWhenAPictureIsMarkedInput(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		locked:   "meshy-7.1",
		also:     []string{meshyImageTo3D, meshyMultiviewImageTo3D},
		pictures: fourViewPictures(),
		purposes: map[string]string{"@img3": "input"},
	}, nil, `{"reference_handles":["@img1","@img2","@img3","@img4"]}`, nil)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("handles with an input-marked picture: %+v %v", run.result, run.err)
	}
	if run.gateway.submitted.OfferingID != meshyMultiviewImageTo3D {
		t.Fatalf("offering = %q, want the multiview shape", run.gateway.submitted.OfferingID)
	}
	wantReferenceOrder(t, run, fourViewPictures())
	if _, written := run.input["reference_views"]; written {
		t.Fatalf("an order-only Offering must get no view labels: %v", run.input["reference_views"])
	}
}

// The same order holds on an artboard-source turn: the call's own pictures keep
// their positions, so the snapshot's own order cannot relabel them (#433). A
// Catalog that declares the purpose argument on the multiview tool gets the same
// rule: the person's "input" mark does not require a picture to lead, because
// every position is a view.
func TestMultiviewHandlesKeepTheirOrderOnAnArtboardTurn(t *testing.T) {
	pictures := fourViewPictures()
	// The person's selection holds the same four pictures in another order.
	selection := []string{"back-url", "right-url", "front-url", "left-url"}
	for _, test := range []struct {
		name           string
		pictures       []string
		selection      []string
		arguments      string
		purposeArg     bool
		wantOfferingID generation.OfferingID
	}{
		{
			name: "the call names the views", pictures: pictures, selection: selection,
			arguments:      `{"reference_handles":["@img1","@img2","@img3","@img4"]}`,
			wantOfferingID: meshyMultiviewImageTo3D,
		},
		{
			name: "the selection is the call's pictures in view order", selection: pictures,
			arguments:      `{}`,
			wantOfferingID: meshyMultiviewImageTo3D,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := runMultiviewIn(t, multiviewScenario{
				locked:    "meshy-7.1",
				also:      []string{meshyImageTo3D, meshyMultiviewImageTo3D},
				pictures:  test.pictures,
				selection: test.selection,
				canvas:    true,
			}, nil, test.arguments, nil)
			if run.err != nil || run.result.NoSubmission {
				t.Fatalf("artboard turn: %+v %v", run.result, run.err)
			}
			if run.gateway.submitted.OfferingID != test.wantOfferingID {
				t.Fatalf("offering = %q, want %q", run.gateway.submitted.OfferingID, test.wantOfferingID)
			}
			wantReferenceOrder(t, run, pictures)
		})
	}
}

// A Catalog that declares reference_purposes on the multiview tool still runs
// the step with an input-marked picture at its view's position: the request's
// order is the views, not a base picture (#433).
func TestMultiviewHandlesDoNotRequireAnInputPictureToLead(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		locked:          "meshy-7.1",
		also:            []string{meshyImageTo3D, meshyMultiviewImageTo3D},
		pictures:        fourViewPictures(),
		purposes:        map[string]string{"@img3": "input"},
		canvas:          true,
		purposeArgument: true,
	}, nil, `{"reference_handles":["@img1","@img2","@img3","@img4"]}`, nil)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("an input-marked back view must run in place: %+v %v", run.result, run.err)
	}
	if run.gateway.submitted.OfferingID != meshyMultiviewImageTo3D {
		t.Fatalf("offering = %q, want the multiview shape", run.gateway.submitted.OfferingID)
	}
	wantReferenceOrder(t, run, fourViewPictures())
}

// A labelled Offering keeps the call's labels and pictures aligned on an
// artboard turn and with an input-marked picture: the frozen input is read as
// the views it names.
func TestMultiviewHandlesKeepTheirLabelsOnArtboardAndInputTurns(t *testing.T) {
	run := runMultiviewIn(t, multiviewScenario{
		pictures:  fourViewPictures(),
		purposes:  map[string]string{"@img3": "input"},
		selection: []string{"back-url", "right-url", "front-url", "left-url"},
		canvas:    true,
	}, nil,
		`{"reference_handles":["@img1","@img2","@img3","@img4"],"reference_views":["front","left","back","right"]}`, nil)
	if run.err != nil || run.result.NoSubmission {
		t.Fatalf("labelled handles on an artboard turn: %+v %v", run.result, run.err)
	}
	if run.gateway.submitted.OfferingID != multiviewOffering {
		t.Fatalf("offering = %q, want the labelled multiview Offering", run.gateway.submitted.OfferingID)
	}
	wantReferenceOrder(t, run, fourViewPictures())
	if got := run.input["reference_views"]; !reflect.DeepEqual(got, []any{"front", "left", "back", "right"}) {
		t.Fatalf("reference_views = %v, want the call's labels in its picture order", got)
	}
	note, _ := run.content["reference_note"].(string)
	if !strings.Contains(note, "正面 / 左侧 / 背面 / 右侧") {
		t.Fatalf("reference_note = %q, want the labels of the pictures that ran", note)
	}
}
