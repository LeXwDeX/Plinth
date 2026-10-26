package tripo

import (
	"bufio"
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
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var multiviewWireOrder = []string{"front", "left", "back", "right"}

type multiviewOptions struct {
	Endpoint        string            `json:"endpoint"`
	UploadEndpoint  string            `json:"upload_endpoint"`
	ObserveEndpoint string            `json:"observe_endpoint"`
	ResponseType    string            `json:"response_type"`
	ViewOrder       []string          `json:"view_order"`
	ViewPrompts     map[string]string `json:"view_prompts"`
}

type multiviewState struct {
	Version          int                      `json:"version"`
	Kind             string                   `json:"kind"`
	Phase            string                   `json:"phase"`
	TaskID           generation.TaskID        `json:"task_id"`
	AttemptID        generation.AttemptID     `json:"attempt_id"`
	OwnerID          generation.OwnerID       `json:"owner_id"`
	SessionID        string                   `json:"session_id"`
	SubmissionKey    string                   `json:"submission_key"`
	OfferingID       generation.OfferingID    `json:"offering_id"`
	OfferingRevision string                   `json:"offering_revision"`
	InputSHA256      string                   `json:"input_sha256"`
	SourceAsset      uploadedConversionSource `json:"source_asset,omitempty"`
	SourceTaskID     generation.TaskID        `json:"source_task_id,omitempty"`
	SourceRemoteID   string                   `json:"source_remote_id,omitempty"`
	FileToken        string                   `json:"file_token,omitempty"`
	RemoteID         string                   `json:"remote_id,omitempty"`
	ConsumedCredit   json.RawMessage          `json:"consumed_credit,omitempty"`
}

func (s multiviewState) encode() json.RawMessage { b, _ := json.Marshal(s); return b }
func multiviewCapability(c generation.Capability) bool {
	return c == schema.ImageToMultiview || c == schema.EditMultiview
}
func multiviewProtocolError() error {
	return failure(generation.FailureProtocol, false, "Tripo 图片组声明、响应或检查点无效")
}
func multiviewInputError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo 图片组输入参数无效")
}

func multiviewParameterError(o generation.Offering, p generation.Parameter, cause error) error {
	return inputParameterRefusal(o, p, cause)
}

func multiviewParameter(o generation.Offering, name string) generation.Parameter {
	for _, p := range o.Parameters {
		if p.Name == name {
			return p
		}
	}
	return generation.Parameter{Name: name}
}

func multiviewIntegrityError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo 图片组来源图片的字节与登记的不一致")
}

func multiviewSourceMismatch(o generation.Offering) error {
	return inputRefusal(o, schema.InputRefusalMismatch, schema.InputRefusalValues{},
		"Tripo 图片组来源与冻结输入不一致")
}

