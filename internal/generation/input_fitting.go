package generation

import (
	"context"
	"encoding/json"
	"reflect"
)

// fitInputs lets the InputFitter replace input images that exceed the
// Offering's declared limits with re-encoded copies of the same width and
// height, then normalizes again so the frozen input stays canonical (#276).
// It runs before inputs are resolved, priced or frozen; the resolved assets
// are then checked against every declared limit as before.
func (m *Module) fitInputs(
	ctx context.Context,
	owner OwnerID,
	capability Capability,
	offering Offering,
	normalized json.RawMessage,
) (Offering, json.RawMessage, error) {
	if m.inputFitter == nil {
		return offering, normalized, nil
	}
	fitted, err := m.inputFitter.Fit(ctx, owner, offering, normalized)
	if err != nil {
		return Offering{}, nil, err
	}
	if reflect.DeepEqual(fitted, normalized) {
		return offering, normalized, nil
	}
	refitted, renormalized, err := m.catalog.Normalize(capability, offering.ID, fitted)
	if err != nil {
		return Offering{}, nil, err
	}
	if refitted.ID != offering.ID || refitted.Revision != offering.Revision {
		return Offering{}, nil, &Error{Code: ErrConflict, Operation: "fit_input_assets", Detail: "供给项修订已变化"}
	}
	return refitted, renormalized, nil
}
