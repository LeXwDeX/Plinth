package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// chainFixture plans onto a finished text-to-3D task of the real default
// catalog, so the Meshy Remesh -> Rigging declarations under test are the ones
// that ship (#121).
type chainFixture struct {
	product *catalog.Catalog
	service *Service
	store   *memoryStore
	gateway *gatewayFake
}

func newChainFixture(t *testing.T, sourceOfferingID generation.OfferingID, sourceInput string) *chainFixture {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	sourceOffering, ok := product.Get(sourceOfferingID)
	if !ok {
		t.Fatalf("source offering %q missing", sourceOfferingID)
	}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: sourceOffering.Capability, State: generation.TaskSucceeded,
		Offering: sourceOffering, Input: json.RawMessage(sourceInput),
		Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready", MediaType: "model/gltf-binary", Size: 1024}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	store := &memoryStore{plans: map[string]Plan{}}
	service, err := New(store, alwaysAvailableCatalog{product}, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &chainFixture{product: product, service: service, store: store, gateway: gateway}
}

func (f *chainFixture) create(t *testing.T, key string, steps ...[2]string) (Plan, error) {
	t.Helper()
	requests := make([]StepRequest, len(steps))
	for index, step := range steps {
		offering, ok := f.product.Get(generation.OfferingID(step[0]))
		if !ok {
			t.Fatalf("offering %q missing", step[0])
		}
		requests[index] = StepRequest{OfferingID: offering.ID, ExpectedOfferingRevision: offering.Revision, Input: json.RawMessage(step[1])}
	}
	return f.service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
		IdempotencyKey: key + "-plan-key-0123456789", Steps: requests})
}

const (
	remeshOffering  = "meshy/processing/remesh"
	riggingOffering = "meshy/processing/rigging"
	textTo3DSource  = "meshy/meshy-7.1/text-to-3d"
)

func remeshInput(topology string, polycount int) string {
	return fmt.Sprintf(`{"topology":%q,"target_polycount":%d}`, topology, polycount)
}

// The Remesh defaults (quad, 300000 faces) deliver about 600000 triangles, over
// Rigging's 320000: the whole plan is refused before any step is paid, with the
// limit and the compatible parameters in the Schema's words.
func TestRemeshDefaultsFollowedByRiggingAreRefusedBeforeAnythingIsPaid(t *testing.T) {
	f := newChainFixture(t, textTo3DSource, "{}")
	_, err := f.create(t, "defaults", [2]string{remeshOffering, `{}`}, [2]string{riggingOffering, `{}`})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("defaults accepted: %v", err)
	}
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.Code != generation.ErrInvalidInput {
		t.Fatalf("refusal is not a Schema reason: %v", err)
	}
	for _, want := range []string{"600000", "320000", "Meshy Remesh", "Meshy Rigging", "quad ≤ 160000 / triangle ≤ 320000", "没有创建", "没有扣积分"} {
		if !strings.Contains(refusal.Detail, want) {
			t.Fatalf("detail %q lacks %q", refusal.Detail, want)
		}
	}
	if len(f.store.plans) != 0 || len(f.gateway.commands) != 0 {
		t.Fatalf("a refused plan left %d plans and %d submissions", len(f.store.plans), len(f.gateway.commands))
	}
	texts, err := catalog.NewTextLocalizer(f.product.Application())
	if err != nil {
		t.Fatal(err)
	}
	english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	if strings.ContainsAny(english, "一二三四五六七八九十的") || !strings.Contains(english, "would deliver about 600000 triangle faces") ||
		!strings.Contains(english, "320000") || !strings.Contains(english, "quad ≤ 160000 / triangle ≤ 320000") {
		t.Fatalf("english refusal = %q", english)
	}
}

// The compatible budget is the Schema's limit divided by each topology's
// factor: quad counts two triangles per face, triangle one.
func TestRemeshThenRiggingBoundaryValues(t *testing.T) {
	for _, test := range []struct {
		name      string
		topology  string
		polycount int
		allowed   bool
	}{
		{"quad at 160000 is exactly the limit", "quad", 160000, true},
		{"quad one face over", "quad", 160001, false},
		// Remesh itself takes at most 300000 faces, so triangle topology always fits.
		{"triangle at the parameter maximum", "triangle", 300000, true},
		{"quad below", "quad", 100, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newChainFixture(t, textTo3DSource, "{}")
			_, err := f.create(t, test.name, [2]string{remeshOffering, remeshInput(test.topology, test.polycount)}, [2]string{riggingOffering, `{}`})
			if test.allowed != (err == nil) {
				t.Fatalf("allowed=%v err=%v", test.allowed, err)
			}
			if !test.allowed && !errors.Is(err, ErrInvalid) {
				t.Fatalf("refusal must still be ErrInvalid: %v", err)
			}
		})
	}
}

// A single Remesh keeps its defaults: nothing limits the model it delivers, and
// the Schema's defaults are not rewritten.
func TestSingleRemeshKeepsItsDefaults(t *testing.T) {
	f := newChainFixture(t, textTo3DSource, "{}")
	if _, err := f.create(t, "single", [2]string{remeshOffering, `{}`}); err != nil {
		t.Fatalf("single Remesh refused: %v", err)
	}
	offering, _ := f.product.Get(remeshOffering)
	defaults := map[string]string{}
	for _, parameter := range offering.Parameters {
		defaults[parameter.Name] = strings.Trim(string(parameter.Default), `"`)
	}
	if defaults["topology"] != "quad" || defaults["target_polycount"] != "300000" {
		t.Fatalf("Remesh defaults changed: %v", defaults)
	}
	if triangles, ok := offering.EstimateOutputTriangles(json.RawMessage(`{}`)); !ok || triangles != 600000 {
		t.Fatalf("default estimate = %d, %v", triangles, ok)
	}
}

