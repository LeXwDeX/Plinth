// Package localmodel implements synchronous, bounded model-container conversion.
package localmodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/assets/modelconvert"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type Config struct {
	Inputs generation.InputAssetResolver
	Assets generation.AssetStore
}
type Adapter struct {
	inputs   generation.InputAssetResolver
	assets   generation.AssetStore
	verdicts *verdictCache
	slots    sync.Map // Offering ID@revision -> chan struct{}: concurrent package conversions
}

func New(config Config) (*Adapter, error) {
	if config.Inputs == nil || config.Assets == nil {
		return nil, errors.New("local model storage ports missing")
	}
	return &Adapter{inputs: config.Inputs, assets: config.Assets, verdicts: newVerdictCache(verdictCacheEntries)}, nil
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func sourceError() error {
	return &generation.Error{Code: generation.ErrInvalidInput, Operation: "local_model", Detail: "本地模型来源与冻结输入不一致"}
}
func domainError(err error) error {
	var inputError *modelinput.Error
	if errors.As(err, &inputError) {
		code := generation.ErrInvalidModelInput
		switch inputError.Kind {
		case modelinput.Unsupported:
			code = generation.ErrUnsupportedModelInput
		case modelinput.Limit:
			code = generation.ErrModelInputLimit
		case modelinput.InvalidPolicy, modelinput.IO:
			code = generation.ErrUnavailable
		case modelinput.Canceled:
			return context.Canceled
		}
		return &generation.Error{Code: code, Operation: "local_model", Detail: "本地模型内容不符合转换约束", Cause: err}
	}
	return err
}
func providerError(err error) error {
	class := generation.FailureInvalidInput
	retryable := false
	var domain *generation.Error
	if errors.As(err, &domain) {
		if domain.Code == generation.ErrUnavailable {
			class = generation.FailureTransport
			retryable = true
		}
	} else {
		class = generation.FailureTransport
		retryable = true
	}
	return &generation.ProviderError{Failure: &generation.Failure{Class: class, Retryable: retryable, SafeDetail: "本地模型转换未完成"}, Cause: err}
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func sourceID(c contract, raw json.RawMessage) (string, error) {
	var input map[string]json.RawMessage
	if closedJSON(raw, &input) != nil || len(input) != 2 {
		return "", sourceError()
	}
	var id, format string
	if json.Unmarshal(input[c.source.Name], &id) != nil || id == "" || strings.TrimSpace(id) != id || json.Unmarshal(input[c.format.Name], &format) != nil || format != c.targetFormat {
		return "", sourceError()
	}
	return id, nil
}
func validDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}

// checkedRead allocates the frozen size once and hashes while reading, so a
// large source costs exactly its own bytes (no growth copies, no second pass).
func checkedRead(ctx context.Context, reader io.ReadCloser, size, maximum int64, expected string) ([]byte, error) {
	if reader == nil {
		return nil, sourceError()
	}
	if err := ctx.Err(); err != nil {
		_ = reader.Close()
		return nil, err
	}
	if size <= 0 || size > maximum || !validDigest(expected) {
		_ = reader.Close()
		return nil, sourceError()
	}
	body := make([]byte, size)
	hasher := sha256.New()
	source := io.TeeReader(contextReader{ctx: ctx, reader: reader}, hasher)
	_, readErr := io.ReadFull(source, body)
	var extra [1]byte
	trailing := 0
	if readErr == nil {
		trailing, readErr = io.ReadFull(source, extra[:])
		if readErr == io.EOF {
			readErr = nil
		}
	}
	closeErr := reader.Close()
	if errors.Is(readErr, io.ErrUnexpectedEOF) || (readErr == io.EOF && trailing == 0) {
		return nil, sourceError()
	}
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if trailing != 0 || !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected) {
		return nil, sourceError()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}

// sourceAsset checks the frozen asset-source identity without reading bytes.
// The package direction trusts only a Task model output: InputAsset.Role is
// projected by the server from the stored asset row of a successful, owned
// Task, and is empty for every direct upload. No client value can set it.
func sourceAsset(request generation.InputValidationRequest, c contract) (generation.InputAsset, error) {
	if request.OwnerID == "" {
		return generation.InputAsset{}, sourceError()
	}
	id, err := sourceID(c, request.Input)
	if err != nil {
		return generation.InputAsset{}, err
	}
	assets := request.InputAssets[c.source.Name]
	if len(assets) != 1 || len(request.InputAssets) != 1 || len(request.TaskDependencies) != 0 {
		return generation.InputAsset{}, sourceError()
	}
	asset := assets[0]
	if string(asset.ID) != id || asset.OwnerID != request.OwnerID || asset.StorageKey == "" || !contains(c.source.MediaTypes, asset.MediaType) || asset.Size <= 0 || asset.Size > c.source.MaxBytes || asset.Size > c.policy.Limits.MaxBytes || !validDigest(asset.SHA256) {
		return generation.InputAsset{}, sourceError()
	}
	if c.options.Direction == schema.ModelConversionGLBToGLTFPackage && asset.Role != taskModelRole {
		return generation.InputAsset{}, untrustedSourceError()
	}
	return asset, nil
}

const taskModelRole = "model"

func untrustedSourceError() error {
	return &generation.Error{Code: generation.ErrUnsupportedModelInput, Operation: "local_model", Detail: "该转换只接受平台任务已保存的模型文件"}
}

func (a *Adapter) readSource(ctx context.Context, request generation.InputValidationRequest, c contract) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.options.Direction != schema.ModelConversionGLTFToGLB {
		asset, err := sourceAsset(request, c)
		if err != nil {
			return nil, err
		}
		reader, err := a.inputs.Open(ctx, asset)
		if err != nil {
			return nil, err
		}
		return checkedRead(ctx, reader, asset.Size, c.source.MaxBytes, asset.SHA256)
	}
	if request.OwnerID == "" {
		return nil, sourceError()
	}
	id, err := sourceID(c, request.Input)
	if err != nil {
		return nil, err
	}
	dependency, exists := request.TaskDependencies[c.source.Name]
	if !exists || len(request.TaskDependencies) != 1 || len(request.InputAssets) != 0 || string(dependency.TaskID) != id {
		return nil, sourceError()
	}
	source := generation.Task{ID: dependency.TaskID, OwnerID: request.OwnerID, State: generation.TaskSucceeded, Capability: dependency.Capability, Offering: generation.Offering{ID: dependency.OfferingID, ProviderID: dependency.ProviderID, ModelID: dependency.ModelID}, Assets: dependency.Assets}
	asset, err := generation.SelectTaskSourceAsset(c.source, source)
	if err != nil {
		return nil, err
	}
	if c.source.SameProvider && dependency.ProviderID != request.Offering.ProviderID {
		return nil, sourceError()
	}
	reader, err := a.assets.Open(ctx, asset)
	if err != nil {
		return nil, err
	}
	return checkedRead(ctx, reader, asset.Size, c.source.MaxBytes, asset.SHA256)
}
func convert(ctx context.Context, raw []byte, c contract) ([]byte, error) {
	limits := modelconvert.Limits{MaxInputBytes: c.source.MaxBytes, MaxOutputBytes: c.output.MaxBytes, MaxWork: c.options.MaxWork}
	if c.options.Direction == schema.ModelConversionGLBToGLTF {
		return modelconvert.GLBToGLTF(ctx, raw, c.policy, limits)
	}
	return modelconvert.GLTFToGLB(ctx, raw, c.policy, limits)
}
func (a *Adapter) convertInput(ctx context.Context, request generation.InputValidationRequest) ([]byte, contract, error) {
	c, err := conversionContract(request.Offering)
	if err != nil {
		return nil, c, &generation.Error{Code: generation.ErrUnavailable, Operation: "local_model", Detail: "本地模型转换配置无效", Cause: err}
	}
	raw, err := a.readSource(ctx, request, c)
	if err != nil {
		return nil, c, err
	}
	output, err := convert(ctx, raw, c)
	return output, c, domainError(err)
}

