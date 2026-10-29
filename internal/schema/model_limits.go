package schema

import (
	"encoding/json"
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
// (#121). CountParameter is the numeric parameter holding the budget and
// TopologyParameter the select parameter naming the face shape;
// TrianglesPerFace maps each topology value to its factor (quad 2, triangle 1).
type OutputTriangleEstimate struct {
	CountParameter    string           `json:"count_parameter"`
	TopologyParameter string           `json:"topology_parameter"`
	TrianglesPerFace  map[string]int64 `json:"triangles_per_face"`
}

// EstimateOutputTriangles returns the triangles the Offering will deliver for input, taking a
// parameter the input omits from the Offering's declared default. ok is false
// when the budget or the topology is missing, not a number/known value.
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
	factor, known := estimate.TrianglesPerFace[topology]
	if !known || factor <= 0 {
		return 0, false
	}
	return int64(count) * factor, true
}

// CompatibleFaceBudgets lists, for every topology the estimate declares, the
// largest face budget whose delivered model still fits limit, in ascending
// order of budget ("quad 160000", "triangle 320000"). It is the compatibility
// advice the Schema's refusal text and the Agent's service description show.
func (e OutputTriangleEstimate) CompatibleFaceBudgets(limit SourceModelLimit) []TopologyBudget {
	budgets := make([]TopologyBudget, 0, len(e.TrianglesPerFace))
	for topology, factor := range e.TrianglesPerFace {
		if factor > 0 {
			budgets = append(budgets, TopologyBudget{Topology: topology, MaxFaces: limit.MaxTriangles / factor})
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
