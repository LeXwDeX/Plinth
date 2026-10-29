package processing

import (
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// chainTriangles follows the triangle count a Processing Plan's model has as
// the steps run (#121). Only the Schema says how a step changes it: an Offering
// that declares output_triangles (Remesh) resets it to the estimate its request
// gives; every other step keeps the model's geometry. When no step so far
// declared an estimate the count is unknown, and nothing is refused here: the
// Provider's own pre-submission check (#103) stays the fallback for a source
// it can measure.
type chainTriangles struct {
	known     bool
	triangles int64
	producer  generation.Offering
}

// after returns the chain once offering has run with input.
func (c chainTriangles) after(offering generation.Offering, input json.RawMessage) chainTriangles {
	if offering.OutputTriangles == nil {
		return c
	}
	triangles, ok := offering.EstimateOutputTriangles(input)
	if !ok {
		return chainTriangles{}
	}
	return chainTriangles{known: true, triangles: triangles, producer: offering}
}

// check refuses offering when the model it would receive is estimated over the
// source-model limit it declares. The refusal is a UserFacing Schema error
// (execution message plan_source_face_limit) that still matches ErrInvalid, so
// existing callers keep their handling and richer ones show the reason.
func (c chainTriangles) check(offering generation.Offering) error {
	limit := offering.SourceModelLimit
	if limit == nil || !c.known || c.triangles <= limit.MaxTriangles {
		return nil
	}
	choices := schema.FormatTopologyBudgets(c.producer.OutputTriangles.CompatibleFaceBudgets(*limit))
	refusal := offering.ExecutionMessages.PlanSourceFaceLimitRefusal(
		offeringServiceName(c.producer), offeringServiceName(offering), c.triangles, limit.MaxTriangles, choices)
	if refusal == nil {
		return ErrInvalid
	}
	refusal.Cause = ErrInvalid
	return refusal
}

func offeringServiceName(offering generation.Offering) string {
	if offering.ModelName != "" {
		return offering.ModelName
	}
	return offering.ModelID
}
