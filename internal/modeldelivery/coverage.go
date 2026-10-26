package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Status of one target format of a task.
type Status string

const (
	// StatusAvailable: a READY local file exists (on the task, or on the
	// task a finished completion plan produced).
	StatusAvailable Status = "available"
	// StatusObtainable: no file yet, but a declared Offering can produce it.
	StatusObtainable Status = "obtainable"
	// StatusConverting: a completion plan for it is queued or running.
	StatusConverting Status = "converting"
	// StatusUnavailable: no declared route can produce it.
	StatusUnavailable Status = "unavailable"
)

// Format is one row of the coverage projection.
type Format struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status Status `json:"status"`
	// TaskID and AssetID locate the READY file through the ordinary task
	// asset route. TaskID differs from the requested task when the file
	// came from a finished completion plan.
	TaskID    generation.TaskID  `json:"task_id,omitempty"`
	AssetID   generation.AssetID `json:"asset_id,omitempty"`
	MediaType string             `json:"media_type,omitempty"`
	Size      int64              `json:"size,omitempty"`
	// Conversion names the Offering that would produce it (obtainable and
	// converting).
	Conversion *Conversion `json:"conversion,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	ReasonText string      `json:"reason_text,omitempty"`
}

type Conversion struct {
	NativeQuote      *schema.NativeQuote   `json:"native_quote,omitempty"`
	OfferingID       generation.OfferingID `json:"offering_id"`
	ProviderID       string                `json:"provider_id"`
	ProviderName     string                `json:"provider_name"`
	EstimatedCredits int                   `json:"estimated_credits"`
	// CreditsMetered is the Offering's declaration that its provider bills in
	// credits; the UI shows the estimate only then.
	CreditsMetered bool `json:"credits_metered,omitempty"`
	// ProviderShortLabel is the Offering's provider_short_label. The formats
	// panel names a local conversion by it ("本地"): a local Offering's
	// provider_name only says what it does ("格式转换").
	ProviderShortLabel string `json:"provider_short_label,omitempty"`
}

// Step is one processing step the completion action needs. State is empty
// while no plan exists for it yet; otherwise it is the plan's state.
type Step struct {
	NativeQuote      *schema.NativeQuote   `json:"native_quote,omitempty"`
	OfferingID       generation.OfferingID `json:"offering_id"`
	Capability       generation.Capability `json:"capability"`
	ProviderID       string                `json:"provider_id"`
	ProviderName     string                `json:"provider_name"`
	Formats          []string              `json:"formats"`
	EstimatedCredits int                   `json:"estimated_credits"`
	State            string                `json:"state,omitempty"`
	PlanID           string                `json:"plan_id,omitempty"`
	PlanVersion      int64                 `json:"plan_version,omitempty"`
	OfferingRevision string                `json:"offering_revision,omitempty"`
	Failure          string                `json:"failure,omitempty"`
	ResultTaskID     generation.TaskID     `json:"result_task_id,omitempty"`
	// ProviderShortLabel: see Conversion.ProviderShortLabel.
	ProviderShortLabel string `json:"provider_short_label,omitempty"`
	// ChargedCredits is set on a failed step whose provider already finished
	// and billed the run (#330): the delivery failed after the charge.
	ChargedCredits int `json:"charged_credits,omitempty"`
	// Refetch is the free upstream refetch (#295) of a failed step's Task,
	// present when its provider finished the run.
	Refetch *StepRefetch `json:"refetch,omitempty"`
	// PreviousFailure is the plan failure of an earlier run of this step that
	// can be neither retried nor fetched again (#330): the step is offered
	// again as a new run, and the client says the last one did not finish.
	PreviousFailure string `json:"previous_failure,omitempty"`
	// RetryBlocked marks a failed step whose retry is not possible (an
	// ambiguous submission: the provider may already have the run, so a new
	// one could charge twice). The client offers no retry for it.
	RetryBlocked bool `json:"retry_blocked,omitempty"`
}

// StepRefetch tells the client whether the failed step's already charged
// result can be fetched again for free, and which refetch Task exists.
type StepRefetch struct {
	Eligible bool              `json:"eligible"`
	Reason   string            `json:"reason,omitempty"`
	TaskID   generation.TaskID `json:"task_id,omitempty"`
	State    string            `json:"state,omitempty"`
	// Failed counts refetches that failed or were canceled.
	Failed int `json:"failed,omitempty"`
}

// Coverage is the format coverage projection of one 3D task.
type Coverage struct {
	PriceTotals []schema.NativeAmount `json:"price_totals,omitempty"`
	TaskID      generation.TaskID     `json:"task_id"`
	// Applicable is false for tasks that are not finished 3D model tasks;
	// the lists are then empty.
	Applicable bool     `json:"applicable"`
	Formats    []Format `json:"formats"`
	Steps      []Step   `json:"steps"`
	// CompletionCredits is the estimate of the steps the completion action
	// would start now; CanComplete is false when there are none.
	CompletionCredits int  `json:"completion_credits"`
	CanComplete       bool `json:"can_complete"`
	// ArchiveAvailable reports whether the task has saved model files that
	// fit the Schema's archive entry and byte bounds, so the one-click ZIP
	// can be prepared instead of answering 413.
	ArchiveAvailable bool `json:"archive_available"`
}

// candidate is one concrete way a conversion Offering can run for a task:
// either all provided formats in one run, or a single format.
type candidate struct {
	conversion catalog.ModelConversionSchema
	offering   generation.Offering
	// format is empty when one run delivers everything the conversion provides.
	format string
	key    string
}

func (c candidate) provides(format string) bool {
	if c.format != "" {
		return c.format == format
	}
	for _, provided := range c.conversion.Provides {
		if provided == format {
			return true
		}
	}
	return false
}

// planKey is deterministic in (task, Offering revision, format) so a repeated
// request finds the plan it already created instead of paying twice.
func planKey(task generation.Task, offering generation.Offering, format string) string {
	if format == "" {
		format = "all"
	}
	return fmt.Sprintf("format-completion:%s:%s:%s:%s", task.ID, offering.ID, offering.Revision, format)
}

// candidates lists every conversion run that exists for the task in the
// current catalog, in the Schema's priority order.
func (s *Service) candidates(task generation.Task) []candidate {
	var result []candidate
	for _, conversion := range s.schema.Conversions {
		offering, exists := s.gateway.Offering(generation.OfferingID(conversion.OfferingID))
		if !exists {
			continue
		}
		if len(conversion.Provides) > 0 {
			result = append(result, candidate{conversion: conversion, offering: offering, key: planKey(task, offering, "")})
		}
		formats := make([]string, 0, len(conversion.FormatValues))
		for format := range conversion.FormatValues {
			formats = append(formats, format)
		}
		sort.Strings(formats)
		for _, format := range formats {
			result = append(result, candidate{conversion: conversion, offering: offering, format: format, key: planKey(task, offering, format)})
		}
	}
	return result
}

type obtained struct {
	asset generation.Asset
	task  generation.Task
}

// view is everything read about one task: its own classified files, the files
// of finished completion plans and every completion plan found for it.
type view struct {
	task     generation.Task
	assets   []generation.Asset
	own      map[string]generation.Asset
	rest     []generation.Asset
	obtained map[string]obtained
	plans    map[string]processing.Plan
	// active maps a format to a completion plan that is still going to
	// produce it (PENDING, RUNNING or PAUSED), whatever Offering revision it
	// was created under. A paused plan counts: resuming it charges.
	active map[string]processing.Plan
	// refetch holds, per failed plan ID, the refetch view of its step Task
	// when its provider finished the run (#330).
	refetch map[string]generation.RefetchEligibility
	// keys maps a candidate's base plan key to the key its current plan lives
	// under: a failed plan that can be neither retried nor fetched again gives
	// its formats back, and the next run takes a key derived from it (#330).
	keys map[string]string
	// previous holds, per current key, the last failed plan that was given up.
	previous map[string]processing.Plan
	// inFlight maps a format to a declared conversion the Agent started
	// directly and that has not finished. No second run may be started for it.
	inFlight map[string]generation.Task
	// err is a read that failed while collecting the view. A projection that
	// could not see an active conversion must fail before offering or starting
	// a paid run, never present the formats as unconverted.
	err error
}

// key is the plan key the candidate's run lives under now.
func (v view) key(base string) string {
	if key, ok := v.keys[base]; ok {
		return key
	}
	return base
}

// maxPlanRenewals bounds how many given-up failed plans one candidate follows.
const maxPlanRenewals = 16

// renewedKey is the deterministic key of the run after a given-up plan, so a
// repeated request finds the run it already created instead of paying twice.
func renewedKey(base, failedPlanID string) string {
	return base + ":after:" + failedPlanID
}

func (s *Service) resolve(ctx context.Context, task generation.Task) view {
	v := view{task: task, assets: sortedAssets(task), obtained: map[string]obtained{}, plans: map[string]processing.Plan{}, active: map[string]processing.Plan{}, refetch: map[string]generation.RefetchEligibility{},
		keys: map[string]string{}, previous: map[string]processing.Plan{}, inFlight: map[string]generation.Task{}}
	v.own, v.rest = s.classify(v.assets)
	known := map[string]bool{}
	for _, candidate := range s.candidates(task) {
		key := candidate.key
		for range maxPlanRenewals {
			plan, err := s.plans.Get(ctx, task.OwnerID, processing.PlanID(task.OwnerID, key))
			if err != nil {
				if !errors.Is(err, processing.ErrMissing) {
					s.logger.Warn("读取格式补齐计划失败", "task_id", task.ID, "error", err)
				}
				break
			}
			known[plan.ID] = true
			plan = s.completedPlan(ctx, task.OwnerID, plan)
			if s.givenUp(ctx, &v, plan) {
				v.previous[renewedKey(candidate.key, plan.ID)] = plan
				key = renewedKey(candidate.key, plan.ID)
				continue
			}
			v.plans[key] = plan
			s.record(ctx, &v, plan, candidate.format)
			break
		}
		if key != candidate.key {
			v.keys[candidate.key] = key
		}
	}
	// Plans created under another Offering revision have another key and are
	// not found above. They are found by what they convert, so a plan that is
	// still running keeps its formats "converting" and a finished one still
	// delivers its file; neither can be paid for a second time.
	if task.SessionID != "" {
		listed, err := s.plans.ListBySource(ctx, task.OwnerID, task.SessionID, task.ID)
		if err != nil {
			s.logger.Warn("列出格式补齐计划失败", "task_id", task.ID, "error", err)
		}
		for _, plan := range listed {
			if known[plan.ID] || plan.OwnerID != task.OwnerID || plan.SourceTaskID != task.ID {
				continue
			}
			plan = s.completedPlan(ctx, task.OwnerID, plan)
			if format, ok := s.completionFormat(plan); ok {
				s.record(ctx, &v, plan, format)
			}
		}
	}
	v.err = s.directConversions(ctx, &v)
	return v
}

// record records what one completion plan of the task contributes: the
// formats it is still going to deliver (active) or the file it delivered.
// candidateFormat is the single format of a per-format run, empty for a run
// that delivers every format it provides.
// completedPlan projects a single-step conversion plan as succeeded when its
// step Task already succeeded, even if the stored Plan was not written back.
// The Formats tab must not offer a retry or show a failure for delivered work;
// the Processing API keeps its own stored state, and Retry refuses a succeeded
// step Task. A failed Task without a local delivery completion is unchanged.
func (s *Service) completedPlan(ctx context.Context, owner generation.OwnerID, plan processing.Plan) processing.Plan {
	if plan.State != processing.Failed || len(plan.Steps) != 1 || plan.Steps[0].TaskID == "" {
		return plan
	}
	task, err := s.gateway.GetScoped(ctx, generation.OwnTasks(owner), plan.Steps[0].TaskID)
	if err != nil || task.State != generation.TaskSucceeded {
		return plan
	}
	plan.State = processing.Succeeded
	plan.Failure = ""
	return plan
}

func (s *Service) record(ctx context.Context, v *view, plan processing.Plan, candidateFormat string) {
	if activePlan(plan) {
		for _, format := range s.planFormats(plan, candidateFormat) {
			if _, taken := v.active[format]; !taken {
				v.active[format] = plan
			}
		}
		return
	}
	task := v.task
	if len(plan.Steps) == 0 || plan.Steps[len(plan.Steps)-1].TaskID == "" {
		return
	}
	resultID := plan.Steps[len(plan.Steps)-1].TaskID
	if plan.State == processing.Failed {
		// A failed run whose provider already finished can be fetched again
		// for free (#295). A refetch that is running keeps the formats
		// converting; one that delivered stands in for the failed run.
		refetch, ok := s.refetchOf(ctx, task.OwnerID, resultID)
		if !ok {
			return
		}
		v.refetch[plan.ID] = refetch
		switch {
		case refetch.RefetchTaskID == "":
			return
		case refetch.RefetchState == generation.TaskSucceeded:
			resultID = refetch.RefetchTaskID
		default:
			for _, format := range s.planFormats(plan, candidateFormat) {
				if _, taken := v.active[format]; !taken {
					v.active[format] = plan
				}
			}
			return
		}
	} else if plan.State != processing.Succeeded {
		return
	}
	result, err := s.gateway.GetScoped(ctx, generation.OwnTasks(task.OwnerID), resultID)
	if err != nil || result.State != generation.TaskSucceeded ||
		result.OwnerID != task.OwnerID || result.SessionID != task.SessionID {
		return
	}
	resultAssets := sortedAssets(result)
	if candidateFormat != "" {
		// A one-format run delivers one converted file; the run, not
		// the file's variant, says which format it is.
		for _, asset := range resultAssets {
			if (asset.Role == modelRole || asset.Role == convertedRole) && !s.isCompanion(asset) {
				s.keepObtained(v, candidateFormat, asset, result)
				break
			}
		}
		return
	}
	classified, _ := s.classify(resultAssets)
	for format, asset := range classified {
		s.keepObtained(v, format, asset, result)
	}
}

// givenUp reports a failed plan that holds nothing any more (#330): its
// failed step failed for a reason that rules out a retry and is not an
// ambiguous submission (the source's original provider task expired, the
// source no longer fits, the submission was rejected before any charge), and
// its result can neither be fetched again for free nor is a refetch running
// or done. Such a plan would otherwise keep its key and leave the formats with
// no way to be obtained; an ambiguous failure keeps its plan, since a new run
// could charge twice.
func (s *Service) givenUp(ctx context.Context, v *view, plan processing.Plan) bool {
	if plan.State != processing.Failed {
		return false
	}
	final := false
	for _, step := range plan.Steps {
		if step.State != processing.Failed {
			continue
		}
		if step.Retryable || !finalFailures[step.Failure] {
			return false
		}
		final = true
	}
	if !final {
		return false
	}
	if len(plan.Steps) > 0 {
		if taskID := plan.Steps[len(plan.Steps)-1].TaskID; taskID != "" {
			if refetch, ok := s.refetchOf(ctx, v.task.OwnerID, taskID); ok &&
				(refetch.RefetchTaskID != "" && refetch.RefetchState != generation.TaskFailed && refetch.RefetchState != generation.TaskCanceled ||
					refetch.Eligible && refetch.RefetchTaskID == "" && refetch.FailedRefetches == 0) {
				return false
			}
		}
	}
	return true
}

// finalFailures are the plan failures after which the step can never run
// again as it was and nothing may have been submitted twice.
var finalFailures = map[string]bool{
	processing.ReasonSourceExpired:           true,
	processing.ReasonRetrySourceExpired:      true,
	processing.ReasonRetrySourceIncompatible: true,
	processing.ReasonSourceIncompatible:      true,
	processing.ReasonSubmitRejected:          true,
}

// refetchOf reads the refetch view of a failed step Task. It reports false
// when the kernel offers no refetch reads or the provider never finished the
// run (nothing was charged and there is nothing to fetch).
func (s *Service) refetchOf(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.RefetchEligibility, bool) {
	reader, ok := s.gateway.(RefetchReader)
	if !ok {
		return generation.RefetchEligibility{}, false
	}
	refetch, err := reader.RefetchEligibility(ctx, owner, id)
	if err != nil {
		s.logger.Warn("读取格式补齐任务的重新取回状态失败", "task_id", id, "error", err)
		return generation.RefetchEligibility{}, false
	}
	if refetch.Reason == generation.RefetchNotApplicable && !refetch.Eligible {
		return generation.RefetchEligibility{}, false
	}
	return refetch, true
}

// stepRefetch projects a failed plan's refetch view onto its step.
func stepRefetch(out *Step, v view, plan processing.Plan) {
	if plan.State == processing.Failed {
		out.RetryBlocked = true
		for _, step := range plan.Steps {
			if step.State == processing.Failed && step.Retryable {
				out.RetryBlocked = false
			}
		}
	}
	refetch, ok := v.refetch[plan.ID]
	if !ok {
		return
	}
	out.ChargedCredits = refetch.ChargedCredits
	out.Refetch = &StepRefetch{Eligible: refetch.Eligible, Reason: string(refetch.Reason), TaskID: refetch.RefetchTaskID, State: string(refetch.RefetchState), Failed: refetch.FailedRefetches}
}

// activePlan reports a plan that has not reached a final state.
func activePlan(plan processing.Plan) bool {
	return plan.State != processing.Succeeded && plan.State != processing.Failed
}

// planFormats lists the formats an active plan is going to deliver: the one
// format of a per-format run, or everything its conversion provides.
func (s *Service) planFormats(plan processing.Plan, candidateFormat string) []string {
	if candidateFormat != "" {
		return []string{candidateFormat}
	}
	if len(plan.Steps) == 0 {
		return nil
	}
	for _, conversion := range s.schema.Conversions {
		if conversion.OfferingID == string(plan.Steps[len(plan.Steps)-1].OfferingID) && len(conversion.Provides) > 0 {
			return conversion.Provides
		}
	}
	return nil
}

// completionFormat recognizes a plan as a format-completion plan by its single
// step: the Offering is a declared conversion and the step input carries the
// conversion's fixed input. The second result is the one format of a per-format
// run, empty for a run that delivers everything the conversion provides.
func (s *Service) completionFormat(plan processing.Plan) (string, bool) {
	if len(plan.Steps) != 1 {
		return "", false
	}
	step := plan.Steps[0]
	return s.conversionInput(step.OfferingID, step.Input)
}

// conversionInput recognizes a run of a declared conversion by its Offering
// and the conversion's fixed input (retired routes included). The first
// result is the one format of a per-format run.
func (s *Service) conversionInput(offering generation.OfferingID, raw json.RawMessage) (string, bool) {
	return ConversionInput(s.schema.Conversions, offering, raw)
}

// ConversionInput recognizes a declared format conversion run by its Offering
// and frozen fixed input (retired routes included). The first result is the
// one format of a per-format run.
func ConversionInput(conversions []catalog.ModelConversionSchema, offering generation.OfferingID, raw json.RawMessage) (string, bool) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", false
	}
	for _, conversion := range conversions {
		if conversion.OfferingID != string(offering) {
			continue
		}
		var fixed map[string]json.RawMessage
		if len(conversion.Input) > 0 && json.Unmarshal(conversion.Input, &fixed) != nil {
			continue
		}
		matches := true
		for name, value := range fixed {
			if !jsonEqual(input[name], value) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		if conversion.FormatParameter != "" {
			if raw, present := input[conversion.FormatParameter]; present {
				for format, value := range conversion.FormatValues {
					if encoded, _ := json.Marshal(value); jsonEqual(raw, encoded) {
						return format, true
					}
				}
				return "", false
			}
		}
		if len(conversion.Provides) > 0 {
			return "", true
		}
	}
	return "", false
}

func jsonEqual(left, right json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func (s *Service) keepObtained(v *view, format string, asset generation.Asset, from generation.Task) {
	if _, own := v.own[format]; own {
		return
	}
	if _, taken := v.obtained[format]; taken {
		return
	}
	v.obtained[format] = obtained{asset: asset, task: from}
}

// isCompanion reports files that only accompany an OBJ (material library and
// textures); they are never a format of their own.
func (s *Service) isCompanion(asset generation.Asset) bool {
	object := s.schema.Archive.ObjPackage
	return matchesAsset(object.Material, asset) || matchesAsset(object.Textures, asset)
}

type plannedStep struct {
	candidate   candidate
	sourceAsset generation.Asset
	input       json.RawMessage
	credits     int
	quote       *schema.NativeQuote
	formats     []string
}

// planSteps decides, per missing format and in Schema order, the first
// conversion whose Offering is available and whose input contract accepts the
// task. One run that provides several missing formats is planned once. The
// reason of an unplanned format is that of the first enabled route that fails;
// with no enabled route it is no_route.
func (s *Service) planSteps(ctx context.Context, task generation.Task, v view, missing []string) ([]plannedStep, map[string]string) {
	reasons := map[string]string{}
	var steps []plannedStep
	covered := map[string]bool{}
	blocked := s.capabilityIn(task.Capability, s.schema.BlockedCapabilities) || task.SessionID == ""
	candidates := s.candidates(task)
	// Reuse only within this analysis; a later request rechecks actual bytes.
	type checkedCandidate struct {
		step plannedStep
		code string
	}
	checked := map[string]checkedCandidate{}
	for _, format := range missing {
		if covered[format] {
			continue
		}
		if _, converting := v.inFlight[format]; converting {
			// A direct conversion already runs for it; no plan may start.
			covered[format] = true
			continue
		}
		if blocked {
			if task.SessionID == "" {
				reasons[format] = reasonSourceIncompatible
			} else {
				reasons[format] = reasonSourceBlocked
			}
			continue
		}
		firstFailure := ""
		chosen := false
		for _, candidate := range candidates {
			// A Schema-disabled Offering is no route at all: it neither plans
			// a step nor names the reason. Otherwise its offering_unavailable
			// would mask why the routes that do exist cannot take this source.
			// Existing plans under it are still found through candidates().
			// A retired route only recognizes plans made under it (#330).
			if !candidate.provides(format) || !candidate.offering.Enabled || candidate.conversion.Retired {
				continue
			}
			check, exists := checked[candidate.key]
			if !exists {
				check.step, check.code = s.buildStep(ctx, task, v, candidate)
				checked[candidate.key] = check
			}
			step, code := check.step, check.code
			if code != "" {
				if firstFailure == "" {
					firstFailure = code
				}
				continue
			}
			step.candidate.key = v.key(candidate.key)
			for _, provided := range missing {
				if candidate.provides(provided) && !covered[provided] {
					step.formats = append(step.formats, provided)
					covered[provided] = true
				}
			}
			steps = append(steps, step)
			chosen = true
			break
		}
		if !chosen {
			if firstFailure == "" {
				firstFailure = reasonNoRoute
			}
			reasons[format] = firstFailure
		}
	}
	return steps, reasons
}

func (s *Service) buildStep(ctx context.Context, task generation.Task, v view, candidate candidate) (plannedStep, string) {
	offering := candidate.offering
	if !offering.Enabled || !s.gateway.OfferingAvailable(offering.ID) {
		return plannedStep{}, reasonOfferingUnavailable
	}
	var source generation.Asset
	found := false
	for _, format := range candidate.conversion.SourceFormats {
		if asset, exists := v.own[format]; exists {
			source, found = asset, true
			break
		}
	}
	if !found {
		return plannedStep{}, reasonSourceIncompatible
	}
	binding, err := generation.BindModelSource(offering, task, source.ID, nil)
	if err != nil {
		if errors.Is(err, generation.ErrSourceExpired) {
			return plannedStep{}, reasonSourceExpired
		}
		return plannedStep{}, reasonSourceIncompatible
	}
	input := map[string]json.RawMessage{}
	if len(candidate.conversion.Input) > 0 {
		if err := json.Unmarshal(candidate.conversion.Input, &input); err != nil {
			return plannedStep{}, reasonSourceIncompatible
		}
	}
	if candidate.format != "" {
		value, _ := json.Marshal(candidate.conversion.FormatValues[candidate.format])
		input[candidate.conversion.FormatParameter] = value
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return plannedStep{}, reasonSourceIncompatible
	}
	// Existing plans already own their execution identity. Reads must not
	// recompute inputs for running/finished work or hide its recovery controls.
	_, existing := v.plans[v.key(candidate.key)]
	if !existing {
		for format := range v.active {
			if candidate.provides(format) {
				existing = true
				break
			}
		}
	}
	if !existing {
		input[binding.Parameter], _ = json.Marshal(binding.Value)
		validationInput, err := json.Marshal(input)
		if err != nil {
			return plannedStep{}, reasonSourceIncompatible
		}
		if err := s.gateway.ValidateOfferingInput(ctx, task.OwnerID, offering.ID, offering.Revision, validationInput); err != nil {
			if generation.IsCode(err, generation.ErrUnavailable) || generation.IsCode(err, generation.ErrInternal) || generation.IsCode(err, generation.ErrConflict) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return plannedStep{}, reasonOfferingUnavailable
			}
			return plannedStep{}, reasonSourceIncompatible
		}
	}
	credits := 0
	var quote *schema.NativeQuote
	if offering.NativePricing != nil {
		var e error
		quote, e = offering.DefaultNativeQuote(encoded)
		if e != nil {
			return plannedStep{}, reasonOfferingUnavailable
		}
	} else {
		credits = offering.EstimateTotalCredits(encoded)
	}
	return plannedStep{candidate: candidate, sourceAsset: source, input: encoded, credits: credits, quote: quote}, ""
}

type analysis struct {
	err      error
	coverage Coverage
	view     view
	steps    []plannedStep
}

func (s *Service) analyze(ctx context.Context, task generation.Task) analysis {
	result := analysis{coverage: Coverage{
		TaskID: task.ID, Formats: []Format{}, Steps: []Step{},
	}}
	if task.State == generation.TaskSucceeded {
		result.view = s.resolve(ctx, task)
		if result.view.err != nil {
			// A failed read may hide an active conversion. Fail the whole
			// projection before Complete can start a second paid run.
			result.err = result.view.err
			return result
		}
		result.coverage.ArchiveAvailable = s.archiveFits(result.view)
	}
	if !s.capabilityIn(task.Capability, s.schema.Capabilities) || task.State != generation.TaskSucceeded {
		return result
	}
	result.coverage.Applicable = true
	v := result.view
	var missing []string
	for _, format := range s.schema.Formats {
		_, own := v.own[format.ID]
		_, got := v.obtained[format.ID]
		if !own && !got {
			missing = append(missing, format.ID)
		}
	}
	steps, reasons := s.planSteps(ctx, task, v, missing)
	result.steps = steps
	stepOf := map[string]int{}
	for index, step := range steps {
		for _, format := range step.formats {
			stepOf[format] = index
		}
	}
	var price schema.PriceAccumulator
	for _, step := range steps {
		out := Step{
			OfferingID: step.candidate.offering.ID, Capability: step.candidate.offering.Capability,
			ProviderID:   step.candidate.offering.ProviderID,
			ProviderName: step.candidate.offering.ProviderName, ProviderShortLabel: step.candidate.offering.ProviderShortLabel,
			Formats:          step.formats,
			EstimatedCredits: step.credits, NativeQuote: step.quote, OfferingRevision: step.candidate.offering.Revision,
		}
		if plan, exists := v.plans[step.candidate.key]; exists {
			out.State, out.PlanID, out.PlanVersion, out.Failure = plan.State, plan.ID, plan.Version, plan.Failure
			if len(plan.Steps) > 0 {
				out.ResultTaskID = plan.Steps[len(plan.Steps)-1].TaskID
				out.OfferingRevision = plan.Steps[len(plan.Steps)-1].Revision
				out.NativeQuote = plan.Steps[len(plan.Steps)-1].NativeQuote
				out.EstimatedCredits = plan.Steps[len(plan.Steps)-1].EstimatedCredits
			}
			stepRefetch(&out, v, plan)
		} else if plan, locked := lockingPlan(v, step); locked {
			// The Offering revision changed while a plan for these formats is
			// still going: that plan, not a new one, is what the user waits for.
			out.State, out.PlanID, out.PlanVersion, out.Failure = plan.State, plan.ID, plan.Version, plan.Failure
			if len(plan.Steps) > 0 {
				out.ResultTaskID = plan.Steps[len(plan.Steps)-1].TaskID
				out.OfferingRevision = plan.Steps[len(plan.Steps)-1].Revision
				out.NativeQuote = plan.Steps[len(plan.Steps)-1].NativeQuote
				out.EstimatedCredits = plan.Steps[len(plan.Steps)-1].EstimatedCredits
			}
			stepRefetch(&out, v, plan)
		} else {
			if previous, renewed := v.previous[step.candidate.key]; renewed {
				out.PreviousFailure = previous.Failure
			}
			if e := price.Add(step.credits, step.quote); e != nil {
				result.err = e
				return result
			}
			result.coverage.CompletionCredits = price.Legacy
			result.coverage.CanComplete = true
		}
		result.coverage.Steps = append(result.coverage.Steps, out)
	}
	var totalErr error
	result.coverage.PriceTotals, totalErr = price.Totals()
	if totalErr != nil {
		result.err = totalErr
		return result
	}
	for _, format := range s.schema.Formats {
		row := Format{ID: format.ID, Label: format.Label}
		switch {
		case hasAsset(v.own, format.ID):
			asset := v.own[format.ID]
			row.Status, row.TaskID, row.AssetID, row.MediaType, row.Size = StatusAvailable, task.ID, asset.ID, s.formatMediaType(asset), asset.Size
		case hasObtained(v.obtained, format.ID):
			got := v.obtained[format.ID]
			row.Status, row.TaskID, row.AssetID, row.MediaType, row.Size = StatusAvailable, got.task.ID, got.asset.ID, s.formatMediaType(got.asset), got.asset.Size
		default:
			if plan, converting := v.active[format.ID]; converting {
				// An active plan converts it, whatever revision it was created
				// under, so it can neither be started again nor shown as obtainable.
				row.Status, row.Reason, row.Conversion = StatusConverting, reasonConverting, s.activeConversion(plan)
				break
			}
			if converting, running := v.inFlight[format.ID]; running {
				// The Agent started the same declared conversion directly; it
				// has no plan to read, and a second run would charge twice.
				row.Status, row.Reason, row.Conversion = StatusConverting, reasonConverting, s.conversionOfTask(converting)
				break
			}
			index, planned := stepOf[format.ID]
			if !planned {
				row.Status, row.Reason = StatusUnavailable, reasons[format.ID]
				break
			}
			step := steps[index]
			row.Conversion = &Conversion{
				OfferingID: step.candidate.offering.ID, ProviderID: step.candidate.offering.ProviderID,
				ProviderName: step.candidate.offering.ProviderName, ProviderShortLabel: step.candidate.offering.ProviderShortLabel,
				EstimatedCredits: step.credits, NativeQuote: step.quote,
				CreditsMetered: step.candidate.offering.CreditsMetered,
			}
			row.Status = StatusObtainable
			plan, exists := v.plans[step.candidate.key]
			switch {
			case !exists || plan.State == processing.Failed:
			case plan.State == processing.Succeeded:
				// The run finished and still did not deliver this format.
				row.Status, row.Conversion, row.Reason = StatusUnavailable, nil, reasonConversionIncomplete
			default:
				row.Status, row.Reason = StatusConverting, reasonConverting
			}
			if row.Status == StatusObtainable {
				// A failed run, or one given up for a new run, is not a format
				// that was never converted (#330).
				_, renewed := v.previous[step.candidate.key]
				if exists || renewed {
					row.Reason = reasonConversionFailed
				} else {
					row.Reason = reasonNotConverted
				}
			}
		}
		row.ReasonText = s.reasonText(row.Reason)
		result.coverage.Formats = append(result.coverage.Formats, row)
	}
	result.steps = steps
	return result
}

// deliversAny reports whether a planned step converts one of the formats.
func deliversAny(step plannedStep, formats []string) bool {
	for _, format := range step.formats {
		for _, wanted := range formats {
			if format == wanted {
				return true
			}
		}
	}
	return false
}

// lockingPlan returns the active plan that already converts one of the step's
// formats. Such a step is never started: it would convert that format twice.
func lockingPlan(v view, step plannedStep) (processing.Plan, bool) {
	for _, format := range step.formats {
		if plan, active := v.active[format]; active {
			return plan, true
		}
	}
	return processing.Plan{}, false
}

// activeConversion names the Offering an active plan runs, from the plan's own
// frozen step, so the user sees who converts and what it was estimated at.
func (s *Service) activeConversion(plan processing.Plan) *Conversion {
	if len(plan.Steps) == 0 {
		return nil
	}
	step := plan.Steps[len(plan.Steps)-1]
	offering, exists := s.gateway.Offering(step.OfferingID)
	if !exists {
		return nil
	}
	return &Conversion{
		OfferingID: offering.ID, ProviderID: offering.ProviderID,
		ProviderName: offering.ProviderName, ProviderShortLabel: offering.ProviderShortLabel,
		EstimatedCredits: step.EstimatedCredits, NativeQuote: step.NativeQuote,
		CreditsMetered: offering.CreditsMetered,
	}
}

func hasAsset(assets map[string]generation.Asset, id string) bool {
	_, exists := assets[id]
	return exists
}

func hasObtained(assets map[string]obtained, id string) bool {
	_, exists := assets[id]
	return exists
}

// Coverage projects the format coverage of a task visible to scope.
func (s *Service) Coverage(ctx context.Context, scope generation.TaskListScope, taskID generation.TaskID) (Coverage, error) {
	task, err := s.gateway.GetScoped(ctx, scope, taskID)
	if err != nil {
		return Coverage{}, err
	}
	state := s.analyze(ctx, task)
	return state.coverage, state.err
}

// Complete starts the processing plans that fill the task's obtainable
// formats and returns the updated coverage. Each plan is idempotent: asking
// again returns the plans that already exist and never starts a second paid
// run. A format that an active plan already converts is never started again,
// even when the Offering revision changed since that plan was created (the
// revision is part of the plan key, so the key alone would not notice). With
// nothing left to start the result is ErrNothingToComplete (HTTP 409). Only
// the owner may complete a task.
//
// formats narrows the request to the steps that deliver at least one of the
// named formats (the row action of one format, #323). A step converts every
// format it provides in one run, so starting it from one row also converts
// the others of that step; the coverage lists them on the step. The plan key
// is the same as for the whole completion, so a row action and 补齐格式 never
// start the same run twice. No formats means every obtainable format.
func (s *Service) Complete(ctx context.Context, owner generation.OwnerID, taskID generation.TaskID, formats ...string) (Coverage, error) {
	task, err := s.gateway.GetScoped(ctx, generation.OwnTasks(owner), taskID)
	if err != nil {
		return Coverage{}, err
	}
	state := s.analyze(ctx, task)
	if state.err != nil {
		return Coverage{}, state.err
	}
	if !state.coverage.Applicable {
		return Coverage{}, ErrNotApplicable
	}
	started := 0
	for _, step := range state.steps {
		if len(formats) > 0 && !deliversAny(step, formats) {
			continue
		}
		if _, exists := state.view.plans[step.candidate.key]; exists {
			continue
		}
		if _, locked := lockingPlan(state.view, step); locked {
			continue
		}
		_, err := s.plans.Create(ctx, processing.CreateCommand{
			OwnerID: owner, IdempotencyKey: step.candidate.key, SessionID: task.SessionID,
			SourceTaskID: task.ID, SourceAssetID: step.sourceAsset.ID,
			Steps: []processing.StepRequest{{
				OfferingID:               step.candidate.offering.ID,
				ExpectedOfferingRevision: step.candidate.offering.Revision,
				Input:                    step.input,
			}},
		})
		if err != nil {
			return Coverage{}, wrap("create plan", err)
		}
		started++
	}
	if started == 0 {
		return Coverage{}, ErrNothingToComplete
	}
	state = s.analyze(ctx, task)
	return state.coverage, state.err
}
