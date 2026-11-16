package schema

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

// SourceModelLimit declares, once, how many triangles the 3D model a step
// takes as its source may have (#103, #121). Meshy documents Rigging as taking
// "up to 320,000 faces" without defining a face; Plinth counts triangles, the
// conservative reading, because one quad is two triangles. CountBasis
// therefore only accepts "triangles".
//
// It is the only place the number is written. The Provider Adapter measures a
// frozen source against it before submission, the Processing Plan checks a
// planned chain against it before anything is paid (see OutputTriangleEstimate)
// and requirement texts spell it as the placeholder {limit}.
//
// It differs from model_input_profile.max_instanced_triangles, which bounds the
// one model a profiled inline input sends upstream (UV unwrap, 40000); this one
// bounds a step's source task or source asset. Both are compared with the same
// measured Profile.InstancedTriangles through modelinput.Profile.
// ExceedsInstancedTriangles.
type SourceModelLimit struct {
	MaxTriangles int64  `json:"max_triangles"`
	CountBasis   string `json:"count_basis"`
}

// SourceModelCountBasisTriangles is the only supported count basis.
const SourceModelCountBasisTriangles = "triangles"

// OutputTriangleEstimate declares how an Offering's requested face budget turns
// into the triangle count of the model it delivers, so a plan can tell before
// any step is paid whether a later step's SourceModelLimit will accept it
// (#121) and a session purpose's triangle budget can bound a face-count
// parameter of a 3D generation step too (#129, #304). CountParameter is the
// numeric parameter holding the budget and TopologyParameter the select
// parameter naming the face shape; TrianglesPerFace maps each topology value to
// its factor, the triangles one face comes to: a positive number that need not
// be whole (Meshy Remesh quad 2, triangle 1; Rodin Gen-2.5 Quad 2.2, Raw 1).
//
// The factor of a generation Offering is measured, not documented upstream:
// the Catalog records the sample and its date beside the declaration, and a new
// sample changes the number in that one place. Every consumer converts with
// TrianglesForFaces and FacesWithinTriangles below, so the rounding is the
// same whichever of them reads it.
type OutputTriangleEstimate struct {
	CountParameter    string             `json:"count_parameter"`
	TopologyParameter string             `json:"topology_parameter"`
	TrianglesPerFace  map[string]float64 `json:"triangles_per_face"`
}

// Factor is the declared triangles per face of topology; false for a topology
// the estimate does not declare or a factor that is not a positive, finite
// number (the Catalog refuses such a declaration at load time, so this only
// guards a hand-built value).
func (e OutputTriangleEstimate) Factor(topology string) (float64, bool) {
	factor, declared := e.TrianglesPerFace[topology]
	return factor, declared && factor > 0 && !math.IsInf(factor, 0)
}

// LargestFactor is the largest declared factor, and at least one: the
// conservative reading for a face shape the request does not name.
func (e OutputTriangleEstimate) LargestFactor() float64 {
	largest := 1.0
	for topology := range e.TrianglesPerFace {
		if factor, usable := e.Factor(topology); usable && factor > largest {
			largest = factor
		}
	}
	return largest
}

// faceEstimateTolerance is the slack, in triangles or faces, that keeps the
// binary floating-point noise of a decimal factor from moving a whole boundary:
// 3200 faces at 2.2 are exactly 7040 triangles although the product is
// 7040.000000000001, and 6600 triangles hold exactly 3000 faces although the
// quotient is 2999.9999999999995. A real fraction of a triangle or a face is
// never this small for a factor written with a few decimals.
const faceEstimateTolerance = 1e-6

// TrianglesForFaces is the triangles faces faces deliver at factor triangles
// per face: a whole number, rounded up, so an estimate never falls short of
// what the request would deliver.
func TrianglesForFaces(faces, factor float64) float64 {
	triangles := math.Ceil(faces*factor - faceEstimateTolerance)
	if triangles == 0 {
		return 0 // not negative zero, which prints as "-0"
	}
	return triangles
}

// FacesWithinTriangles is the largest whole number of faces that deliver at
// most triangles at factor triangles per face: the face count a limit admits.
// It is the inverse of TrianglesForFaces: the result delivers at most
// triangles, and one more face delivers more.
func FacesWithinTriangles(triangles, factor float64) float64 {
	return math.Floor(triangles/factor + faceEstimateTolerance)
}

// EstimateOutputTriangles returns the triangles the Offering will deliver for
// input (rounded up, see TrianglesForFaces), taking a parameter the input omits
// from the Offering's declared default. ok is false when the budget or the
// topology is missing, not a number/known value.
func (o Offering) EstimateOutputTriangles(input json.RawMessage) (triangles int64, ok bool) {
	estimate := o.OutputTriangles
	if estimate == nil {
		return 0, false
	}
	var values map[string]json.RawMessage
	if len(input) > 0 && json.Unmarshal(input, &values) != nil {
		return 0, false
	}
	value := func(name string) json.RawMessage {
		if raw, present := values[name]; present {
			return raw
		}
		for _, parameter := range o.Parameters {
			if parameter.Name == name {
				return parameter.Default
			}
		}
		return nil
	}
	var count float64
	var topology string
	if json.Unmarshal(value(estimate.CountParameter), &count) != nil || count <= 0 ||
		json.Unmarshal(value(estimate.TopologyParameter), &topology) != nil {
		return 0, false
	}
	factor, known := estimate.Factor(topology)
	if !known {
		return 0, false
	}
	estimated := TrianglesForFaces(count, factor)
	if estimated >= math.MaxInt64 {
		// An absurd budget is over every limit, whatever the platform does with a
		// float that does not fit an int64.
		return math.MaxInt64, true
	}
	return int64(estimated), true
}

// CompatibleFaceBudgets lists, for every topology the estimate declares, the
// largest face budget whose delivered model still fits limit, in ascending
// order of budget ("quad 160000", "triangle 320000"). It is the compatibility
// advice the Schema's refusal text and the Agent's service description show.
func (e OutputTriangleEstimate) CompatibleFaceBudgets(limit SourceModelLimit) []TopologyBudget {
	budgets := make([]TopologyBudget, 0, len(e.TrianglesPerFace))
	for topology := range e.TrianglesPerFace {
		if factor, usable := e.Factor(topology); usable {
			budgets = append(budgets, TopologyBudget{Topology: topology, MaxFaces: int64(FacesWithinTriangles(float64(limit.MaxTriangles), factor))})
		}
	}
	sort.Slice(budgets, func(i, j int) bool {
		if budgets[i].MaxFaces != budgets[j].MaxFaces {
			return budgets[i].MaxFaces < budgets[j].MaxFaces
		}
		return budgets[i].Topology < budgets[j].Topology
	})
	return budgets
}

// TopologyBudget is one compatible parameter choice.
type TopologyBudget struct {
	Topology string
	MaxFaces int64
}

// String renders "topology <= faces" in a language-neutral form.
func (b TopologyBudget) String() string {
	return b.Topology + " ≤ " + strconv.FormatInt(b.MaxFaces, 10)
}

// FormatTopologyBudgets joins budgets as "quad ≤ 160000 / triangle ≤ 320000".
func FormatTopologyBudgets(budgets []TopologyBudget) string {
	parts := make([]string, len(budgets))
	for index, budget := range budgets {
		parts[index] = budget.String()
	}
	return strings.Join(parts, " / ")
}
