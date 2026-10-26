package tripo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/rigcheckreport"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type rigCheckOptions struct {
	Endpoint        string `json:"endpoint"`
	ObserveEndpoint string `json:"observe_endpoint"`
}
type rigCheckState struct {
	Version          int                   `json:"version"`
	Kind             string                `json:"kind"`
	Phase            string                `json:"phase"`
	TaskID           generation.TaskID     `json:"task_id"`
	AttemptID        generation.AttemptID  `json:"attempt_id"`
	OfferingID       generation.OfferingID `json:"offering_id"`
	OfferingRevision string                `json:"offering_revision"`
	SubmissionKey    string                `json:"submission_key"`
	InputSHA256      string                `json:"input_sha256"`
	SourceTaskID     generation.TaskID     `json:"source_task_id"`
	SourceRemoteID   string                `json:"source_remote_id"`
	RemoteID         string                `json:"remote_id,omitempty"`
}

func (s rigCheckState) encode() json.RawMessage { raw, _ := json.Marshal(s); return raw }
func rigCheckProtocolError() error {
	return failure(generation.FailureProtocol, false, "Tripo Rig Check 声明、报告或检查点无效")
}
func rigCheckInputError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo Rig Check 输入参数无效")
}

func rigCheckParameterError(o generation.Offering, p generation.Parameter, cause error) error {
	return inputParameterRefusal(o, p, cause)
}

func rigCheckSourceMismatch(o generation.Offering) error {
	return inputRefusal(o, schema.InputRefusalMismatch, schema.InputRefusalValues{},
		"Tripo Rig Check 来源与冻结输入不一致")
}

