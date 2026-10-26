package resolve

import (
	"context"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"reflect"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

type Proposal struct {
	PriceTotals      []schema.NativeAmount `json:"price_totals,omitempty"`
	Goal             harness.Selection     `json:"goal"`
	Steps            []ProposalStep        `json:"steps"`
	EstimatedCredits int                   `json:"estimated_credits"`
	Executed         bool                  `json:"executed"`
	NeedsPreparation bool                  `json:"needs_preparation"`
}
type ProposalStep struct {
	NativeQuote       *schema.NativeQuote `json:"native_quote,omitempty"`
	OperationID       string              `json:"operation_id"`
	OfferingID        string              `json:"offering_id"`
	OfferingRevision  string              `json:"offering_revision"`
	Label             string              `json:"label"`
	SourceStep        int                 `json:"source_step"`
	EstimatedCredits  int                 `json:"estimated_credits"`
	MissingParameters []string            `json:"missing_parameters"`
}

// Propose describes a possible chain. No task, plan, prepared input or grant is
// created. Predicted outputs are used only to check the declared dependency
// contract; every real step still requires fresh preparation and authorization.
func (s Service) Propose(ctx context.Context, snapshot Snapshot, path []string) (Proposal, error) {
	selected := snapshot.Selection
	current, err := s.Selection(ctx, selected.OwnerID, selected.SessionID)
	if err != nil {
		return Proposal{}, err
	}
	if !harness.SameBinding(current.Binding, selected.Binding) {
		return Proposal{}, harness.ErrStale
	}
	normalized, err := harness.NormalizeSelection(selected)
	if err != nil || normalized.Digest != selected.Digest {
		return Proposal{}, harness.ErrInvalid
	}
	if !snapshot.CanPropose || !selected.AllowPrerequisites || len(path) == 0 || !containsPath(snapshot.PrerequisitePaths, path) {
		return Proposal{}, harness.ErrInvalid
	}
	goal, tool, offering, err := s.declarations(selected)
	if err != nil {
		return Proposal{}, err
	}
	source, _, err := s.source(ctx, selected, offering, true)
	if err != nil {
		return Proposal{}, err
	}
	if source == nil || source.State != generation.TaskSucceeded || len(snapshot.SourceFingerprints) != 1 {
		return Proposal{}, harness.ErrInvalid
	}
	frozen := snapshot.SourceFingerprints[0]
	frozenSource := selected.Sources[0]
	if frozenSource.AssetID == "" {
		frozenSource.AssetID = string(frozen.AssetID)
	}
	asset, ok := fingerprintAsset(*source, frozenSource, generation.ModelSourceBinding{}, sourcePreference(tool))
	if !ok || frozen.TaskID != source.ID || frozen.AssetID != asset.ID || frozen.SHA256 != asset.SHA256 || frozen.Size != asset.Size || frozen.Variant != asset.Variant {
		return Proposal{}, harness.ErrStale
	}
	if !containsPath(s.availablePaths(goal, selected, *source, asset.ID, sourcePreference(tool)), path) {
		return Proposal{}, harness.ErrInvalid
	}
	proposal := Proposal{Goal: selected, Steps: []ProposalStep{}, NeedsPreparation: true}
	ids := append(append([]string(nil), path...), selected.OperationID)
	candidates := s.compatiblePath(ids, selected, *source, asset.ID, sourcePreference(tool))
	if len(candidates) != len(ids) {
		return Proposal{}, harness.ErrInvalid
	}
	var prices schema.PriceAccumulator
	var previous generation.Offering
	for index, id := range ids {
		operation, known := s.operation(id)
		if !known || !slices.Contains(selected.EnabledOperations, id) {
			return Proposal{}, harness.ErrInvalid
		}
		candidate := candidates[index]
		// Only the first step has real saved bytes. Later steps are checked
		// against output declarations; their sources are bound at preparation.
		var parameter, value string
		if index == 0 {
			binding, bindErr := generation.BindModelSource(candidate, *source, asset.ID, sourcePreference(tool))
			if bindErr != nil {
				return Proposal{}, harness.ErrInvalid
			}
			parameter, value = binding.Parameter, binding.Value
		} else {
			contract, bindErr := generation.BindProjectedModelSource(candidate, previous)
			if bindErr != nil {
				return Proposal{}, harness.ErrInvalid
			}
			// Normalize checks required parameters, but this placeholder never
			// leaves the local input map or represents an asset or Task.
			parameter, value = contract.Parameter, "dependency-projection"
		}
		encoded, missing, valid := s.proposalInput(candidate, selected, index == len(ids)-1, parameter, value)
		if !valid {
			return Proposal{}, harness.ErrInvalid
		}
		step := ProposalStep{OperationID: id, OfferingID: string(candidate.ID), OfferingRevision: candidate.Revision, Label: operation.Label, SourceStep: index - 1, EstimatedCredits: 0, MissingParameters: missing}
		if candidate.NativePricing != nil {
			step.NativeQuote, err = candidate.DefaultNativeQuote(encoded)
			if err != nil {
				return Proposal{}, harness.ErrInvalid
			}
		} else {
			step.EstimatedCredits = candidate.EstimateTotalCredits(encoded)
		}
		if err := prices.Add(step.EstimatedCredits, step.NativeQuote); err != nil {
			return Proposal{}, harness.ErrInvalid
		}
		proposal.Steps = append(proposal.Steps, step)
		proposal.EstimatedCredits = prices.Legacy
		previous = candidate
	}
	proposal.PriceTotals, err = prices.Totals()
	if err != nil {
		return Proposal{}, harness.ErrInvalid
	}
	return proposal, nil
}

// Preserve proposal admission: missing required values can be described for
// preparation, but a complete input rejected by Normalize cannot be selected.
// Candidate search and the final proposal use the same read-only qualification.
func (s Service) proposalInput(candidate generation.Offering, selected harness.Selection, final bool, parameter, value string) (json.RawMessage, []string, bool) {
	input := map[string]json.RawMessage{}
	if final {
		input = cloneValues(selected.FixedParameters)
	}
	input[parameter], _ = json.Marshal(value)
	missing := []string{}
	values := conditionValues(candidate, input)
	for _, parameter := range candidate.Parameters {
		if parameter.Required && !generation.ParameterOmitted(parameter, values) {
			if v, ok := values[parameter.Name]; !ok || v == nil || v == "" {
				missing = append(missing, parameter.Name)
			}
		}
	}
	encoded, _ := json.Marshal(input)
	if len(missing) == 0 {
		_, normalized, err := s.Catalog.Normalize(candidate.Capability, candidate.ID, encoded)
		if err != nil {
			return nil, nil, false
		}
		encoded = normalized
	}
	return encoded, missing, true
}

func containsPath(paths [][]string, path []string) bool {
	for _, candidate := range paths {
		if reflect.DeepEqual(candidate, path) {
			return true
		}
	}
	return false
}
