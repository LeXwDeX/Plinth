package generation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (m *Module) validateProviderInput(ctx context.Context, owner OwnerID, session string, offering Offering, input []byte, assets map[string][]InputAsset, dependencies map[string]TaskDependency) error {
	provider, ok := m.providers.Provider(offering)
	if !ok {
		return &Error{Code: ErrUnavailable, Operation: "validate_provider_input", Detail: "供给项适配器不可用"}
	}
	validator, ok := provider.(ProviderInputValidator)
	if !ok {
		return nil
	}
	if err := validator.ValidateInput(ctx, InputValidationRequest{
		OwnerID: owner, SessionID: session, Offering: offering, Input: input,
		InputAssets: assets, TaskDependencies: dependencies,
	}); err != nil {
		return inputValidationError(err)
	}
	return nil
}

func inputValidationError(err error) error {
	code := ErrUnavailable
	var domain *schema.Error
	if errors.As(err, &domain) {
		switch domain.Code {
		case ErrInvalidInput, ErrInvalidModelInput, ErrUnsupportedModelInput, ErrModelInputLimit, ErrConflict, ErrNotFound, ErrForbidden, ErrUnavailable, ErrInternal:
			code = domain.Code
		}
	}
	var provider *ProviderError
	if errors.As(err, &provider) && provider.Failure != nil && provider.Failure.Class == FailureInvalidInput {
		code = ErrInvalidInput
	}
	// Keep the source's own reason. A UserFacing Schema refusal is found in
	// Cause by the Agent tool; an unmarked Provider diagnostic stays visible
	// instead of being replaced by a generic sentence that hides it.
	return &Error{Code: code, Operation: "validate_provider_input", Detail: err.Error(), Cause: err}
}

// ValidateOfferingInput performs only a read-only provider input check; it does
// not freeze inputs or create a Task, grant or processing plan.
func (m *Module) ValidateOfferingInput(ctx context.Context, owner OwnerID, offeringID OfferingID, revision string, input json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner == "" || offeringID == "" || revision == "" {
		return &Error{Code: ErrInvalidInput, Operation: "validate_offering_input", Detail: "来源范围或供给项身份无效"}
	}
	offering, ok := m.catalog.Get(offeringID)
	if !ok {
		return &Error{Code: ErrUnknownOffering, Operation: "validate_offering_input", Detail: "供给项不存在"}
	}
	if offering.Revision != revision {
		return &Error{Code: ErrConflict, Operation: "validate_offering_input", Detail: "供给项修订已变化"}
	}
	provider, ok := m.providers.Provider(offering)
	if !ok {
		return &Error{Code: ErrUnavailable, Operation: "validate_offering_input", Detail: "供给项适配器不可用"}
	}
	if _, validates := provider.(ProviderInputValidator); !validates {
		return nil
	}
	normalizedOffering, normalized, err := m.catalog.Normalize(offering.Capability, offering.ID, input)
	if err != nil {
		return err
	}
	if normalizedOffering.ID != offering.ID || normalizedOffering.Revision != revision {
		return &Error{Code: ErrConflict, Operation: "validate_offering_input", Detail: "供给项修订已变化"}
	}
	if err := rejectUnfrozenSourceURLs(normalizedOffering, normalized); err != nil {
		return err
	}
	// Check the input as a submission would send it: an image over a byte
	// limit is checked as its re-encoded copy (#276).
	normalizedOffering, normalized, err = m.fitInputs(ctx, owner, offering.Capability, normalizedOffering, normalized)
	if err != nil {
		return err
	}
	assets, err := m.inputs.Resolve(ctx, owner, normalizedOffering, normalized)
	if err != nil {
		return err
	}
	dependencies, err := m.resolveTaskDependencies(ctx, owner, normalizedOffering, normalized, "")
	if err != nil {
		return err
	}
	return m.validateProviderInput(ctx, owner, "", normalizedOffering, normalized, assets, dependencies)
}