func multiviewDeclaration(o generation.Offering) (generation.Parameter, multiviewOptions, error) {
	var options multiviewOptions
	if !multiviewCapability(o.Capability) || strictUploadedJSON(o.ProviderOptions, &options) != nil ||
		!validRigCheckRoute(options.Endpoint, false) || !validRigCheckRoute(options.ObserveEndpoint, true) ||
		!slices.Equal(options.ViewOrder, multiviewWireOrder) || len(o.Outputs) != 4 || o.Retry.MaxAttempts != 1 || o.IdempotentSubmission {
		return generation.Parameter{}, options, multiviewProtocolError()
	}
	if o.Capability == schema.ImageToMultiview && (!validRigCheckRoute(options.UploadEndpoint, false) || options.ResponseType != "image_to_multiview" || len(options.ViewPrompts) != 0 || len(o.Parameters) != 1) {
		return generation.Parameter{}, options, multiviewProtocolError()
	}
	if o.Capability == schema.EditMultiview && (options.UploadEndpoint != "" || options.ResponseType != "edit_multiview" || len(options.ViewPrompts) != 4 || len(o.Parameters) != 6) {
		return generation.Parameter{}, options, multiviewProtocolError()
	}
	for i, view := range options.ViewOrder {
		output := o.Outputs[i]
		if output.Role != "image" || output.Variant != view || output.Delivery != schema.AssetDeliveryRequired || output.Minimum == nil || (*output.Minimum != 1 && *output.Minimum != 0) || output.MaxBytes <= 0 || !slices.Contains(output.MediaTypes, "image/jpeg") || !slices.Contains(output.MediaTypes, "image/png") {
			return generation.Parameter{}, options, multiviewProtocolError()
		}
		if o.Capability == schema.EditMultiview && (output.RequiredWhen == nil || output.RequiredWhen.Parameter != options.ViewPrompts[view] || output.RequiredWhen.Operator != "present") {
			return generation.Parameter{}, options, multiviewProtocolError()
		}
	}
	if o.Capability == schema.ImageToMultiview {
		p := o.Parameters[0]
		if p.Type != "asset" || !p.Required || p.Semantic != schema.SemanticReferenceImage || p.WireName != "input" || p.MaxBytes <= 0 || p.MaxBytes > 20_000_000 || len(p.MediaTypes) != 2 || !slices.Contains(p.MediaTypes, "image/png") || !slices.Contains(p.MediaTypes, "image/jpeg") {
			return p, options, multiviewProtocolError()
		}
		return p, options, nil
	}
	var source generation.Parameter
	for _, p := range o.Parameters {
		if p.Type == "task" && p.Semantic == schema.SemanticSourceTask {
			source = p
		}
	}
	if source.Name == "" || source.WireName != "input" || !source.Required || source.TaskSourceKind != schema.TaskSourceUpstreamJob || !source.SameProvider || !source.SameSession || source.TaskSourceBinding != "provider" || !slices.Equal(source.TaskOfferings, []generation.OfferingID{"tripo/image-to-multiview"}) || !slices.Equal(source.TaskCapabilities, []generation.Capability{schema.ImageToMultiview}) {
		return source, options, multiviewProtocolError()
	}
	for _, view := range options.ViewOrder {
		name := options.ViewPrompts[view]
		found := false
		for _, p := range o.Parameters {
			if p.Name == name && p.Type == "text" && !p.Required && p.MaxLength > 0 {
				found = true
			}
		}
		if !found {
			return source, options, multiviewProtocolError()
		}
	}
	return source, options, nil
}

func multiviewInput(o generation.Offering, input json.RawMessage) (generation.Parameter, []map[string]string, generation.TaskID, error) {
	source, options, err := multiviewDeclaration(o)
	if err != nil {
		return source, nil, "", err
	}
	var values map[string]json.RawMessage
	if strictUploadedJSON(input, &values) != nil || values == nil {
		return source, nil, "", inputParametersRefusal(o, nil)
	}
	var sourceID string
	if err := json.Unmarshal(values[source.Name], &sourceID); err != nil || !uploadedTokenValid(sourceID) {
		return source, nil, "", multiviewParameterError(o, source, err)
	}
	if o.Capability == schema.ImageToMultiview {
		if len(values) != 1 {
			return source, nil, "", inputParametersRefusal(o, nil)
		}
		return source, nil, generation.TaskID(sourceID), nil
	}
	for name := range values {
		known := name == source.Name || name == o.ExtraConsentParameter()
		for _, view := range options.ViewOrder {
			known = known || name == options.ViewPrompts[view]
		}
		if !known {
			return source, nil, "", inputParametersRefusal(o, nil)
		}
	}
	var consent bool
	if json.Unmarshal(values[o.ExtraConsentParameter()], &consent) != nil || !consent {
		return source, nil, "", multiviewInputError()
	}
	quote, err := o.EvaluateNativeQuote(input)
	if err != nil || quote == nil {
		return source, nil, "", multiviewInputError()
	}
	prompts := make([]map[string]string, 0, 4)
	for _, view := range options.ViewOrder {
		var prompt string
		if raw := values[options.ViewPrompts[view]]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &prompt); err != nil {
				return source, nil, "", multiviewParameterError(o, multiviewParameter(o, options.ViewPrompts[view]), err)
			}
		}
		if strings.TrimSpace(prompt) != "" {
			maxLength := 0
			for _, parameter := range o.Parameters {
				if parameter.Name == options.ViewPrompts[view] {
					maxLength = parameter.MaxLength
					break
				}
			}
			if maxLength <= 0 || utf8.RuneCountInString(prompt) > maxLength {
				return source, nil, "", multiviewParameterError(o, multiviewParameter(o, options.ViewPrompts[view]), nil)
			}
			prompts = append(prompts, map[string]string{"view": view, "prompt": prompt})
		}
	}
	if len(prompts) == 0 || len(prompts) > 4 {
		return source, nil, "", multiviewInputError()
	}
	return source, prompts, generation.TaskID(sourceID), nil
}

