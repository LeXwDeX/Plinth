// Package localparts implements the free local model split and merge
// capabilities. It reads only Task model outputs of the same owner and chat
// session, rewrites a bounded static-GLB subset and stores each produced GLB as
// a public Task asset. It never contacts an upstream service.
package localparts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const (
	modelMIME            = "model/gltf-binary"
	manifestRole         = "parts_manifest"
	manifestVariant      = "json"
	manifestMIME         = "application/json"
	maxSplitParts        = 64
	maxPartsConcurrency  = 8
	maxPiecesPerDocument = 100000
	maxManifestBytes     = 16 << 20
)

type contract struct {
	options        schema.ModelPartsOptions
	policy         modelinput.Policy
	capability     generation.Capability
	source         generation.Parameter
	mode           generation.Parameter
	minRatio       generation.Parameter
	maxParts       generation.Parameter
	name           generation.Parameter
	modelOutput    generation.AssetRequirement
	manifestOutput *generation.AssetRequirement
}

func invalidContract() error { return errors.New("local model parts contract is invalid") }

// closedJSON decodes one bounded JSON value, rejects duplicate keys and then
// decodes into target with unknown fields disallowed.
func closedJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return invalidContract()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	remaining := 1024
	var value func(int) error
	value = func(depth int) error {
		remaining--
		if depth > 8 || remaining < 0 {
			return invalidContract()
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return invalidContract()
				}
				seen[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return invalidContract()
		}
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalidContract()
	}
	typed := json.NewDecoder(bytes.NewReader(raw))
	typed.DisallowUnknownFields()
	return typed.Decode(target)
}

func decodeInput(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var values map[string]json.RawMessage
	if closedJSON(raw, &values) != nil || values == nil {
		return nil, invalidContract()
	}
	return values, nil
}

func rawString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" || strings.TrimSpace(value) != value {
		return "", false
	}
	return value, true
}

