package catalog

import (
	"encoding/json"
	"math"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// Purpose budget limits (#129): model3d_usage.budget_limits says which
// parameter semantics a purpose's budget bounds. A step's value of such a
// parameter is measured in the bound's unit (triangles, texture edge pixels)
// and compared with the budget of the session's purpose. The measuring and
// the fitting live here once; the Catalog check of the purposes and the chat
// tools both use them.

// Budget fields a limit may name.
const (
	BudgetBoundMaxTriangles   = "max_triangles"
	BudgetBoundMaxTextureSize = "max_texture_size"
)

// BudgetLimit returns the limit declared for a parameter semantic.
func (usage ChatModel3DUsageSchema) BudgetLimit(semantic schemacontract.ParameterSemantic) (ChatModel3DUsageBudgetLimit, bool) {
	if semantic == "" {
		return ChatModel3DUsageBudgetLimit{}, false
	}
	for _, limit := range usage.BudgetLimits {
		if limit.Semantic == semantic {
			return limit, true
		}
	}
	return ChatModel3DUsageBudgetLimit{}, false
}

// Maximum is the budget's bound for the limit; false when the budget declares
// none (a purpose without max_texture_size does not bound textures).
func (limit ChatModel3DUsageBudgetLimit) Maximum(budget ChatModel3DUsageBudget) (float64, bool) {
	switch limit.Bound {
	case BudgetBoundMaxTriangles:
		return float64(budget.MaxTriangles), budget.MaxTriangles > 0
	case BudgetBoundMaxTextureSize:
		if budget.MaxTextureSize == nil {
			return 0, false
		}
		return float64(*budget.MaxTextureSize), true
	}
	return 0, false
}

// Measure is the amount a value of parameter stands for in the bound's unit:
// a number times the triangles per face the Offering's output estimate
// declares for the step's topology, or the declared amount of a select
// option. values are the step's other parameter values; an absent one reads
// the Offering default.
func (limit ChatModel3DUsageBudgetLimit) Measure(
	offering schemacontract.Offering,
	parameter schemacontract.Parameter,
	value json.RawMessage,
	values map[string]json.RawMessage,
) (float64, bool) {
	switch parameter.Type {
	case "number":
		var number float64
		if json.Unmarshal(value, &number) != nil {
			return 0, false
		}
		return number * float64(limit.trianglesPerFace(offering, parameter, values)), true
	case "select":
		var option string
		if json.Unmarshal(value, &option) != nil {
			return 0, false
		}
		amount, declared := limit.OptionValues[option]
		return amount, declared
	}
	return 0, false
}

// Fit is the value a bounded parameter nobody chose takes under maximum.
// baseline is the value it would otherwise have (the purpose's override, the
// tool's or the Offering's default). A parameter without one is left unset:
// the upstream then decides it adaptively (Tripo P2's low-poly face count,
// Tripo decimation), which is the Offering's own design. ok is false for such
// a parameter, for a baseline that already fits and for one that cannot be
// measured. Otherwise the result is the largest legal value within maximum, or
// the smallest legal value when none is; the step's note then reports it.
func (limit ChatModel3DUsageBudgetLimit) Fit(
	offering schemacontract.Offering,
	parameter schemacontract.Parameter,
	baseline json.RawMessage,
	values map[string]json.RawMessage,
	maximum float64,
) (json.RawMessage, bool) {
	if len(baseline) == 0 {
		return nil, false
	}
	current, measured := limit.Measure(offering, parameter, baseline, values)
	if !measured || current <= maximum {
		return nil, false
	}
	switch parameter.Type {
	case "number":
		factor := float64(limit.trianglesPerFace(offering, parameter, values))
		target := math.Floor(maximum / factor)
		if parameter.Maximum != nil && target > *parameter.Maximum {
			target = *parameter.Maximum
		}
		if parameter.Minimum != nil && target < *parameter.Minimum {
			target = *parameter.Minimum
		}
		encoded, err := json.Marshal(target)
		return encoded, err == nil
	case "select":
		best, smallest := "", ""
		bestAmount, smallestAmount := math.Inf(-1), math.Inf(1)
		for _, option := range parameter.Options {
			amount, declared := limit.OptionValues[option.Value]
			if !declared {
				continue
			}
			if amount <= maximum && amount > bestAmount {
				best, bestAmount = option.Value, amount
			}
			if amount < smallestAmount {
				smallest, smallestAmount = option.Value, amount
			}
		}
		if bestAmount == math.Inf(-1) {
			if smallestAmount == math.Inf(1) {
				return nil, false
			}
			best = smallest
		}
		encoded, err := json.Marshal(best)
		return encoded, err == nil
	}
	return nil, false
}

// trianglesPerFace is how many triangles one unit of a face-count parameter
// delivers: the factor the Offering's output_triangles estimate declares for
// the step's topology (a quad is two triangles), or one. An unknown topology
// takes the largest declared factor, the conservative reading.
func (limit ChatModel3DUsageBudgetLimit) trianglesPerFace(
	offering schemacontract.Offering,
	parameter schemacontract.Parameter,
	values map[string]json.RawMessage,
) int64 {
	estimate := offering.OutputTriangles
	if limit.Bound != BudgetBoundMaxTriangles || estimate == nil || estimate.CountParameter != parameter.Name {
		return 1
	}
	raw, present := values[estimate.TopologyParameter]
	if !present {
		for _, candidate := range offering.Parameters {
			if candidate.Name == estimate.TopologyParameter {
				raw = candidate.Default
			}
		}
	}
	var topology string
	if json.Unmarshal(raw, &topology) == nil {
		if factor := estimate.TrianglesPerFace[topology]; factor > 0 {
			return factor
		}
	}
	largest := int64(1)
	for _, factor := range estimate.TrianglesPerFace {
		if factor > largest {
			largest = factor
		}
	}
	return largest
}
