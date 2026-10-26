package localmodel

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

type contract struct {
	options                              schema.ModelConversionOptions
	policy                               modelinput.Policy
	source                               generation.Parameter
	format                               generation.Parameter
	output                               generation.AssetRequirement
	sourceMIME, targetMIME, targetFormat string
}

func invalidContract() error { return errors.New("local model conversion contract is invalid") }
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
func conversionContract(offering generation.Offering) (contract, error) {
	var c contract
	if offering.Capability != generation.FormatConversion || offering.ID == "" || offering.ProviderID == "" || offering.EstimatedCredits != 0 || len(offering.Parameters) != 2 || len(offering.Outputs) != 1 {
		return c, invalidContract()
	}
	if offering.Cost != nil && (offering.Cost.BaseCredits != 0 || offering.Cost.ExtraCredits != 0) {
		return c, invalidContract()
	}
	if closedJSON(offering.ProviderOptions, &c.options) != nil || c.options.ProfileLimits == nil || c.options.MaxWork <= 0 || c.options.MaxWork > 1<<32 {
		return c, invalidContract()
	}
	if (c.options.Direction == schema.ModelConversionGLBToGLTFPackage) != (c.options.MaxConcurrent > 0) || c.options.MaxConcurrent > maxPackageConcurrency {
		return c, invalidContract()
	}
	p := *c.options.ProfileLimits
	c.policy = modelinput.Policy{ProfileID: c.options.ProfileID, Revision: c.options.ProfileRevision, Limits: modelinput.Limits{MaxBytes: p.MaxBytes, MaxJSONBytes: p.MaxJSONBytes, MaxDepth: p.MaxDepth, MaxElements: p.MaxElements, MaxVertices: p.MaxVertices, MaxTriangles: p.MaxTriangles, MaxNodes: p.MaxNodes, MaxWork: p.MaxWork, MaxImagePixels: p.MaxImagePixels, MaxTotalImagePixels: p.MaxTotalImagePixels}}
	if c.policy.Validate() != nil {
		return c, invalidContract()
	}
	// The upload profile and the Task-output profile are never interchangeable.
	switch c.options.Direction {
	case schema.ModelConversionGLBToGLTF:
		c.sourceMIME, c.targetMIME, c.targetFormat = "model/gltf-binary", "model/gltf+json", "gltf"
		if !c.policy.Uploadable() {
			return c, invalidContract()
		}
	case schema.ModelConversionGLTFToGLB:
		c.sourceMIME, c.targetMIME, c.targetFormat = "model/gltf+json", "model/gltf-binary", "glb"
		if !c.policy.Uploadable() {
			return c, invalidContract()
		}
	case schema.ModelConversionGLBToGLTFPackage:
		c.sourceMIME, c.targetMIME, c.targetFormat = "model/gltf-binary", "application/zip", "gltf"
		if c.policy.ProfileID != modelinput.GeneratedProfileID {
			return c, invalidContract()
		}
	default:
		return c, invalidContract()
	}
	sources := 0
	for _, parameter := range offering.Parameters {
		if _, source := schema.SourceParameterRole(parameter); source {
			c.source = parameter
			sources++
		} else {
			c.format = parameter
		}
	}
	if sources != 1 || !c.source.Required || c.source.MaxBytes <= 0 || c.source.MaxBytes > math.MaxUint32 || c.source.Name == c.format.Name || len(c.source.MediaTypes) == 0 || len(c.source.Extensions) == 0 {
		return c, invalidContract()
	}
	if c.options.Direction == schema.ModelConversionGLBToGLTF {
		if c.source.Type != "asset" || c.source.UploadedModel == nil || c.source.UploadedModel.ProfileID != c.options.ProfileID || c.source.UploadedModel.RequireTextures {
			return c, invalidContract()
		}
	} else if c.options.Direction == schema.ModelConversionGLBToGLTFPackage {
		// A Task model output only: no uploaded-model route is declared, so
		// the harness and model-source resolver refuse direct uploads, and the
		// adapter additionally requires the server-projected Task model role.
		if c.source.Type != "asset" || c.source.UploadedModel != nil || c.source.MaxBytes != c.policy.Limits.MaxBytes {
			return c, invalidContract()
		}
	} else {
		if c.source.Type != "task" || c.source.TaskSourceKind != schema.TaskSourceAssets || len(c.source.TaskOfferings) == 0 || len(c.source.TaskCapabilities) == 0 {
			return c, invalidContract()
		}
		for _, id := range c.source.TaskOfferings {
			if id == "" {
				return c, invalidContract()
			}
		}
	}
	for _, mediaType := range c.source.MediaTypes {
		if mediaType != c.sourceMIME && (c.options.Direction != schema.ModelConversionGLBToGLTF || mediaType != "application/octet-stream") {
			return c, invalidContract()
		}
	}
	expectedExtension := "glb"
	if c.options.Direction == schema.ModelConversionGLTFToGLB {
		expectedExtension = "gltf"
	}
	for _, extension := range c.source.Extensions {
		if strings.TrimPrefix(extension, ".") != expectedExtension {
			return c, invalidContract()
		}
	}
	var fixed string
	if c.format.Type != "select" || !c.format.Required || !c.format.Fixed || json.Unmarshal(c.format.Default, &fixed) != nil || fixed != c.targetFormat || len(c.format.Options) != 1 || c.format.Options[0].Value != fixed {
		return c, invalidContract()
	}
	if offering.Cost != nil && offering.Cost.FormatParameter != "" && offering.Cost.FormatParameter != c.format.Name {
		return c, invalidContract()
	}
	c.output = offering.Outputs[0]
	if c.output.Role != "model" || c.output.Variant == "" || c.output.Minimum == nil || *c.output.Minimum != 1 || c.output.MaxBytes <= 0 || c.output.MaxBytes > math.MaxUint32 || len(c.output.MediaTypes) != 1 || c.output.MediaTypes[0] != c.targetMIME || c.output.Delivery != "" || c.output.VariantFrom != "" {
		return c, invalidContract()
	}
	return c, nil
}

// ValidateOffering validates the closed free protocol using only frozen Schema.
func ValidateOffering(offering generation.Offering) error {
	_, err := conversionContract(offering)
	return err
}
