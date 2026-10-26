package resolve

import (
	"context"
	"errors"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (s Service) source(ctx context.Context, selected harness.Selection, offering generation.Offering, strict bool) (*generation.Task, bool, error) {
	if len(selected.Sources) == 0 {
		return nil, false, nil
	}
	if len(selected.Sources) != 1 {
		return nil, false, invalid("exactly one model source is supported")
	}
	source := selected.Sources[0]
	if source.TaskID == "" {
		return nil, false, invalid("asset-only sources require a source Task")
	}
	parameterValid := false
	for _, parameter := range offering.Parameters {
		role, ok := generationSourceRole(parameter)
		if parameter.Name == source.Parameter && ok && (role == schema.SemanticSourceTask || role == schema.SemanticSourceAsset) {
			parameterValid = true
		}
	}
	if !parameterValid {
		return nil, false, invalid("source parameter is not declared for this offering")
	}
	if s.Gateway == nil {
		return nil, false, ErrUnavailable
	}
	task, err := s.Gateway.Get(ctx, generation.OwnerID(selected.OwnerID), generation.TaskID(source.TaskID))
	if err != nil {
		var domain *generation.Error
		if errors.Is(err, generation.RepositoryNotFound) || (errors.As(err, &domain) && domain.Code == generation.ErrNotFound) {
			if strict {
				return nil, false, harness.ErrNotFound
			}
			return nil, false, nil
		}
		return nil, false, err
	}
	if task.ID != generation.TaskID(source.TaskID) || task.OwnerID != generation.OwnerID(selected.OwnerID) || task.SessionID != selected.SessionID {
		if strict {
			return nil, false, harness.ErrNotFound
		}
		return nil, false, nil
	}
	if source.AssetID != "" {
		found := false
		for _, asset := range task.Assets {
			if asset.ID == generation.AssetID(source.AssetID) && asset.Role == "model" && (asset.TaskID == "" || asset.TaskID == task.ID) {
				found = true
				break
			}
		}
		if !found {
			if strict {
				return nil, false, invalid("selected model asset does not belong to source Task")
			}
			return nil, false, nil
		}
	}
	return &task, task.State == generation.TaskSucceeded, nil
}

func sourcePreference(tool *catalog.ChatToolSchema) []string {
	if tool != nil && tool.Processing != nil {
		return tool.Processing.SourceVariantPreference
	}
	return nil
}

func fingerprintAsset(task generation.Task, source harness.Source, binding generation.ModelSourceBinding, prefer []string) (generation.Asset, bool) {
	chosen := generation.AssetID(source.AssetID)
	if binding.AssetID != "" {
		if chosen != "" && chosen != binding.AssetID {
			return generation.Asset{}, false
		}
		chosen = binding.AssetID
	} else if binding.Role == schema.SemanticSourceAsset {
		chosen = generation.AssetID(binding.Value)
	}
	assets := []generation.Asset{}
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.StorageKey != "" && (asset.TaskID == "" || asset.TaskID == task.ID) && (chosen == "" || asset.ID == chosen) {
			assets = append(assets, asset)
		}
	}
	// The main model first, as the panel and the tools bind it (#326).
	slices.SortStableFunc(assets, generation.CompareModelAssets(prefer))
	if len(assets) == 0 {
		return generation.Asset{}, false
	}
	return assets[0], true
}

func (s Service) availablePaths(goal catalog.HarnessOperationSchema, selected harness.Selection, source generation.Task, chosen generation.AssetID, prefer []string) [][]string {
	paths := [][]string{}
	if s.Catalog == nil || s.Gateway == nil {
		return nil
	}
	planWired := false
	for _, tool := range s.Tools {
		if tool.Name == s.Schema.PrerequisiteTool && tool.Processing != nil && tool.Processing.Plan != nil && slices.Contains(tool.Execution.Capabilities, goal.Capability) {
			planWired = true
			break
		}
	}
	if !planWired {
		return nil
	}
	for _, path := range goal.Prerequisites {
		if len(path) == 0 {
			continue
		}
		valid := true
		for _, id := range path {
			operation, known := s.operation(id)
			if !known || operation.ToolName == "" || !slices.Contains(selected.EnabledOperations, id) {
				valid = false
				break
			}
			wired := false
			for _, tool := range s.Tools {
				if tool.Name == operation.ToolName && tool.Processing != nil && tool.Processing.Plan == nil && slices.Contains(tool.Execution.Capabilities, operation.Capability) {
					wired = true
					break
				}
			}
			if !wired {
				valid = false
				break
			}
			available := false
			for _, offering := range s.Catalog.List(operation.Capability) {
				if offering.Enabled && (len(operation.ModelIDs) == 0 || slices.Contains(operation.ModelIDs, offering.ModelID)) && s.Gateway.OfferingAvailable(offering.ID) {
					available = true
					break
				}
			}
			if !available {
				valid = false
				break
			}
		}
		if valid && s.compatiblePath(append(append([]string(nil), path...), selected.OperationID), selected, source, chosen, prefer) != nil {
			paths = append(paths, append([]string(nil), path...))
		}
	}
	return paths
}

// Check the complete declared chain, including the caller's exact goal. An
// available capability alone does not prove that its Provider accepts this
// source or that its outputs can feed the next Offering. This only predicts
// contracts; real preparation still binds saved output bytes independently.
func (s Service) compatiblePath(ids []string, selected harness.Selection, source generation.Task, chosen generation.AssetID, prefer []string) []generation.Offering {
	var visit func(int, generation.Offering) []generation.Offering
	visit = func(index int, previous generation.Offering) []generation.Offering {
		if index == len(ids) {
			return []generation.Offering{}
		}
		operation, known := s.operation(ids[index])
		if !known {
			return nil
		}
		for _, candidate := range s.Catalog.List(operation.Capability) {
			if !candidate.Enabled || !s.Gateway.OfferingAvailable(candidate.ID) || (len(operation.ModelIDs) > 0 && !slices.Contains(operation.ModelIDs, candidate.ModelID)) {
				continue
			}
			if index == len(ids)-1 && (string(candidate.ID) != selected.OfferingID || candidate.Revision != selected.OfferingRevision) {
				continue
			}
			var parameter, value string
			if index == 0 {
				binding, err := generation.BindModelSource(candidate, source, chosen, prefer)
				if err != nil {
					continue
				}
				parameter, value = binding.Parameter, binding.Value
			} else {
				contract, err := generation.BindProjectedModelSource(candidate, previous)
				if err != nil {
					continue
				}
				parameter, value = contract.Parameter, "dependency-projection"
			}
			if _, _, valid := s.proposalInput(candidate, selected, index == len(ids)-1, parameter, value); !valid {
				continue
			}
			if remaining := visit(index+1, candidate); remaining != nil {
				return append([]generation.Offering{candidate}, remaining...)
			}
		}
		return nil
	}
	return visit(0, generation.Offering{})
}

// sourceOffering keeps the caller's explicit route. The generic binder's
// provider preference must not replace source_asset with source_task.
func sourceOffering(offering generation.Offering, name string) generation.Offering {
	narrowed := offering
	narrowed.Parameters = make([]generation.Parameter, 0, len(offering.Parameters))
	for _, p := range offering.Parameters {
		role, source := generationSourceRole(p)
		if source && (role == schema.SemanticSourceAsset || role == schema.SemanticSourceTask) && p.Name != name {
			continue
		}
		narrowed.Parameters = append(narrowed.Parameters, p)
	}
	return narrowed
}
