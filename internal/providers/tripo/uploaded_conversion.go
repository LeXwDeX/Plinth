package tripo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Uploaded conversion has two independent private mutations. Each is performed
// only by the lease that persisted its own intent and received a one-use permit.
// A token is preparation state, never an upstream task identity.
const uploadedConversionKind = "uploaded-model-conversion"
const maximumTripoModelBytes int64 = 150_000_000 // documented v3 /files protocol maximum

type uploadedConversionSource struct {
	Parameter string             `json:"parameter"`
	ID        generation.AssetID `json:"id"`
	OwnerID   generation.OwnerID `json:"owner_id"`
	SHA256    string             `json:"sha256"`
	Size      int64              `json:"size"`
	MediaType string             `json:"media_type"`
}
type uploadedConversionState struct {
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
	Format           string                   `json:"format"`
	WireFormat       string                   `json:"wire_format"`
	FileToken        string                   `json:"file_token,omitempty"`
	RemoteID         string                   `json:"remote_id,omitempty"`
	ConsumedCredit   json.RawMessage          `json:"consumed_credit,omitempty"`
}

func (s uploadedConversionState) encode() json.RawMessage { raw, _ := json.Marshal(s); return raw }
func uploadedDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func uploadedHashValid(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && value == strings.ToLower(value)
}
func uploadedTokenValid(value string) bool {
	return value != "" && len(value) <= 4096 && strings.TrimSpace(value) == value && !isHTTP(value) && !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}
func hasUploadedConversionSource(offering generation.Offering) bool {
	for _, parameter := range offering.Parameters {
		role, source := schema.SourceParameterRole(parameter)
		if source && role == generation.SemanticSourceAsset {
			return true
		}
	}
	return false
}
func uploadedProtocolError() error {
	return failure(generation.FailureProtocol, false, "Tripo 上传模型转换声明或检查点无效")
}
func uploadedInputError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo 上传模型转换输入与冻结来源不一致")
}

// strictUploadedJSON rejects duplicate decoded keys and trailing data before
// typed decoding, including the nested source identity in persisted checkpoints.
func strictUploadedJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return uploadedProtocolError()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	remaining := 4096
	var value func(int) error
	value = func(depth int) error {
		remaining--
		if depth > 16 || remaining < 0 {
			return uploadedProtocolError()
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
					return uploadedProtocolError()
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
			return uploadedProtocolError()
		}
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return uploadedProtocolError()
	}
	typed := json.NewDecoder(bytes.NewReader(raw))
	typed.DisallowUnknownFields()
	return typed.Decode(target)
}
func uploadedConversionInput(offering generation.Offering, raw json.RawMessage) (generation.Parameter, string, string, string, error) {
	var input map[string]json.RawMessage
	if strictUploadedJSON(raw, &input) != nil || len(input) != 2 || len(offering.Parameters) != 2 || offering.Cost == nil {
		return generation.Parameter{}, "", "", "", uploadedInputError()
	}
	var source generation.Parameter
	sourceCount := 0
	for _, parameter := range offering.Parameters {
		role, ok := schema.SourceParameterRole(parameter)
		if ok && role == generation.SemanticSourceAsset {
			source = parameter
			sourceCount++
		}
	}
	if sourceCount != 1 || source.Type != "asset" || source.UploadedModel == nil || source.UploadedModel.ProfileID == "" || !source.UploadedModel.NativeOnly || source.WireName != "input" {
		return generation.Parameter{}, "", "", "", uploadedProtocolError()
	}
	var id string
	if json.Unmarshal(input[source.Name], &id) != nil || strings.TrimSpace(id) == "" {
		return generation.Parameter{}, "", "", "", uploadedInputError()
	}
	var selected, wire string
	for _, parameter := range offering.Parameters {
		if parameter.Name == source.Name {
			continue
		}
		var fixed string
		if parameter.Name != offering.Cost.FormatParameter || parameter.Type != "select" || !parameter.Fixed || parameter.WireName != "format" || json.Unmarshal(parameter.Default, &fixed) != nil || json.Unmarshal(input[parameter.Name], &selected) != nil || selected != fixed {
			return generation.Parameter{}, "", "", "", uploadedInputError()
		}
		wire = selected
		if mapping, ok := parameter.WireValues[selected]; ok && json.Unmarshal(mapping, &wire) != nil {
			return generation.Parameter{}, "", "", "", uploadedProtocolError()
		}
	}
	if wire != "STL" {
		return generation.Parameter{}, "", "", "", uploadedProtocolError()
	}
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	if err := planConversionDispatch(offering, values); err != nil {
		return generation.Parameter{}, "", "", "", err
	}
	return source, id, selected, wire, nil
}
func uploadedRequiredOutput(offering generation.Offering) (generation.AssetRequirement, error) {
	if len(offering.Outputs) != 1 {
		return generation.AssetRequirement{}, uploadedProtocolError()
	}
	output := offering.Outputs[0]
	if output.Role != "model" || output.Variant == "" || output.Minimum == nil || *output.Minimum < 1 || len(output.MediaTypes) != 1 || output.MediaTypes[0] != "model/stl" {
		return generation.AssetRequirement{}, uploadedProtocolError()
	}
	return output, nil
}