func (a *Adapter) multiviewImage(ctx context.Context, r generation.InputValidationRequest) (generation.InputAsset, []byte, error) {
	p, _, id, err := multiviewInput(r.Offering, r.Input)
	if err != nil {
		return generation.InputAsset{}, nil, err
	}
	assets := r.InputAssets[p.Name]
	if a.inputAssets == nil || r.OwnerID == "" {
		return generation.InputAsset{}, nil, multiviewProtocolError()
	}
	if len(r.InputAssets) != 1 || len(assets) != 1 || len(r.TaskDependencies) != 0 {
		return generation.InputAsset{}, nil, multiviewSourceMismatch(r.Offering)
	}
	asset := assets[0]
	if asset.OwnerID != r.OwnerID || string(asset.ID) != string(id) || asset.StorageKey == "" || asset.Size <= 0 || !uploadedHashValid(asset.SHA256) {
		return asset, nil, multiviewSourceMismatch(r.Offering)
	}
	if asset.Size > p.MaxBytes {
		return asset, nil, inputRefusal(r.Offering, schema.InputRefusalTooLarge, schema.InputRefusalValues{
			Size: schema.SizeText(asset.Size, true), Limit: schema.SizeText(p.MaxBytes, false),
		}, "Tripo 图片组来源图片超过上限")
	}
	if !slices.Contains(p.MediaTypes, asset.MediaType) {
		return asset, nil, inputRefusal(r.Offering, schema.InputRefusalMediaType, schema.InputRefusalValues{
			MediaType: asset.MediaType, Formats: strings.Join(p.MediaTypes, ", "),
		}, "Tripo 图片组来源图片格式不受支持")
	}
	body, err := a.inputAssets.Open(ctx, asset)
	if err != nil || body == nil {
		return asset, nil, inputUnavailable(r.Offering, "无法读取 Tripo 图片组来源图片", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(splatContextReader{ctx, body}, asset.Size+1))
	closeErr := body.Close()
	if ctx.Err() != nil {
		return asset, nil, ctx.Err()
	}
	if readErr != nil || closeErr != nil {
		return asset, nil, inputUnavailable(r.Offering, "读取 Tripo 图片组来源图片失败", errors.Join(readErr, closeErr))
	}
	if int64(len(raw)) != asset.Size || uploadedDigest(raw) != asset.SHA256 {
		return asset, nil, multiviewIntegrityError()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || "image/"+format != asset.MediaType || cfg.Width <= 0 || cfg.Height <= 0 {
		return asset, nil, inputRefusal(r.Offering, schema.InputRefusalInvalid, schema.InputRefusalValues{},
			"Tripo 图片组来源图片无法按声明的格式解码")
	}
	return asset, raw, nil
}

func multiviewTaskSource(r generation.InputValidationRequest) (generation.TaskDependency, []map[string]string, error) {
	p, prompts, id, err := multiviewInput(r.Offering, r.Input)
	if err != nil {
		return generation.TaskDependency{}, nil, err
	}
	if r.OwnerID == "" || r.SessionID == "" {
		return generation.TaskDependency{}, nil, multiviewProtocolError()
	}
	d, ok := r.TaskDependencies[p.Name]
	if !ok || len(r.TaskDependencies) != 1 || len(r.InputAssets) != 0 || d.SessionID != r.SessionID || d.TaskID != id || d.ProviderID != r.Offering.ProviderID || d.UpstreamJob.ProviderID != d.ProviderID || !uploadedTokenValid(d.UpstreamJob.RemoteID) || d.Capability != schema.ImageToMultiview || d.OfferingID != "tripo/image-to-multiview" {
		return d, nil, multiviewSourceMismatch(r.Offering)
	}
	var binding struct {
		TaskID    generation.TaskID    `json:"task_id"`
		AttemptID generation.AttemptID `json:"attempt_id"`
		RemoteID  string               `json:"remote_id"`
	}
	if strictUploadedJSON(d.ProviderBinding, &binding) != nil || binding.TaskID != d.TaskID || binding.AttemptID == "" || binding.RemoteID != d.UpstreamJob.RemoteID {
		return d, nil, multiviewSourceMismatch(r.Offering)
	}
	return d, prompts, nil
}

func (a *Adapter) validateMultiviewInput(ctx context.Context, r generation.InputValidationRequest) error {
	if r.Offering.Capability == schema.ImageToMultiview {
		_, _, err := a.multiviewImage(ctx, r)
		return err
	}
	_, _, err := multiviewTaskSource(r)
	return err
}

func multiviewPermit(ctx context.Context, s multiviewState) (*generation.DispatchReceipt, error) {
	if _, ok := generation.DispatchCheckpointerFromContext(ctx); !ok {
		return nil, failure(generation.FailureRejected, false, "Tripo 图片组缺少持久派发许可")
	}
	return generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: s.encode()})
}

