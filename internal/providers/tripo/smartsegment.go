package tripo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	_ "golang.org/x/image/webp"
)

// A Smart Segmentation upload and the paid creation are private mutations of
// one public Generation Attempt. Neither upstream child ID becomes a public Task.
const smartSegmentKind = "smartsegment"

// These are conservative local resource bounds for decoding and /v3/files.
// Tripo documents image upload as 20 MB without defining MB in bytes.
const smartSegmentMaxUploadImageBytes int64 = 20_000_000
const smartSegmentMaxDecodedPixels int64 = 16_000_000

var smartSegmentIdentity = []float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}

type smartSegmentState struct {
	Version          int                      `json:"version"`
	Kind             string                   `json:"kind"`
	Phase            string                   `json:"phase"`
	TaskID           generation.TaskID        `json:"task_id"`
	AttemptID        generation.AttemptID     `json:"attempt_id"`
	OwnerID          generation.OwnerID       `json:"owner_id"`
	SubmissionKey    string                   `json:"submission_key"`
	OfferingID       generation.OfferingID    `json:"offering_id"`
	OfferingRevision string                   `json:"offering_revision"`
	OfferingSHA256   string                   `json:"offering_sha256"`
	InputSHA256      string                   `json:"input_sha256"`
	Source           uploadedConversionSource `json:"source"`
	UploadMediaType  string                   `json:"upload_media_type"`
	UploadSHA256     string                   `json:"upload_sha256"`
	UploadSize       int64                    `json:"upload_size"`
	FileToken        string                   `json:"file_token,omitempty"`
	RemoteID         string                   `json:"remote_id,omitempty"`
	ConsumedCredit   json.RawMessage          `json:"consumed_credit,omitempty"`
	nativeConversionLedger
}

func (s smartSegmentState) encode() json.RawMessage { raw, _ := json.Marshal(s); return raw }
func smartSegmentProtocol(o generation.Offering) bool {
	var value struct {
		Protocol string `json:"protocol"`
	}
	return json.Unmarshal(o.ProviderOptions, &value) == nil && value.Protocol == smartSegmentKind
}
func smartSegmentProtocolError() error {
	return failure(generation.FailureProtocol, false, "Tripo Smart Segmentation 声明、响应或检查点无效")
}
func smartSegmentInputError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo Smart Segmentation 输入参数无效")
}

func smartSegmentIntegrityError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo Smart Segmentation 输入与冻结资产不一致")
}

func smartSegmentParameterError(o generation.Offering, p generation.Parameter, cause error) error {
	return inputParameterRefusal(o, p, cause)
}

func smartSegmentSourceMismatch(o generation.Offering) error {
	return inputRefusal(o, schema.InputRefusalMismatch, schema.InputRefusalValues{},
		"Tripo Smart Segmentation 来源与冻结输入不一致")
}

func smartSegmentTooLarge(o generation.Offering, size, limit int64, diagnostic string) error {
	return inputRefusal(o, schema.InputRefusalTooLarge, schema.InputRefusalValues{
		Size: schema.SizeText(size, true), Limit: schema.SizeText(limit, false),
	}, diagnostic)
}

func smartSegmentMediaType(o generation.Offering, mediaType string, formats []string) error {
	return inputRefusal(o, schema.InputRefusalMediaType, schema.InputRefusalValues{
		MediaType: mediaType, Formats: strings.Join(formats, ", "),
	}, "来源文件的格式不是 Tripo Smart Segmentation 接受的类型")
}

func smartSegmentPixels(o generation.Offering, width, height, limit int64) error {
	return inputRefusal(o, schema.InputRefusalPixels, schema.InputRefusalValues{
		Width: width, Height: height, MaxPixels: limit,
	}, "来源图片的像素超过 Tripo Smart Segmentation 的上限")
}

