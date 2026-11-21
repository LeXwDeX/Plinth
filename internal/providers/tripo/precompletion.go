package tripo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
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

// requiredConvertedOutputs counts the converted-model slots the frozen input
// requires: an unconditional required slot always, a required_when slot only when
// its condition holds for the input (the kernel pins the same decision into the
// Task's Offering as minimum 0, so a pinned and an unpinned Offering agree).
func requiredConvertedOutputs(offering generation.Offering, values map[string]any) int {
	required := 0
	for _, output := range offering.Outputs {
		if output.Role == "converted_model" && output.Delivery == generation.AssetDeliveryRequired &&
			output.Variant != "" && output.Minimum != nil && *output.Minimum == 1 &&
			(output.RequiredWhen == nil || schema.ConditionMatches(*output.RequiredWhen, values)) {
			required++
		}
	}
	return required
}

// validatePreCompletionInput reads the frozen format selection and checks that
// it is exactly the set of conversions the Offering's required slots need: no
// format whose slot is not required, none missing, none repeated or unknown.
func validatePreCompletionInput(offering generation.Offering, input json.RawMessage) ([]string, error) {
	var values map[string]any
	if json.Unmarshal(input, &values) != nil || offering.PriceSelectionParameter() == "" {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 全格式输入无效")
	}
	items, isList := values[offering.PriceSelectionParameter()].([]any)
	if !isList {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 全格式输入无效")
	}
	formats := make([]string, 0, len(items))
	for _, item := range items {
		format, isText := item.(string)
		if !isText {
			return nil, failure(generation.FailureInvalidInput, false, "Tripo 全格式输入无效")
		}
		formats = append(formats, format)
	}
	required := requiredConvertedOutputs(offering, values)
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
	// The model role is fulfilled by this conversion's self-contained GLB (see
	// observePreCompletionFormats); a selection without it would end with no
	// model claim after the paid generation, so it is refused before any spend.
	if !seen[primaryModelConversion] {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 必须请求 "+primaryModelConversion+" 转换以交付自包含 GLB 主模型")
	}
	return formats, nil
}

// primaryModelConversion is the export whose binary glTF replaces the source
// GLB (which can carry upstream addresses) as the model-role claim.
const primaryModelConversion = "GLTF"

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

