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

// sourceModelLimit is the Product Schema declaration of an upstream input-face
// ceiling (#103). Meshy's Rigging documentation states "up to 320,000 faces"
// without defining a face; Plinth counts triangles, which is the conservative
// reading because one quad is two triangles. count_basis therefore only
// accepts "triangles".
type sourceModelLimit struct {
	MaxTriangles int64  `json:"max_triangles"`
	CountBasis   string `json:"count_basis"`
}

// sourceFaceReadLimits are parser safety ceilings for reading a delivered or
// owned GLB, not product limits: the product limit is max_triangles above. The
// values are the generated-profile maxima accepted by modelinput.Policy.
var sourceFaceReadLimits = modelinput.Limits{
	MaxBytes: 256 << 20, MaxJSONBytes: 32 << 20, MaxDepth: 256, MaxElements: 1000000,
	MaxVertices: 16000000, MaxTriangles: 16000000, MaxNodes: 100000, MaxWork: 1 << 32,
	MaxImagePixels: 1 << 28, MaxTotalImagePixels: 1 << 30,
}

func validateSourceModelLimit(offering generation.Offering, options wireOptions) error {
	limit := options.SourceModelLimit
	if limit == nil {
		return nil
	}
	if limit.MaxTriangles <= 0 || limit.MaxTriangles > sourceFaceReadLimits.MaxTriangles || limit.CountBasis != "triangles" {
		return errors.New("source_model_limit 需要正的 max_triangles 与 count_basis=triangles")
	}
	for _, parameter := range offering.Parameters {
		if role, ok := schema.SourceParameterRole(parameter); ok && (role == schema.SemanticSourceTask || role == schema.SemanticSourceAsset) {
			return nil
		}
	}
	return errors.New("source_model_limit 需要 source-task 或 source-asset 参数")
}

// validateSourceFaceLimit refuses a step before submission when the frozen 3D
// source has more triangles than the Offering declares. It reads only bytes the
// platform already owns (an uploaded asset or the GLB a successful source Task
// delivered). A source that cannot be measured here (a bare URL, no stored GLB,
// bytes the static parser does not accept) is not blocked; the upstream
// remains the authority for it.
func (a *Adapter) validateSourceFaceLimit(ctx context.Context, request generation.InputValidationRequest) error {
	var options wireOptions
	if json.Unmarshal(request.Offering.ProviderOptions, &options) != nil || options.SourceModelLimit == nil {
		return nil
	}
	limit := options.SourceModelLimit
	if validateSourceModelLimit(request.Offering, options) != nil {
		return modelSourceFailure(generation.ErrUnavailable, errors.New("invalid source_model_limit"))
	}
	if a.inputAssets == nil {
		return nil
	}
	policy := modelinput.Policy{ProfileID: modelinput.GeneratedProfileID, Revision: modelinput.GeneratedProfileRevision, Limits: sourceFaceReadLimits}
	var worst int64
	for _, source := range sourceModelAssets(request) {
		triangles, known, err := a.countSourceTriangles(ctx, request.Offering, source, policy)
		if err != nil {
			return err
		}
		if known && triangles > worst {
			worst = triangles
		}
	}
	if worst <= limit.MaxTriangles {
		return nil
	}
	return inputRefusal(request.Offering, schema.InputRefusalFaceLimit,
		schema.InputRefusalValues{Faces: worst, FaceLimit: limit.MaxTriangles},
		generation.ErrInvalidInput, fmt.Errorf("source model has %d triangles, limit %d", worst, limit.MaxTriangles))
}

// sourceModelAssets lists the stored GLBs of the declared source parameters.
func sourceModelAssets(request generation.InputValidationRequest) []generation.InputAsset {
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
		return asset.Size <= 0 || asset.Size > sourceFaceReadLimits.MaxBytes
	})
}

// countSourceTriangles returns the instanced triangle count of one stored GLB.
// known is false when the bytes cannot be measured; only a canceled context or
// an unreadable store is an error.
func (a *Adapter) countSourceTriangles(ctx context.Context, offering generation.Offering, asset generation.InputAsset, policy modelinput.Policy) (int64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	reader, err := a.inputAssets.Open(ctx, asset)
	if err != nil || reader == nil {
		return 0, false, inputRefusal(offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{}, generation.ErrUnavailable, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(modelContextReader{ctx, reader}, asset.Size+1))
	_ = reader.Close()
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	digest := sha256.Sum256(raw)
	if readErr != nil || int64(len(raw)) != asset.Size || (asset.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(digest[:]), asset.SHA256)) {
		return 0, false, nil
	}
	profile, err := modelinput.ValidateGLBBytes(ctx, raw, policy)
	if err != nil {
		var validation *modelinput.Error
		if errors.As(err, &validation) && validation.Kind == modelinput.Canceled {
			return 0, false, context.Canceled
		}
		return 0, false, nil
	}
	return profile.InstancedTriangles, true, nil
}