func smartSegmentInvalid(o generation.Offering, diagnostic string, cause error) error {
	return inputRefusalWithCause(o, schema.InputRefusalInvalid, schema.InputRefusalValues{}, diagnostic, cause)
}

// Two closed shapes exist. The 2026-10-17 snapshot delivers only the segmented
// GLB and mask. The current shape adds the declared private exports of that
// GLB, priced and consented through the same frozen cost declaration; a cost
// without exports (or exports without a cost) is neither shape.
func smartSegmentDeclaration(o generation.Offering) error {
	_, err := smartSegmentOptions(o)
	return err
}

func smartSegmentOptions(o generation.Offering) (providerOptions, error) {
	var options providerOptions
	if o.ID == "" || o.Revision == "" || o.ProviderID == "" || o.IdempotentSubmission ||
		o.Retry.MaxAttempts != 1 || o.PostSuccess != nil ||
		strictUploadedJSON(o.ProviderOptions, &options) != nil ||
		options.Protocol != smartSegmentKind || options.Endpoint != "mesh/smartsegment" ||
		options.UploadEndpoint != "files" || options.ObserveEndpoint != "tasks/{task_id}" ||
		options.Model != "" || options.SourceRig != nil || len(options.ResponseAliases) != 2 {
		return options, smartSegmentProtocolError()
	}
	exports := map[int]bool{}
	controls := 0
	if options.NativeConversion == nil {
		if o.Cost != nil || o.CostPolicy != nil || options.ConvertEndpoint != "" {
			return options, smartSegmentProtocolError()
		}
	} else {
		var err error
		if exports, err = nativeConversionDeclaration(o, options); err != nil || options.NativeConversion.SourceAlias != "seg_model_url" {
			return options, smartSegmentProtocolError()
		}
		controls = 2
	}
	if len(o.Parameters) != 4+controls || len(o.Outputs) != 2+len(exports) {
		return options, smartSegmentProtocolError()
	}
	var mode string
	var source generation.Parameter
	found := map[string]bool{}
	for _, p := range o.Parameters {
		if found[p.Name] || p.Name == "" {
			return options, smartSegmentProtocolError()
		}
		found[p.Name] = true
		if nativeConversionControl(o, options, p.Name) {
			if p.WireName != "" {
				return options, smartSegmentProtocolError()
			}
			continue
		}
		switch p.WireName {
		case "input":
			if source.Name != "" || p.Type != "asset" || !p.Required || p.MaxBytes <= 0 || p.MaxBytes > maximumTripoModelBytes {
				return options, smartSegmentProtocolError()
			}
			source = p
		case "seg_type":
			if p.Name != "seg_type" || p.Type != "select" || !p.Required || !p.Fixed || json.Unmarshal(p.Default, &mode) != nil || len(p.Options) != 1 || p.Options[0].Value != mode {
				return options, smartSegmentProtocolError()
			}
		case "granularity":
			if p.Name != "granularity" || p.Type != "select" || len(p.Options) != 3 || string(p.Default) != `"medium"` ||
				!slices.Equal([]string{p.Options[0].Value, p.Options[1].Value, p.Options[2].Value}, []string{"coarse", "medium", "fine"}) {
				return options, smartSegmentProtocolError()
			}
		case "hint":
			if p.Name != "hint" || p.Type != "text" || p.MaxLength <= 0 || p.MaxLength > 1024 || p.Required {
				return options, smartSegmentProtocolError()
			}
		default:
			return options, smartSegmentProtocolError()
		}
	}
	if source.Name == "" || !found["seg_type"] || !found["granularity"] || !found["hint"] {
		return options, smartSegmentProtocolError()
	}
	switch mode {
	case "image":
		if o.Capability != schema.ImageTo3D || source.Name != "image" || source.MaxBytes > smartSegmentMaxUploadImageBytes || source.Semantic != schema.SemanticReferenceImage ||
			!slices.Equal(source.MediaTypes, []string{"image/png", "image/jpeg", "image/webp"}) ||
			source.UploadedModel != nil || len(options.Transform) != 0 {
			return options, smartSegmentProtocolError()
		}
	case "model":
		if o.Capability != nativeMeshSegmentation || source.Name != "source_asset" || source.Semantic != schema.SemanticSourceAsset ||
			!slices.Equal(source.MediaTypes, []string{"model/gltf-binary"}) ||
			source.UploadedModel == nil || source.UploadedModel.ProfileID == "" ||
			!reflect.DeepEqual(options.Transform, smartSegmentIdentity) {
			return options, smartSegmentProtocolError()
		}
	default:
		return options, smartSegmentProtocolError()
	}
	// Exports were matched to exact required slots by nativeConversionDeclaration.
	// The remaining slots are the segmented GLB (always required) and the mask,
	// which the Schema may enforce only when upstream returns it (minimum 0):
	// the official page shows no model-mode result example.
	expected := map[string]generation.AssetRequirement{}
	for i, output := range o.Outputs {
		if exports[i] {
			continue
		}
		key := output.Role + ":" + output.Variant
		if _, duplicate := expected[key]; duplicate || output.Delivery != schema.AssetDeliveryRequired ||
			output.Minimum == nil || output.MaxBytes <= 0 || len(output.MediaTypes) != 1 {
			return options, smartSegmentProtocolError()
		}
		expected[key] = output
	}
	if model, ok := expected["model:glb"]; !ok || model.MediaTypes[0] != "model/gltf-binary" || *model.Minimum != 1 {
		return options, smartSegmentProtocolError()
	}
	if mask, ok := expected["mask:png"]; !ok || mask.MediaTypes[0] != "image/png" || *mask.Minimum < 0 || *mask.Minimum > 1 ||
		(options.NativeConversion == nil && *mask.Minimum != 1) {
		return options, smartSegmentProtocolError()
	}
	aliases := map[string]string{}
	for _, alias := range options.ResponseAliases {
		key := alias.Role + ":" + alias.Variant
		if alias.Key == "" || alias.ClaimID == "" || len(alias.Fields) != 1 || aliases[key] != "" {
			return options, smartSegmentProtocolError()
		}
		aliases[key] = alias.Fields[0]
	}
	if aliases["model:glb"] != "seg_model_url" || aliases["mask:png"] != "mask_url" {
		return options, smartSegmentProtocolError()
	}
	return options, nil
}