// Rigging with no earlier Remesh in the plan is not judged here: its source is
// whatever the task delivered, and the Provider's pre-submission check (#103)
// stays the safeguard for a source it can measure.
func TestRiggingAloneIsNotRefusedByThePlan(t *testing.T) {
	f := newChainFixture(t, textTo3DSource, "{}")
	if _, err := f.create(t, "rigging-only", [2]string{riggingOffering, `{}`}); err != nil {
		t.Fatalf("rigging alone refused: %v", err)
	}
}

// A finished Remesh task the plan starts from counts as the earlier step.
func TestRiggingOnAFinishedDefaultRemeshTaskIsRefused(t *testing.T) {
	f := newChainFixture(t, remeshOffering, `{}`)
	_, err := f.create(t, "after-task", [2]string{riggingOffering, `{}`})
	if refusal := generation.UserFacingError(err); refusal == nil || !strings.Contains(refusal.Detail, "600000") {
		t.Fatalf("rigging after a default Remesh task: %v", err)
	}
	f = newChainFixture(t, remeshOffering, remeshInput("quad", 160000))
	if _, err := f.create(t, "after-fit-task", [2]string{riggingOffering, `{}`}); err != nil {
		t.Fatalf("rigging after a compatible Remesh task refused: %v", err)
	}
}

// A step that declares nothing keeps the model's geometry, so a Remesh still
// counts when another step lies between it and the limited one; a Remesh whose
// budget cannot be read leaves the count unknown, and nothing is refused.
func TestChainEstimateSurvivesNeutralStepsAndStaysUnknownWhenUnreadable(t *testing.T) {
	remesh := generation.Offering{ID: "remesh", ModelName: "Remesh model", Revision: "v1", ProviderID: "p", Capability: "remesh", Domain: generation.DomainProcessing, Enabled: true,
		Parameters: []generation.Parameter{
			{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}},
			{Name: "topology", Type: "select", Default: json.RawMessage(`"quad"`), Options: []generation.ParameterOption{{Value: "quad"}, {Value: "triangle"}}},
			{Name: "target_polycount", Type: "number", Default: json.RawMessage(`300000`)}},
		OutputTriangles: &generation.OutputTriangleEstimate{CountParameter: "target_polycount", TopologyParameter: "topology",
			TrianglesPerFace: map[string]int64{"quad": 2, "triangle": 1}}}
	neutral := generation.Offering{ID: "neutral", Revision: "v1", ProviderID: "p", Capability: "retexture", Domain: generation.DomainProcessing, Enabled: true,
		Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"remesh"}}}}
	limited := generation.Offering{ID: "limited", ModelName: "Rigging model", Revision: "v1", ProviderID: "p", Capability: "rigging", Domain: generation.DomainProcessing, Enabled: true,
		SourceModelLimit: &generation.SourceModelLimit{MaxTriangles: 320000, CountBasis: "triangles"},
		Parameters:       []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"retexture"}}}}
	create := func(t *testing.T, remeshInput, limitedMessage string) error {
		t.Helper()
		limited := limited
		limited.ExecutionMessages.PlanSourceFaceLimit = limitedMessage
		items := memoryCatalog{"remesh": remesh, "neutral": neutral, "limited": limited}
		source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
			Offering: generation.Offering{ProviderID: "p"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
		gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
		service, err := New(&memoryStore{plans: map[string]Plan{}}, items, gateway, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model",
			IdempotencyKey: "neutral-step-plan-key-0123", Steps: []StepRequest{
				{OfferingID: "remesh", ExpectedOfferingRevision: "v1", Input: json.RawMessage(remeshInput)},
				{OfferingID: "neutral", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)},
				{OfferingID: "limited", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}})
		return err
	}
	message := "{source_service}->{service}: {estimate}/{limit} {choices}"
	err := create(t, `{}`, message)
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.Detail != "Remesh model->Rigging model: 600000/320000 quad ≤ 160000 / triangle ≤ 320000" {
		t.Fatalf("estimate lost across a neutral step: %v", err)
	}
	// The boundary values: 160000 quad faces and 320000 triangle faces both
	// deliver exactly 320000 triangles; one face more does not fit.
	for _, test := range []struct {
		input   string
		allowed bool
	}{
		{`{"topology":"quad","target_polycount":160000}`, true},
		{`{"topology":"quad","target_polycount":160001}`, false},
		{`{"topology":"triangle","target_polycount":320000}`, true},
		{`{"topology":"triangle","target_polycount":320001}`, false},
	} {
		if err := create(t, test.input, message); test.allowed != (err == nil) {
			t.Fatalf("%s: allowed=%v err=%v", test.input, test.allowed, err)
		}
	}
	// An unreadable budget or topology leaves the count unknown.
	for _, unreadable := range []string{`{"topology":"hexagon"}`, `{"target_polycount":"many"}`, `{"target_polycount":0}`} {
		if err := create(t, unreadable, message); err != nil {
			t.Fatalf("unknown count refused for %s: %v", unreadable, err)
		}
	}
	// Without the Schema text the plan is still refused, only without a reason.
	if err := create(t, `{}`, ""); !errors.Is(err, ErrInvalid) || generation.UserFacingError(err) != nil {
		t.Fatalf("no-text refusal = %v", err)
	}
}
