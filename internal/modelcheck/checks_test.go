package modelcheck

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

func bound(value float64) *float64 { return &value }

func TestEvaluateTrianglesPassesFailsAndIsUnknown(t *testing.T) {
	report := modelinspect.Report{Triangles: 1200}
	checks := []budget.PlanCheck{{Check: CheckTriangles, Maximum: bound(2000)}}
	if results := Evaluate(report, checks); results[0].Status != StatusPass {
		t.Fatalf("triangles within maximum: %+v", results)
	}
	checks[0].Maximum = bound(1000)
	if results := Evaluate(report, checks); results[0].Status != StatusFail {
		t.Fatalf("triangles over maximum: %+v", results)
	}
	report.Warnings = []string{"read_error"}
	if results := Evaluate(report, checks); results[0].Status != StatusUnknown {
		t.Fatalf("read error must be unknown: %+v", results)
	}
}

func TestEvaluateBooleanChecksRequireTheProperty(t *testing.T) {
	report := modelinspect.Report{UV: modelinspect.UVReport{AllHaveUV: true}, Triangles: 12}
	checks := []budget.PlanCheck{
		{Check: CheckUV, Required: true},
		{Check: CheckWatertight, Required: true},
		{Check: CheckSkin, Required: true},
	}
	results := Evaluate(report, checks)
	if results[0].Status != StatusPass || results[1].Status != StatusFail || results[2].Status != StatusFail {
		t.Fatalf("boolean checks: %+v", results)
	}
	// Not required means the purpose does not ask, which is unknown, never a pass.
	checks[0].Required = false
	if results := Evaluate(report, checks); results[0].Status != StatusUnknown {
		t.Fatalf("unrequired check: %+v", results)
	}
}

func TestEvaluateRatioAndTextureBounds(t *testing.T) {
	report := modelinspect.Report{
		Triangles: 100,
		Topology:  modelinspect.TopologyReport{DebrisTriangles: 30},
		Materials: []modelinspect.MaterialReport{{Slots: []modelinspect.MaterialSlotReport{
			{Present: true, Width: 2048, Height: 1024},
		}}},
	}
	results := Evaluate(report, []budget.PlanCheck{
		{Check: CheckDebris, MaximumRatio: bound(0.5)},
		{Check: CheckTextureSize, Maximum: bound(2048)},
	})
	if results[0].Status != StatusPass || results[1].Status != StatusPass {
		t.Fatalf("ratio/texture: %+v", results)
	}
	results = Evaluate(report, []budget.PlanCheck{
		{Check: CheckDebris, MaximumRatio: bound(0.2)},
		{Check: CheckTextureSize, Maximum: bound(1024)},
	})
	if results[0].Status != StatusFail || results[1].Status != StatusFail {
		t.Fatalf("ratio/texture over: %+v", results)
	}
}

func TestEvaluatePivotUsesTheBBoxDiagonal(t *testing.T) {
	report := modelinspect.Report{BBox: modelinspect.BBoxReport{
		Size: [3]float64{1, 1, 1}, BottomCenterOffset: [3]float64{0, 0.5, 0},
	}}
	check := []budget.PlanCheck{{Check: CheckPivotBottomCenter, MaximumRatio: bound(0.2)}}
	if results := Evaluate(report, check); results[0].Status != StatusFail {
		t.Fatalf("pivot offset fail: %+v", results)
	}
	report.BBox.BottomCenterOffset = [3]float64{0, 0, 0}
	if results := Evaluate(report, check); results[0].Status != StatusPass {
		t.Fatalf("pivot centre pass: %+v", results)
	}
}

func TestKnownRefusesAnUndeclaredCheck(t *testing.T) {
	if !Known(CheckPivotBottomCenter) || Known("style") {
		t.Fatal("Known must match the declared check ids")
	}
	if len(KnownIDs()) != 10 {
		t.Fatalf("KnownIDs = %v", KnownIDs())
	}
}

func TestFailedOnlyCountsFails(t *testing.T) {
	if Failed([]Result{{Status: StatusPass}, {Status: StatusUnknown}}) {
		t.Fatal("unknown must not block")
	}
	if !Failed([]Result{{Status: StatusPass}, {Status: StatusFail}}) {
		t.Fatal("a fail must block")
	}
}

