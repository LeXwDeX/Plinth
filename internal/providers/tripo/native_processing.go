package tripo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Native processing is a single public job. Its source is a resolved native
// upstream task. Declared exports remain private children of that same job.
const nativeProcessingKind = "native-processing"

type nativeProcessingState struct {
	rigCheckState
	OwnerID        generation.OwnerID `json:"owner_id"`
	OfferingSHA256 string             `json:"offering_sha256"`
	ConsumedCredit json.RawMessage    `json:"consumed_credit,omitempty"`
	nativeConversionLedger
}

func (s nativeProcessingState) encode() json.RawMessage { raw, _ := json.Marshal(s); return raw }
func nativeProcessingCapability(capability generation.Capability) bool {
	return capability == generation.Retexture || capability == generation.Rigging || capability == generation.Animation || nativeGeometryCapability(capability)
}
func nativeProcessingProtocolError() error {
	return failure(generation.FailureProtocol, false, "Tripo 原生处理声明、响应或检查点无效")
}
func nativeProcessingInputError() error {
	return failure(generation.FailureInvalidInput, false, "Tripo 原生处理输入参数无效")
}

func nativeParameterValueError() error {
	return failure(generation.FailureInvalidInput, false, "参数值无效")
}

func nativeProcessingParameterError(o generation.Offering, p generation.Parameter, cause error) error {
	return inputParameterRefusal(o, p, cause)
}

func nativeProcessingSourceMismatch(o generation.Offering) error {
	return inputRefusal(o, schema.InputRefusalMismatch, schema.InputRefusalValues{},
		"Tripo 原生处理来源与冻结输入不一致")
}
func nativeOfferingDigest(offering generation.Offering) string {
	raw, _ := json.Marshal(offering)
	return uploadedDigest(raw)
}

// Dotted wire names describe object paths (for example texture_prompt.text).
// Paths are built from declared scalars, never arbitrary caller-provided maps.
func setNativeWire(body map[string]any, name string, value any) bool {
	parts := strings.Split(name, ".")
	if len(parts) > 8 {
		return false
	}
	for i, part := range parts {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
		}) {
			return false
		}
		if i == len(parts)-1 {
			if _, exists := body[part]; exists {
				return false
			}
			body[part] = value
			return true
		}
		next, exists := body[part]
		if !exists {
			nested := map[string]any{}
			body[part] = nested
			body = nested
			continue
		}
		nested, ok := next.(map[string]any)
		if !ok {
			return false
		}
		body = nested
	}
	return false
}
func nativeProcessingDeclaration(offering generation.Offering) (generation.Parameter, providerOptions, error) {
	var options providerOptions
	if offering.ID == "" || offering.Revision == "" || offering.ProviderID == "" || offering.IdempotentSubmission || !nativeProcessingCapability(offering.Capability) || strictUploadedJSON(offering.ProviderOptions, &options) != nil || !validRigCheckRoute(options.Endpoint, false) || !validRigCheckRoute(options.ObserveEndpoint, true) || options.UploadEndpoint != "" || len(options.ResponseAliases) == 0 {
		return generation.Parameter{}, options, nativeProcessingProtocolError()
	}
	converted, err := nativeConversionDeclaration(offering, options)
	if err != nil {
		return generation.Parameter{}, options, err
	}
	var source generation.Parameter
	sourceCount := 0
	names := map[string]bool{}
	wires := map[string]any{"model": true}
	for _, p := range offering.Parameters {
		wire := p.WireName
		if wire == "" {
			wire = p.Name
		}
		if p.Name == "" || p.Name == "model" || names[p.Name] || !setNativeWire(wires, wire, true) || len(p.WireTemplate) > 0 || len(p.OmitWhen) > 0 || len(p.ForcedWhen) > 0 {
			return source, options, nativeProcessingProtocolError()
		}
		names[p.Name] = true
		if nativeConversionControl(offering, options, p.Name) {
			continue
		}
		if p.Semantic == generation.SemanticSourceTask {
			source = p
			sourceCount++
			if p.Type != "task" || p.WireName != "input" || !p.Required || !p.SameProvider || p.TaskSourceKind != schema.TaskSourceUpstreamJob || len(p.TaskCapabilities) == 0 || len(p.TaskOfferings) == 0 || p.Fixed || len(p.WireValues) > 0 {
				return source, options, nativeProcessingProtocolError()
			}
			for _, id := range p.TaskOfferings {
				if id == "" || id == offering.ID {
					return source, options, nativeProcessingProtocolError()
				}
			}
			continue
		}
		if p.Semantic == generation.SemanticSourceAsset || (p.Type != "text" && p.Type != "select" && p.Type != "boolean" && p.Type != "number") || p.Fixed && len(p.Default) == 0 {
			return source, options, nativeProcessingProtocolError()
		}
		if len(p.Default) > 0 {
			if _, err := nativeScalar(p, p.Default); err != nil {
				return source, options, nativeProcessingProtocolError()
			}
		}
	}
	if sourceCount != 1 || validateNativeGeometrySource(offering, source) != nil || nativeSourceRigDeclaration(offering, options, source) != nil {
		return source, options, nativeProcessingProtocolError()
	}
	// Each wire alias must resolve to one declared output contract. Required
	// formats remain schema-owned; the materializer checks their actual bytes.
	matched := converted
	requiredModel := false
	for _, alias := range options.ResponseAliases {
		index := -1
		if alias.Key == "" || len(alias.Fields) == 0 {
			return source, options, nativeProcessingProtocolError()
		}
		for i, output := range offering.Outputs {
			if output.Role == alias.Role && output.Variant == alias.Variant {
				if index != -1 {
					return source, options, nativeProcessingProtocolError()
				}
				index = i
			}
		}
		if index < 0 || matched[index] {
			return source, options, nativeProcessingProtocolError()
		}
		matched[index] = true
		output := offering.Outputs[index]
		if output.Role == "model" {
			if len(output.MediaTypes) != 1 || output.Variant == "" {
				return source, options, nativeProcessingProtocolError()
			}
			if output.Minimum != nil && *output.Minimum > 0 {
				requiredModel = true
			}
		}
	}
	for i, output := range offering.Outputs {
		if output.Minimum != nil && *output.Minimum > 0 && !matched[i] {
			return source, options, nativeProcessingProtocolError()
		}
	}
	if !requiredModel {
		return source, options, nativeProcessingProtocolError()
	}
	return source, options, nil
}

