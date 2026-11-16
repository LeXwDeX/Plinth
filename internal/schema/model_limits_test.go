package schema_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #304: the factor of an output estimate is a positive, possibly fractional
// number. One Offering of a generation capability declares Rodin's quad as 2.2
// (one real sample: 10,000 quads delivered about 21,800 triangles), another
// keeps Meshy Remesh's whole numbers.

// rodinLikeOffering reads like the shipped Rodin Gen-2.5: a triangle default
// topology and a quad factor that is not a whole number.
func rodinLikeOffering() schema.Offering {
	return schema.Offering{
		Parameters: []schema.Parameter{
			{Name: "mesh_mode", Type: "select", Default: []byte(`"Raw"`)},
			{Name: "quality_override", Type: "number", Default: []byte(`2000000`)},
		},
		OutputTriangles: &schema.OutputTriangleEstimate{CountParameter: "quality_override", TopologyParameter: "mesh_mode",
			TrianglesPerFace: map[string]float64{"Raw": 1, "Quad": 2.2}},
	}
}

func TestEstimateOutputTrianglesAcceptsAFractionalFactor(t *testing.T) {
	offering := rodinLikeOffering()
	for name, test := range map[string]struct {
		input string
		want  int64
		ok    bool
	}{
		"the real sample":        {`{"mesh_mode":"Quad","quality_override":10000}`, 22000, true},
		"a product with noise":   {`{"mesh_mode":"Quad","quality_override":3200}`, 7040, true}, // 3200 * 2.2 is 7040.000000000001 in binary floating point
		"another whole product":  {`{"mesh_mode":"Quad","quality_override":4000}`, 8800, true},
		"rounded up":             {`{"mesh_mode":"Quad","quality_override":10001}`, 22003, true}, // 22002.2
		"one face":               {`{"mesh_mode":"Quad","quality_override":1}`, 3, true},         // 2.2
		"the triangle topology":  {`{"mesh_mode":"Raw","quality_override":50000}`, 50000, true},
		"the declared defaults":  {`{}`, 2000000, true},
		"the quad ceiling":       {`{"mesh_mode":"Quad","quality_override":200000}`, 440000, true},
		"an undeclared topology": {`{"mesh_mode":"Hexagon","quality_override":10}`, 0, false},
		"no count":               {`{"mesh_mode":"Quad","quality_override":0}`, 0, false},
		"a count past an int64":  {`{"mesh_mode":"Quad","quality_override":1e300}`, math.MaxInt64, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := offering.EstimateOutputTriangles([]byte(test.input))
			if got != test.want || ok != test.ok {
				t.Fatalf("estimate = %d, %v; want %d, %v", got, ok, test.want, test.ok)
			}
		})
	}
	// An unusable factor is no estimate; it is never a division or a zero.
	for name, factor := range map[string]float64{"zero": 0, "negative": -2, "not a number": math.NaN(), "infinite": math.Inf(1)} {
		broken := rodinLikeOffering()
		broken.OutputTriangles.TrianglesPerFace["Quad"] = factor
		if got, ok := broken.EstimateOutputTriangles([]byte(`{"mesh_mode":"Quad","quality_override":10}`)); ok || got != 0 {
			t.Fatalf("a %s factor estimated %d, %v", name, got, ok)
		}
	}
}

func TestCompatibleFaceBudgetsRoundDownForAFractionalFactor(t *testing.T) {
	estimate := rodinLikeOffering().OutputTriangles
	for limit, want := range map[int64]string{
		320000: "Quad ≤ 145454 / Raw ≤ 320000", // 320000 / 2.2 = 145454.5
		40000:  "Quad ≤ 18181 / Raw ≤ 40000",   // 18181.8
		22000:  "Quad ≤ 10000 / Raw ≤ 22000",   // exactly 10000 faces, not 9999 for the binary noise of 2.2
		6600:   "Quad ≤ 3000 / Raw ≤ 6600",     // 3000 * 2.2 = 6600.000000000001 would round to 2999 without a tolerance
	} {
		got := schema.FormatTopologyBudgets(estimate.CompatibleFaceBudgets(schema.SourceModelLimit{MaxTriangles: limit, CountBasis: "triangles"}))
		if got != want {
			t.Fatalf("limit %d: budgets = %q, want %q", limit, got, want)
		}
	}
}