func (a *Adapter) startMultiview(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	if r.TaskID == "" || r.AttemptID == "" || r.SubmissionKey == "" {
		return generation.Observation{}, multiviewInputError()
	}
	s := multiviewState{Version: 1, Kind: string(r.Offering.Capability), TaskID: r.TaskID, AttemptID: r.AttemptID, OwnerID: r.OwnerID, SessionID: r.SessionID, SubmissionKey: r.SubmissionKey, OfferingID: r.Offering.ID, OfferingRevision: r.Offering.Revision, InputSHA256: uploadedDigest(r.Input)}
	if r.Offering.Capability == schema.ImageToMultiview {
		asset, raw, err := a.multiviewImage(ctx, generation.InputValidationRequest{OwnerID: r.OwnerID, SessionID: r.SessionID, Offering: r.Offering, Input: r.Input, InputAssets: r.InputAssets, TaskDependencies: r.TaskDependencies})
		if err != nil {
			return generation.Observation{}, err
		}
		filename, err := imageUploadFilename(asset.MediaType)
		if err != nil {
			return generation.Observation{}, err
		}
		s.SourceAsset = uploadedConversionSource{Parameter: r.Offering.Parameters[0].Name, ID: asset.ID, OwnerID: asset.OwnerID, SHA256: asset.SHA256, Size: asset.Size, MediaType: asset.MediaType}
		s.Phase = "upload_dispatching"
		permit, err := multiviewPermit(ctx, s)
		if err != nil {
			return generation.Observation{}, err
		}
		token, err := a.uploadReader(ctx, r, asset.MediaType, filename, io.NopCloser(bytes.NewReader(raw)))
		if err != nil {
			return generation.Observation{}, err
		}
		if !uploadedTokenValid(token) {
			return generation.Observation{}, submissionUnknown("Tripo 图片组上传响应无法确认，已停止重发")
		}
		s.Phase, s.FileToken = "token_ready", token
		return generation.Observation{Phase: generation.UpstreamPreparing, Opaque: s.encode(), Dispatch: permit, PollAfter: r.Offering.PollInterval, DedupeKey: privateDedupeKey(string(r.AttemptID), "multiview_upload_ready")}, nil
	}
	source, prompts, err := multiviewTaskSource(generation.InputValidationRequest{OwnerID: r.OwnerID, SessionID: r.SessionID, Offering: r.Offering, Input: r.Input, InputAssets: r.InputAssets, TaskDependencies: r.TaskDependencies})
	if err != nil {
		return generation.Observation{}, err
	}
	options := multiviewOptions{}
	_ = json.Unmarshal(r.Offering.ProviderOptions, &options)
	s.SourceTaskID, s.SourceRemoteID = source.TaskID, source.UpstreamJob.RemoteID
	s.Phase = "create_dispatching"
	permit, err := multiviewPermit(ctx, s)
	if err != nil {
		return generation.Observation{}, err
	}
	return a.createMultiview(ctx, r.Offering, options, s, permit, map[string]any{"input": source.UpstreamJob.RemoteID, "prompts": prompts})
}

