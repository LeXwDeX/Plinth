package meshy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// validateSourceFaceLimit refuses a step before submission when the frozen 3D
// source has more triangles than the Offering declares in source_model_limit
// (#103). The ceiling is declared once in the Product Schema (the Catalog
// validates the declaration at load); this reads it from the Offering and
// compares it with the measured model through the same helper the profiled
// inline model input uses (modelinput.Profile.ExceedsInstancedTriangles). It
// reads only bytes the platform already owns (an uploaded asset or the GLB a
// successful source Task delivered). A source that cannot be measured here (a
// bare URL, no stored GLB, bytes larger than the Offering's inline model input
// or that the static parser does not accept) is not blocked; the upstream
// remains the authority for it.
func (a *Adapter) validateSourceFaceLimit(ctx context.Context, request generation.InputValidationRequest) error {
	limit := request.Offering.SourceModelLimit
	if limit == nil || a.inputAssets == nil {
		return nil
	}
	policy := modelinput.GeneratedPolicy()
	readLimit := sourceReadLimit(request.Offering, policy)
	var worst modelinput.Profile
	for _, source := range sourceModelAssets(request, readLimit) {
		profile, known, err := a.measureSourceModel(ctx, request.Offering, source, policy)
		if err != nil {
			return err
		}
		if known && profile.InstancedTriangles > worst.InstancedTriangles {
			worst = profile
		}
	}
	if !worst.ExceedsInstancedTriangles(limit.MaxTriangles) {
		return nil
	}
	return inputRefusal(request.Offering, schema.InputRefusalFaceLimit,
		schema.InputRefusalValues{Faces: worst.InstancedTriangles, FaceLimit: limit.MaxTriangles},
		generation.ErrInvalidInput, fmt.Errorf("source model has %d triangles, limit %d", worst.InstancedTriangles, limit.MaxTriangles))
}

// sourceReadLimit is the largest source file this check loads into memory
// (B11, #121): the Offering's own inline model input size, the file size the
// step is declared to take, so a source it could not take is never read. The
// parser's safety ceiling still applies, and an Offering that declares no
// inline input is bounded by it alone.
func sourceReadLimit(offering generation.Offering, policy modelinput.Policy) int64 {
	limit := policy.Limits.MaxBytes
	var options wireOptions
	if json.Unmarshal(offering.ProviderOptions, &options) == nil && options.InlineModelInput != nil &&
		options.InlineModelInput.MaxBytes > 0 && options.InlineModelInput.MaxBytes < limit {
		limit = options.InlineModelInput.MaxBytes
	}
	return limit
}

// sourceModelAssets lists the stored GLBs of the declared source parameters.
func sourceModelAssets(request generation.InputValidationRequest, readLimit int64) []generation.InputAsset {
	var assets []generation.InputAsset
	for _, parameter := range request.Offering.Parameters {
		role, ok := schema.SourceParameterRole(parameter)
		if !ok {
			continue
		}
		switch role {
		case schema.SemanticSourceAsset:
			for _, asset := range request.InputAssets[parameter.Name] {
				if asset.MediaType == "model/gltf-binary" {
					assets = append(assets, asset)
				}
			}
		case schema.SemanticSourceTask:
			dependency, exists := request.TaskDependencies[parameter.Name]
			if !exists {
				continue
			}
			for _, asset := range dependency.Assets {
				if asset.Role != "model" || asset.MediaType != "model/gltf-binary" || asset.StorageKey == "" || asset.Size <= 0 {
					continue
				}
				assets = append(assets, generation.InputAsset{
					ID: asset.ID, OwnerID: request.OwnerID, StorageKey: asset.StorageKey,
					AssetType: "model", MediaType: asset.MediaType, Size: asset.Size,
					SHA256: asset.SHA256, Role: asset.Role, Variant: asset.Variant,
				})
			}
		}
	}
	return slices.DeleteFunc(assets, func(asset generation.InputAsset) bool {
		return asset.Size <= 0 || asset.Size > readLimit
	})
}

// measureSourceModel returns the measured profile of one stored GLB. known is
// false when the bytes cannot be measured; only a canceled context or an
// unreadable store is an error.
func (a *Adapter) measureSourceModel(ctx context.Context, offering generation.Offering, asset generation.InputAsset, policy modelinput.Policy) (modelinput.Profile, bool, error) {
	if err := ctx.Err(); err != nil {
		return modelinput.Profile{}, false, err
	}
	reader, err := a.inputAssets.Open(ctx, asset)
	if err != nil || reader == nil {
		return modelinput.Profile{}, false, inputRefusal(offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{}, generation.ErrUnavailable, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(modelContextReader{ctx, reader}, asset.Size+1))
	_ = reader.Close()
	if err := ctx.Err(); err != nil {
		return modelinput.Profile{}, false, err
	}
	digest := sha256.Sum256(raw)
	if readErr != nil || int64(len(raw)) != asset.Size || (asset.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(digest[:]), asset.SHA256)) {
		return modelinput.Profile{}, false, nil
	}
	profile, err := modelinput.ValidateGLBBytes(ctx, raw, policy)
	if err != nil {
		var validation *modelinput.Error
		if errors.As(err, &validation) && validation.Kind == modelinput.Canceled {
			return modelinput.Profile{}, false, context.Canceled
		}
		return modelinput.Profile{}, false, nil
	}
	return profile, true, nil
}
