package tripo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
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
//
// A mixed Offering (#462) declares post_success and also holds the public task
// until its blocking conversion (the GLTF export that produces the
// self-contained primary model) has landed. Only that blocking slot is worked
// on before success; the requested non-blocking formats are converted by
// observePostSuccessConversion after the task is already SUCCEEDED and usable.
func preCompletionFormats(offering generation.Offering) bool {
	if offering.PriceSelectionParameter() == "" {
		return false
	}
	// A mixed Offering (#462) declares post_success and still blocks on its own
	// conversion: without the primary model the task cannot succeed. An Offering
	// whose only converted slots are on-demand (the historical post-success
	// shapes) has no blocking conversion and never runs this path.
	return hasBlockingConversion(offering)
}

// hasBlockingConversion reports whether the Offering declares a converted-model
// slot the task must deliver before it may succeed.
func hasBlockingConversion(offering generation.Offering) bool {
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

// validatePreCompletionInput reads the frozen format selection and returns the
// conversions that must finish before the task may succeed: the formats of the
// Offering's required converted-model slots. It refuses a selection that names
// a format the Offering does not declare, repeats one, or leaves a required
// conversion unselected.
//
// In a mixed Offering (#462) the selection is wider than that set: the extra
// entries are the requested non-blocking formats, converted after success, so
// they are accepted here and never dispatched before the blocking set is done.
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
	if required == 0 {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 必须请求全部声明的转换格式")
	}
	blocking := make([]string, 0, len(formats))
	seen := make(map[string]bool, len(formats))
	for _, format := range formats {
		if seen[format] || !conversionSelectionDeclared(offering, format) {
			return nil, failure(generation.FailureInvalidInput, false, "Tripo 转换格式重复或不受支持")
		}
		seen[format] = true
		if _, found := preCompletionOutput(offering, format); found {
			blocking = append(blocking, format)
			continue
		}
		if requestedConversionFormat(offering, values, format) {
			continue
		}
		return nil, failure(generation.FailureProtocol, false, "Tripo 转换输出契约不完整")
	}
	if len(blocking) != required {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 必须请求全部声明的转换格式")
	}
	// The model role is fulfilled by this conversion's self-contained GLB (see
	// observePreCompletionFormats); a selection without it would end with no
	// model claim after the paid generation, so it is refused before any spend.
	if !slices.Contains(blocking, primaryModelConversion) {
		return nil, failure(generation.FailureInvalidInput, false, "Tripo 必须请求 "+primaryModelConversion+" 转换以交付自包含 GLB 主模型")
	}
	return blocking, nil
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
		// 结果不明（发送无回执）时停止再次付费并终结 attempt：上游可能仍在
		// 跑这个子作业，但名额按「有意取舍」还回去（本函数的 defer），否则
		// 这条已知失败的路径会把名额握到 IdleTTL。多占出的并发由上游自己的
		// 429（code 2000）兜住——池是进程内软上限，不是硬配额。
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
		updated, wait := a.applyConversionChild(item, phase, locator, child.CreditsConsumed, taskErrorCode(child.ErrorCode))
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
	if backoff {
		// A format just entered its retry backoff: let the wait pass first
		// (official 1, 2, 4… seconds). This comes before the changed return
		// because entering the backoff also records an attempt: the two always
		// fire together and neither may dispatch, so only the wait differs.
		observation := preCompletionRunning(base, state)
		observation.PollAfter = conversionRetryWait(state.Conversions)
		return observation, nil
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
	// Dispatch one more format while the in-flight children are below the
	// declared concurrency: a pending retry first, then a format never started.
	// One observation carries one permit (§4.3), so a round starts at most one
	// child and never waits for the others.
	if inflight := len(submittedFormats(selections, state.Conversions)); inflight < a.conversionConcurrency(request.Offering, options) {
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
				return a.poolQueueObservation(preCompletionRunning(base, state), request.Offering, wait), nil
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
				if a.poolEnabled(request.Offering) {
					if retryAfter, full := poolFullRetryAfter(err); full && permit != nil {
						permit.Result = generation.DispatchResultNotDispatched
						restoreConversionItem(state.Conversions, format, previous, existed)
						slot.Release()
						observation := a.poolQueueObservation(preCompletionRunning(base, state), request.Offering, retryAfter)
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
	//
	// D10 (fall back to the original GLB as the primary model when the GLTF
	// conversion keeps failing) is deliberately NOT enabled here: its precondition
	// is a desensitized sample proving the original GLB passes the isolation
	// check, and without that evidence a GLTF failure still fails the whole task
	// rather than delivering a model whose bytes were never checked (#457 D10).
	// D11 (converting the other formats from a successful P2 task) is not enabled
	// either: the official documentation does not yet confirm that convert accepts
	// a P2 task (#457 D11).
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

// parsePreCompletionState reads the checkpoint the same way the post-success
// path does: the Generation checkpoint stores the dispatch intent verbatim
// before the paid POST, and parsePostSuccessState recovers its dispatching
// marker from details so a crash never looks like an unstarted format.
func parsePreCompletionState(opaque json.RawMessage) postSuccessState {
	return parsePostSuccessState(opaque)
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