func (a *Adapter) createMultiview(ctx context.Context, o generation.Offering, options multiviewOptions, s multiviewState, permit *generation.DispatchReceipt, body map[string]any) (generation.Observation, error) {
	var response envelope[struct {
		TaskID string `json:"task_id"`
		Type   string `json:"type"`
		Status string `json:"status"`
	}]
	if err := a.client.DoJSON(ctx, http.MethodPost, options.Endpoint, body, s.SubmissionKey+":create", &response, true); err != nil {
		return generation.Observation{}, err
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return generation.Observation{}, err
	}
	if !uploadedTokenValid(response.Data.TaskID) || response.Data.Type != "" && response.Data.Type != options.ResponseType {
		return generation.Observation{}, submissionUnknown("Tripo 图片组创建响应无法确认，已停止重发")
	}
	s.Phase, s.RemoteID = "submitted", response.Data.TaskID
	return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: o.ProviderID, RemoteID: s.RemoteID}, Opaque: s.encode(), Dispatch: permit, PollAfter: o.PollInterval, DedupeKey: privateDedupeKey(s.RemoteID, "accepted")}, nil
}

func (a *Adapter) resumeMultiview(ctx context.Context, r generation.PreparationRequest) (generation.Observation, error) {
	s, options, err := decodeMultiviewState(r.Offering, r.Input, r.Opaque, r.TaskID, r.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if s.OwnerID != r.OwnerID || s.SubmissionKey != r.SubmissionKey {
		return generation.Observation{}, multiviewInputError()
	}
	if s.Phase == "upload_dispatching" || s.Phase == "create_dispatching" {
		return generation.Observation{}, submissionUnknown("Tripo 图片组派发结果未知，已停止重发")
	}
	if r.Offering.Capability != schema.ImageToMultiview {
		return generation.Observation{}, multiviewProtocolError()
	}
	if s.Phase != "token_ready" || !uploadedTokenValid(s.FileToken) || s.RemoteID != "" {
		return generation.Observation{}, multiviewProtocolError()
	}
	s.Phase = "create_dispatching"
	permit, err := multiviewPermit(ctx, s)
	if err != nil {
		return generation.Observation{}, err
	}
	return a.createMultiview(ctx, r.Offering, options, s, permit, map[string]any{"input": s.FileToken})
}

func decodeMultiviewState(o generation.Offering, input, opaque json.RawMessage, taskID generation.TaskID, attemptID generation.AttemptID) (multiviewState, multiviewOptions, error) {
	_, options, err := multiviewDeclaration(o)
	var s multiviewState
	if err != nil || strictUploadedJSON(opaque, &s) != nil || s.Version != 1 || s.Kind != string(o.Capability) || s.TaskID != taskID || s.AttemptID != attemptID || taskID == "" || attemptID == "" || s.OwnerID == "" || s.SubmissionKey == "" || s.OfferingID != o.ID || s.OfferingRevision != o.Revision || s.InputSHA256 != uploadedDigest(input) {
		return s, options, multiviewProtocolError()
	}
	if o.Capability == schema.ImageToMultiview {
		p, _, id, err := multiviewInput(o, input)
		if err != nil || s.SourceAsset.ID != generation.AssetID(id) || s.SourceAsset.Parameter != p.Name || s.SourceAsset.OwnerID != s.OwnerID || !uploadedHashValid(s.SourceAsset.SHA256) || s.SourceAsset.Size <= 0 || s.SourceAsset.Size > p.MaxBytes {
			return s, options, multiviewProtocolError()
		}
	} else {
		p, _, id, err := multiviewInput(o, input)
		if err != nil || s.SourceTaskID != id || p.Name == "" || !uploadedTokenValid(s.SourceRemoteID) {
			return s, options, multiviewProtocolError()
		}
	}
	return s, options, nil
}

func (a *Adapter) observeMultiview(ctx context.Context, r generation.ObserveRequest) (generation.Observation, error) {
	s, options, err := decodeMultiviewState(r.Offering, r.Input, r.Opaque, r.TaskID, r.AttemptID)
	if err != nil || s.Phase != "submitted" || s.RemoteID != r.Job.RemoteID || r.Job.ProviderID != r.Offering.ProviderID {
		return generation.Observation{}, multiviewProtocolError()
	}
	value, err := a.observeTask(ctx, providerOptions{ObserveEndpoint: options.ObserveEndpoint}, s.RemoteID, string(r.AttemptID))
	if err != nil {
		return generation.Observation{}, err
	}
	if value.TaskID != s.RemoteID || value.Type != "" && value.Type != options.ResponseType {
		return generation.Observation{}, multiviewProtocolError()
	}
	obs, err := a.observation(r, value)
	if err != nil {
		return generation.Observation{}, err
	}
	if len(value.CreditsConsumed) > 0 {
		s.ConsumedCredit = append(json.RawMessage(nil), value.CreditsConsumed...)
	}
	obs.Opaque = s.encode()
	if obs.Phase == generation.UpstreamCompleted {
		_, selected, _, err := multiviewInput(r.Offering, r.Input)
		if err != nil {
			return generation.Observation{}, err
		}
		wanted := options.ViewOrder
		if r.Offering.Capability == schema.EditMultiview {
			wanted = make([]string, 0, len(selected))
			for _, p := range selected {
				wanted = append(wanted, p["view"])
			}
		}
		obs.Artifacts, err = multiviewClaims(value.Output, r.AttemptID, wanted)
		if err != nil {
			return generation.Observation{}, err
		}
	}
	return obs, nil
}

func multiviewClaims(output map[string]any, attemptID generation.AttemptID, wanted []string) ([]generation.ArtifactClaim, error) {
	inner := output
	if nested, exists := output["generate_multiview_image"]; exists {
		converted, ok := nested.(map[string]any)
		if !ok {
			return nil, multiviewProtocolError()
		}
		for key := range output {
			if strings.HasSuffix(key, "_view_url") {
				return nil, multiviewProtocolError()
			}
		}
		inner = converted
	}
	wantSet := map[string]bool{}
	for _, view := range wanted {
		wantSet[view] = true
	}
	for key := range inner {
		if !strings.HasSuffix(key, "_view_url") {
			continue
		}
		view := strings.TrimSuffix(key, "_view_url")
		if !wantSet[view] {
			return nil, multiviewProtocolError()
		}
	}
	claims := make([]generation.ArtifactClaim, 0, len(wanted))
	seen := map[string]bool{}
	for _, view := range wanted {
		locator, ok := inner[view+"_view_url"].(string)
		if !ok || !strings.HasPrefix(locator, "https://") || seen[locator] {
			return nil, multiviewProtocolError()
		}
		seen[locator] = true
		claims = append(claims, generation.ArtifactClaim{ID: string(attemptID) + ":view:" + view, Role: "image", Variant: view, Locator: locator})
	}
	return claims, nil
}

type bufferedAsset struct {
	io.Reader
	io.Closer
}

func (a *Adapter) openMultiviewAsset(ctx context.Context, r generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	body, meta, err := httpjson.OpenClaim(ctx, a.client.HTTPClient, r.Claim, r.Offering.AssetHosts, a.allowHTTPAssets)
	if err != nil {
		return nil, meta, err
	}
	buffer := bufio.NewReader(body)
	peek, _ := buffer.Peek(512)
	media := http.DetectContentType(peek)
	if media != "image/jpeg" && media != "image/png" {
		_ = body.Close()
		return nil, generation.AssetMetadata{}, multiviewProtocolError()
	}
	meta.MediaType = media
	return bufferedAsset{Reader: buffer, Closer: body}, meta, nil
}

// ResolveDependencyBinding is read-only evidence for Generation's exact
// owner/session-scoped upstream-job source. Generation verifies these stored
// bytes before invoking this method.
func (a *Adapter) ResolveDependencyBinding(r generation.DependencyBindingRequest) (json.RawMessage, error) {
	if r.Offering.Capability == schema.Rigging {
		return nativeRigDependencyBinding(r)
	}
	if r.Offering.Capability != schema.ImageToMultiview || r.TaskID == "" || r.AttemptID == "" || r.Job.ProviderID != r.Offering.ProviderID || !uploadedTokenValid(r.Job.RemoteID) {
		return nil, multiviewProtocolError()
	}
	seen := map[string]bool{}
	for _, asset := range r.Assets {
		if asset.Role == "image" && slices.Contains(multiviewWireOrder, asset.Variant) && asset.Required && asset.Size > 0 && uploadedHashValid(asset.SHA256) {
			seen[asset.Variant] = true
		}
	}
	if len(seen) != 4 {
		return nil, multiviewProtocolError()
	}
	b, _ := json.Marshal(map[string]any{"task_id": r.TaskID, "attempt_id": r.AttemptID, "remote_id": r.Job.RemoteID})
	return b, nil
}
