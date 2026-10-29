package tripo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	_ "golang.org/x/image/webp"
)

const splatPreparationKind = "image-to-splat"
const maximumSplatImageBytes int64 = 20_000_000 // documented endpoint input limit

type splatOptions struct {
	Endpoint        string                     `json:"endpoint"`
	ObserveEndpoint string                     `json:"observe_endpoint"`
	UploadEndpoint  string                     `json:"upload_endpoint"`
	ResponseAliases []generation.ResponseAlias `json:"response_aliases"`
}

type splatState struct {
	Version          int                      `json:"version"`
	Kind             string                   `json:"kind"`
	Phase            string                   `json:"phase"`
	TaskID           generation.TaskID        `json:"task_id"`
	AttemptID        generation.AttemptID     `json:"attempt_id"`
	SubmissionKey    string                   `json:"submission_key"`
	OfferingID       generation.OfferingID    `json:"offering_id"`
	OfferingRevision string                   `json:"offering_revision"`
	InputSHA256      string                   `json:"input_sha256"`
	Source           uploadedConversionSource `json:"source"`
	FileToken        string                   `json:"file_token,omitempty"`
	RemoteID         string                   `json:"remote_id,omitempty"`
	ConsumedCredit   json.RawMessage          `json:"consumed_credit,omitempty"`
}

// splatTaskType is the task type Tripo reports for image-to-splat; captured from the official API (#89).
const splatTaskType = "image_to_splat"

func (s splatState) encode() json.RawMessage { raw, _ := json.Marshal(s); return raw }
func splatProtocolError() error {
	return failure(generation.FailureProtocol, false, "Tripo Splat 声明、产物或检查点无效")
}
func splatInputError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo Splat 输入参数无效")
}

func splatParameterError(o generation.Offering, p generation.Parameter, cause error) error {
	return inputParameterRefusal(o, p, cause)
}

func splatIntegrityError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo Splat 来源图片的字节与登记的不一致")
}

func splatSourceMismatch(o generation.Offering) error {
	return inputRefusal(o, schema.InputRefusalMismatch, schema.InputRefusalValues{},
		"Tripo Splat 来源与冻结输入不一致")
}

func splatDeclaration(o generation.Offering) (generation.Parameter, *generation.Parameter, generation.AssetRequirement, splatOptions, error) {
	var options splatOptions
	var source generation.Parameter
	var seed *generation.Parameter
	var output generation.AssetRequirement
	if o.Capability != schema.ImageToSplat || len(o.Parameters) < 1 || len(o.Parameters) > 2 || len(o.Outputs) != 1 || o.IdempotentSubmission || o.Retry.MaxAttempts != 1 || len(o.Retry.Retryable) != 0 || o.PostSuccess != nil || o.Cost != nil || strictUploadedJSON(o.ProviderOptions, &options) != nil || !validRigCheckRoute(options.Endpoint, false) || !validRigCheckRoute(options.UploadEndpoint, false) || !validRigCheckRoute(options.ObserveEndpoint, true) || len(options.ResponseAliases) != 1 {
		return source, seed, output, options, splatProtocolError()
	}
	for _, p := range o.Parameters {
		switch p.WireName {
		case "input":
			if source.Name != "" || p.Type != "asset" || !p.Required || p.Semantic != schema.SemanticReferenceImage || p.MaxBytes <= 0 || p.MaxBytes > maximumSplatImageBytes || len(p.MediaTypes) == 0 || p.UploadedModel != nil {
				return source, seed, output, options, splatProtocolError()
			}
			for _, media := range p.MediaTypes {
				if !slices.Contains([]string{"image/png", "image/jpeg", "image/webp"}, media) {
					return source, seed, output, options, splatProtocolError()
				}
			}
			source = p
		case "model_seed":
			if seed != nil || p.Type != "number" || !p.Integer || p.Required || len(p.WireValues) != 0 {
				return source, seed, output, options, splatProtocolError()
			}
			copy := p
			seed = &copy
		default:
			return source, seed, output, options, splatProtocolError()
		}
	}
	output = o.Outputs[0]
	alias := options.ResponseAliases[0]
	if source.Name == "" || output.Role != schema.GaussianSplatRole || output.Variant != schema.GaussianSplatVariant || output.DeliveryProfile != schema.DeliveryProfileGaussianSplat32V1 || output.Delivery != schema.AssetDeliveryRequired || output.Minimum == nil || *output.Minimum != 1 || output.MaxBytes <= 0 || output.MaxBytes > schema.GaussianSplatMaxBytes || len(output.MediaTypes) != 1 || output.MediaTypes[0] != schema.GaussianSplatMediaType || output.VariantPrefix != "" || output.VariantFrom != "" || output.Fidelity != "" || alias.Role != output.Role || alias.Variant != output.Variant || !slices.Equal(alias.Fields, []string{"model_url"}) || alias.Key == "" {
		return source, seed, output, options, splatProtocolError()
	}
	return source, seed, output, options, nil
}