// The two conversions are inverse at every whole boundary: the faces a limit
// admits deliver at most the limit, and one more face delivers more.
func TestFaceConversionsAreExactAtTheBoundaries(t *testing.T) {
	for _, factor := range []float64{1, 2, 2.2, 2.18, 2.25, 1.5} {
		for _, limit := range []float64{1, 7, 100, 2999, 6600, 7040, 8000, 20000, 22000, 40000, 60000, 100000, 320000, 2000000} {
			faces := schema.FacesWithinTriangles(limit, factor)
			if faces != math.Trunc(faces) || faces < 0 {
				t.Fatalf("factor %v limit %v: %v faces is not a whole number", factor, limit, faces)
			}
			if got := schema.TrianglesForFaces(faces, factor); got > limit {
				t.Fatalf("factor %v limit %v: %v faces deliver %v triangles, over the limit", factor, limit, faces, got)
			}
			if got := schema.TrianglesForFaces(faces+1, factor); got <= limit {
				t.Fatalf("factor %v limit %v: %v faces already deliver %v triangles, so %v was not the most", factor, limit, faces+1, got, faces)
			}
		}
	}
	if got := schema.TrianglesForFaces(0, 2.2); got != 0 || math.Signbit(got) {
		t.Fatalf("no faces delivered %v triangles", got)
	}
}

// Declarations persisted before #304 hold whole numbers; a frozen Task or a
// prepared input is read back unchanged, and a whole number is written the way
// it was, so the stored bytes of such a declaration do not change.
func TestLegacyIntegerFactorsStillRead(t *testing.T) {
	const legacy = `{"count_parameter":"target_polycount","topology_parameter":"topology","triangles_per_face":{"quad":2,"triangle":1}}`
	var estimate schema.OutputTriangleEstimate
	if err := json.Unmarshal([]byte(legacy), &estimate); err != nil {
		t.Fatalf("a persisted whole-number factor no longer reads: %v", err)
	}
	if estimate.TrianglesPerFace["quad"] != 2 || estimate.TrianglesPerFace["triangle"] != 1 {
		t.Fatalf("factors = %v", estimate.TrianglesPerFace)
	}
	encoded, err := json.Marshal(estimate)
	if err != nil || string(encoded) != legacy {
		t.Fatalf("re-encoded = %s (%v), want the original bytes %s", encoded, err, legacy)
	}

	// The whole Offering as the Task row stores it.
	var stored schema.Offering
	snapshot := `{"id":"meshy/processing/remesh","parameters":[` +
		`{"name":"topology","type":"select","default":"quad","options":[{"value":"quad"},{"value":"triangle"}]},` +
		`{"name":"target_polycount","type":"number","default":300000}],"output_triangles":` + legacy + `}`
	if err := json.Unmarshal([]byte(snapshot), &stored); err != nil {
		t.Fatalf("a stored Offering with whole-number factors no longer reads: %v", err)
	}
	if triangles, ok := stored.EstimateOutputTriangles([]byte(`{"target_polycount":160000}`)); !ok || triangles != 320000 {
		t.Fatalf("estimate of the stored Offering = %d, %v", triangles, ok)
	}

	// A fractional factor round-trips as written.
	var fractional schema.OutputTriangleEstimate
	decimal := `{"count_parameter":"quality_override","topology_parameter":"mesh_mode","triangles_per_face":{"Quad":2.2,"Raw":1}}`
	if err := json.Unmarshal([]byte(decimal), &fractional); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(fractional)
	if err != nil || !strings.Contains(string(encoded), `"Quad":2.2`) || !strings.Contains(string(encoded), `"Raw":1`) {
		t.Fatalf("re-encoded = %s (%v)", encoded, err)
	}
}