func (a *Adapter) observePreCompletionFormats(ctx context.Context, request generation.ObserveRequest) (observation generation.Observation, err error) {
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.Count(options.ObserveEndpoint, "{task_id}") != 1 || options.ConvertEndpoint == "" {
		return generation.Observation{}, failure(generation.FailureProtocol, false, "Tripo Offering 缺少转换端点")
	}
	a.touchPool(request.Offering, request.AttemptID)
	defer func() {
		if err == nil && terminalPhase(observation.Phase) {
			a.releasePool(request.Offering, request.AttemptID)
		}
	}()
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
	// A restart begins with an empty pool: re-register the children the
	// checkpoint still shows as submitted so the count reflects what is really
	// running upstream (Readopt may exceed the limit for one poll cycle).
	if len(submittedFormats(selections, state.Conversions)) > 0 {
		a.readoptPool(request.Offering, request.AttemptID)
	}
	// Poll every in-flight child (bounded concurrency, stable order). A child
	// that ended failed or cancelled is resubmitted under a new permit while its
	// own budget lasts (Tripo bills neither); a child whose bounded unknown
	// window ran out leaves the whole task ambiguous, never resent.
	backoff, changed := false, false
	for _, format := range submittedFormats(selections, state.Conversions) {
		item := state.Conversions[format]
		child, err := a.observeTask(ctx, options, item.RemoteID, string(request.AttemptID)+":convert:"+format)
		if err != nil {
			return generation.Observation{}, err
		}
		status := conversionStatusWord(child.Status)
		rule, known := declaredStatusRule(a.statusMapping, status)
		if !known {
			return generation.Observation{}, failure(generation.FailureProtocol, false, "Tripo 转换返回未知任务状态")
		}
		if a.unknownStatusExpired(item.RemoteID, status, rule) {
			item.Status = conversionItemUnknown
			item.Locator = ""
			item.RemoteID = ""
			item.Attempts = append(item.Attempts, conversionAttempt{
				RemoteID:        child.TaskID,
				CreditsConsumed: bytes.Clone(child.CreditsConsumed),
				Outcome:         conversionAttemptUnknown,
			})
			state.Conversions[format] = item
			return preCompletionUnknownFailure(base, state, "Tripo 格式转换结果不明，超过轮询时限"), nil
		}
		phase, _ := conversionChildPhase(a.statusMapping, child.Status)
		locator := ""
		if phase == generation.UpstreamCompleted {
			locator = firstHTTPOutput(child.Output, "model_url", "model")
			if locator == "" {
				state.Conversions[format] = conversionItem{
					Status:           conversionItemFailed,
					RateLimitRetries: item.RateLimitRetries,
					RetryAttempts:    item.RetryAttempts,
					Attempts:         append(item.Attempts, conversionAttempt{RemoteID: item.RemoteID, CreditsConsumed: bytes.Clone(child.CreditsConsumed), Outcome: conversionAttemptDone}),
				}
				return preCompletionFailure(base, state, "Tripo 转换缺少模型产物"), nil
			}
		}
		updated, wait := applyConversionChild(item, phase, locator, child.CreditsConsumed)
		if wait > 0 {
			backoff = true
		}
		if updated.Status != item.Status || len(updated.Attempts) != len(item.Attempts) {
			changed = true
		}
		state.Conversions[format] = updated
	}
	// A required format spent its retry budget: fail before another paid
	// export is started.
	for _, format := range selections {
		if item, exists := state.Conversions[format]; exists && item.Status == conversionItemFailed {
			return preCompletionFailure(base, state, "Tripo 必需格式转换失败"), nil
		}
	}
	if changed {
		// A child reached an outcome this round: report it and dispatch
		// nothing, so one round belongs to one action. The claims go in the
		// next round, when nothing new was learned.
		return preCompletionRunning(base, state), nil
	}
	if allFormatsDone(selections, state.Conversions) {
		return a.preCompletionClaims(ctx, request, options, base, state, selections)
	}
	if backoff {
		// A format just entered its retry backoff: let the wait pass first
		// (official 1, 2, 4… seconds).
		observation := preCompletionRunning(base, state)
		observation.PollAfter = conversionRetryWait(state.Conversions)
		return observation, nil
	}
	// Dispatch one more format while the in-flight children are below the
	// declared concurrency: a pending retry first, then a format never started.
	// One observation carries one permit (§4.3), so a round starts at most one
	// child and never waits for the others.
	if inflight := len(submittedFormats(selections, state.Conversions)); inflight < a.conversionConcurrency(options) {
		if format, ok := nextConversionDispatch(selections, state.Conversions); ok {
			if err := planPostSuccessConversion(request.Offering, request.Input); err != nil {
				return generation.Observation{}, err
			}
			previous, existed := state.Conversions[format]
			// The export options come from the frozen declaration and input; a
			// declaration that cannot be written stops here, before any checkpoint.
			extras, err := conversionExtras(request.Offering, options, request.Input, format)
			if err != nil {
				return generation.Observation{}, err
			}
			// Fixed order (C1): take the upstream slot first, then change the
			// entry state, then take the dispatch permit, then send. A full pool
			// leaves the entry untouched and returns a running observation that
			// carries the upstream queue clock.
			slot, acquired, wait := a.acquirePool(ctx, request.Offering, request.AttemptID)
			if !acquired {
				return poolQueueObservation(preCompletionRunning(base, state), wait), nil
			}
			attempts := previous.RetryAttempts
			if previous.Status == conversionItemRetryPending || previous.Status == conversionItemRateLimited {
				attempts++
			}
			submissionKey := string(request.AttemptID) + ":convert:" + format
			if attempts > 0 {
				submissionKey += ":retry:" + strconv.Itoa(attempts)
			}
			state.Conversions[format] = conversionItem{
				Status:           conversionItemDispatching,
				RateLimitRetries: previous.RateLimitRetries,
				RetryAttempts:    attempts,
				Attempts:         previous.Attempts,
			}
			permit, err := privateDispatchPermit(ctx, submissionKey, dispatchIntentStage{
				Stage: "models/convert",
				Details: map[string]any{
					"format":      format,
					"source_task": request.Job.RemoteID,
					"conversions": state.Conversions,
				},
			})
			if err != nil {
				slot.Release()
				return generation.Observation{}, err
			}
			remoteID, err := a.convertModel(ctx, options.ConvertEndpoint, request.Job.RemoteID, format, submissionKey, extras)
			if err != nil {
				// The account pool is full (code 2000) and this path is gated by
				// the pool: this is not a per-format rate limit. Restore the
				// entry, release the slot and queue again without spending the
				// retry budget (C4). Without a pool the historical per-format
				// budget below applies.
				if a.poolEnabled() {
					if retryAfter, full := poolFullRetryAfter(err); full && permit != nil {
						permit.Result = generation.DispatchResultNotDispatched
						restoreConversionItem(state.Conversions, format, previous, existed)
						slot.Release()
						observation := poolQueueObservation(preCompletionRunning(base, state), retryAfter)
						observation.Dispatch = permit
						return observation, nil
					}
				}
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
				// Any other failure leaves the child's result unknown: the entry
				// stays dispatching and the slot stays held, because the child may
				// still be running upstream.
				return generation.Observation{}, err
			}
			// The slot now belongs to this attempt's children (C2): the parent
			// job hands it over and the children share it idempotently.
			a.handoffPool(request.Offering, request.AttemptID)
			state.Conversions[format] = conversionItem{
				Status:           conversionItemSubmitted,
				RemoteID:         remoteID,
				RateLimitRetries: previous.RateLimitRetries,
				RetryAttempts:    attempts,
				Attempts:         previous.Attempts,
			}
			observation := preCompletionRunning(base, state)
			observation.Dispatch = permit
			return observation, nil
		}
	}
	if !hasPendingRetry(selections, state.Conversions) &&
		len(submittedFormats(selections, state.Conversions)) == 0 &&
		len(unstartedFormats(selections, state.Conversions)) == 0 {
		// Nothing is in flight, no retry is waiting and no format is left to
		// start: a required format spent its budget and ended failed.
		return preCompletionFailure(base, state, "Tripo 必需格式转换失败"), nil
	}
	// Waiting on in-flight children or on a retry's backoff.
	observation = preCompletionRunning(base, state)
	if wait := conversionRetryWait(state.Conversions); wait > 0 {
		observation.PollAfter = wait
	}
	return observation, nil
}