func splatInput(o generation.Offering, raw json.RawMessage) (generation.Parameter, string, map[string]any, error) {
	source, seed, _, _, err := splatDeclaration(o)
	if err != nil {
		return source, "", nil, err
	}
	var values map[string]json.RawMessage
	if strictUploadedJSON(raw, &values) != nil || values == nil {
		return source, "", nil, inputParametersRefusal(o, nil)
	}
	var id string
	if err := json.Unmarshal(values[source.Name], &id); err != nil || !uploadedTokenValid(id) {
		return source, "", nil, splatParameterError(o, source, err)
	}
	body := map[string]any{}
	for name, value := range values {
		if name == source.Name {
			continue
		}
		if seed == nil || name != seed.Name {
			return source, "", nil, inputParametersRefusal(o, nil)
		}
		var number int64
		if err := json.Unmarshal(value, &number); err != nil || number < -9007199254740991 || number > 9007199254740991 {
			return source, "", nil, splatParameterError(o, *seed, err)
		}
		body[seed.WireName] = number
	}
	return source, id, body, nil
}

// Validation is local-only, before Grant preparation and again before upload.
// Hash and actual MIME are checked on the exact buffer sent to the upload wire.
func (a *Adapter) splatSource(ctx context.Context, request generation.InputValidationRequest) (generation.Parameter, generation.InputAsset, []byte, error) {
	p, id, _, err := splatInput(request.Offering, request.Input)
	var asset generation.InputAsset
	if err != nil {
		return p, asset, nil, err
	}
	assets := request.InputAssets[p.Name]
	if a.inputAssets == nil || request.OwnerID == "" {
		return p, asset, nil, splatProtocolError()
	}
	if len(request.InputAssets) != 1 || len(assets) != 1 || len(request.TaskDependencies) != 0 {
		return p, asset, nil, splatSourceMismatch(request.Offering)
	}
	asset = assets[0]
	if asset.OwnerID != request.OwnerID || string(asset.ID) != id || asset.StorageKey == "" || asset.Size <= 0 || !uploadedHashValid(asset.SHA256) {
		return p, asset, nil, splatSourceMismatch(request.Offering)
	}
	if asset.Size > p.MaxBytes {
		return p, asset, nil, inputRefusal(request.Offering, schema.InputRefusalTooLarge, schema.InputRefusalValues{
			Size: schema.SizeText(asset.Size, true), Limit: schema.SizeText(p.MaxBytes, false),
		}, "Tripo Splat 来源图片超过上限")
	}
	if !slices.Contains(p.MediaTypes, asset.MediaType) {
		return p, asset, nil, inputRefusal(request.Offering, schema.InputRefusalMediaType, schema.InputRefusalValues{
			MediaType: asset.MediaType, Formats: strings.Join(p.MediaTypes, ", "),
		}, "Tripo Splat 来源图片格式不受支持")
	}
	if err = ctx.Err(); err != nil {
		return p, asset, nil, err
	}
	r, err := a.inputAssets.Open(ctx, asset)
	if err != nil || r == nil {
		return p, asset, nil, inputUnavailable(request.Offering, "无法读取 Tripo Splat 来源图片", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(splatContextReader{ctx, r}, asset.Size+1))
	closeErr := r.Close()
	if err = ctx.Err(); err != nil {
		return p, asset, nil, err
	}
	if readErr != nil || closeErr != nil {
		return p, asset, nil, inputUnavailable(request.Offering, "读取 Tripo Splat 来源图片失败", errors.Join(readErr, closeErr))
	}
	if int64(len(raw)) != asset.Size || uploadedDigest(raw) != asset.SHA256 {
		return p, asset, nil, splatIntegrityError()
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || "image/"+format != asset.MediaType || config.Width <= 0 || config.Height <= 0 {
		return p, asset, nil, inputRefusal(request.Offering, schema.InputRefusalInvalid, schema.InputRefusalValues{},
			"Tripo Splat 来源图片无法按声明的格式解码")
	}
	return p, asset, raw, nil
}

type splatContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r splatContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func splatPermit(ctx context.Context, state splatState) (*generation.DispatchReceipt, error) {
	if _, ok := generation.DispatchCheckpointerFromContext(ctx); !ok {
		return nil, failure(generation.FailureRejected, false, "Tripo Splat 缺少持久派发许可")
	}
	return generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: state.encode()})
}

