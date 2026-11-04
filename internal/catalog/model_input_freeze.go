package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// freezeOfferingModelProfiles is the only authoring boundary for parameter
// snapshots. Every protocol is checked before the Catalog adds derived values.
func freezeOfferingModelProfiles(offering *schema.Offering, application ApplicationSchema) error {
	profile := application.Runtime.InputAssets.ModelProfile
	for index := range offering.Parameters {
		parameter := &offering.Parameters[index]
		constraint := parameter.UploadedModel
		if constraint == nil {
			continue
		}
		if constraint.ProfileRevision != "" || constraint.ProfileLimits != nil {
			return fmt.Errorf("uploaded_model profile snapshots are supplied by Catalog")
		}
		if profile == nil || constraint.ProfileID != profile.ID {
			return fmt.Errorf("uploaded_model references an undeclared profile")
		}
		limits := profile.Limits
		constraint.ProfileRevision = profile.Revision
		constraint.ProfileLimits = &limits
	}
	var options map[string]json.RawMessage
	if len(offering.ProviderOptions) == 0 {
		return nil
	}
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		return fmt.Errorf("invalid Provider options: %w", err)
	}
	raw, present := options["model_input_profile"]
	if !present {
		return nil
	}
	if ProtocolByAdapterKey(application.Runtime.ProviderConnections)[offering.AdapterKey] != "meshy" {
		return fmt.Errorf("model_input_profile requires the Meshy protocol")
	}
	var binding schema.ModelInputProfileOptions
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil {
		return fmt.Errorf("model_input_profile: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("model_input_profile must be one object")
	}
	if binding.ProfileLimits != nil {
		return fmt.Errorf("model_input_profile cannot override model_profile limits")
	}
	if profile == nil || binding.ProfileID != profile.ID || binding.ProfileRevision != profile.Revision || binding.MaxInstancedTriangles < 0 {
		return fmt.Errorf("model_input_profile references an invalid profile or limit")
	}
	var source *schema.Parameter
	for index := range offering.Parameters {
		parameter := &offering.Parameters[index]
		if parameter.Name != binding.Parameter {
			continue
		}
		role, declared := schema.SourceParameterRole(*parameter)
		// Direct uploads stay within the profile (the upload contract); the
		// parameter's byte limit is the inline ceiling for a successful
		// platform Task's GLB (#93), never below the profile.
		if source != nil || !declared || role != schema.SemanticSourceAsset || parameter.Type != "asset" || parameter.UploadedModel == nil || parameter.UploadedModel.ProfileID != binding.ProfileID || parameter.MaxBytes < profile.Limits.MaxBytes || parameter.MaxBytes != inlineModelInputMaxBytes(options) {
			return fmt.Errorf("model_input_profile requires one exact uploaded model source")
		}
		source = parameter
	}
	if source == nil {
		return fmt.Errorf("model_input_profile source parameter is not declared")
	}
	limits := profile.Limits
	binding.ProfileLimits = &limits
	encoded, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	options["model_input_profile"] = encoded
	offering.ProviderOptions, err = json.Marshal(options)
	return err
}

func cloneParametersWithModelProfiles(source []schema.Parameter) []schema.Parameter {
	result := append([]schema.Parameter(nil), source...)
	for index := range result {
		if source[index].ImageInput != nil {
			constraint := *source[index].ImageInput
			result[index].ImageInput = &constraint
		}
		if source[index].AlphaFlatten != nil {
			policy := *source[index].AlphaFlatten
			policy.KeepWhen = append([]schema.ParameterCondition(nil), policy.KeepWhen...)
			result[index].AlphaFlatten = &policy
		}
		if source[index].UploadedModel == nil {
			continue
		}
		constraint := *source[index].UploadedModel
		if constraint.ProfileLimits != nil {
			limits := *constraint.ProfileLimits
			constraint.ProfileLimits = &limits
		}
		result[index].UploadedModel = &constraint
	}
	return result
}

func cloneOfferingModelProfiles(source schema.Offering) schema.Offering {
	if source.NativePricing != nil {
		raw, _ := json.Marshal(source.NativePricing)
		source.NativePricing = &schema.NativePricing{}
		_ = json.Unmarshal(raw, source.NativePricing)
	}
	source.Parameters = cloneParametersWithModelProfiles(source.Parameters)
	source.Outputs = append([]schema.AssetRequirement(nil), source.Outputs...)
	for i := range source.Outputs {
		output := &source.Outputs[i]
		if output.Minimum != nil {
			minimum := *output.Minimum
			output.Minimum = &minimum
		}
		if output.RequiredWhen != nil {
			condition := *output.RequiredWhen
			condition.Value = append(json.RawMessage(nil), condition.Value...)
			output.RequiredWhen = &condition
		}
		output.MediaTypes = append([]string(nil), output.MediaTypes...)
	}
	source.ProviderOptions = append(json.RawMessage(nil), source.ProviderOptions...)
	source.SourceModelLimit = cloneSourceModelLimit(source.SourceModelLimit)
	source.OutputTriangles = cloneOutputTriangles(source.OutputTriangles)
	// 调用方拿到的 Offering 不得与共享的 Catalog 共用任何底层数据。
	return deepCopy(source)
}

func cloneSourceModelLimit(source *schema.SourceModelLimit) *schema.SourceModelLimit {
	if source == nil {
		return nil
	}
	copied := *source
	return &copied
}

func cloneOutputTriangles(source *schema.OutputTriangleEstimate) *schema.OutputTriangleEstimate {
	if source == nil {
		return nil
	}
	copied := *source
	copied.TrianglesPerFace = make(map[string]int64, len(source.TrianglesPerFace))
	for topology, factor := range source.TrianglesPerFace {
		copied.TrianglesPerFace[topology] = factor
	}
	return &copied
}

// inlineModelInputMaxBytes is the declared inline_model_input.max_bytes, or
// zero when it is missing or malformed.
func inlineModelInputMaxBytes(options map[string]json.RawMessage) int64 {
	var inline struct {
		MaxBytes int64 `json:"max_bytes"`
	}
	if raw, present := options["inline_model_input"]; !present || json.Unmarshal(raw, &inline) != nil {
		return 0
	}
	return inline.MaxBytes
}