func rigCheckSourceRequired(o generation.Offering) error {
	return inputRefusal(o, schema.InputRefusalRigRequired, schema.InputRefusalValues{},
		"Tripo Rig Check 需要受支持的供应商原生生成任务作为来源")
}
func validRigCheckRoute(value string, observe bool) bool {
	parsed, err := url.Parse(value)
	if err != nil || value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(value, "..") {
		return false
	}
	if observe {
		return strings.Count(value, "{task_id}") == 1
	}
	return !strings.ContainsAny(value, "{}")
}
func rigCheckDeclaration(offering generation.Offering) (generation.Parameter, generation.AssetRequirement, rigCheckOptions, error) {
	var options rigCheckOptions
	if offering.Capability != schema.RigCheck || len(offering.Parameters) != 1 || len(offering.Outputs) != 1 || offering.Retry.MaxAttempts != 1 || offering.EstimatedCredits != 0 || !offering.CreditsMetered || offering.Cost != nil || strictUploadedJSON(offering.ProviderOptions, &options) != nil || !validRigCheckRoute(options.Endpoint, false) || !validRigCheckRoute(options.ObserveEndpoint, true) {
		return generation.Parameter{}, generation.AssetRequirement{}, options, rigCheckProtocolError()
	}
	parameter := offering.Parameters[0]
	if parameter.Type != "task" || parameter.Semantic != generation.SemanticSourceTask || parameter.WireName != "input" || parameter.TaskSourceKind != schema.TaskSourceUpstreamJob || !parameter.SameProvider || !parameter.Required || len(parameter.TaskOfferings) == 0 || len(parameter.TaskCapabilities) == 0 {
		return parameter, generation.AssetRequirement{}, options, rigCheckProtocolError()
	}
	for _, capability := range parameter.TaskCapabilities {
		if capability != generation.TextTo3D && capability != generation.ImageTo3D {
			return parameter, generation.AssetRequirement{}, options, rigCheckProtocolError()
		}
	}
	for _, id := range parameter.TaskOfferings {
		if id == "" || id == offering.ID {
			return parameter, generation.AssetRequirement{}, options, rigCheckProtocolError()
		}
	}
	output := offering.Outputs[0]
	if output.Role != "other" || output.Variant == "" || len(output.MediaTypes) != 1 || output.MediaTypes[0] != rigcheckreport.MediaType || output.Minimum == nil || *output.Minimum != 1 || output.MaxBytes <= 0 || output.MaxBytes > rigcheckreport.MaxBytes {
		return parameter, output, options, rigCheckProtocolError()
	}
	return parameter, output, options, nil
}
func rigCheckSourceID(o generation.Offering, parameter generation.Parameter, raw json.RawMessage) (generation.TaskID, error) {
	var values map[string]json.RawMessage
	if strictUploadedJSON(raw, &values) != nil || len(values) != 1 {
		return "", rigCheckParameterError(o, parameter, nil)
	}
	var id string
	if err := json.Unmarshal(values[parameter.Name], &id); err != nil || !uploadedTokenValid(id) {
		return "", rigCheckParameterError(o, parameter, err)
	}
	return generation.TaskID(id), nil
}
func rigCheckSource(request generation.InputValidationRequest) (generation.TaskDependency, error) {
	parameter, _, _, err := rigCheckDeclaration(request.Offering)
	if err != nil {
		return generation.TaskDependency{}, err
	}
	id, err := rigCheckSourceID(request.Offering, parameter, request.Input)
	if err != nil {
		return generation.TaskDependency{}, err
	}
	if request.OwnerID == "" {
		return generation.TaskDependency{}, rigCheckProtocolError()
	}
	source, ok := request.TaskDependencies[parameter.Name]
	if !ok || len(request.TaskDependencies) != 1 || len(request.InputAssets) != 0 || source.TaskID != id || source.ProviderID != request.Offering.ProviderID || source.UpstreamJob.ProviderID != source.ProviderID || !uploadedTokenValid(source.UpstreamJob.RemoteID) {
		return source, rigCheckSourceMismatch(request.Offering)
	}
	allowedOffering := false
	for _, id := range parameter.TaskOfferings {
		if source.OfferingID == id {
			allowedOffering = true
		}
	}
	allowedCapability := false
	for _, capability := range parameter.TaskCapabilities {
		if source.Capability == capability {
			allowedCapability = true
		}
	}
	if !allowedOffering || !allowedCapability || (source.Capability != generation.TextTo3D && source.Capability != generation.ImageTo3D) {
		return source, rigCheckSourceRequired(request.Offering)
	}
	return source, nil
}

// ValidateInput runs only on inputs already resolved by Generation. The kernel
// owns owner, successful-source and execution-scope checks; this adapter checks
// the closed native-task wire before preparation and again before dispatch.
func (a *Adapter) ValidateInput(ctx context.Context, request generation.InputValidationRequest) error {
	return tripoInputRefusal(request.Offering, a.validateInput(ctx, request))
}

// tripoInputRefusal explains a temporary source-read failure with the
// Offering's Schema copy (#358). Parameter, integrity and protocol failures
// keep their own diagnostic; a UserFacing failure is already done.
func tripoInputRefusal(o generation.Offering, err error) error {
	if err == nil {
		return nil
	}
	var domain *schema.Error
	if errors.As(err, &domain) && domain.UserFacing {
		return err
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		return err
	}
	if provider.Failure.Class != generation.FailureTransport {
		return err
	}
	diagnostic := provider.Failure.SafeDetail
	if diagnostic == "" {
		diagnostic = "Tripo 输入校验失败"
	}
	return generation.ProviderInputRefusalFailure(provider.Failure, o, schema.InputRefusalUnavailable,
		schema.InputRefusalValues{}, diagnostic, provider.Cause)
}

