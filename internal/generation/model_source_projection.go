package generation

import (
	"github.com/LeXwDeX/Plinth/internal/schema"
	"strings"
)

// ModelSourceContract identifies a compatible parameter from declarations only.
// It carries no Task, Asset, byte proof or execution authorization.
type ModelSourceContract struct {
	Parameter string
	Role      schema.ParameterSemantic
}

// BindProjectedModelSource checks a predicted step against its source Offering.
// Real source ownership and bytes must still be resolved before preparation.
func BindProjectedModelSource(target Offering, source Offering) (ModelSourceContract, error) {
	for _, parameter := range target.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		if !isSource || role != schema.SemanticSourceTask || parameter.Type != "task" || !taskCapabilityAllowed(parameter.TaskCapabilities, source.Capability) || !taskOfferingAllowed(parameter.TaskOfferings, source.ID) {
			continue
		}
		switch parameter.TaskSourceKind {
		case schema.TaskSourceAssets:
			if source.ID == "" || (parameter.SameProvider && source.ProviderID != target.ProviderID) || !projectedTaskAssetCompatible(parameter, source.Outputs) {
				continue
			}
		case "", schema.TaskSourceUpstreamJob:
			if source.ProviderID != target.ProviderID {
				continue
			}
		default:
			continue
		}
		return ModelSourceContract{Parameter: parameter.Name, Role: role}, nil
	}
	for _, parameter := range target.Parameters {
		role, isSource := schema.SourceParameterRole(parameter)
		if !isSource || role != schema.SemanticSourceAsset || parameter.Type != "asset" || (parameter.UploadedModel != nil && parameter.UploadedModel.NativeOnly) {
			continue
		}
		// Asset selection can choose one output; this is compatibility prediction,
		// not a guarantee of structural validity, actual size or materialization.
		for _, output := range source.Outputs {
			if output.Role != "model" {
				continue
			}
			if len(parameter.MediaTypes) == 0 {
				return ModelSourceContract{Parameter: parameter.Name, Role: role}, nil
			}
			for _, mediaType := range output.MediaTypes {
				if mediaTypeAllowed(parameter.MediaTypes, mediaType) {
					return ModelSourceContract{Parameter: parameter.Name, Role: role}, nil
				}
			}
		}
	}
	return ModelSourceContract{}, incompatibleSource("来源供给项的输出契约不兼容")
}

// Task-ID-only assets inputs must independently select one exact saved model.
// A predicted source therefore needs one guaranteed static model declaration.
func projectedTaskAssetCompatible(parameter Parameter, outputs []AssetRequirement) bool {
	if parameter.MaxBytes <= 0 || len(parameter.MediaTypes) == 0 || len(parameter.Extensions) == 0 {
		return false
	}
	var model *AssetRequirement
	for index := range outputs {
		if outputs[index].Role != "model" {
			continue
		}
		if model != nil {
			return false
		}
		model = &outputs[index]
	}
	if model == nil || (model.Delivery != "" && model.Delivery != schema.AssetDeliveryRequired) || (model.Minimum != nil && *model.Minimum != 1) || model.Variant == "" || model.VariantFrom != "" || model.VariantPrefix != "" || model.MaxBytes <= 0 || model.MaxBytes > parameter.MaxBytes || len(model.MediaTypes) == 0 {
		return false
	}
	extensionAllowed := false
	for _, extension := range parameter.Extensions {
		if strings.EqualFold(strings.TrimPrefix(extension, "."), model.Variant) {
			extensionAllowed = true
			break
		}
	}
	if !extensionAllowed {
		return false
	}
	for _, mediaType := range model.MediaTypes {
		exact := false
		for _, allowed := range parameter.MediaTypes {
			if mediaType != "" && mediaType == allowed {
				exact = true
				break
			}
		}
		if !exact {
			return false
		}
	}
	return true
}