func failedUploadedDispatch(state uploadedConversionState, permit *generation.DispatchReceipt, err error, sent bool) (generation.Observation, error) {
	var providerError *generation.ProviderError
	if errors.As(err, &providerError) && providerError.Failure != nil && providerError.Failure.Ambiguous {
		return generation.Observation{}, err
	}
	classified := &generation.Failure{Class: generation.FailureTransport, Retryable: true, SafeDetail: "Tripo 本地来源或私有请求暂不可用"}
	if providerError != nil && providerError.Failure != nil {
		copy := *providerError.Failure
		classified = &copy
	}
	permit.Result = generation.DispatchResultNotDispatched
	if sent {
		permit.Result = generation.DispatchResultDispatched
	}
	return generation.Observation{Phase: generation.UpstreamFailed, Opaque: state.encode(), Dispatch: permit, Failure: classified}, nil
}

// ValidateOffering checks the closed native-processing and uploaded-model
// protocols while retaining historical Tripo generation/task conversions.
func ValidateOffering(offering generation.Offering) error {
	if smartSegmentProtocol(offering) {
		return smartSegmentDeclaration(offering)
	}
	if multiviewCapability(offering.Capability) {
		_, _, err := multiviewDeclaration(offering)
		return err
	}
	if offering.Capability == schema.ImageToSplat {
		_, _, _, _, err := splatDeclaration(offering)
		return err
	}
	if nativeProcessingCapability(offering.Capability) {
		_, _, err := nativeProcessingDeclaration(offering)
		return err
	}
	if offering.Capability == schema.RigCheck {
		_, _, _, err := rigCheckDeclaration(offering)
		return err
	}
	if !hasUploadedConversionSource(offering) {
		return nil
	}
	if offering.Capability != generation.FormatConversion {
		return uploadedProtocolError()
	}
	values := map[string]json.RawMessage{}
	for _, parameter := range offering.Parameters {
		role, source := schema.SourceParameterRole(parameter)
		if source && role == generation.SemanticSourceAsset {
			values[parameter.Name] = json.RawMessage(`"declaration-source"`)
		} else {
			values[parameter.Name] = parameter.Default
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return uploadedProtocolError()
	}
	if _, _, _, _, err := uploadedConversionInput(offering, raw); err != nil {
		return err
	}
	output, err := uploadedRequiredOutput(offering)
	if err != nil {
		return err
	}
	var options providerOptions
	if strictUploadedJSON(offering.ProviderOptions, &options) != nil || options.Endpoint == "" || options.UploadEndpoint == "" || strings.Count(options.ObserveEndpoint, "{task_id}") != 1 || len(options.ResponseAliases) != 1 {
		return uploadedProtocolError()
	}
	alias := options.ResponseAliases[0]
	if alias.Role != output.Role || alias.Variant != output.Variant || len(alias.Fields) == 0 {
		return uploadedProtocolError()
	}
	return nil
}

func requireUploadedPermit(ctx context.Context, state uploadedConversionState) (*generation.DispatchReceipt, error) {
	if _, ok := generation.DispatchCheckpointerFromContext(ctx); !ok {
		return nil, failure(generation.FailureRejected, false, "Tripo 上传模型转换缺少执行派发许可")
	}
	return generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: state.encode()})
}
func (a *Adapter) startUploadedConversion(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	source, id, format, wire, err := uploadedConversionInput(request.Offering, request.Input)
	if err != nil {
		return generation.Observation{}, err
	}
	if _, err := uploadedRequiredOutput(request.Offering); err != nil {
		return generation.Observation{}, err
	}
	options, err := a.optionsFor(request.Offering)
	if err != nil || options.Endpoint == "" || options.UploadEndpoint == "" || strings.Count(options.ObserveEndpoint, "{task_id}") != 1 {
		return generation.Observation{}, uploadedProtocolError()
	}
	assets := request.InputAssets[source.Name]
	if a.inputAssets == nil || request.TaskID == "" || request.AttemptID == "" || request.OwnerID == "" || request.SubmissionKey == "" || len(assets) != 1 || len(request.InputAssets) != 1 || len(request.TaskDependencies) != 0 {
		return generation.Observation{}, uploadedInputError()
	}
	asset := assets[0]
	if string(asset.ID) != id || asset.OwnerID != request.OwnerID || asset.MediaType != "model/gltf-binary" || asset.Size < 28 || asset.Size > maximumTripoModelBytes || !uploadedHashValid(asset.SHA256) {
		return generation.Observation{}, uploadedInputError()
	}
	state := uploadedConversionState{Version: 1, Kind: uploadedConversionKind, Phase: "upload_dispatching", TaskID: request.TaskID, AttemptID: request.AttemptID, SubmissionKey: request.SubmissionKey, OfferingID: request.Offering.ID, OfferingRevision: request.Offering.Revision, InputSHA256: uploadedDigest(request.Input), Format: format, WireFormat: wire, Source: uploadedConversionSource{Parameter: source.Name, ID: asset.ID, OwnerID: asset.OwnerID, SHA256: asset.SHA256, Size: asset.Size, MediaType: asset.MediaType}}
	permit, err := requireUploadedPermit(ctx, state)
	if err != nil {
		return generation.Observation{}, err
	}
	file, err := a.verifiedUploadedFile(ctx, asset)
	if err != nil {
		return failedUploadedDispatch(state, permit, err, false)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	token, err := a.uploadReader(ctx, request, asset.MediaType, "input.glb", file)
	if err != nil {
		return generation.Observation{}, err
	}
	if !uploadedTokenValid(token) {
		return generation.Observation{}, submissionUnknown("Tripo 上传响应缺少有效 file_token")
	}
	state.Phase = "token_ready"
	state.FileToken = token
	return generation.Observation{Phase: generation.UpstreamPreparing, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(string(request.AttemptID), "upload_ready")}, nil
}

// verifiedUploadedFile copies the stored GLB into a private scratch file while
// checking it, so the upload sends exactly the checked bytes. The copy lives in
// the configured scratch directory (the storage volume), never in the
// container's small /tmp (#321 A).
func (a *Adapter) verifiedUploadedFile(ctx context.Context, asset generation.InputAsset) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(a.scratchDir, httpjson.ScratchFilePrefix+"tripo-model-*")
	if err != nil {
		return nil, modelScratchError()
	}
	valid := false
	defer func() {
		if !valid {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	if err := a.checkStoredModel(ctx, asset, file); err != nil {
		return nil, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, modelScratchError()
	}
	valid = true
	return file, nil
}

// checkStoredModel reads the stored GLB once, where it is stored, and checks
// its size, digest and GLB 2.0 header against the frozen evidence. The bytes
// also go to copy (io.Discard for a check without an upload). A refused model
// names its reason; a server that cannot read the file or write its copy
// reports a retryable server condition, never a bad input.
func (a *Adapter) checkStoredModel(ctx context.Context, asset generation.InputAsset, copy io.Writer) error {
	source, err := a.inputAssets.Open(ctx, asset)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return modelReadError()
	}
	defer source.Close()
	sink := &trackedWriter{writer: copy}
	header := &glbHeader{}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(sink, hash, header), io.LimitReader(source, asset.Size+1))
	switch {
	case sink.err != nil:
		return modelScratchError()
	case err != nil:
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return modelReadError()
	case size != asset.Size:
		return failure(generation.FailureInvalidInput, false, "来源模型文件的字节数与登记的不一致，文件可能已损坏或被替换")
	case !header.glb():
		return failure(generation.FailureInvalidInput, false, "来源文件不是 glTF 2.0 二进制（GLB）模型")
	case header.declaredLength() != size:
		return failure(generation.FailureInvalidInput, false, "来源 GLB 文件头声明的长度与实际字节数不一致，文件可能不完整")
	case hex.EncodeToString(hash.Sum(nil)) != asset.SHA256:
		return failure(generation.FailureInvalidInput, false, "来源模型文件的内容与登记的校验值不一致，文件可能已损坏或被替换")
	}
	return ctx.Err()
}

