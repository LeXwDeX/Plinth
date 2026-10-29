package meshy

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type modelInputContract struct {
	source    schema.Parameter
	options   schema.ModelInputProfileOptions
	policy    modelinput.Policy
	mediaType string
}

func profiledModelContract(offering generation.Offering) (modelInputContract, bool, error) {
	var wire wireOptions
	if err := json.Unmarshal(offering.ProviderOptions, &wire); err != nil {
		return modelInputContract{}, false, err
	}
	if wire.ModelInputProfile == nil {
		return modelInputContract{}, false, nil
	}
	c := modelInputContract{options: *wire.ModelInputProfile}
	invalid := func() (modelInputContract, bool, error) {
		return c, true, fmt.Errorf("invalid frozen model_input_profile contract")
	}
	if c.options.ProfileLimits == nil || c.options.Parameter == "" || c.options.MaxInstancedTriangles < 0 || wire.InlineModelInput == nil {
		return invalid()
	}
	l := *c.options.ProfileLimits
	c.policy = modelinput.Policy{ProfileID: c.options.ProfileID, Revision: c.options.ProfileRevision, Limits: modelinput.Limits{MaxBytes: l.MaxBytes, MaxJSONBytes: l.MaxJSONBytes, MaxDepth: l.MaxDepth, MaxElements: l.MaxElements, MaxVertices: l.MaxVertices, MaxTriangles: l.MaxTriangles, MaxNodes: l.MaxNodes, MaxWork: l.MaxWork, MaxImagePixels: l.MaxImagePixels, MaxTotalImagePixels: l.MaxTotalImagePixels}}
	if c.policy.Validate() != nil || wire.InlineModelInput.MaxBytes != l.MaxBytes {
		return invalid()
	}
	c.mediaType = wire.InlineModelInput.MediaType
	if c.mediaType != "application/octet-stream" && c.mediaType != "model/gltf-binary" {
		return invalid()
	}
	sources, assets := 0, 0
	for _, p := range offering.Parameters {
		if _, source := schema.SourceParameterRole(p); source {
			sources++
		}
		if p.Type == "asset" || p.Type == "asset-array" {
			assets++
		}
		if p.Name == c.options.Parameter {
			if c.source.Name != "" {
				return invalid()
			}
			c.source = p
		}
	}
	p := c.source
	role, isSource := schema.SourceParameterRole(p)
	u := p.UploadedModel
	if sources != 1 || assets != 1 || !isSource || role != schema.SemanticSourceAsset || p.Type != "asset" || !p.Required || p.MaxBytes != l.MaxBytes || !slices.Contains(p.MediaTypes, "model/gltf-binary") || u == nil || u.ProfileID != c.options.ProfileID || u.ProfileRevision != c.options.ProfileRevision || u.ProfileLimits == nil || *u.ProfileLimits != l {
		return invalid()
	}
	return c, true, nil
}
func modelSourceFailure(code generation.ErrorCode, cause error) error {
	return &generation.Error{Code: code, Operation: "meshy_model_input", Detail: "模型来源不符合已冻结的输入约束", Cause: cause}
}
func modelProfileFailure(err error) error {
	var validation *modelinput.Error
	if !errors.As(err, &validation) {
		return err
	}
	code := generation.ErrInvalidModelInput
	switch validation.Kind {
	case modelinput.Unsupported:
		code = generation.ErrUnsupportedModelInput
	case modelinput.Limit:
		code = generation.ErrModelInputLimit
	case modelinput.InvalidPolicy, modelinput.IO:
		code = generation.ErrUnavailable
	case modelinput.Canceled:
		return context.Canceled
	}
	return modelSourceFailure(code, err)
}

type modelContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r modelContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func (a *Adapter) readProfiledModel(ctx context.Context, request generation.InputValidationRequest, c modelInputContract) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.OwnerID == "" || a.inputAssets == nil {
		return nil, modelSourceFailure(generation.ErrInvalidInput, nil)
	}
	missing := func() error {
		return inputRefusal(request.Offering, schema.InputRefusalMismatch, schema.InputRefusalValues{},
			generation.ErrInvalidInput, modelSourceFailure(generation.ErrInvalidInput, nil))
	}
	var input map[string]json.RawMessage
	var id string
	if json.Unmarshal(request.Input, &input) != nil || json.Unmarshal(input[c.source.Name], &id) != nil || id == "" || strings.TrimSpace(id) != id {
		return nil, missing()
	}
	assets := request.InputAssets[c.source.Name]
	if len(assets) != 1 || len(request.InputAssets) != 1 || len(request.TaskDependencies) != 0 {
		return nil, missing()
	}
	asset := assets[0]
	digest, err := hex.DecodeString(asset.SHA256)
	if err != nil || len(digest) != 32 || asset.ID != generation.AssetID(id) || asset.OwnerID != request.OwnerID || asset.StorageKey == "" || asset.Size <= 0 {
		return nil, missing()
	}
	if asset.Size > c.source.MaxBytes {
		return nil, inputRefusal(request.Offering, schema.InputRefusalTooLarge, schema.InputRefusalValues{
			Size: schema.SizeText(asset.Size, true), Limit: schema.SizeText(c.source.MaxBytes, false),
		}, generation.ErrInvalidInput, nil)
	}
	if !slices.Contains(c.source.MediaTypes, asset.MediaType) {
		return nil, inputRefusal(request.Offering, schema.InputRefusalMediaType, schema.InputRefusalValues{
			MediaType: asset.MediaType, Formats: strings.Join(c.source.MediaTypes, ", "),
		}, generation.ErrInvalidInput, nil)
	}
	reader, err := a.inputAssets.Open(ctx, asset)
	if err != nil {
		return nil, inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{},
			generation.ErrUnavailable, err)
	}
	if reader == nil {
		return nil, inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{},
			generation.ErrUnavailable, nil)
	}
	raw, readErr := io.ReadAll(io.LimitReader(modelContextReader{ctx, reader}, asset.Size+1))
	closeErr := reader.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{},
			generation.ErrUnavailable, readErr)
	}
	if closeErr != nil {
		return nil, inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{},
			generation.ErrUnavailable, closeErr)
	}
	if int64(len(raw)) != asset.Size {
		return nil, modelSourceFailure(generation.ErrConflict, nil)
	}
	profile, err := modelinput.ValidateGLBBytes(ctx, raw, c.policy)
	if err != nil {
		return nil, modelProfileFailure(err)
	}
	if !strings.EqualFold(profile.SHA256, asset.SHA256) {
		return nil, modelSourceFailure(generation.ErrConflict, nil)
	}
	if c.options.MaxInstancedTriangles > 0 && profile.InstancedTriangles > c.options.MaxInstancedTriangles {
		return nil, modelSourceFailure(generation.ErrModelInputLimit, nil)
	}
	if err := modelsource.ValidateRoute(c.source, profile); err != nil {
		return nil, err
	}
	return raw, nil
}

// ValidateInput reads local owned bytes only. Legacy Offerings retain their
// original validator path; every new profiled route is checked before admission.
func (a *Adapter) ValidateInput(ctx context.Context, request generation.InputValidationRequest) error {
	if err := validateFrozenSourceResults(request); err != nil {
		return err
	}
	for _, p := range request.Offering.Parameters {
		if p.ImageInput == nil {
			continue
		}
		if !p.Required && len(request.InputAssets[p.Name]) == 0 {
			continue
		}
		if _, _, err := a.readImageInput(ctx, request, p); err != nil {
			return err
		}
	}
	c, bound, err := profiledModelContract(request.Offering)
	if err != nil {
		return modelSourceFailure(generation.ErrUnavailable, err)
	}
	if !bound {
		return a.validateSourceFaceLimit(ctx, request)
	}
	_, err = a.readProfiledModel(ctx, request, c)
	return err
}

// profiledModelWireValue validates and encodes the same single read buffer.
// Start always rechecks; an earlier preparation never authorizes changed bytes.
func (a *Adapter) profiledModelWireValue(ctx context.Context, request generation.StartRequest, parameter string) (string, bool, error) {
	c, bound, err := profiledModelContract(request.Offering)
	if err != nil {
		return "", true, providerFailure(generation.FailureInvalidInput, false, "模型输入约束无效")
	}
	if !bound {
		return "", false, nil
	}
	if parameter != c.source.Name {
		return "", true, providerFailure(generation.FailureInvalidInput, false, "模型来源参数不一致")
	}
	raw, err := a.readProfiledModel(ctx, generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies}, c)
	if err != nil {
		return "", true, &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureInvalidInput, Retryable: false, SafeDetail: "模型输入与已批准的来源不一致"}, Cause: err}
	}
	return "data:" + c.mediaType + ";base64," + base64.StdEncoding.EncodeToString(raw), true, nil
}

var _ generation.ProviderInputValidator = (*Adapter)(nil)
