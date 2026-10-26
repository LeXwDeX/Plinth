package catalog

import (
	"fmt"
	"slices"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// ModelInputProfile is a product limit/format declaration. It contains no
// execution or Provider configuration and is safe to expose to the browser.
type ModelInputProfile struct {
	ID                string           `json:"id"`
	Revision          string           `json:"revision"`
	Label             string           `json:"label"`
	Description       string           `json:"description"`
	AssetType         string           `json:"asset_type"`
	Extensions        []string         `json:"extensions"`
	MediaTypes        []string         `json:"media_types"`
	PreviewMediaTypes []string         `json:"preview_media_types,omitempty"`
	UploadMediaType   string           `json:"upload_media_type"`
	Limits            ModelInputLimits `json:"limits"`
}

type ModelInputLimits = schemacontract.ModelInputLimits

// GeneratedModelProfile is the conversion-input contract for a GLB produced by
// a successful platform Task (#264). Real generation results (tens of MiB,
// about a million vertices, 8192-square textures) exceed the upload profile,
// which stays the security limit for direct uploads.
type GeneratedModelProfile struct {
	ID          string           `json:"id"`
	Revision    string           `json:"revision"`
	Description string           `json:"description"`
	Limits      ModelInputLimits `json:"limits"`
}

func validateGeneratedModelProfile(runtime InputAssetRuntimeSchema) error {
	profile := runtime.GeneratedModelProfile
	if profile == nil {
		return nil
	}
	if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.Revision) == "" || strings.TrimSpace(profile.Description) == "" {
		return fmt.Errorf("generated_model_profile id, revision and description are required")
	}
	upload := runtime.ModelProfile
	if upload == nil || upload.ID == profile.ID {
		return fmt.Errorf("generated_model_profile requires a distinct upload model_profile")
	}
	l, u := profile.Limits, upload.Limits
	pairs := [][2]int64{{l.MaxBytes, u.MaxBytes}, {l.MaxJSONBytes, u.MaxJSONBytes}, {l.MaxDepth, u.MaxDepth}, {l.MaxElements, u.MaxElements}, {l.MaxVertices, u.MaxVertices}, {l.MaxTriangles, u.MaxTriangles}, {l.MaxNodes, u.MaxNodes}, {l.MaxWork, u.MaxWork}, {l.MaxImagePixels, u.MaxImagePixels}, {l.MaxTotalImagePixels, u.MaxTotalImagePixels}}
	for _, pair := range pairs {
		// A Task-output profile is never narrower than the upload profile.
		if pair[0] <= 0 || pair[0] < pair[1] {
			return fmt.Errorf("generated_model_profile limits must be positive and not below model_profile")
		}
	}
	if l.MaxJSONBytes > l.MaxBytes || l.MaxTotalImagePixels < l.MaxImagePixels {
		return fmt.Errorf("generated_model_profile resource limits are inconsistent")
	}
	return nil
}

func cloneModelInputProfile(source *ModelInputProfile) *ModelInputProfile {
	if source == nil {
		return nil
	}
	result := *source
	result.Extensions = append([]string(nil), source.Extensions...)
	result.MediaTypes = append([]string(nil), source.MediaTypes...)
	result.PreviewMediaTypes = append([]string(nil), source.PreviewMediaTypes...)
	return &result
}

func validateModelInputProfile(runtime InputAssetRuntimeSchema) error {
	profile := runtime.ModelProfile
	if profile == nil {
		return nil
	} // Catalogs without uploaded-model capabilities remain valid.
	for name, value := range map[string]string{"id": profile.ID, "revision": profile.Revision, "label": profile.Label, "description": profile.Description, "asset_type": profile.AssetType, "upload_media_type": profile.UploadMediaType} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("model_profile.%s is required", name)
		}
	}
	var rule *InputAssetTypeRule
	for _, candidate := range runtime.TypeRules {
		if candidate.AssetType == profile.AssetType {
			copy := candidate
			rule = &copy
			break
		}
	}
	if rule == nil {
		return fmt.Errorf("model_profile.asset_type is not registered")
	}
	if len(profile.Extensions) == 0 || len(profile.MediaTypes) == 0 {
		return fmt.Errorf("model_profile extensions and media_types are required")
	}
	seen := map[string]bool{}
	for _, extension := range profile.Extensions {
		if !strings.HasPrefix(extension, ".") || extension != strings.ToLower(extension) || !slices.Contains(rule.Extensions, extension) || seen[extension] {
			return fmt.Errorf("model_profile extension is invalid or duplicated")
		}
		seen[extension] = true
	}
	seen = map[string]bool{}
	for _, media := range profile.MediaTypes {
		if media != strings.ToLower(media) || !strings.Contains(media, "/") || seen[media] || !slices.Contains(runtime.AllowedUploadMediaTypes, media) {
			return fmt.Errorf("model_profile media type is invalid or unsupported")
		}
		seen[media] = true
	}
	seen = map[string]bool{}
	for _, media := range profile.PreviewMediaTypes {
		if !slices.Contains(profile.MediaTypes, media) || seen[media] {
			return fmt.Errorf("model_profile preview media type is unsupported or duplicated")
		}
		seen[media] = true
	}
	if !slices.Contains(profile.MediaTypes, profile.UploadMediaType) {
		return fmt.Errorf("model_profile upload_media_type must be supported")
	}
	l := profile.Limits
	for _, value := range []int64{l.MaxBytes, l.MaxJSONBytes, l.MaxDepth, l.MaxElements, l.MaxVertices, l.MaxTriangles, l.MaxNodes, l.MaxWork, l.MaxImagePixels, l.MaxTotalImagePixels} {
		if value <= 0 {
			return fmt.Errorf("model_profile limits must be positive")
		}
	}
	if l.MaxBytes > runtime.UploadMaxBytes || l.MaxJSONBytes > l.MaxBytes || l.MaxTotalImagePixels < l.MaxImagePixels {
		return fmt.Errorf("model_profile resource limits are inconsistent")
	}
	return nil
}

func validateUploadedModelParameters(offering schemacontract.Offering, profile *ModelInputProfile) error {
	for _, parameter := range offering.Parameters {
		constraint := parameter.UploadedModel
		if constraint == nil {
			continue
		}
		role, ok := schemacontract.SourceParameterRole(parameter)
		if !ok || role != schemacontract.SemanticSourceAsset || parameter.Type != "asset" {
			return fmt.Errorf("uploaded_model requires a model source asset parameter")
		}
		if profile == nil || constraint.ProfileID != profile.ID {
			return fmt.Errorf("uploaded_model references an undeclared profile")
		}
		if parameter.MaxBytes > 0 && parameter.MaxBytes < profile.Limits.MaxBytes {
			return fmt.Errorf("uploaded_model profile exceeds parameter byte limit")
		}
		if len(parameter.Extensions) > 0 {
			for _, extension := range profile.Extensions {
				if !slices.Contains(parameter.Extensions, extension) {
					return fmt.Errorf("uploaded_model profile format is not accepted by parameter")
				}
			}
		}
		if (constraint.RequireTextures || constraint.RequireSingleBaseColorTexture) && strings.TrimSpace(constraint.Requirements) == "" {
			return fmt.Errorf("uploaded_model texture condition needs an explicit requirements message")
		}
	}
	return nil
}