func modelScratchError() error {
	return failure(generation.FailureTransport, true, "服务端暂时无法写入模型文件的上传副本（临时空间不足或不可写），没有提交，可稍后重试")
}

func modelReadError() error {
	return failure(generation.FailureTransport, true, "服务端暂时读不到已保存的来源模型文件，没有提交，可稍后重试")
}

// trackedWriter keeps the error of its own writes apart from read errors.
type trackedWriter struct {
	writer io.Writer
	err    error
}

func (w *trackedWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

// glbHeader keeps the first 12 bytes of the stream.
type glbHeader struct {
	bytes [12]byte
	n     int
}

func (h *glbHeader) Write(p []byte) (int, error) {
	if h.n < len(h.bytes) {
		h.n += copy(h.bytes[h.n:], p)
	}
	return len(p), nil
}

func (h *glbHeader) glb() bool {
	return h.n == len(h.bytes) && string(h.bytes[:4]) == "glTF" && binary.LittleEndian.Uint32(h.bytes[4:]) == 2
}

func (h *glbHeader) declaredLength() int64 { return int64(binary.LittleEndian.Uint32(h.bytes[8:])) }

func decodeUploadedState(offering generation.Offering, input, opaque json.RawMessage, taskID generation.TaskID, attemptID generation.AttemptID) (uploadedConversionState, error) {
	var state uploadedConversionState
	parameter, id, format, wire, err := uploadedConversionInput(offering, input)
	if err != nil || strictUploadedJSON(opaque, &state) != nil {
		return state, uploadedProtocolError()
	}
	if state.Version != 1 || state.Kind != uploadedConversionKind || state.TaskID != taskID || state.AttemptID != attemptID || state.TaskID == "" || state.AttemptID == "" || state.SubmissionKey == "" || state.OfferingID != offering.ID || state.OfferingRevision != offering.Revision || state.InputSHA256 != uploadedDigest(input) || string(state.Source.ID) != id || state.Source.Parameter != parameter.Name || state.Source.OwnerID == "" || state.Source.MediaType != "model/gltf-binary" || state.Source.Size < 28 || state.Source.Size > maximumTripoModelBytes || !uploadedHashValid(state.Source.SHA256) || state.Format != format || state.WireFormat != wire {
		return state, uploadedProtocolError()
	}
	return state, nil
}

// ResumePreparation is a mutation entry, never a polling GET. Generation supplies
// only the persisted lease identity and frozen input; the private token stays opaque.
func (a *Adapter) ResumePreparation(ctx context.Context, request generation.PreparationRequest) (generation.Observation, error) {
	if smartSegmentProtocol(request.Offering) {
		return a.resumeSmartSegment(ctx, request)
	}
	if request.Offering.Capability == schema.ImageToMultiview {
		return a.resumeMultiview(ctx, request)
	}
	if request.Offering.Capability == schema.ImageToSplat {
		return a.resumeSplat(ctx, request)
	}
	if nativeProcessingCapability(request.Offering.Capability) {
		return resumeNativeProcessing(request)
	}
	state, err := decodeUploadedState(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if state.Source.OwnerID != request.OwnerID || state.SubmissionKey != request.SubmissionKey || request.OwnerID == "" {
		return generation.Observation{}, uploadedInputError()
	}
	if state.Phase == "upload_dispatching" || state.Phase == "convert_dispatching" {
		return generation.Observation{}, submissionUnknown("Tripo 私有上传或转换派发结果未知，已停止自动重发")
	}
	if state.Phase != "token_ready" || !uploadedTokenValid(state.FileToken) || state.RemoteID != "" {
		return generation.Observation{}, uploadedProtocolError()
	}
	options, err := a.optionsFor(request.Offering)
	if err != nil || options.Endpoint == "" {
		return generation.Observation{}, uploadedProtocolError()
	}
	state.Phase = "convert_dispatching"
	permit, err := requireUploadedPermit(ctx, state)
	if err != nil {
		return generation.Observation{}, err
	}
	var response envelope[struct {
		TaskID string `json:"task_id"`
	}]
	if err := a.client.DoJSON(ctx, http.MethodPost, options.Endpoint, map[string]string{"input": state.FileToken, "format": state.WireFormat}, request.SubmissionKey+":convert", &response, true); err != nil {
		return generation.Observation{}, err
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return failedUploadedDispatch(state, permit, err, true)
	}
	if !uploadedTokenValid(response.Data.TaskID) {
		return generation.Observation{}, submissionUnknown("Tripo 转换响应缺少 task_id")
	}
	state.Phase = "submitted"
	state.RemoteID = response.Data.TaskID
	return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: state.RemoteID}, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(state.RemoteID, "accepted")}, nil
}
func (a *Adapter) observeUploadedConversion(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	state, err := decodeUploadedState(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if state.Phase != "submitted" || state.RemoteID == "" || state.RemoteID != request.Job.RemoteID || request.Job.ProviderID != request.Offering.ProviderID {
		return generation.Observation{}, uploadedProtocolError()
	}
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.Count(options.ObserveEndpoint, "{task_id}") != 1 {
		return generation.Observation{}, uploadedProtocolError()
	}
	value, err := a.observeTask(ctx, options, state.RemoteID, string(request.AttemptID))
	if err != nil {
		return generation.Observation{}, err
	}
	if value.TaskID != "" && value.TaskID != state.RemoteID {
		return generation.Observation{}, uploadedProtocolError()
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
		output, outputErr := uploadedRequiredOutput(request.Offering)
		if outputErr != nil {
			return generation.Observation{}, outputErr
		}
		if len(observation.Artifacts) != 1 || observation.Artifacts[0].Role != output.Role || observation.Artifacts[0].Variant != output.Variant {
			return generation.Observation{}, uploadedProtocolError()
		}
		observation.Artifacts[0].MediaType = "model/stl"
	}
	return observation, nil
}

var _ generation.ProviderPreparationResumer = (*Adapter)(nil)