func nativeScalar(p generation.Parameter, raw json.RawMessage) (any, error) {
	var value any
	if strictUploadedJSON(raw, &value) != nil || value == nil {
		return nil, nativeParameterValueError()
	}
	switch p.Type {
	case "text", "select", "task":
		text, ok := value.(string)
		if !ok || p.Required && strings.TrimSpace(text) == "" || p.MaxLength > 0 && utf8.RuneCountInString(text) > p.MaxLength {
			return nil, nativeParameterValueError()
		}
		if p.Type == "select" {
			allowed := false
			for _, option := range p.Options {
				allowed = allowed || text == option.Value
			}
			if !allowed {
				return nil, nativeParameterValueError()
			}
		}
	case "string-array":
		items, ok := value.([]any)
		if !ok || len(items) == 0 || len(items) > maximumNativeConversionFormats {
			return nil, nativeParameterValueError()
		}
		seen := map[string]bool{}
		for _, item := range items {
			text, ok := item.(string)
			if !ok || seen[text] {
				return nil, nativeParameterValueError()
			}
			allowed := false
			for _, option := range p.Options {
				allowed = allowed || option.Value == text
			}
			if !allowed {
				return nil, nativeParameterValueError()
			}
			seen[text] = true
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return nil, nativeParameterValueError()
		}
	case "number":
		n, ok := value.(float64)
		if !ok || p.Integer && math.Trunc(n) != n || p.Minimum != nil && n < *p.Minimum || p.Maximum != nil && n > *p.Maximum {
			return nil, nativeParameterValueError()
		}
	default:
		return nil, nativeProcessingProtocolError()
	}
	if p.Fixed {
		var fixed any
		if strictUploadedJSON(p.Default, &fixed) != nil || !reflect.DeepEqual(value, fixed) {
			return nil, nativeParameterValueError()
		}
	}
	if len(p.WireValues) > 0 {
		selected, ok := value.(string)
		mapped, declared := p.WireValues[selected]
		if !ok || !declared || strictUploadedJSON(mapped, &value) != nil {
			return nil, nativeParameterValueError()
		}
		switch value.(type) {
		case string, float64, bool:
		default:
			return nil, nativeProcessingProtocolError()
		}
	}
	return value, nil
}
func nativeProcessingInput(offering generation.Offering, raw json.RawMessage) (generation.Parameter, generation.TaskID, map[string]any, providerOptions, error) {
	source, options, err := nativeProcessingDeclaration(offering)
	if err != nil {
		return source, "", nil, options, err
	}
	var values map[string]json.RawMessage
	if strictUploadedJSON(raw, &values) != nil || values == nil {
		return source, "", nil, options, inputParametersRefusal(offering, nil)
	}
	body := map[string]any{}
	if options.Model != "" {
		body["model"] = options.Model
	}
	var sourceID generation.TaskID
	for _, p := range offering.Parameters {
		raw, present := values[p.Name]
		delete(values, p.Name)
		if !present {
			if p.Required || p.Fixed {
				return source, "", nil, options, nativeProcessingParameterError(offering, p, nil)
			}
			continue
		}
		value, err := nativeScalar(p, raw)
		if err != nil {
			return source, "", nil, options, nativeProcessingParameterError(offering, p, err)
		}
		if nativeConversionControl(offering, options, p.Name) {
			continue
		}
		if p.Name == source.Name {
			id := value.(string)
			if !uploadedTokenValid(id) {
				return source, "", nil, options, nativeProcessingParameterError(offering, p, nil)
			}
			sourceID = generation.TaskID(id)
		}
		wire := p.WireName
		if wire == "" {
			wire = p.Name
		}
		if !setNativeWire(body, wire, value) {
			return source, "", nil, options, nativeProcessingProtocolError()
		}
	}
	if len(values) != 0 {
		return source, "", nil, options, inputParametersRefusal(offering, nil)
	}
	if options.NativeConversion != nil {
		if err := planPostSuccessConversion(offering, raw); err != nil {
			return source, "", nil, options, err
		}
	}
	return source, sourceID, body, options, nil
}
func nativeProcessingSource(request generation.InputValidationRequest) (generation.TaskDependency, map[string]any, providerOptions, error) {
	parameter, id, body, options, err := nativeProcessingInput(request.Offering, request.Input)
	if err != nil {
		return generation.TaskDependency{}, nil, options, err
	}
	if request.OwnerID == "" {
		return generation.TaskDependency{}, nil, options, nativeProcessingProtocolError()
	}
	source, ok := request.TaskDependencies[parameter.Name]
	if !ok || len(request.TaskDependencies) != 1 || len(request.InputAssets) != 0 || source.TaskID != id || source.ProviderID != request.Offering.ProviderID || source.UpstreamJob.ProviderID != source.ProviderID || !uploadedTokenValid(source.UpstreamJob.RemoteID) || !slices.Contains(parameter.TaskOfferings, source.OfferingID) || !slices.Contains(parameter.TaskCapabilities, source.Capability) {
		return source, nil, options, nativeProcessingSourceMismatch(request.Offering)
	}
	if err := verifyNativeSourceRig(request.Offering, options, source); err != nil {
		return source, nil, options, err
	}
	body[parameter.WireName] = source.UpstreamJob.RemoteID
	return source, body, options, nil
}
func (a *Adapter) startNativeProcessing(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	source, body, options, err := nativeProcessingSource(generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies})
	if err != nil {
		return generation.Observation{}, err
	}
	if request.TaskID == "" || request.AttemptID == "" || request.SubmissionKey == "" {
		return generation.Observation{}, nativeProcessingInputError()
	}
	if _, ok := generation.DispatchCheckpointerFromContext(ctx); !ok {
		return generation.Observation{}, failure(generation.FailureRejected, false, "Tripo 原生处理缺少持久执行派发许可")
	}
	state := nativeProcessingState{rigCheckState: rigCheckState{Version: 1, Kind: nativeProcessingKind, Phase: "dispatching", TaskID: request.TaskID, AttemptID: request.AttemptID, OfferingID: request.Offering.ID, OfferingRevision: request.Offering.Revision, SubmissionKey: request.SubmissionKey, InputSHA256: uploadedDigest(request.Input), SourceTaskID: source.TaskID, SourceRemoteID: source.UpstreamJob.RemoteID}, OwnerID: request.OwnerID, OfferingSHA256: nativeOfferingDigest(request.Offering)}
	permit, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: state.encode()})
	if err != nil {
		return generation.Observation{}, err
	}
	var raw json.RawMessage
	if err = a.client.DoJSON(ctx, http.MethodPost, options.Endpoint, body, request.SubmissionKey, &raw, true); err != nil {
		return generation.Observation{}, err
	}
	data, err := a.rigCheckData(raw)
	if err != nil {
		var provider *generation.ProviderError
		if errors.As(err, &provider) && provider.Failure != nil && provider.Failure.Class != generation.FailureProtocol {
			return generation.Observation{Phase: generation.UpstreamFailed, Opaque: state.encode(), Dispatch: permit, Failure: provider.Failure}, nil
		}
		return generation.Observation{}, submissionUnknown("Tripo 原生处理创建响应无法确认，已停止重发")
	}
	if json.Unmarshal(data["task_id"], &state.RemoteID) != nil || !uploadedTokenValid(state.RemoteID) {
		return generation.Observation{}, submissionUnknown("Tripo 原生处理创建响应缺少 task_id")
	}
	state.Phase = "submitted"
	return generation.Observation{Phase: generation.UpstreamAccepted, Job: &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: state.RemoteID}, Opaque: state.encode(), Dispatch: permit, PollAfter: request.Offering.PollInterval, DedupeKey: privateDedupeKey(state.RemoteID, "accepted")}, nil
}
func nativeProcessingCheckpoint(offering generation.Offering, input, opaque json.RawMessage, taskID generation.TaskID, attemptID generation.AttemptID) (nativeProcessingState, providerOptions, error) {
	_, id, _, options, err := nativeProcessingInput(offering, input)
	var state nativeProcessingState
	if err != nil || strictUploadedJSON(opaque, &state) != nil || state.Version != 1 || state.Kind != nativeProcessingKind || state.TaskID != taskID || state.AttemptID != attemptID || taskID == "" || attemptID == "" || state.OwnerID == "" || state.SubmissionKey == "" || state.OfferingID != offering.ID || state.OfferingRevision != offering.Revision || state.OfferingSHA256 != nativeOfferingDigest(offering) || state.InputSHA256 != uploadedDigest(input) || state.SourceTaskID != id || !uploadedTokenValid(state.SourceRemoteID) {
		return state, options, nativeProcessingProtocolError()
	}
	if err := nativeConversionCheckpoint(state.nativeConversionLedger, options, state.RemoteID, state.SourceRemoteID); err != nil {
		return state, options, err
	}
	return state, options, nil
}
func (a *Adapter) observeNativeProcessing(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	state, options, err := nativeProcessingCheckpoint(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if request.PostSuccess || state.Phase != "submitted" || !uploadedTokenValid(state.RemoteID) || state.RemoteID != request.Job.RemoteID || request.Job.ProviderID != request.Offering.ProviderID {
		return generation.Observation{}, nativeProcessingProtocolError()
	}
	if hasDispatchingRemnant(state.Conversions) {
		return generation.Observation{}, submissionUnknown("Tripo 私有转换派发结果未知，已停止重发")
	}
	value, err := a.observeNativeTask(ctx, options, state.RemoteID, string(request.AttemptID))
	if err != nil {
		return generation.Observation{}, err
	}
	observation, err := a.observation(request, value)
	if err != nil {
		return generation.Observation{}, err
	}
	if len(value.CreditsConsumed) > 0 {
		state.ConsumedCredit = bytes.Clone(value.CreditsConsumed)
	}
	observation.Opaque = state.encode()
	observation.CheckpointBaseSHA256 = uploadedDigest(request.Opaque)
	// Generation deduplicates before copying Opaque. Billing evidence can arrive
	// while status/progress stay unchanged, so include the merged checkpoint.
	observation.DedupeKey = privateDedupeKey(observation.DedupeKey, uploadedDigest(observation.Opaque))
	// The declared alias gives the output slot; a URL suffix is insufficient to
	// establish its media type. The asset store verifies the declared bytes.
	for i := range observation.Artifacts {
		claim := &observation.Artifacts[i]
		for _, output := range request.Offering.Outputs {
			if claim.Role == output.Role && claim.Variant == output.Variant && len(output.MediaTypes) == 1 {
				claim.MediaType = output.MediaTypes[0]
			}
		}
	}
	if options.NativeConversion != nil {
		run := &nativeConversionRun{ledger: &state.nativeConversionLedger, baseRemoteID: state.RemoteID, sourceRemoteID: state.SourceRemoteID, submissionKey: state.SubmissionKey, encode: func() json.RawMessage { return state.encode() }}
		return a.observeNativeConversions(ctx, request, options, run, observation)
	}
	return observation, nil
}
func resumeNativeProcessing(request generation.PreparationRequest) (generation.Observation, error) {
	state, _, err := nativeProcessingCheckpoint(request.Offering, request.Input, request.Opaque, request.TaskID, request.AttemptID)
	if err != nil {
		return generation.Observation{}, err
	}
	if state.OwnerID != request.OwnerID || state.SubmissionKey != request.SubmissionKey {
		return generation.Observation{}, nativeProcessingInputError()
	}
	if state.Phase == "dispatching" && state.RemoteID == "" || hasDispatchingRemnant(state.Conversions) {
		return generation.Observation{}, submissionUnknown("Tripo 原生处理派发结果未知，已停止自动重发")
	}
	return generation.Observation{}, nativeProcessingProtocolError()
}