// TestEvaluateYUpFailsOnlyALyingUprightSubject is #280 at a plan checkpoint:
// Required marks a purpose whose subject stands, and only then a box whose Y
// edge is the shortest fails; a wide quadruped or vehicle stays unknown and
// never pauses the plan.
func TestEvaluateYUpFailsOnlyALyingUprightSubject(t *testing.T) {
	cases := []struct {
		name     string
		size     [3]float64
		upright  bool
		want     string
		wantAxis string
	}{
		{"standing humanoid", [3]float64{0.6, 1.8, 0.3}, true, StatusPass, "y"},
		{"lying humanoid", [3]float64{0.6, 0.3, 1.8}, true, StatusFail, "z"},
		{"lying humanoid on a wide purpose", [3]float64{0.6, 0.3, 1.8}, false, StatusUnknown, "z"},
		{"quadruped", [3]float64{0.4, 0.9, 1.5}, true, StatusUnknown, "z"},
		{"vehicle", [3]float64{1.8, 1.5, 4.5}, false, StatusUnknown, "z"},
		{"table", [3]float64{1.5, 0.75, 0.8}, false, StatusUnknown, "x"},
		{"flat sheet", [3]float64{1, 0.01, 1}, true, StatusUnknown, "flat"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := modelinspect.Report{BBox: modelinspect.BBoxReport{Size: test.size}}
			results := Evaluate(report, []budget.PlanCheck{{Check: CheckYUp, MaximumRatio: bound(0.05), Required: test.upright}})
			if results[0].Status != test.want || results[0].Actual != test.wantAxis {
				t.Fatalf("y_up = %+v, want %s/%s", results[0], test.want, test.wantAxis)
			}
		})
	}
}

// #292: the deliverable a purpose sized is what the texture budget judges; the
// native file, written at the upstream's own resolution, is neither judged nor a
// pass. Without a sized deliverable the file decides, as before.
func TestTextureSizeJudgesTheSizedDeliverableNotTheNativeFile(t *testing.T) {
	native := modelinspect.Report{Materials: []modelinspect.MaterialReport{{Slots: []modelinspect.MaterialSlotReport{
		{Present: true, Width: 8192, Height: 8192}, {Present: true, Width: 4096, Height: 4096},
	}}}}
	check := []budget.PlanCheck{{Check: CheckTextureSize, Maximum: bound(2048)}}

	// The file alone: 8192 against 2048 fails.
	if results := Evaluate(native, check); results[0].Status != StatusFail || results[0].Actual != 8192 || results[0].Detail != "" {
		t.Fatalf("native file: %+v", results)
	}
	// An export sized to 2048: the budget is met by the deliverable, and the
	// result says so.
	results := EvaluateSized(native, check, 2048)
	if results[0].Status != StatusPass || results[0].Actual != 2048 || results[0].Detail != DetailSized {
		t.Fatalf("sized export within the bound: %+v", results)
	}
	// A deliverable sized over the bound fails as itself.
	if results := EvaluateSized(native, check, 4096); results[0].Status != StatusFail || results[0].Actual != 4096 {
		t.Fatalf("sized export over the bound: %+v", results)
	}
	// No bound: unknown, with the basis still named.
	if results := EvaluateSized(native, []budget.PlanCheck{{Check: CheckTextureSize}}, 2048); results[0].Status != StatusUnknown || results[0].Detail != DetailSized {
		t.Fatalf("sized export without a bound: %+v", results)
	}
	// Zero means no sized deliverable: EvaluateSized is Evaluate.
	if results := EvaluateSized(native, check, 0); results[0].Status != StatusFail || results[0].Actual != 8192 {
		t.Fatalf("no sized deliverable: %+v", results)
	}
	// Only texture_size reads the sized edge.
	other := EvaluateSized(modelinspect.Report{Triangles: 10}, []budget.PlanCheck{{Check: CheckTriangles, Maximum: bound(20)}}, 2048)
	if other[0].Status != StatusPass || other[0].Detail != "" {
		t.Fatalf("triangles with a sized deliverable: %+v", other)
	}
}

func TestTextureEdgeReportsBothEdges(t *testing.T) {
	report := modelinspect.Report{Materials: []modelinspect.MaterialReport{{Slots: []modelinspect.MaterialSlotReport{
		{Present: true, Width: 1024, Height: 512}, {Present: false, Width: 9999, Height: 9999},
	}}}}
	if judged, native, bySize := TextureEdge(report, 0); judged != 1024 || native != 1024 || bySize {
		t.Fatalf("file only: %d %d %v", judged, native, bySize)
	}
	if judged, native, bySize := TextureEdge(report, 2048); judged != 2048 || native != 1024 || !bySize {
		t.Fatalf("sized larger than the file: %d %d %v", judged, native, bySize)
	}
}