// ValidateInput is a read-only preparation/submission check. It runs the same
// bounded conversion and discards bytes; no artifact, Task or grant is written.
func (a *Adapter) ValidateInput(ctx context.Context, request generation.InputValidationRequest) error {
	c, err := conversionContract(request.Offering)
	if err == nil && c.options.Direction == schema.ModelConversionGLBToGLTFPackage {
		return a.validatePackage(ctx, request, c)
	}
	_, _, err = a.convertInput(ctx, request)
	return err
}
func (a *Adapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	if request.TaskID == "" || request.AttemptID == "" {
		return generation.Observation{}, providerError(sourceError())
	}
	validation := generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies}
	if c, err := conversionContract(request.Offering); err == nil && c.options.Direction == schema.ModelConversionGLBToGLTFPackage {
		return a.startPackage(ctx, request, validation, c)
	}
	body, c, err := a.convertInput(ctx, validation)
	if err != nil {
		return generation.Observation{}, providerError(err)
	}
	metadata, _ := json.Marshal(struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}{digest(body), int64(len(body))})
	claim := generation.ArtifactClaim{ID: string(request.AttemptID) + ":model:" + c.output.Variant, Role: c.output.Role, Variant: c.output.Variant, MediaType: c.targetMIME, Inline: body, Metadata: metadata}
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Artifacts: []generation.ArtifactClaim{claim}, DedupeKey: digest(body)}, nil
}
func (*Adapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, providerError(sourceError())
}
func (*Adapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	c, err := conversionContract(request.Offering)
	if err != nil {
		return nil, generation.AssetMetadata{}, providerError(err)
	}
	if c.options.Direction == schema.ModelConversionGLBToGLTFPackage {
		// The package is stored during Start and claimed as a stored asset; the
		// kernel re-hashes that blob instead of asking for bytes here.
		return nil, generation.AssetMetadata{}, providerError(sourceError())
	}
	claim := request.Claim
	var metadata struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	if claim.StoredAsset != nil || claim.Locator != "" || claim.Role != c.output.Role || claim.Variant != c.output.Variant || claim.MediaType != c.targetMIME || len(claim.Inline) == 0 || int64(len(claim.Inline)) > c.output.MaxBytes || closedJSON(claim.Metadata, &metadata) != nil || metadata.Size != int64(len(claim.Inline)) || metadata.SHA256 != digest(claim.Inline) {
		return nil, generation.AssetMetadata{}, providerError(sourceError())
	}
	body := bytes.Clone(claim.Inline)
	// Revalidate the delivered container, not its declared suffix or metadata.
	limits := modelconvert.Limits{MaxInputBytes: c.output.MaxBytes, MaxOutputBytes: c.policy.Limits.MaxBytes, MaxWork: c.options.MaxWork}
	if c.options.Direction == schema.ModelConversionGLBToGLTF {
		_, err = modelconvert.GLTFToGLB(ctx, body, c.policy, limits)
	} else {
		_, err = modelinput.ValidateGLBBytes(ctx, body, c.policy)
	}
	if err != nil {
		return nil, generation.AssetMetadata{}, providerError(domainError(err))
	}
	return io.NopCloser(bytes.NewReader(body)), generation.AssetMetadata{MediaType: c.targetMIME, Size: int64(len(body))}, nil
}

var _ generation.ProviderPort = (*Adapter)(nil)
var _ generation.ProviderInputValidator = (*Adapter)(nil)