func smartSegmentMaskRequired(o generation.Offering) bool {
	for _, output := range o.Outputs {
		if output.Role == "mask" && output.Variant == "png" {
			return output.Minimum != nil && *output.Minimum > 0
		}
	}
	return true
}

func smartSegmentInput(o generation.Offering, raw json.RawMessage) (generation.Parameter, string, map[string]any, error) {
	options, err := smartSegmentOptions(o)
	if err != nil {
		return generation.Parameter{}, "", nil, err
	}
	var values map[string]json.RawMessage
	if strictUploadedJSON(raw, &values) != nil || values == nil {
		return generation.Parameter{}, "", nil, inputParametersRefusal(o, nil)
	}
	body := map[string]any{}
	var source generation.Parameter
	var id string
	for _, p := range o.Parameters {
		value, exists := values[p.Name]
		delete(values, p.Name)
		if !exists {
			if p.Required || p.Fixed {
				return source, "", nil, smartSegmentParameterError(o, p, nil)
			}
			if p.Name == "granularity" {
				body["granularity"] = "medium"
			}
			continue
		}
		if p.WireName == "input" {
			if err := json.Unmarshal(value, &id); err != nil || !uploadedTokenValid(id) {
				return source, "", nil, smartSegmentParameterError(o, p, err)
			}
			source = p
			continue
		}
		scalar, err := nativeScalar(p, value)
		if err != nil {
			return source, "", nil, smartSegmentParameterError(o, p, err)
		}
		if nativeConversionControl(o, options, p.Name) {
			continue
		}
		body[p.WireName] = scalar
	}
	if len(values) != 0 {
		return source, "", nil, inputParametersRefusal(o, nil)
	}
	if source.Name == "" || id == "" {
		return source, "", nil, inputParametersRefusal(o, nil)
	}
	// The full delivery (segmentation plus every declared export) is consented
	// before the private upload; no paid step runs on a partial approval.
	if options.NativeConversion != nil {
		if err := planPostSuccessConversion(o, raw); err != nil {
			return source, "", nil, err
		}
	}
	return source, id, body, nil
}