// preCompletionClaims hands every converted format to materialization: the
// source GLB stays the source claim, the GLTF conversion's self-contained GLB
// replaces the model claim, and each format keeps its own converted-model claim.
// Every child is refreshed with a read-only GET first, because a signed URL is
// short-lived.
func (a *Adapter) preCompletionClaims(
	ctx context.Context,
	request generation.ObserveRequest,
	options providerOptions,
	base generation.Observation,
	state postSuccessState,
	selections []string,
) (generation.Observation, error) {
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
		conversion := generation.ArtifactClaim{
			ID:        "convert:" + format,
			Role:      requirement.Role,
			Variant:   requirement.Variant,
			Locator:   locator,
			MediaType: modelMediaType(locator),
		}
		base.Artifacts = append(base.Artifacts, conversion)
		if format == primaryModelConversion {
			primaryModel.Locator = locator
			primaryModel.MediaType = modelMediaType(locator)
			// The model slot and the converted slot are one file at one locator,
			// so materialization downloads it once and stores it for both (#300).
			primaryModel.SameBytesAs = conversion.ID
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

// preCompletionUnknownFailure ends the task on an outcome that is not known:
// the upstream may still be working (or may already have billed), so the task
// is never sent again and the failure is not offered as a retryable one.
func preCompletionUnknownFailure(base generation.Observation, state postSuccessState, detail string) generation.Observation {
	base.Phase = generation.UpstreamFailed
	base.Artifacts = nil
	base.Opaque = encodePostSuccessState(state)
	base.Failure = &generation.Failure{
		Class:      generation.FailureSubmissionUnknown,
		Retryable:  false,
		Ambiguous:  true,
		SafeDetail: detail,
	}
	base.DedupeKey = "conversion-outcome-unknown"
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
