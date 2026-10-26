package tripo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// New fixed-format Offerings hold the public task in RUNNING until every
// authorized private conversion has a downloadable result. Historical
// Offerings retain their post-success conversion lifecycle.
func preCompletionFormats(offering generation.Offering) bool {
	if offering.PostSuccess != nil || offering.PriceSelectionParameter() == "" {
		return false
	}
	for _, output := range offering.Outputs {
		if output.Role == "converted_model" && output.Delivery == generation.AssetDeliveryRequired &&
			output.Variant != "" && output.Minimum != nil && *output.Minimum == 1 {
			return true
		}
	}
	return false
}

// Conversion formats are wire enum values; the exact public variant and role
// come from the frozen output contract, not a second Provider format table.
func preCompletionOutput(offering generation.Offering, format string) (generation.AssetRequirement, bool) {
	want := "." + strings.ToLower(format)
	var matched generation.AssetRequirement
	found := false
	for _, output := range offering.Outputs {
		if output.Role != "converted_model" || output.Delivery != generation.AssetDeliveryRequired ||
			output.Minimum == nil || *output.Minimum != 1 || !strings.HasSuffix(output.Variant, want) {
			continue
		}
		if found {
			return generation.AssetRequirement{}, false
		}
		matched, found = output, true
	}
	return matched, found
}

func validatePreCompletionInput(offering generation.Offering, input json.RawMessage) ([]string, error) {
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil || offering.PriceSelectionParameter() == "" {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 全格式输入无效")
	}
	var formats []string
	if json.Unmarshal(values[offering.PriceSelectionParameter()], &formats) != nil {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 全格式输入无效")
	}
	required := 0
	for _, output := range offering.Outputs {
		if output.Role == "converted_model" && output.Delivery == generation.AssetDeliveryRequired &&
			output.Variant != "" && output.Minimum != nil && *output.Minimum == 1 {
			required++
		}
	}
	if required == 0 || len(formats) != required {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 必须请求全部声明的转换格式")
	}
	seen := make(map[string]bool, len(formats))
	for _, format := range formats {
		if seen[format] || !conversionSelectionDeclared(offering, format) {
			return nil, failure(generation.FailureInvalidInput, false, "Tripo 转换格式重复或不受支持")
		}
		seen[format] = true
		if _, found := preCompletionOutput(offering, format); !found {
			return nil, failure(generation.FailureProtocol, false, "Tripo 转换输出契约不完整")
		}
	}
	return formats, nil
}

func conversionSelectionDeclared(offering generation.Offering, format string) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Name == offering.PriceSelectionParameter() {
			for _, option := range parameter.Options {
				if option.Value == format {
					return true
				}
			}
			return false
		}
	}
	return false
}

