// Package modelsource validates owned uploads at the Generation input boundary.
// Task outputs continue through the existing owner-scoped input projection.
package modelsource

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type Native interface {
	generation.InputAssetResolver
	OpenOwnedUpload(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error)
}
type Service struct {
	native Native
	policy modelinput.Policy
}

func New(native Native, policy modelinput.Policy) (*Service, error) {
	if native == nil {
		return nil, errors.New("modelsource: missing native input service")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if !policy.Uploadable() {
		return nil, errors.New("modelsource: policy is not an upload profile")
	}
	return &Service{native: native, policy: policy}, nil
}

// Inspect deliberately uses the native uploads table. LoadInputAssets may also
// project Task outputs, but those cannot become an input_asset selection.
func (s *Service) Inspect(ctx context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, modelinput.Profile, error) {
	return s.inspectOwned(ctx, owner, id, s.policy)
}

// InspectForParameter applies the immutable policy captured by a source
// parameter. Only historical declarations with neither snapshot field use the
// current policy; an incomplete or invalid snapshot never falls back.
func (s *Service) InspectForParameter(ctx context.Context, owner generation.OwnerID, id generation.AssetID, parameter generation.Parameter) (generation.InputAsset, modelinput.Profile, error) {
	policy, frozen, err := frozenPolicy(parameter)
	if err != nil {
		return generation.InputAsset{}, modelinput.Profile{}, err
	}
	if parameter.UploadedModel == nil {
		return generation.InputAsset{}, modelinput.Profile{}, domain(generation.ErrUnsupportedModelInput, nil)
	}
	if !frozen {
		policy = s.policy
	}
	asset, profile, err := s.inspectOwned(ctx, owner, id, policy)
	if err != nil {
		return generation.InputAsset{}, modelinput.Profile{}, err
	}
	if err := ValidateRoute(parameter, profile); err != nil {
		return generation.InputAsset{}, modelinput.Profile{}, err
	}
	return asset, profile, nil
}
func (s *Service) inspectOwned(ctx context.Context, owner generation.OwnerID, id generation.AssetID, policy modelinput.Policy) (generation.InputAsset, modelinput.Profile, error) {
	asset, body, err := s.native.OpenOwnedUpload(ctx, owner, id)
	if err != nil {
		if schema.IsCode(err, generation.ErrNotFound) || schema.IsCode(err, generation.ErrForbidden) || errors.Is(err, generation.RepositoryNotFound) {
			err = domain(generation.ErrNotFound, err)
		}
		return generation.InputAsset{}, modelinput.Profile{}, err
	}
	profile, validateErr := s.inspect(ctx, owner, id, asset, body, policy)
	closeErr := body.Close()
	if validateErr != nil {
		return generation.InputAsset{}, modelinput.Profile{}, validateErr
	}
	if closeErr != nil {
		return generation.InputAsset{}, modelinput.Profile{}, domain(generation.ErrUnavailable, closeErr)
	}
	return asset, profile, nil
}
func (s *Service) inspect(ctx context.Context, owner generation.OwnerID, id generation.AssetID, asset generation.InputAsset, body io.Reader, policy modelinput.Policy) (modelinput.Profile, error) {
	if owner == "" || asset.OwnerID != owner || id == "" || asset.ID != id {
		return modelinput.Profile{}, domain(generation.ErrNotFound, nil)
	}
	file, ok := body.(*os.File)
	if !ok {
		return modelinput.Profile{}, domain(generation.ErrUnavailable, nil)
	}
	profile, err := modelinput.ValidateGLB(ctx, file, policy)
	if err != nil {
		return modelinput.Profile{}, validationError(err)
	}
	if asset.SHA256 == "" || profile.SHA256 != asset.SHA256 || profile.Size != asset.Size {
		return modelinput.Profile{}, domain(generation.ErrConflict, nil)
	}
	return profile, nil
}

func frozenPolicy(parameter generation.Parameter) (modelinput.Policy, bool, error) {
	declared := parameter.UploadedModel
	if declared == nil || (declared.ProfileRevision == "" && declared.ProfileLimits == nil) {
		return modelinput.Policy{}, false, nil
	}
	if declared.ProfileRevision == "" || declared.ProfileLimits == nil {
		return modelinput.Policy{}, false, domain(generation.ErrInvalidInput, nil)
	}
	limits := declared.ProfileLimits
	policy := modelinput.Policy{ProfileID: declared.ProfileID, Revision: declared.ProfileRevision, Limits: modelinput.Limits{MaxBytes: limits.MaxBytes, MaxJSONBytes: limits.MaxJSONBytes, MaxDepth: limits.MaxDepth, MaxElements: limits.MaxElements, MaxVertices: limits.MaxVertices, MaxTriangles: limits.MaxTriangles, MaxNodes: limits.MaxNodes, MaxWork: limits.MaxWork, MaxImagePixels: limits.MaxImagePixels, MaxTotalImagePixels: limits.MaxTotalImagePixels}}
	if err := policy.Validate(); err != nil {
		return modelinput.Policy{}, false, domain(generation.ErrInvalidInput, err)
	}
	// A Task-output conversion profile never validates a direct upload, even
	// if a declaration were to name it.
	if !policy.Uploadable() {
		return modelinput.Policy{}, false, domain(generation.ErrUnsupportedModelInput, nil)
	}
	return policy, true, nil
}
func (s *Service) Resolve(ctx context.Context, owner generation.OwnerID, offering generation.Offering, input json.RawMessage) (map[string][]generation.InputAsset, error) {
	return s.resolve(ctx, owner, offering, input, false)
}

// ResolveAccepted is only selected by Generation for a persisted Task. An old
// frozen parameter without uploaded_model retains its original native contract;
// new admissions still go through Resolve and cannot request this exception.
func (s *Service) ResolveAccepted(ctx context.Context, task generation.Task) (map[string][]generation.InputAsset, error) {
	return s.resolve(ctx, task.OwnerID, task.Offering, task.Input, true)
}

func (s *Service) resolve(ctx context.Context, owner generation.OwnerID, offering generation.Offering, input json.RawMessage, accepted bool) (map[string][]generation.InputAsset, error) {
	policies := map[string]modelinput.Policy{}
	for _, parameter := range offering.Parameters {
		if role, source := schema.SourceParameterRole(parameter); !source || role != schema.SemanticSourceAsset {
			continue
		}
		policy, frozen, err := frozenPolicy(parameter)
		if err != nil {
			return nil, err
		}
		if !frozen {
			policy = s.policy
		}
		policies[parameter.Name] = policy
	}
	resolved, err := s.native.Resolve(ctx, owner, offering, input)
	if err != nil {
		return nil, err
	}
	for _, parameter := range offering.Parameters {
		role, source := schema.SourceParameterRole(parameter)
		if !source || role != schema.SemanticSourceAsset {
			continue
		}
		if accepted && parameter.UploadedModel == nil {
			continue
		}
		for _, candidate := range resolved[parameter.Name] {
			asset, body, openErr := s.native.OpenOwnedUpload(ctx, owner, candidate.ID)
			if openErr != nil {
				// The native resolver already validated ownership, successful source Task
				// and the original Offering constraints for this historical Task output.
				if schema.IsCode(openErr, generation.ErrNotFound) || errors.Is(openErr, generation.RepositoryNotFound) {
					if parameter.UploadedModel != nil && parameter.UploadedModel.NativeOnly {
						return nil, domain(generation.ErrUnsupportedModelInput, nil)
					}
					// A parameter that declares an uploaded-model contract may
					// only take a successful Task's model output. Splat,
					// texture and converted artifacts are not model sources, so
					// they must not reach a paid provider request through a raw
					// PrepareInput or an accepted-task retry. Parameters with
					// no such contract keep their historical native behavior.
					if parameter.UploadedModel != nil && candidate.Role != "model" {
						return nil, domain(generation.ErrUnsupportedModelInput, nil)
					}
					continue
				}
				return nil, openErr
			}
			profile, validateErr := s.inspect(ctx, owner, candidate.ID, asset, body, policies[parameter.Name])
			closeErr := body.Close()
			if validateErr != nil {
				return nil, validateErr
			}
			if closeErr != nil {
				return nil, domain(generation.ErrUnavailable, closeErr)
			}
			if candidate.OwnerID != asset.OwnerID || candidate.SHA256 != asset.SHA256 || candidate.Size != asset.Size || candidate.StorageKey != asset.StorageKey {
				return nil, domain(generation.ErrConflict, nil)
			}
			if err := ValidateRoute(parameter, profile); err != nil {
				return nil, err
			}
		}
	}
	return resolved, nil
}
func (s *Service) Open(ctx context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	return s.native.Open(ctx, asset)
}

// ValidateRoute is shared by source readiness and execution. HasTextures is
// structural evidence only; semantic requirements such as humanoid shape stay
// explicit Offering requirements and Provider acceptance conditions.
func ValidateRoute(parameter generation.Parameter, profile modelinput.Profile) error {
	role, source := schema.SourceParameterRole(parameter)
	if !source || role != schema.SemanticSourceAsset || parameter.Type != "asset" || parameter.UploadedModel == nil || parameter.UploadedModel.ProfileID != profile.ProfileID {
		return domain(generation.ErrUnsupportedModelInput, nil)
	}
	policy, frozen, err := frozenPolicy(parameter)
	if err != nil {
		return err
	}
	if frozen && policy.Revision != profile.Revision {
		return domain(generation.ErrUnsupportedModelInput, nil)
	}
	if parameter.MaxBytes > 0 && profile.Size > parameter.MaxBytes {
		return domain(generation.ErrModelInputLimit, nil)
	}
	if parameter.UploadedModel.RequireTextures && !profile.HasTextures {
		return domain(generation.ErrUnsupportedModelInput, nil)
	}
	if parameter.UploadedModel.RequireSingleBaseColorTexture && !profile.HasSingleBaseColorTexture {
		return domain(generation.ErrUnsupportedModelInput, nil)
	}
	return nil
}

// UploadValidator runs against the same private staging file that will enter
// CAS. Unrelated image and document uploads retain their existing path.
func UploadValidator(policy modelinput.Policy, assetType string, extensions, mediaTypes []string) func(context.Context, *os.File, generation.InputAssetUpload) error {
	extensions = slices.Clone(extensions)
	mediaTypes = slices.Clone(mediaTypes)
	uploadable := policy.Uploadable()
	return func(ctx context.Context, file *os.File, upload generation.InputAssetUpload) error {
		extension := strings.ToLower(filepath.Ext(upload.OriginalFilename))
		media := strings.ToLower(strings.TrimSpace(upload.MediaType))
		selected := upload.AssetType == assetType || slices.Contains(extensions, extension) || (media != "application/octet-stream" && slices.Contains(mediaTypes, media))
		if !selected {
			return nil
		}
		if !slices.Contains(extensions, extension) || !uploadable {
			return domain(generation.ErrUnsupportedModelInput, nil)
		}
		_, err := modelinput.ValidateGLB(ctx, file, policy)
		return validationError(err)
	}
}
func validationError(err error) error {
	if err == nil {
		return nil
	}
	code := generation.ErrUnavailable
	var validation *modelinput.Error
	if errors.As(err, &validation) {
		switch validation.Kind {
		case modelinput.Invalid:
			code = generation.ErrInvalidModelInput
		case modelinput.Unsupported:
			code = generation.ErrUnsupportedModelInput
		case modelinput.Limit:
			code = generation.ErrModelInputLimit
		}
	}
	return domain(code, err)
}
func domain(code generation.ErrorCode, cause error) error {
	return &generation.Error{Code: code, Operation: "validate_model_input", Cause: cause}
}