func (a *Adapter) startSplat(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	p, asset, raw, err := a.splatSource(ctx, generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies})
	if err != nil {
		return generation.Observation{}, err
	}
	if request.TaskID == "" || request.AttemptID == "" || request.SubmissionKey == "" {
		return generation.Observation{}, splatInputError()
	}
	filename, err := imageUploadFilename(asset.MediaType)
	if err != nil {
		return generation.Observation{}, err
	}
	state := splatState{Version: 1, Kind: splatPreparationKind, Phase: "upload_dispatching", TaskID: request.TaskID, AttemptID: request.AttemptID, SubmissionKey: request.SubmissionKey, OfferingID: request.Offering.ID, OfferingRevision: request.Offering.Revision, InputSHA256: uploadedDigest(request.Input), Source: uploadedConversionSource{Parameter: p.Name, ID: asset.ID, OwnerID: asset.OwnerID, SHA256: asset.SHA256, Size: asset.Size, MediaType: asset.MediaType}}
	permit, err := splatPermit(ctx, state)
	if err != nil {
		return generation.Observation{}, err
	}
	token, err := a.uploadReader(ctx, request, asset.MediaType, filename, io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		return generation.Observation{}, err
	}
	if !uploadedTokenValid(token) {
		return generation.Observation{}, submissionUnknown("Tripo Splat 上传响应无法确认，已停止重发")
	}
	state.Phase, state.FileToken = "token_ready", token
	return generation.Observation{Phase: generation.UpstreamPreparing, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(string(request.AttemptID), "splat_upload_ready")}, nil
}

func decodeSplatState(o generation.Offering, input, opaque json.RawMessage, taskID generation.TaskID, attemptID generation.AttemptID) (splatState, map[string]any, error) {
	var state splatState
	p, id, body, err := splatInput(o, input)
	if err != nil || strictUploadedJSON(opaque, &state) != nil || state.Version != 1 || state.Kind != splatPreparationKind || state.TaskID != taskID || state.AttemptID != attemptID || taskID == "" || attemptID == "" || state.SubmissionKey == "" || state.OfferingID != o.ID || state.OfferingRevision != o.Revision || state.InputSHA256 != uploadedDigest(input) || state.Source.Parameter != p.Name || string(state.Source.ID) != id || state.Source.OwnerID == "" || !uploadedHashValid(state.Source.SHA256) || state.Source.Size <= 0 || state.Source.Size > p.MaxBytes || !slices.Contains(p.MediaTypes, state.Source.MediaType) {
		return state, nil, splatProtocolError()
	}
	return state, body, nil
}

func (a *Adapter) resumeSplat(ctx context.Context, request generation.PreparationRequest) (generation.Observation, error) {
	state, body, err := decodeSplatState(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if state.Source.OwnerID != request.OwnerID || state.SubmissionKey != request.SubmissionKey {
		return generation.Observation{}, splatInputError()
	}
	if state.Phase == "upload_dispatching" || state.Phase == "create_dispatching" {
		return generation.Observation{}, submissionUnknown("Tripo Splat 派发结果未知，已停止重发")
	}
	if state.Phase != "token_ready" || !uploadedTokenValid(state.FileToken) || state.RemoteID != "" {
		return generation.Observation{}, splatProtocolError()
	}
	_, _, _, options, err := splatDeclaration(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	state.Phase = "create_dispatching"
	permit, err := splatPermit(ctx, state)
	if err != nil {
		return generation.Observation{}, err
	}
	body["input"] = state.FileToken
	var response envelope[struct {
		TaskID string `json:"task_id"`
	}]
	if err = a.client.DoJSON(ctx, http.MethodPost, options.Endpoint, body, request.SubmissionKey+":create", &response, true); err != nil {
		return generation.Observation{}, err
	}
	if err = businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return generation.Observation{}, err
	}
	if !uploadedTokenValid(response.Data.TaskID) {
		return generation.Observation{}, submissionUnknown("Tripo Splat 创建响应无法确认，已停止重发")
	}
	state.Phase, state.RemoteID = "submitted", response.Data.TaskID
	return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: state.RemoteID}, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(state.RemoteID, "accepted")}, nil
}

func (a *Adapter) observeSplat(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	state, _, err := decodeSplatState(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if state.Phase != "submitted" || state.RemoteID == "" || state.RemoteID != request.Job.RemoteID || request.Job.ProviderID != request.Offering.ProviderID {
		return generation.Observation{}, splatProtocolError()
	}
	_, _, output, options, err := splatDeclaration(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	value, err := a.observeTask(ctx, providerOptions{ObserveEndpoint: options.ObserveEndpoint}, state.RemoteID, string(request.AttemptID))
	if err != nil {
		return generation.Observation{}, err
	}
	if value.TaskID != state.RemoteID || value.Type != "" && value.Type != splatTaskType {
		return generation.Observation{}, splatProtocolError()
	}
	observation, err := a.observation(request, value)
	if err != nil {
		return generation.Observation{}, err
	}
	if len(value.CreditsConsumed) > 0 {
		state.ConsumedCredit = append(json.RawMessage(nil), value.CreditsConsumed...)
	}
	observation.Opaque = state.encode()
	if observation.Phase == generation.UpstreamCompleted {
		if len(observation.Artifacts) != 1 || observation.Artifacts[0].Role != output.Role || observation.Artifacts[0].Variant != output.Variant {
			return generation.Observation{}, splatProtocolError()
		}
		observation.Artifacts[0].MediaType = output.MediaTypes[0]
	}
	return observation, nil
}