func parameterByName(parameters []generation.Parameter, name string) (generation.Parameter, bool) {
	for _, parameter := range parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validModelParameters(parameter generation.Parameter) bool {
	return parameter.Type == "asset" || parameter.Type == "asset-array"
}

func validateModelSource(parameter generation.Parameter, limits modelinput.Limits) error {
	if !validModelParameters(parameter) || !parameter.Required || !parameter.SameSession || parameter.UploadedModel != nil ||
		parameter.TaskSourceKind != "" || len(parameter.TaskOfferings) != 0 || parameter.MaxBytes != limits.MaxBytes ||
		len(parameter.MediaTypes) != 1 || parameter.MediaTypes[0] != modelMIME || !containsString(parameter.Extensions, ".glb") {
		return invalidContract()
	}
	return nil
}

func validateModelOutput(output generation.AssetRequirement, maxBytes int64) error {
	if output.Role != "model" || output.Variant != "glb" || output.Minimum == nil || *output.Minimum != 1 ||
		output.MaxBytes <= 0 || output.MaxBytes > math.MaxUint32 || output.MaxBytes != maxBytes ||
		output.Delivery != "" || output.DeliveryProfile != "" || output.VariantPrefix != "" || output.VariantFrom != "" ||
		output.Fidelity != "" || output.RetainDependencyIdentity || output.RequiredWhen != nil ||
		len(output.MediaTypes) != 1 || output.MediaTypes[0] != modelMIME {
		return invalidContract()
	}
	return nil
}

func validateManifestOutput(output generation.AssetRequirement) error {
	if output.Role != manifestRole || output.Variant != manifestVariant || output.Minimum == nil || *output.Minimum != 1 ||
		output.MaxBytes <= 0 || output.MaxBytes > maxManifestBytes ||
		output.Delivery != "" || output.DeliveryProfile != "" || output.VariantPrefix != "" || output.VariantFrom != "" ||
		output.Fidelity != "" || output.RetainDependencyIdentity || output.RequiredWhen != nil ||
		len(output.MediaTypes) != 1 || output.MediaTypes[0] != manifestMIME {
		return invalidContract()
	}
	return nil
}

func partsPolicy(options schema.ModelPartsOptions) (modelinput.Policy, error) {
	if options.ProfileLimits == nil || options.MaxWork <= 0 || options.MaxWork > 1<<32 ||
		options.MaxConcurrent <= 0 || options.MaxConcurrent > maxPartsConcurrency {
		return modelinput.Policy{}, invalidContract()
	}
	p := *options.ProfileLimits
	policy := modelinput.Policy{ProfileID: options.ProfileID, Revision: options.ProfileRevision, Limits: modelinput.Limits{
		MaxBytes: p.MaxBytes, MaxJSONBytes: p.MaxJSONBytes, MaxDepth: p.MaxDepth, MaxElements: p.MaxElements,
		MaxVertices: p.MaxVertices, MaxTriangles: p.MaxTriangles, MaxNodes: p.MaxNodes, MaxWork: p.MaxWork,
		MaxImagePixels: p.MaxImagePixels, MaxTotalImagePixels: p.MaxTotalImagePixels,
	}}
	if policy.Validate() != nil || policy.ProfileID != modelinput.GeneratedProfileID {
		return modelinput.Policy{}, invalidContract()
	}
	return policy, nil
}

func splitContract(offering generation.Offering) (contract, error) {
	var c contract
	c.capability = generation.ModelSplit
	if offering.Capability != generation.ModelSplit || offering.ID == "" || offering.ProviderID == "" ||
		offering.EstimatedCredits != 0 || len(offering.Parameters) != 4 || len(offering.Outputs) != 2 {
		return c, invalidContract()
	}
	if offering.Cost != nil && (offering.Cost.BaseCredits != 0 || offering.Cost.ExtraCredits != 0) {
		return c, invalidContract()
	}
	if closedJSON(offering.ProviderOptions, &c.options) != nil {
		return c, invalidContract()
	}
	policy, err := partsPolicy(c.options)
	if err != nil {
		return c, err
	}
	c.policy = policy
	var source, mode, ratio, parts bool
	for _, parameter := range offering.Parameters {
		if _, isSource := schema.SourceParameterRole(parameter); isSource {
			c.source, source = parameter, true
			continue
		}
		switch parameter.Name {
		case "mode":
			c.mode, mode = parameter, true
		case "min_part_ratio":
			c.minRatio, ratio = parameter, true
		case "max_parts":
			c.maxParts, parts = parameter, true
		}
	}
	if !source || !mode || !ratio || !parts || c.source.Name == "" {
		return c, invalidContract()
	}
	if c.source.Type != "asset" || c.source.Semantic != schema.SemanticSourceAsset || validateModelSource(c.source, policy.Limits) != nil {
		return c, invalidContract()
	}
	if c.mode.Type != "select" || c.mode.Fixed || len(c.mode.Options) != 4 {
		return c, invalidContract()
	}
	modes := map[string]bool{}
	for _, option := range c.mode.Options {
		modes[option.Value] = true
	}
	for _, wanted := range []string{schema.ModelSplitModeNode, schema.ModelSplitModePrimitive, schema.ModelSplitModeMaterial, schema.ModelSplitModeConnectedComponent} {
		if !modes[wanted] {
			return c, invalidContract()
		}
	}
	var defaultMode string
	if json.Unmarshal(c.mode.Default, &defaultMode) != nil || defaultMode != schema.ModelSplitModeConnectedComponent {
		return c, invalidContract()
	}
	var defaultRatio float64
	if c.minRatio.Type != "number" || c.minRatio.Integer || c.minRatio.Minimum == nil || c.minRatio.Maximum == nil ||
		*c.minRatio.Minimum < 0 || *c.minRatio.Maximum > 0.2 || *c.minRatio.Maximum < *c.minRatio.Minimum ||
		json.Unmarshal(c.minRatio.Default, &defaultRatio) != nil || defaultRatio < *c.minRatio.Minimum || defaultRatio > *c.minRatio.Maximum {
		return c, invalidContract()
	}
	var defaultParts int
	if c.maxParts.Type != "number" || !c.maxParts.Integer || c.maxParts.Minimum == nil || c.maxParts.Maximum == nil ||
		*c.maxParts.Minimum < 1 || *c.maxParts.Maximum > maxSplitParts || *c.maxParts.Maximum < *c.maxParts.Minimum ||
		json.Unmarshal(c.maxParts.Default, &defaultParts) != nil || float64(defaultParts) < *c.maxParts.Minimum || float64(defaultParts) > *c.maxParts.Maximum {
		return c, invalidContract()
	}
	if validateModelOutput(offering.Outputs[0], policy.Limits.MaxBytes) != nil || validateManifestOutput(offering.Outputs[1]) != nil {
		return c, invalidContract()
	}
	c.modelOutput = offering.Outputs[0]
	manifest := offering.Outputs[1]
	c.manifestOutput = &manifest
	return c, nil
}

func mergeContract(offering generation.Offering) (contract, error) {
	var c contract
	c.capability = generation.ModelMerge
	if offering.Capability != generation.ModelMerge || offering.ID == "" || offering.ProviderID == "" ||
		offering.EstimatedCredits != 0 || len(offering.Parameters) != 2 || len(offering.Outputs) != 1 {
		return c, invalidContract()
	}
	if offering.Cost != nil && (offering.Cost.BaseCredits != 0 || offering.Cost.ExtraCredits != 0) {
		return c, invalidContract()
	}
	if closedJSON(offering.ProviderOptions, &c.options) != nil {
		return c, invalidContract()
	}
	policy, err := partsPolicy(c.options)
	if err != nil {
		return c, err
	}
	c.policy = policy
	var source, name bool
	for _, parameter := range offering.Parameters {
		if _, isSource := schema.SourceParameterRole(parameter); isSource {
			c.source, source = parameter, true
			continue
		}
		if parameter.Name == "name" {
			c.name, name = parameter, true
		}
	}
	if !source || !name || c.source.Name == "" {
		return c, invalidContract()
	}
	if c.source.Type != "asset-array" || c.source.Semantic != schema.SemanticSourceAsset ||
		validateModelSource(c.source, policy.Limits) != nil ||
		c.source.MinItems < 2 || c.source.MaxItems < c.source.MinItems || c.source.MaxItems > maxSplitParts ||
		c.source.MaxTotalEncodedBytes <= 0 || c.source.MaxTotalEncodedBytes > 1<<32 {
		return c, invalidContract()
	}
	if c.name.Type != "text" || c.name.Fixed || c.name.Required || c.name.MaxLength <= 0 || c.name.MaxLength > 128 {
		return c, invalidContract()
	}
	if validateModelOutput(offering.Outputs[0], policy.Limits.MaxBytes) != nil {
		return c, invalidContract()
	}
	c.modelOutput = offering.Outputs[0]
	return c, nil
}

func partsContract(offering generation.Offering) (contract, error) {
	switch offering.Capability {
	case generation.ModelSplit:
		return splitContract(offering)
	case generation.ModelMerge:
		return mergeContract(offering)
	default:
		return contract{}, invalidContract()
	}
}

// ValidateOffering validates the closed free protocol using only frozen Schema.
func ValidateOffering(offering generation.Offering) error {
	_, err := partsContract(offering)
	return err
}