func (a *Adapter) validateInput(ctx context.Context, request generation.InputValidationRequest) error {
	if smartSegmentProtocol(request.Offering) {
		upload, err := a.smartSegmentSource(ctx, request, false)
		if upload.body != nil {
			upload.body.Close()
		}
		return err
	}
	if multiviewCapability(request.Offering.Capability) {
		return a.validateMultiviewInput(ctx, request)
	}
	if request.Offering.Capability == schema.ImageToSplat {
		_, _, _, err := a.splatSource(ctx, request)
		return err
	}
	if nativeProcessingCapability(request.Offering.Capability) {
		_, _, _, err := nativeProcessingSource(request)
		return err
	}
	if request.Offering.Capability != schema.RigCheck {
		return nil
	}
	_, err := rigCheckSource(request)
	return err
}
func (a *Adapter) startRigCheck(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	source, err := rigCheckSource(generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies})
	if err != nil {
		return generation.Observation{}, err
	}
	_, _, options, err := rigCheckDeclaration(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	if request.TaskID == "" || request.AttemptID == "" || request.SubmissionKey == "" {
		return generation.Observation{}, rigCheckInputError()
	}
	if _, ok := generation.DispatchCheckpointerFromContext(ctx); !ok {
		return generation.Observation{}, failure(generation.FailureRejected, false, "Tripo Rig Check 缺少持久执行派发许可")
	}
	state := rigCheckState{Version: 1, Kind: "rig-check", Phase: "dispatching", TaskID: request.TaskID, AttemptID: request.AttemptID, OfferingID: request.Offering.ID, OfferingRevision: request.Offering.Revision, SubmissionKey: request.SubmissionKey, InputSHA256: uploadedDigest(request.Input), SourceTaskID: source.TaskID, SourceRemoteID: source.UpstreamJob.RemoteID}
	permit, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: state.encode()})
	if err != nil {
		return generation.Observation{}, err
	}
	var raw json.RawMessage
	if err = a.client.DoJSON(ctx, http.MethodPost, options.Endpoint, map[string]string{"input": source.UpstreamJob.RemoteID}, request.SubmissionKey, &raw, true); err != nil {
		return generation.Observation{}, err
	}
	data, err := a.rigCheckData(raw)
	if err != nil {
		var provider *generation.ProviderError
		if errors.As(err, &provider) && provider.Failure.Class != generation.FailureProtocol {
			return generation.Observation{Phase: generation.UpstreamFailed, Opaque: state.encode(), Dispatch: permit, Failure: provider.Failure}, nil
		}
		return generation.Observation{}, submissionUnknown("Tripo Rig Check 创建响应无法确认，已停止重发")
	}
	var remoteID string
	if json.Unmarshal(data["task_id"], &remoteID) != nil || !uploadedTokenValid(remoteID) {
		return generation.Observation{}, submissionUnknown("Tripo Rig Check 创建响应缺少 task_id")
	}
	state.Phase = "submitted"
	state.RemoteID = remoteID
	return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: state.RemoteID}, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(state.RemoteID, "accepted")}, nil
}
func rigCheckCheckpoint(request generation.ObserveRequest) (rigCheckState, error) {
	parameter, _, _, err := rigCheckDeclaration(request.Offering)
	if err != nil {
		return rigCheckState{}, err
	}
	id, err := rigCheckSourceID(request.Offering, parameter, request.Input)
	if err != nil {
		return rigCheckState{}, err
	}
	var state rigCheckState
	if strictUploadedJSON(request.Opaque, &state) != nil || state.Version != 1 || state.Kind != "rig-check" || state.Phase != "submitted" || state.TaskID != request.TaskID || state.AttemptID != request.AttemptID || state.TaskID == "" || state.AttemptID == "" || state.SubmissionKey == "" || state.OfferingID != request.Offering.ID || state.OfferingRevision != request.Offering.Revision || state.InputSHA256 != uploadedDigest(request.Input) || state.SourceTaskID != id || !uploadedTokenValid(state.SourceRemoteID) || !uploadedTokenValid(state.RemoteID) || state.RemoteID != request.Job.RemoteID || request.Job.ProviderID != request.Offering.ProviderID {
		return state, rigCheckProtocolError()
	}
	return state, nil
}
func rigCheckOutput(raw json.RawMessage) ([]byte, error) {
	if rigcheckreport.CheckJSONBounds(raw) != nil {
		return nil, rigCheckProtocolError()
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, rigCheckProtocolError()
	}
	// Rebuild only these public fields. Unknown upstream fields (including private
	// URLs and messages) are discarded. Both riggable and rig_type are required;
	// a missing/null boolean cannot become false or invent a recommended type.
	species, present := values["rig_type"]
	if !present {
		return nil, rigCheckProtocolError()
	}
	public := map[string]json.RawMessage{"format": json.RawMessage(`"` + rigcheckreport.Format + `"`), "riggable": values["riggable"], "rig_type": species}
	normalized, err := json.Marshal(public)
	if err != nil {
		return nil, rigCheckProtocolError()
	}
	report, err := rigcheckreport.Decode(normalized)
	if err != nil {
		return nil, rigCheckProtocolError()
	}
	normalized, err = rigcheckreport.Marshal(report)
	if err != nil {
		return nil, rigCheckProtocolError()
	}
	return normalized, nil
}
func (a *Adapter) observeRigCheck(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	state, err := rigCheckCheckpoint(request)
	if err != nil {
		return generation.Observation{}, err
	}
	if request.PostSuccess {
		return generation.Observation{}, rigCheckProtocolError()
	}
	_, output, options, err := rigCheckDeclaration(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	var raw json.RawMessage
	if err = a.client.DoJSON(ctx, http.MethodGet, strings.ReplaceAll(options.ObserveEndpoint, "{task_id}", url.PathEscape(state.RemoteID)), nil, string(request.AttemptID), &raw, false); err != nil {
		return generation.Observation{}, err
	}
	data, err := a.rigCheckData(raw)
	if err != nil {
		return generation.Observation{}, err
	}
	var value task
	if json.Unmarshal(data["task_id"], &value.TaskID) != nil || value.TaskID != state.RemoteID || json.Unmarshal(data["status"], &value.Status) != nil || value.Status == "" {
		return generation.Observation{}, rigCheckProtocolError()
	}
	if progress, exists := data["progress"]; exists {
		if bytes.Equal(bytes.TrimSpace(progress), []byte("null")) || json.Unmarshal(progress, &value.Progress) != nil || value.Progress < 0 || value.Progress > 100 {
			return generation.Observation{}, rigCheckProtocolError()
		}
	}
	observation, err := a.observation(request, value)
	if err != nil {
		return generation.Observation{}, err
	}
	observation.Opaque = bytes.Clone(request.Opaque)
	if observation.Phase == generation.UpstreamCompleted {
		normalized, err := rigCheckOutput(data["output"])
		if err != nil {
			return generation.Observation{}, err
		}
		if int64(len(normalized)) > output.MaxBytes {
			return generation.Observation{}, rigCheckProtocolError()
		}
		observation.Artifacts = []generation.ArtifactClaim{{ID: string(request.AttemptID) + ":rig-check-report", Role: output.Role, Variant: output.Variant, MediaType: rigcheckreport.MediaType, Inline: normalized}}
	}
	return observation, nil
}

// Read exact wire keys after duplicate/budget validation; typed envelope decoding
// alone would erase duplicates, accept case aliases and turn null code into 0.
func (a *Adapter) rigCheckData(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if rigcheckreport.CheckJSONBounds(raw) != nil {
		return nil, rigCheckProtocolError()
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return nil, rigCheckProtocolError()
	}
	var code int
	codeRaw, present := envelope["code"]
	if !present || bytes.Equal(bytes.TrimSpace(codeRaw), []byte("null")) || json.Unmarshal(codeRaw, &code) != nil {
		return nil, rigCheckProtocolError()
	}
	if err := businessEnvelopeError(a.errorMapping, code, ""); err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(envelope["data"], &data) != nil || data == nil {
		return nil, rigCheckProtocolError()
	}
	return data, nil
}