// The same exact bytes are checked before Grant and before the private upload.
// Model uploads are streamed through the existing GLB verifier.
type smartSegmentUpload struct {
	asset     generation.InputAsset
	body      io.ReadCloser
	mediaType string
	size      int64
	sha256    string
}

// forUpload is false for the read-only check before Grant: a model is then only
// read where it is stored. The upload makes its private copy of the checked
// bytes (#321 A).
func (a *Adapter) smartSegmentSource(ctx context.Context, request generation.InputValidationRequest, forUpload bool) (smartSegmentUpload, error) {
	source, id, _, err := smartSegmentInput(request.Offering, request.Input)
	if err != nil {
		return smartSegmentUpload{}, err
	}
	assets := request.InputAssets[source.Name]
	if a.inputAssets == nil || request.OwnerID == "" {
		return smartSegmentUpload{}, smartSegmentProtocolError()
	}
	if len(request.InputAssets) != 1 || len(assets) != 1 || len(request.TaskDependencies) != 0 {
		return smartSegmentUpload{}, smartSegmentSourceMismatch(request.Offering)
	}
	asset := assets[0]
	if asset.ID != generation.AssetID(id) || asset.OwnerID != request.OwnerID || asset.StorageKey == "" ||
		asset.Size <= 0 || !uploadedHashValid(asset.SHA256) {
		return smartSegmentUpload{}, smartSegmentSourceMismatch(request.Offering)
	}
	if asset.Size > source.MaxBytes {
		return smartSegmentUpload{}, smartSegmentTooLarge(request.Offering, asset.Size, source.MaxBytes,
			"来源文件 "+schema.SizeText(asset.Size, true)+" 超过 Tripo Smart Segmentation 的上限 "+schema.SizeText(source.MaxBytes, false))
	}
	if !slices.Contains(source.MediaTypes, asset.MediaType) {
		return smartSegmentUpload{}, smartSegmentMediaType(request.Offering, asset.MediaType, source.MediaTypes)
	}
	if request.Offering.Capability == nativeMeshSegmentation {
		if !forUpload {
			if err := a.checkStoredModel(ctx, asset, io.Discard); err != nil {
				return smartSegmentUpload{}, err
			}
			return smartSegmentUpload{asset: asset, mediaType: asset.MediaType, size: asset.Size, sha256: asset.SHA256}, nil
		}
		file, err := a.verifiedUploadedFile(ctx, asset)
		if err != nil {
			return smartSegmentUpload{}, err
		}
		// Unlink while the verified descriptor remains open; no private bytes
		// survive a completed upload or a failed lease.
		_ = os.Remove(file.Name())
		return smartSegmentUpload{asset: asset, body: file, mediaType: asset.MediaType, size: asset.Size, sha256: asset.SHA256}, nil
	}
	r, err := a.inputAssets.Open(ctx, asset)
	if err != nil || r == nil {
		return smartSegmentUpload{}, inputUnavailable(request.Offering, "无法读取来源文件", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(splatContextReader{ctx, r}, asset.Size+1))
	closeErr := r.Close()
	if readErr != nil || closeErr != nil {
		return smartSegmentUpload{}, inputUnavailable(request.Offering, "读取来源文件失败", errors.Join(readErr, closeErr))
	}
	if int64(len(raw)) != asset.Size || uploadedDigest(raw) != asset.SHA256 {
		return smartSegmentUpload{}, smartSegmentIntegrityError()
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || "image/"+format != asset.MediaType || config.Width <= 0 || config.Height <= 0 {
		return smartSegmentUpload{}, smartSegmentInvalid(request.Offering, "来源图片无法按声明的格式解码", err)
	}
	if int64(config.Width)*int64(config.Height) > smartSegmentMaxDecodedPixels {
		return smartSegmentUpload{}, smartSegmentPixels(request.Offering, int64(config.Width), int64(config.Height), smartSegmentMaxDecodedPixels)
	}
	upload := raw
	mediaType := asset.MediaType
	if mediaType == "image/webp" {
		upload, err = smartSegmentPNGFromWebP(request.Offering, raw, smartSegmentMaxUploadImageBytes)
		if err != nil {
			if generation.UserFacingError(err) != nil {
				return smartSegmentUpload{}, err
			}
			return smartSegmentUpload{}, smartSegmentInvalid(request.Offering, "来源图片无法转换为 PNG", err)
		}
		mediaType = "image/png"
	}
	if int64(len(upload)) > smartSegmentMaxUploadImageBytes {
		return smartSegmentUpload{}, smartSegmentTooLarge(request.Offering, int64(len(upload)), smartSegmentMaxUploadImageBytes,
			"转换后的来源图片超过 Tripo Smart Segmentation 的上传上限")
	}
	return smartSegmentUpload{asset: asset, body: io.NopCloser(bytes.NewReader(upload)), mediaType: mediaType, size: int64(len(upload)), sha256: uploadedDigest(upload)}, nil
}

func smartSegmentPNGFromWebP(o generation.Offering, raw []byte, maxBytes int64) ([]byte, error) {
	decoded, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil || format != "webp" {
		return nil, smartSegmentInvalid(o, "来源图片无法解码", err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&limitedSmartSegmentWriter{o: o, buffer: &encoded, remaining: maxBytes, limit: maxBytes}, decoded); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

type limitedSmartSegmentWriter struct {
	o         generation.Offering
	buffer    *bytes.Buffer
	remaining int64
	limit     int64
}

func (w *limitedSmartSegmentWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, smartSegmentTooLarge(w.o, w.limit-w.remaining+int64(len(p)), w.limit, "转换后的来源图片超过 Tripo Smart Segmentation 的上传上限")
	}
	n, err := w.buffer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func smartSegmentPermit(ctx context.Context, state smartSegmentState) (*generation.DispatchReceipt, error) {
	if _, ok := generation.DispatchCheckpointerFromContext(ctx); !ok {
		return nil, failure(generation.FailureRejected, false, "Tripo Smart Segmentation 缺少持久派发许可")
	}
	return generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: state.encode()})
}

func (a *Adapter) startSmartSegment(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	upload, err := a.smartSegmentSource(ctx, generation.InputValidationRequest{
		OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input,
		InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies,
	}, true)
	if err != nil {
		return generation.Observation{}, err
	}
	defer upload.body.Close()
	if request.TaskID == "" || request.AttemptID == "" || request.SubmissionKey == "" {
		return generation.Observation{}, smartSegmentInputError()
	}
	filename := "input.glb"
	if request.Offering.Capability != nativeMeshSegmentation {
		if filename, err = imageUploadFilename(upload.mediaType); err != nil {
			return generation.Observation{}, err
		}
	}
	source, _, _, _ := smartSegmentInput(request.Offering, request.Input)
	state := smartSegmentState{
		Version: 1, Kind: smartSegmentKind, Phase: "upload_dispatching",
		TaskID: request.TaskID, AttemptID: request.AttemptID, OwnerID: request.OwnerID,
		SubmissionKey: request.SubmissionKey, OfferingID: request.Offering.ID,
		OfferingRevision: request.Offering.Revision, OfferingSHA256: nativeOfferingDigest(request.Offering),
		InputSHA256:     uploadedDigest(request.Input),
		Source:          uploadedConversionSource{Parameter: source.Name, ID: upload.asset.ID, OwnerID: upload.asset.OwnerID, SHA256: upload.asset.SHA256, Size: upload.asset.Size, MediaType: upload.asset.MediaType},
		UploadMediaType: upload.mediaType, UploadSHA256: upload.sha256, UploadSize: upload.size,
	}
	permit, err := smartSegmentPermit(ctx, state)
	if err != nil {
		return generation.Observation{}, err
	}
	token, err := a.uploadReader(ctx, request, upload.mediaType, filename, upload.body)
	if err != nil {
		return generation.Observation{}, submissionUnknown("Tripo Smart Segmentation 上传结果未知，已停止重发")
	}
	if !uploadedTokenValid(token) || !strings.HasPrefix(token, "file_") {
		return generation.Observation{}, submissionUnknown("Tripo Smart Segmentation 上传响应无有效 file_token，已停止重发")
	}
	state.Phase, state.FileToken = "token_ready", token
	return generation.Observation{Phase: generation.UpstreamPreparing, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(string(request.AttemptID), "smartsegment_upload_ready")}, nil
}

func decodeSmartSegmentState(o generation.Offering, input, opaque json.RawMessage, taskID generation.TaskID, attemptID generation.AttemptID) (smartSegmentState, map[string]any, error) {
	var state smartSegmentState
	options, optionsErr := smartSegmentOptions(o)
	source, id, body, err := smartSegmentInput(o, input)
	if err == nil {
		err = optionsErr
	}
	if err != nil || strictUploadedJSON(opaque, &state) != nil ||
		state.Version != 1 || state.Kind != smartSegmentKind || state.TaskID != taskID ||
		state.AttemptID != attemptID || taskID == "" || attemptID == "" ||
		state.OwnerID == "" || state.SubmissionKey == "" || state.OfferingID != o.ID ||
		state.OfferingRevision != o.Revision || state.OfferingSHA256 != nativeOfferingDigest(o) ||
		state.InputSHA256 != uploadedDigest(input) || state.Source.Parameter != source.Name ||
		string(state.Source.ID) != id || state.Source.OwnerID != state.OwnerID ||
		state.Source.Size <= 0 || state.Source.Size > source.MaxBytes ||
		!uploadedHashValid(state.Source.SHA256) || !slices.Contains(source.MediaTypes, state.Source.MediaType) ||
		!uploadedHashValid(state.UploadSHA256) || state.UploadSize <= 0 || state.UploadSize > smartSegmentMaxUploadImageBytes && state.Source.MediaType != "model/gltf-binary" ||
		(state.Source.MediaType == "image/webp" && state.UploadMediaType != "image/png") ||
		(state.Source.MediaType != "image/webp" && (state.UploadMediaType != state.Source.MediaType || state.UploadSHA256 != state.Source.SHA256 || state.UploadSize != state.Source.Size)) {
		return state, nil, smartSegmentProtocolError()
	}
	if len(state.Conversions) > 0 && (state.Phase != "submitted" || state.RemoteID == "") ||
		nativeConversionCheckpoint(state.nativeConversionLedger, options, state.RemoteID) != nil {
		return state, nil, smartSegmentProtocolError()
	}
	return state, body, nil
}

func (s *smartSegmentState) conversionRun() *nativeConversionRun {
	return &nativeConversionRun{ledger: &s.nativeConversionLedger, baseRemoteID: s.RemoteID, submissionKey: s.SubmissionKey, encode: func() json.RawMessage { return s.encode() }}
}

func (a *Adapter) resumeSmartSegment(ctx context.Context, request generation.PreparationRequest) (generation.Observation, error) {
	state, body, err := decodeSmartSegmentState(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if state.OwnerID != request.OwnerID || state.SubmissionKey != request.SubmissionKey {
		return generation.Observation{}, smartSegmentInputError()
	}
	if state.Phase == "upload_dispatching" || state.Phase == "create_dispatching" || hasDispatchingRemnant(state.Conversions) {
		return generation.Observation{}, submissionUnknown("Tripo Smart Segmentation 派发结果未知，已停止重发")
	}
	if state.Phase != "token_ready" || !strings.HasPrefix(state.FileToken, "file_") || state.RemoteID != "" {
		return generation.Observation{}, smartSegmentProtocolError()
	}
	var options providerOptions
	if strictUploadedJSON(request.Offering.ProviderOptions, &options) != nil {
		return generation.Observation{}, smartSegmentProtocolError()
	}
	state.Phase = "create_dispatching"
	permit, err := smartSegmentPermit(ctx, state)
	if err != nil {
		return generation.Observation{}, err
	}
	body["input"] = state.FileToken
	if len(options.Transform) == 16 {
		body["transform"] = options.Transform
	}
	var response envelope[struct {
		TaskID string `json:"task_id"`
	}]
	if err := a.client.DoJSON(ctx, http.MethodPost, options.Endpoint, body, request.SubmissionKey+":create", &response, true); err != nil {
		return generation.Observation{}, submissionUnknown("Tripo Smart Segmentation 创建结果未知，已停止重发")
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return generation.Observation{}, err
	}
	if !uploadedTokenValid(response.Data.TaskID) {
		return generation.Observation{}, submissionUnknown("Tripo Smart Segmentation 创建响应缺少 task_id，已停止重发")
	}
	state.Phase, state.RemoteID = "submitted", response.Data.TaskID
	return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: state.RemoteID}, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(state.RemoteID, "accepted")}, nil
}

func (a *Adapter) observeSmartSegment(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	state, _, err := decodeSmartSegmentState(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if request.PostSuccess || state.Phase != "submitted" || state.RemoteID != request.Job.RemoteID ||
		request.Job.ProviderID != request.Offering.ProviderID {
		return generation.Observation{}, smartSegmentProtocolError()
	}
	if hasDispatchingRemnant(state.Conversions) {
		return generation.Observation{}, submissionUnknown("Tripo Smart Segmentation 私有导出派发结果未知，已停止重发")
	}
	options, err := smartSegmentOptions(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	value, err := a.observeTask(ctx, options, state.RemoteID, string(request.AttemptID))
	if err != nil {
		return generation.Observation{}, err
	}
	expectedType := "smartsegment_image"
	if request.Offering.Capability == nativeMeshSegmentation {
		expectedType = "smartsegment_model"
	}
	if value.TaskID != state.RemoteID || value.Type != expectedType {
		return generation.Observation{}, smartSegmentProtocolError()
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
		maskRequired := smartSegmentMaskRequired(request.Offering)
		if len(observation.Artifacts) != 2 && (maskRequired || len(observation.Artifacts) != 1) {
			return generation.Observation{}, smartSegmentProtocolError()
		}
		seen := map[string]bool{}
		for i := range observation.Artifacts {
			claim := &observation.Artifacts[i]
			key := claim.Role + ":" + claim.Variant
			if seen[key] {
				return generation.Observation{}, smartSegmentProtocolError()
			}
			seen[key] = true
			switch key {
			case "model:glb":
				claim.MediaType = "model/gltf-binary"
			case "mask:png":
				claim.MediaType = "image/png"
			default:
				return generation.Observation{}, smartSegmentProtocolError()
			}
		}
		if !seen["model:glb"] || maskRequired && !seen["mask:png"] {
			return generation.Observation{}, smartSegmentProtocolError()
		}
	}
	if options.NativeConversion != nil {
		return a.observeNativeConversions(ctx, request, options, state.conversionRun(), observation)
	}
	return observation, nil
}