func (a *Adapter) observePreCompletionFormats(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.Count(options.ObserveEndpoint, "{task_id}") != 1 || options.ConvertEndpoint == "" {
		return generation.Observation{}, failure(generation.FailureProtocol, false, "Tripo Offering 缺少转换端点")
	}
	baseTask, err := a.observeTask(ctx, options, request.Job.RemoteID, string(request.AttemptID))
	if err != nil {
		return generation.Observation{}, err
	}
	base, err := a.observation(request, baseTask)
	if err != nil {
		return base, err
	}
	// Generation replaces opaque state on each accepted observation. A source
	// regression must retain confirmed private conversions and dispatch intent.
	base.Opaque = request.Opaque
	if base.Phase != generation.UpstreamCompleted {
		return base, nil
	}
	selections, err := validatePreCompletionInput(request.Offering, request.Input)
	if err != nil {
		return generation.Observation{}, err
	}
	state := parsePreCompletionState(request.Opaque)
	if hasDispatchingRemnant(state.Conversions) {
		return preCompletionFailure(base, state, "Tripo 转换提交结果不明，已停止再次付费"), nil
	}
	if format, item, found := inflightConversion(state.Conversions); found {
		child, err := a.observeTask(ctx, options, item.RemoteID, string(request.AttemptID)+":convert:"+format)
		if err != nil {
			return generation.Observation{}, err
		}
		if _, known := declaredStatusRule(a.statusMapping, strings.ToLower(strings.TrimSpace(child.Status))); !known {
			return generation.Observation{}, failure(generation.FailureProtocol, false, "Tripo 转换返回未知任务状态")
		}
		phase, _ := conversionChildPhase(a.statusMapping, child.Status)
		switch phase {
		case generation.UpstreamCompleted:
			locator := firstHTTPOutput(child.Output, "model_url", "model")
			if locator == "" {
				return preCompletionFailure(base, state, "Tripo 转换缺少模型产物"), nil
			}
			item.Status = conversionItemDone
			item.Locator = locator
			item.CreditsConsumed = child.CreditsConsumed
			state.Conversions[format] = item
		case generation.UpstreamFailed, generation.UpstreamCanceled:
			return preCompletionFailure(base, state, "Tripo 格式转换失败"), nil
		}
		return preCompletionRunning(base, state), nil
	}
	remaining := unstartedFormats(selections, state.Conversions)
	if len(remaining) > 0 {
		if err := planPostSuccessConversion(request.Offering, request.Input); err != nil {
			return generation.Observation{}, err
		}
		format := remaining[0]
		retries := state.Conversions[format].RateLimitRetries
		state.Conversions[format] = conversionItem{Status: conversionItemDispatching, RateLimitRetries: retries}
		submissionKey := string(request.AttemptID) + ":convert:" + format
		permit, err := privateDispatchPermit(ctx, submissionKey, dispatchIntentStage{
			Stage: "models/convert",
			Details: map[string]any{
				"format":      format,
				"source_task": request.Job.RemoteID,
				"conversions": state.Conversions,
			},
		})
		if err != nil {
			return generation.Observation{}, err
		}
		remoteID, err := a.convertModel(ctx, options.ConvertEndpoint, request.Job.RemoteID, format, submissionKey)
		if err != nil {
			// An explicit 429 refused the child: settle the permit as not
			// dispatched and resend the same format after Retry-After or the
			// backoff. Unknown results still stop here without a resend.
			if retryAfter, refused := explicitRateLimit(err); refused && permit != nil {
				permit.Result = generation.DispatchResultNotDispatched
				wait, exhausted := recordConversionRefusal(state.Conversions, format, retryAfter)
				if exhausted {
					observation := preCompletionFailure(base, state, "Tripo 格式转换持续被限流，有限重试已用尽")
					observation.Failure = conversionRateLimitFailure(observation.Failure.SafeDetail)
					observation.Dispatch = permit
					return observation, nil
				}
				observation := preCompletionRunning(base, state)
				observation.PollAfter = wait
				observation.Dispatch = permit
				return observation, nil
			}
			return generation.Observation{}, err
		}
		state.Conversions[format] = conversionItem{Status: conversionItemSubmitted, RemoteID: remoteID, RateLimitRetries: retries}
		observation := preCompletionRunning(base, state)
		observation.Dispatch = permit
		return observation, nil
	}
	// A completed private conversion may have a short-lived signed URL. Refresh
	// every child with read-only GET before handing all claims to materialization.
	// Tripo's source GLB can contain upstream URLs in its metadata. The GLTF
	// conversion is a self-contained binary glTF, so use that conversion for
	// the primary model claim as well as its declared converted-model claim.
	var primaryModel generation.ArtifactClaim
	retained := make([]generation.ArtifactClaim, 0, len(base.Artifacts))
	for _, claim := range base.Artifacts {
		if claim.Role == "model" {
			if primaryModel.ID == "" {
				primaryModel = claim
			}
			continue
		}
		retained = append(retained, claim)
	}
	if primaryModel.ID == "" {
		return generation.Observation{}, failure(generation.FailureProtocol, false, "Tripo 源模型产物缺失")
	}
	base.Artifacts = retained
	formats := append([]string(nil), selections...)
	sort.Strings(formats)
	for _, format := range formats {
		item := state.Conversions[format]
		if item.Status != conversionItemDone || item.RemoteID == "" {
			return preCompletionFailure(base, state, "Tripo 转换产物不完整"), nil
		}
		child, err := a.observeTask(ctx, options, item.RemoteID, string(request.AttemptID)+":convert:"+format+":refresh")
		if err != nil {
			return generation.Observation{}, err
		}
		phase, _ := conversionChildPhase(a.statusMapping, child.Status)
		if phase != generation.UpstreamCompleted {
			return preCompletionFailure(base, state, "Tripo 转换产物无法确认"), nil
		}
		locator := firstHTTPOutput(child.Output, "model_url", "model")
		if locator == "" {
			return preCompletionFailure(base, state, "Tripo 转换缺少模型产物"), nil
		}
		item.Locator = locator
		state.Conversions[format] = item
		requirement, _ := preCompletionOutput(request.Offering, format)
		base.Artifacts = append(base.Artifacts, generation.ArtifactClaim{
			ID:        "convert:" + format,
			Role:      requirement.Role,
			Variant:   requirement.Variant,
			Locator:   locator,
			MediaType: modelMediaType(locator),
		})
		if format == "GLTF" {
			primaryModel.Locator = locator
			primaryModel.MediaType = modelMediaType(locator)
			base.Artifacts = append(base.Artifacts, primaryModel)
		}
	}
	base.DedupeKey = "all-conversions:" + string(request.AttemptID)
	base.Opaque = encodePostSuccessState(state)
	return base, nil
}

// The Generation checkpoint stores the dispatch intent verbatim before the
// paid POST. If that POST has no durable receipt, recover its dispatching
// marker from details and stop; never infer that the format was unstarted.
func parsePreCompletionState(opaque json.RawMessage) postSuccessState {
	state := parsePostSuccessState(opaque)
	if len(state.Conversions) > 0 {
		return state
	}
	var intent struct {
		Stage   string `json:"stage"`
		Details struct {
			Conversions map[string]conversionItem `json:"conversions"`
		} `json:"details"`
	}
	if json.Unmarshal(opaque, &intent) == nil && intent.Stage == "models/convert" &&
		len(intent.Details.Conversions) > 0 {
		state.Conversions = intent.Details.Conversions
	}
	return state
}

func preCompletionRunning(base generation.Observation, state postSuccessState) generation.Observation {
	base.Phase = generation.UpstreamRunning
	// The public task may already be at 98% when the source job finishes.
	// Keep that progress and order each private conversion observation by its
	// local poll time, or Generation will discard the new opaque state as stale.
	base.Progress = 98
	base.ObservedAt = time.Now().UTC()
	base.Artifacts = nil
	base.Opaque = encodePostSuccessState(state)
	digest := sha256.Sum256(base.Opaque)
	base.DedupeKey = "conversions:" + hex.EncodeToString(digest[:8])
	return base
}

func preCompletionFailure(base generation.Observation, state postSuccessState, detail string) generation.Observation {
	base.Phase = generation.UpstreamFailed
	base.Artifacts = nil
	base.Opaque = encodePostSuccessState(state)
	base.Failure = &generation.Failure{Class: generation.FailureRejected, SafeDetail: detail}
	base.DedupeKey = "conversion-failed"
	return base
}
