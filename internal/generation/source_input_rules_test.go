package generation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #294: Meshy Rigging adds no finger bones to a model made with quad topology
// (the real API answered HTTP 400 on 2026-11-12), and the topology is a
// parameter of the Task that made the model. The shipped Schema says so as a
// source_input_rules entry of the rigging step's source_task parameter; the
// kernel applies it where it resolves the dependency, which Prepare, Submit and
// the dispatch of a queued Task all go through, so the combination is refused
// before anything is submitted or paid.

const meshyRiggingID = "meshy/processing/rigging"

func meshyRigging(t *testing.T) (*catalog.Catalog, Offering) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rigging, found := product.Get(meshyRiggingID)
	if !found {
		t.Fatalf("%s is not in the Catalog", meshyRiggingID)
	}
	return product, rigging
}

// riggingSource is a succeeded Meshy task with the frozen input given, the way
// the repository holds it.
func riggingSource(offeringID OfferingID, capability Capability, input string) (dependencySourceRepository, *dependencyProjection) {
	job := UpstreamJob{ProviderID: "meshy", RemoteID: "private-meshy-task"}
	source := Aggregate{
		Task: Task{ID: "source", OwnerID: "owner", State: TaskSucceeded, Capability: capability, Input: json.RawMessage(input),
			Offering: Offering{ID: offeringID, ProviderID: "meshy", AdapterKey: "meshy.v2"}},
		CurrentAttempt: &Attempt{State: AttemptUpstreamSucceeded, UpstreamJob: &job},
	}
	return dependencySourceRepository{source: source}, &dependencyProjection{job: job}
}

func TestMeshyRiggingRefusesFingersOnAQuadSourceBeforeAnythingIsSent(t *testing.T) {
	product, rigging := meshyRigging(t)
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	english := func(text string) string { return texts.Text("en", text) }

	for _, tc := range []struct {
		name         string
		source       OfferingID
		capability   Capability
		sourceInput  string
		requestInput string
		refused      bool
	}{
		{"a Meshy 7.1 quad model with fingers", "meshy/meshy-7.1/text-to-3d", TextTo3D,
			`{"prompt":"a robot","topology":"quad","should_remesh":true,"target_polycount":8000}`,
			`{"source_task":"source","include_fingers":true}`, true},
		{"the Meshy 7 default is quad too", "meshy/meshy-7/image-to-3d", ImageTo3D,
			`{"topology":"quad"}`, `{"source_task":"source","include_fingers":true,"skeleton_template":"ue5_mannequin"}`, true},
		{"a quad remesh with fingers", "meshy/processing/remesh", Remesh,
			`{"topology":"quad","target_polycount":10000}`, `{"source_task":"source","include_fingers":true}`, true},
		{"a quad model without fingers", "meshy/meshy-7.1/text-to-3d", TextTo3D,
			`{"topology":"quad"}`, `{"source_task":"source","include_fingers":false}`, false},
		{"a quad model with the fingers left out", "meshy/meshy-7.1/text-to-3d", TextTo3D,
			`{"topology":"quad"}`, `{"source_task":"source"}`, false},
		{"a triangle model with fingers", "meshy/meshy-7.1/text-to-3d", TextTo3D,
			`{"topology":"triangle"}`, `{"source_task":"source","include_fingers":true}`, false},
		// Smart Topology models have no topology parameter and deliver triangles.
		{"a Smart Topology model with fingers", "meshy/meshy-t2.5/text-to-3d", TextTo3D,
			`{"prompt":"a robot","target_polycount":20000}`, `{"source_task":"source","include_fingers":true}`, false},
		// The rule reads the source Task's own frozen input: a retexture has no
		// topology of its own, so a retexture of a quad model is not recognised here
		// and the upstream decides (the limitation the Schema comment states).
		{"a retexture carries no topology", "meshy/processing/retexture", Retexture,
			`{"source_task":"earlier","text_style_prompt":"red"}`, `{"source_task":"source","include_fingers":true}`, false},
		// An unreadable frozen input is not a reason to block.
		{"a source whose input cannot be read", "meshy/meshy-7.1/text-to-3d", TextTo3D,
			`not json`, `{"source_task":"source","include_fingers":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository, projection := riggingSource(tc.source, tc.capability, tc.sourceInput)
			module := &Module{repository: repository, providers: &dependencySourceRegistry{port: projection}}
			dependencies, err := module.resolveTaskDependencies(context.Background(), "owner", rigging, json.RawMessage(tc.requestInput), "")
			if !tc.refused {
				if err != nil || dependencies["source_task"].TaskID != "source" {
					t.Fatalf("the source was refused: %v (%+v)", err, dependencies)
				}
				return
			}
			refusal := UserFacingError(err)
			if refusal == nil || refusal.Code != ErrInvalidInput {
				t.Fatalf("the quad source was not refused as invalid input: %v", err)
			}
			for _, want := range []string{"四边面拓扑", "手指骨骼", "这一步没有提交，也没有扣积分"} {
				if !strings.Contains(refusal.Detail, want) {
					t.Errorf("the refusal %q does not say %q", refusal.Detail, want)
				}
			}
			// Parameter names stay inside: the person reads labels.
			for _, internal := range []string{"include_fingers", "topology", "source_task"} {
				if strings.Contains(refusal.Detail, internal) {
					t.Errorf("the refusal names the internal parameter %q: %q", internal, refusal.Detail)
				}
			}
			localized := refusal.LocalizedDetail(english)
			for _, want := range []string{"quad topology", "finger bones", "no credits were spent"} {
				if !strings.Contains(localized, want) {
					t.Errorf("the English refusal %q does not say %q", localized, want)
				}
			}
			if strings.ContainsAny(localized, "{}") || localized == refusal.Detail {
				t.Errorf("the English refusal is not English text: %q", localized)
			}
			// Refused before the provider was asked for anything.
			if projection.calls != 0 || len(dependencies) != 0 {
				t.Errorf("the provider was asked (%d calls) before the refusal; dependencies %+v", projection.calls, dependencies)
			}
		})
	}
}

// A parameter without rules keeps resolving as it did, and a rule never reads
// another parameter's source: the rules belong to the parameter that declares
// them.
func TestSourceInputRulesBelongToTheirParameter(t *testing.T) {
	_, rigging := meshyRigging(t)
	plain := rigging
	plain.Parameters = append([]Parameter(nil), rigging.Parameters...)
	for index := range plain.Parameters {
		plain.Parameters[index].SourceInputRules = nil
	}
	repository, projection := riggingSource("meshy/meshy-7.1/text-to-3d", TextTo3D, `{"topology":"quad"}`)
	module := &Module{repository: repository, providers: &dependencySourceRegistry{port: projection}}
	request := json.RawMessage(`{"source_task":"source","include_fingers":true}`)

	if _, err := module.resolveTaskDependencies(context.Background(), "owner", plain, request, ""); err != nil {
		t.Fatalf("an Offering that declares no rule refused the source: %v", err)
	}
	if _, err := module.resolveTaskDependencies(context.Background(), "owner", rigging, request, ""); err == nil {
		t.Fatal("the shipped rigging Offering accepted finger bones on a quad source")
	}
}
