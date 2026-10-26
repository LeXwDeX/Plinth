package processing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const (
	Pending   = "PENDING"
	Running   = "RUNNING"
	Succeeded = "SUCCEEDED"
	Failed    = "FAILED"
	// Paused 表示计划因不能自动判定的情况停下：提交结果不明、供给项
	// 修订/费用变化或不可用、步骤任务被取消。暂停计划不被后台推进，只有
	// 用户按当前费用显式确认 Resume 后才继续；FAILED 只用于可判定的终态
	// 失败（上游任务失败、输入不兼容、提交被确定拒绝）。
	Paused = "PAUSED"
)

// 计划与步骤的原因码；Product Schema 在 ui.workspace.processing.<code>
// 下声明对应的用户文案。
const (
	ReasonTaskFailed          = "plan_task_failed"
	ReasonTaskCanceled        = "plan_task_canceled"
	ReasonOfferingChanged     = "plan_offering_changed"
	ReasonOfferingUnavailable = "plan_offering_unavailable"
	ReasonSourceIncompatible  = "plan_source_incompatible"
	ReasonSubmitRejected      = "plan_submit_rejected"
	// ReasonSourceExpired: the source's original Provider task is gone
	// (#325), at submission or as the Task's failure. Never retryable: the
	// frozen input names that task. A new plan binds the saved file instead.
	ReasonSourceExpired = "plan_source_expired"
	// A failed step's paid retry is judged again under today's rules before
	// it is shown and before it runs (#326; a plan that failed before #325
	// kept plan_task_failed): the source's original task is past its age and
	// the step takes no saved file that fits (no retry), the source no longer
	// fits the step at all (no retry), or the retry will use the saved file
	// instead of the expired original task (retry, with that reason).
	ReasonRetrySourceExpired      = "plan_retry_source_expired"
	ReasonRetrySourceIncompatible = "plan_retry_source_incompatible"
	ReasonRetrySavedFile          = "plan_retry_saved_file"
	// A PAUSED plan's resumable step is judged by the same rule before a
	// continue is shown and before it runs (#326 P): its source's original
	// task is gone and the step takes no saved file that fits, or the source
	// no longer fits the step (no continue; the plan is shown FAILED so it
	// holds nothing); its frozen input names the gone task while an approved
	// grant holds that input and key (no continue: rebinding under the same
	// key, or a new key beside the grant, is not safe); or it does not, and
	// Resume freezes the step again on the saved file, to be confirmed at the
	// new price.
	ReasonResumeSourceExpired      = "plan_resume_source_expired"
	ReasonResumeSourceIncompatible = "plan_resume_source_incompatible"
	ReasonResumeFrozenExpired      = "plan_resume_frozen_expired"
	ReasonResumeSavedFile          = "plan_resume_saved_file"
	ReasonPrepareUnavailable       = "plan_prepare_unavailable"
	ReasonSubmitUnknown            = "plan_submit_unknown"
	// ReasonSubmitUnreconciled: the kernel reports an idempotency conflict for
	// the step key, i.e. an earlier submission under that key was accepted
	// with a different request (catalog revision or input changed since).
	// The paid Task exists but cannot be linked automatically, so the plan
	// stays paused and is not resumable; replaying would risk a second charge.
	ReasonSubmitUnreconciled = "plan_submit_unreconciled"
	ReasonConsentRequired    = "plan_consent_required"
	ReasonReconciledAbsent   = "plan_reconciled_absent"
	// A same-scope Task is linked for observation, but its frozen request cannot
	// be proved to be this step. Its output must never feed a following step.
	ReasonReconciledMismatch = "plan_reconciled_mismatch"
)

var (
	ErrInvalid                = errors.New("处理计划无效")
	ErrMissing                = errors.New("处理计划不存在")
	ErrConflict               = errors.New("处理计划已变化")
	ErrReconcileUndetermined  = errors.New("processing reconciliation undetermined")
	ErrReconcileFrozenChanged = errors.New("reconciled processing input remains bound to the original grant")
)

type Step struct {
	NativeQuote      *schema.NativeQuote   `json:"native_quote,omitempty"`
	Capability       generation.Capability `json:"capability"`
	OfferingID       generation.OfferingID `json:"offering_id"`
	Input            json.RawMessage       `json:"input"`
	EstimatedCredits int                   `json:"estimated_credits"`
	Revision         string                `json:"revision"`
	State            string                `json:"state"`
	TaskID           generation.TaskID     `json:"task_id,omitempty"`
	Retry            int                   `json:"retry"`
	Failure          string                `json:"failure,omitempty"`
	Retryable        bool                  `json:"retryable,omitempty"`
	// Resumable marks a PAUSED step that Resume may continue after the user
	// confirms the current cost; unreconciled submissions are never resumable.
	Resumable bool `json:"resumable,omitempty"`
	// SubmissionUncertain keeps the original catalog revision frozen until a
	// previously accepted Task is recovered or the original key is safely replayed.
	SubmissionUncertain bool                       `json:"submission_uncertain,omitempty"`
	PreparedInputID     generation.PreparedInputID `json:"prepared_input_id,omitempty"`
	PreparedInputHash   string                     `json:"prepared_input_hash,omitempty"`
	OperationID         string                     `json:"operation_id,omitempty"`
	PlanStepID          string                     `json:"plan_step_id,omitempty"`
	IdempotencyKey      string                     `json:"idempotency_key,omitempty"`
	AuthorizationRef    string                     `json:"authorization_request_id,omitempty"`
}

type Plan struct {
	Harness       harness.Binding    `json:"harness,omitempty"`
	ID            string             `json:"id"`
	OwnerID       generation.OwnerID `json:"-"`
	SessionID     string             `json:"session_id"`
	SourceTaskID  generation.TaskID  `json:"source_task_id"`
	SourceAssetID generation.AssetID `json:"source_asset_id"`
	State         string             `json:"state"`
	Steps         []Step             `json:"steps"`
	Failure       string             `json:"failure,omitempty"`
	Version       int64              `json:"version"`
	RequestHash   string             `json:"-"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type StepRequest struct {
	OfferingID               generation.OfferingID `json:"offering_id"`
	ExpectedOfferingRevision string                `json:"expected_offering_revision"`
	Input                    json.RawMessage       `json:"input"`
}

type CreateCommand struct {
	Harness        harness.Binding    `json:"-"`
	OwnerID        generation.OwnerID `json:"-"`
	IdempotencyKey string             `json:"-"`
	SessionID      string             `json:"session_id"`
	SourceTaskID   generation.TaskID  `json:"source_task_id"`
	SourceAssetID  generation.AssetID `json:"source_asset_id"`
	Steps          []StepRequest      `json:"steps"`
}

type Store interface {
	CreateProcessingPlan(context.Context, Plan) error
	GetProcessingPlan(context.Context, generation.OwnerID, string) (Plan, error)
	ListProcessingPlans(context.Context, generation.OwnerID, string) ([]Plan, error)
	// ListProcessingPlansBySource returns every plan of the owner's session
	// that was made from one source task, newest first and without the page
	// cap of ListProcessingPlans: a caller deciding whether a paid step may be
	// started again must see each plan of that task, however old it is.
	ListProcessingPlansBySource(ctx context.Context, owner generation.OwnerID, session string, source generation.TaskID) ([]Plan, error)
	// ListActiveProcessingPlans 按 id 升序返回 id > after 的 PENDING/RUNNING
	// 计划，至多 limit 条。
	ListActiveProcessingPlans(ctx context.Context, after string, limit int) ([]Plan, error)
	SaveProcessingPlan(context.Context, Plan) error
}

type Catalog interface {
	Get(generation.OfferingID) (generation.Offering, bool)
	// OfferingAvailable 报告运行时接线是否可用；Resume 复用同一判定，
	// 避免把因供给不可用而暂停的步骤提交成必然失败的任务。
	OfferingAvailable(generation.OfferingID) bool
	Normalize(generation.Capability, generation.OfferingID, json.RawMessage) (generation.Offering, json.RawMessage, error)
}

// RetryCommand and ResumeCommand carry the plan and catalog revisions the user
// saw before explicitly requesting another paid action.
type RetryCommand struct {
	OwnerID                  generation.OwnerID `json:"-"`
	PlanID                   string             `json:"-"`
	ExpectedPlanVersion      int64              `json:"expected_plan_version"`
	ExpectedOfferingRevision string             `json:"expected_offering_revision"`
}

type ResumeCommand struct {
	OwnerID                  generation.OwnerID `json:"-"`
	PlanID                   string             `json:"-"`
	ExpectedPlanVersion      int64              `json:"expected_plan_version"`
	ExpectedOfferingRevision string             `json:"expected_offering_revision"`
	AuthorizationRef         string             `json:"authorization_request_id"`
}

type Gateway interface {
	Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error)
	LookupByIdempotency(context.Context, generation.OwnerID, string) (generation.Task, error)
	VerifyPreparedTaskReplay(context.Context, generation.SubmitPreparedCommand) (generation.Task, error)
	// GetPreparedInput reads a step's frozen input, so a paused step can be
	// judged against today's source rules before it is continued (#326).
	GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error)
	PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error)
	SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error)
}

type Service struct {
	store   Store
	catalog Catalog
	gateway Gateway
	logger  *slog.Logger
	// grants lets the session approval policy approve a step that needs no
	// human. Without it every step waits for a person, as before.
	grants *executiongrant.Service
	// sourcePreference orders the model variants a later step binds from the
	// previous step's result (#334); see WithSourcePreference.
	sourcePreference []string
}

// WithGrants enables approval by session policy for plan steps. The grant is
// still bound to the exact frozen input, plan operation and step key.
func (s *Service) WithGrants(grants *executiongrant.Service) *Service {
	s.grants = grants
	return s
}

// WithSourcePreference sets the model variant order (the Schema's
// source_variant_preference, catalog.ProcessingSourcePreference) used to pick
// the file a step from the second on takes from the previous step's result,
// so it is the file the chat tool and the panel would pick (GLB first), not
// whichever the Provider listed first. The first step keeps the plan's chosen
// source asset. Without it the main model is picked by required-first and
// ordinal only, as before.
func (s *Service) WithSourcePreference(prefer []string) *Service {
	s.sourcePreference = append([]string(nil), prefer...)
	return s
}

// planActor is the non-human principal that asks for a plan step grant. The
// grant is created pending and only session policy may approve it here.
const planActor = "processing-plan"

// MaxPlanSteps 是持久计划的步骤上限；Chat 工具 Schema 的 maxItems 不得超过它。
const MaxPlanSteps = 10

// approveStepByPolicy creates the plan step's exact grant and lets the session
// approval policy decide. It returns the approved grant's request ID, or ""
// when a person must decide (or anything fails: the paused consent step is the
// safe fallback and never submits).
func (s *Service) approveStepByPolicy(ctx context.Context, plan *Plan, step *Step) string {
	if s.grants == nil || s.grants.Grants == nil || s.grants.Prepared == nil {
		return ""
	}
	grant, err := s.grants.Prepare(ctx, executiongrant.PrepareCommand{
		Harness:           plan.Harness,
		Principal:         executiongrant.Principal{OwnerID: string(plan.OwnerID), ActorID: planActor, Source: executiongrant.HumanBrowser},
		SessionID:         plan.SessionID,
		OfferingID:        string(step.OfferingID),
		OfferingRevision:  step.Revision,
		PreparedInputID:   string(step.PreparedInputID),
		PreparedInputHash: step.PreparedInputHash,
		IdempotencyKey:    step.IdempotencyKey,
		OperationID:       step.OperationID,
		PlanStepID:        step.PlanStepID,
	})
	if err == nil {
		grant, err = s.grants.ApplyPolicy(ctx, grant)
	}
	if err != nil {
		s.logger.Warn("按批准方式准备处理步骤失败，改为等待确认", "plan_id", plan.ID, "error", err)
		return ""
	}
	if grant.Status == executiongrant.Approved && grant.ApprovedByPolicy() {
		return grant.RequestID
	}
	return ""
}

func New(store Store, catalog Catalog, gateway Gateway, logger *slog.Logger) (*Service, error) {
	if store == nil || catalog == nil || gateway == nil {
		return nil, errors.New("处理计划缺少存储、目录或任务内核")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, catalog: catalog, gateway: gateway, logger: logger}, nil
}

// PlanID is the identity Create gives a plan: it only depends on the owner
// and the idempotency key, so a caller that derives its keys deterministically
// can find the plan again without keeping a reference to it.
func PlanID(owner generation.OwnerID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(string(owner) + ":" + idempotencyKey))
	return hex.EncodeToString(digest[:])
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Plan, error) {
	if !command.Harness.Valid() || command.OwnerID == "" || command.SessionID == "" || command.SourceTaskID == "" || command.SourceAssetID == "" || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 200 || len(command.Steps) == 0 || len(command.Steps) > MaxPlanSteps {
		return Plan{}, ErrInvalid
	}
	requestBytes, err := json.Marshal(command)
	if err != nil {
		return Plan{}, err
	}
	if !command.Harness.IsZero() {
		requestBytes, err = json.Marshal(struct {
			Request json.RawMessage `json:"request"`
			Harness harness.Binding `json:"harness"`
		}{requestBytes, command.Harness})
		if err != nil {
			return Plan{}, err
		}
	}
	requestHash := sha256.Sum256(requestBytes)
	planID := PlanID(command.OwnerID, command.IdempotencyKey)
	if existing, err := s.store.GetProcessingPlan(ctx, command.OwnerID, planID); err == nil {
		if existing.RequestHash != hex.EncodeToString(requestHash[:]) {
			return Plan{}, ErrConflict
		}
		return existing, nil
	} else if !errors.Is(err, ErrMissing) {
		return Plan{}, err
	}
	source, err := s.gateway.Get(ctx, command.OwnerID, command.SourceTaskID)
	if err != nil || source.SessionID != command.SessionID || source.State != generation.TaskSucceeded || !hasAsset(source, command.SourceAssetID) {
		return Plan{}, ErrInvalid
	}
	steps := make([]Step, 0, len(command.Steps))
	previous := source.Offering
	for _, requested := range command.Steps {
		if requested.ExpectedOfferingRevision == "" {
			return Plan{}, ErrInvalid
		}
		offering, ok := s.catalog.Get(requested.OfferingID)
		if !ok || !offering.Enabled || offering.Domain != generation.DomainProcessing {
			return Plan{}, ErrInvalid
		}
		if offering.Revision != requested.ExpectedOfferingRevision {
			return Plan{}, ErrConflict
		}
		var input map[string]json.RawMessage
		if len(requested.Input) == 0 || json.Unmarshal(requested.Input, &input) != nil || input == nil {
			return Plan{}, ErrInvalid
		}
		for _, parameter := range offering.Parameters {
			if _, source := schema.SourceParameterRole(parameter); !source {
				continue
			}
			if _, provided := input[parameter.Name]; provided {
				return Plan{}, ErrInvalid
			}
		}
		var previewInput json.RawMessage
		if len(steps) == 0 {
			// The first step has a real persisted source and selected asset.
			previewInput, err = buildInput(offering, source, command.SourceAssetID, s.sourcePreference, requested.Input)
		} else {
			var contract generation.ModelSourceContract
			contract, err = generation.BindProjectedModelSource(offering, previous)
			if err == nil {
				// Normalize parameter shape only; this placeholder is never
				// stored as a step input or presented as a Task/Asset identity.
				input[contract.Parameter] = json.RawMessage(`"projected-source"`)
				previewInput, err = json.Marshal(input)
			}
		}
		if err != nil {
			return Plan{}, ErrInvalid
		}

		if _, _, err := s.catalog.Normalize(offering.Capability, offering.ID, previewInput); err != nil {
			return Plan{}, ErrInvalid
		}
		// B8 按数量计费：声明了 unit 参数（n/max_images）的 Offering
		// 按用户选择的张数乘算预计积分。
		stepCredits, quote, priceErr := processingPrice(offering, requested.Input)
		if priceErr != nil {
			return Plan{}, ErrInvalid
		}
		steps = append(steps, Step{Capability: offering.Capability, OfferingID: offering.ID, Input: requested.Input, EstimatedCredits: stepCredits, NativeQuote: quote, Revision: offering.Revision, State: Pending})
		previous = offering
	}
	now := time.Now().UTC()
	plan := Plan{Harness: command.Harness, ID: planID, OwnerID: command.OwnerID, SessionID: command.SessionID, SourceTaskID: command.SourceTaskID, SourceAssetID: command.SourceAssetID, State: Pending, Steps: steps, Version: 1, RequestHash: hex.EncodeToString(requestHash[:]), CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateProcessingPlan(ctx, plan); err != nil {
		if existing, lookupErr := s.store.GetProcessingPlan(ctx, command.OwnerID, planID); lookupErr == nil {
			if existing.RequestHash == plan.RequestHash {
				return existing, nil
			}
			return Plan{}, ErrConflict
		}
		return Plan{}, err
	}
	return plan, nil
}

func hasAsset(task generation.Task, id generation.AssetID) bool {
	for _, asset := range task.Assets {
		if asset.ID == id && asset.Role == "model" && asset.StorageKey != "" {
			return true
		}
	}
	return false
}

// Get reads a plan as it is shown: a failed step's paid retry and a paused
// step's paid continue are judged under today's rules (assess).
func (s *Service) Get(ctx context.Context, owner generation.OwnerID, id string) (Plan, error) {
	plan, err := s.store.GetProcessingPlan(ctx, owner, id)
	if err != nil {
		return Plan{}, err
	}
	return s.assess(ctx, plan), nil
}

func (s *Service) List(ctx context.Context, owner generation.OwnerID, session string) ([]Plan, error) {
	plans, err := s.store.ListProcessingPlans(ctx, owner, session)
	return s.assessAll(ctx, plans), err
}

// ListBySource returns all plans of the owner's session made from one source
// task, uncapped.
func (s *Service) ListBySource(ctx context.Context, owner generation.OwnerID, session string, source generation.TaskID) ([]Plan, error) {
	plans, err := s.store.ListProcessingPlansBySource(ctx, owner, session, source)
	return s.assessAll(ctx, plans), err
}

func (s *Service) assessAll(ctx context.Context, plans []Plan) []Plan {
	for index := range plans {
		plans[index] = s.assess(ctx, plans[index])
	}
	return plans
}

// assess is the plan as it is shown (#326). A FAILED plan's retryable step
// and a PAUSED plan's resumable step are judged again under today's rules
// (generation.JudgeSourceRetry). A paused step that cannot continue is shown
// FAILED with the reason, so nothing offers a paid continue and neither the
// 加工 panel nor the format coverage counts the plan as active. The stored
// plan is not changed; Retry and Resume apply the same judgment before they
// accept a paid attempt.
func (s *Service) assess(ctx context.Context, plan Plan) Plan {
	switch plan.State {
	case Failed:
		for index := range plan.Steps {
			step := plan.Steps[index]
			if step.State != Failed || !step.Retryable {
				continue
			}
			reason, retryable, known := s.retryOutlook(ctx, plan, index)
			if !known {
				return plan
			}
			plan.Steps = slices.Clone(plan.Steps)
			shown := &plan.Steps[index]
			shown.Retryable = retryable
			if reason != "" {
				shown.Failure, plan.Failure = reason, reason
			}
			return plan
		}
	case Paused:
		for index := range plan.Steps {
			if !judgedOnResume(plan.Steps[index]) {
				continue
			}
			reason, resumable, known := s.resumeOutlook(ctx, plan, index)
			if !known || reason == "" {
				return plan
			}
			plan.Steps = slices.Clone(plan.Steps)
			shown := &plan.Steps[index]
			shown.Failure, plan.Failure = reason, reason
			if !resumable {
				shown.State, shown.Resumable, shown.Retryable = Failed, false, false
				plan.State = Failed
			}
			return plan
		}
	}
	return plan
}

// judgedOnResume: a paused step a continue would run. A submission whose
// result is unknown is reconciled first; its frozen input is not rebound.
func judgedOnResume(step Step) bool {
	return step.State == Paused && step.Resumable && !step.SubmissionUncertain && step.Failure != ReasonSubmitUnknown
}

// preservesFrozenInput: an approved grant (or a reconciled-absent step's
// grant) reserves this step's key for its exact frozen input; Resume keeps
// both rather than rebinding (see Resume).
func preservesFrozenInput(step Step) bool {
	return step.Failure != ReasonTaskCanceled && step.PreparedInputID != "" &&
		(step.AuthorizationRef != "" || step.Failure == ReasonReconciledAbsent)
}

// sourceOutlook judges step index's source as a new attempt would bind it
// (advance → buildInput) against frozen, the input the earlier attempt froze
// (#326). known is false when the source or the Offering cannot be read now;
// the stored plan then stands.
func (s *Service) sourceOutlook(ctx context.Context, plan Plan, index int, frozen json.RawMessage) (generation.SourceRetry, bool) {
	offering, ok := s.catalog.Get(plan.Steps[index].OfferingID)
	if !ok {
		return "", false
	}
	sourceID, asset := plan.SourceTaskID, plan.SourceAssetID
	if index > 0 {
		sourceID, asset = plan.Steps[index-1].TaskID, ""
	}
	if sourceID == "" {
		return "", false
	}
	source, err := s.gateway.Get(ctx, plan.OwnerID, sourceID)
	if err != nil {
		return "", false
	}
	return generation.JudgeSourceRetry(offering, source, asset, frozen), true
}

// retryOutlook says what a failed step's retry can do: whether it can run,
// and the reason to show when it differs from the stored one. The failed
// Task's input is what the earlier attempt froze.
func (s *Service) retryOutlook(ctx context.Context, plan Plan, index int) (reason string, retryable bool, known bool) {
	var frozen json.RawMessage
	if taskID := plan.Steps[index].TaskID; taskID != "" {
		if failed, err := s.gateway.Get(ctx, plan.OwnerID, taskID); err == nil {
			frozen = failed.Input
		}
	}
	judgment, known := s.sourceOutlook(ctx, plan, index, frozen)
	switch {
	case !known:
		return "", false, false
	case judgment == generation.SourceRetryExpired:
		return ReasonRetrySourceExpired, false, true
	case judgment == generation.SourceRetryIncompatible:
		return ReasonRetrySourceIncompatible, false, true
	case judgment == generation.SourceRetrySavedFile:
		return ReasonRetrySavedFile, true, true
	}
	return "", true, true
}

// resumeOutlook says what a paused step's continue can do (#326 P), judged
// against the step's frozen PreparedInput when it has one: the reason to
// show ("" keeps the stored one) and whether a continue may run. A frozen
// input that cannot be read now is judged as none: a continue certain to fail
// is still found, and nothing is frozen again on a guess.
func (s *Service) resumeOutlook(ctx context.Context, plan Plan, index int) (reason string, resumable bool, known bool) {
	step := plan.Steps[index]
	var frozen json.RawMessage
	if step.PreparedInputID != "" {
		if prepared, err := s.gateway.GetPreparedInput(ctx, plan.OwnerID, step.PreparedInputID); err == nil {
			frozen = prepared.NormalizedInput
		}
	}
	judgment, known := s.sourceOutlook(ctx, plan, index, frozen)
	switch {
	case !known:
		return "", false, false
	case judgment == generation.SourceRetryExpired:
		return ReasonResumeSourceExpired, false, true
	case judgment == generation.SourceRetryIncompatible:
		return ReasonResumeSourceIncompatible, false, true
	case judgment == generation.SourceRetrySavedFile && preservesFrozenInput(step):
		return ReasonResumeFrozenExpired, false, true
	case judgment == generation.SourceRetrySavedFile:
		return ReasonResumeSavedFile, true, true
	}
	return "", true, true
}

// GrantIdentity returns only server-frozen coordinates for a step currently
// waiting for a human decision. HTTP must never trust plan scope or the
// idempotency key supplied in a grant request body.
type GrantIdentity struct {
	Harness        harness.Binding
	SessionID      string
	OperationID    string
	PlanStepID     string
	IdempotencyKey string
}

func (s *Service) ExecutionGrantIdentity(ctx context.Context, owner generation.OwnerID, planID, preparedID, planStepID string) (GrantIdentity, error) {
	if owner == "" || planID == "" || preparedID == "" || planStepID == "" {
		return GrantIdentity{}, ErrInvalid
	}
	plan, err := s.store.GetProcessingPlan(ctx, owner, planID)
	if err != nil {
		return GrantIdentity{}, err
	}
	if plan.State != Paused || plan.Failure != ReasonConsentRequired {
		return GrantIdentity{}, ErrConflict
	}
	for _, step := range plan.Steps {
		if string(step.PreparedInputID) == preparedID && step.PlanStepID == planStepID &&
			step.State == Paused && step.Failure == ReasonConsentRequired &&
			step.OperationID == plan.ID && step.IdempotencyKey != "" {
			return GrantIdentity{Harness: plan.Harness, SessionID: plan.SessionID, OperationID: step.OperationID,
				PlanStepID: step.PlanStepID, IdempotencyKey: step.IdempotencyKey}, nil
		}
	}
	return GrantIdentity{}, ErrMissing
}

// Retry is an explicit new paid attempt. Completed predecessors keep their Task IDs.
func (s *Service) Retry(ctx context.Context, command RetryCommand) (Plan, error) {
	if command.ExpectedPlanVersion <= 0 || command.ExpectedOfferingRevision == "" {
		return Plan{}, ErrInvalid
	}
	plan, err := s.store.GetProcessingPlan(ctx, command.OwnerID, command.PlanID)
	if err != nil {
		return Plan{}, err
	}
	if plan.Version != command.ExpectedPlanVersion || plan.State != Failed {
		return Plan{}, ErrConflict
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.State != Failed {
			continue
		}
		if step.TaskID == "" {
			return Plan{}, ErrConflict
		}
		// #326: a retry that is certain to fail under today's rules is not a
		// paid attempt to offer or accept.
		if _, retryable, known := s.retryOutlook(ctx, plan, index); known && !retryable {
			return Plan{}, ErrConflict
		}
		task, err := s.gateway.Get(ctx, command.OwnerID, step.TaskID)
		if err != nil || task.State != generation.TaskFailed || (task.Failure != nil && (task.Failure.Ambiguous || task.Failure.Class == generation.FailureSourceExpired)) {
			return Plan{}, ErrConflict
		}
		offering, ok := s.catalog.Get(step.OfferingID)
		if !ok || !offering.Enabled || !s.catalog.OfferingAvailable(step.OfferingID) ||
			offering.Domain != generation.DomainProcessing || offering.Capability != step.Capability ||
			offering.Revision != command.ExpectedOfferingRevision {
			return Plan{}, ErrConflict
		}
		step.State, step.TaskID, step.Failure, step.Retryable = Pending, "", "", false
		step.PreparedInputID, step.PreparedInputHash, step.AuthorizationRef = "", "", ""
		step.OperationID, step.PlanStepID, step.IdempotencyKey = "", "", ""
		credits, quote, priceErr := processingPrice(offering, step.Input)
		if priceErr != nil {
			return Plan{}, ErrConflict
		}
		step.Revision, step.EstimatedCredits, step.NativeQuote = offering.Revision, credits, quote
		step.Retry++
		plan.State, plan.Failure, plan.UpdatedAt = Pending, "", time.Now().UTC()
		if err := s.savePlan(ctx, &plan); err != nil {
			return Plan{}, err
		}
		return plan, nil
	}
	return Plan{}, ErrConflict
}

// Resume continues a PAUSED plan after the user confirms the paused step's
// current cost. Unknown submissions reuse the same idempotency key, so a
// submission that actually landed is replayed instead of charged again;
// a canceled step task is an explicit new paid attempt with a new key.
func (s *Service) Resume(ctx context.Context, command ResumeCommand) (Plan, error) {
	if command.ExpectedPlanVersion <= 0 || command.ExpectedOfferingRevision == "" {
		return Plan{}, ErrInvalid
	}
	plan, err := s.store.GetProcessingPlan(ctx, command.OwnerID, command.PlanID)
	if err != nil {
		return Plan{}, err
	}
	if plan.Version != command.ExpectedPlanVersion || plan.State != Paused {
		return Plan{}, ErrConflict
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.State != Paused {
			continue
		}
		if !resumableReason(step.Failure) {
			return Plan{}, ErrConflict
		}
		// #326 P: a continue certain to fail is not a paid attempt to accept;
		// a frozen input naming a gone task is frozen again on the saved file
		// (whatever grant came with this request was made for the old input).
		if judgedOnResume(*step) {
			if reason, resumable, known := s.resumeOutlook(ctx, plan, index); known && !resumable {
				return Plan{}, ErrConflict
			} else if known && reason == ReasonResumeSavedFile {
				return s.freezeAgain(ctx, plan, index, command.ExpectedOfferingRevision)
			}
		}
		if step.Failure == ReasonConsentRequired {
			if command.AuthorizationRef == "" || step.PreparedInputID == "" {
				return Plan{}, ErrInvalid
			}
			step.AuthorizationRef = command.AuthorizationRef
			step.State, step.Failure, step.Resumable = Pending, "", false
			plan.State, plan.Failure, plan.UpdatedAt = Pending, "", time.Now().UTC()
			if err := s.savePlan(ctx, &plan); err != nil {
				return Plan{}, err
			}
			return plan, nil
		}
		// Older steps_json has no submission_uncertain field. Its persisted
		// failure reason is enough to identify a lost Submit response. Recover
		// the original Task before consulting today's Catalog: an offering may
		// have changed or disappeared after the paid Task was accepted.
		if step.Failure == ReasonSubmitUnknown || step.SubmissionUncertain {
			step.SubmissionUncertain = true
			stepKey := stepIdempotencyKey(plan, index)
			recovered, lookupErr := s.gateway.LookupByIdempotency(ctx, plan.OwnerID, stepKey)
			switch {
			case lookupErr == nil:
				if err := s.verifyRecoveredStep(ctx, recovered, plan, step, stepKey); err != nil {
					if !replayRejected(err) {
						return Plan{}, err
					}
					step.Failure, step.Resumable = ReasonSubmitUnreconciled, false
					plan.Failure, plan.UpdatedAt = ReasonSubmitUnreconciled, time.Now().UTC()
					if err := s.savePlan(ctx, &plan); err != nil {
						return Plan{}, err
					}
					return plan, nil
				}
				step.TaskID, step.State, plan.State = recovered.ID, Running, Running
				step.Failure, step.SubmissionUncertain, step.Resumable = "", false, false
				plan.Failure, plan.UpdatedAt = "", time.Now().UTC()
				if err := s.savePlan(ctx, &plan); err != nil {
					return Plan{}, err
				}
				return plan, nil
			case generation.IsCode(lookupErr, generation.ErrNotFound):
				// Only this outcome may proceed to a same-key replay.
			case generation.IsCode(lookupErr, generation.ErrConflict):
				step.Failure, step.Resumable = ReasonSubmitUnreconciled, false
				plan.Failure, plan.UpdatedAt = ReasonSubmitUnreconciled, time.Now().UTC()
				if err := s.savePlan(ctx, &plan); err != nil {
					return Plan{}, err
				}
				return plan, nil
			default:
				return Plan{}, lookupErr
			}
		}
		offering, ok := s.catalog.Get(step.OfferingID)
		if !ok || !offering.Enabled || !s.catalog.OfferingAvailable(step.OfferingID) ||
			offering.Domain != generation.DomainProcessing ||
			offering.Capability != step.Capability ||
			offering.Revision != command.ExpectedOfferingRevision {
			return Plan{}, ErrConflict
		}
		stepCredits, quote, priceErr := processingPrice(offering, step.Input)
		if priceErr != nil {
			return Plan{}, ErrConflict
		}
		// No Task does not mean no Grant: an approved grant reserves this key
		// for its exact PreparedInput, whatever the current pause reason is
		// (for example offering_unavailable after a reconciled resume). Keep
		// that frozen identity while the offering revision and price are
		// unchanged; the key cannot be rebound to a replacement input.
		preserveFrozenInput := preservesFrozenInput(*step)
		if preserveFrozenInput && !step.SubmissionUncertain && (offering.Revision != step.Revision || stepCredits != step.EstimatedCredits || !schema.SameNativeQuote(quote, step.NativeQuote)) {
			// The current cost differs from the frozen one. The approved grant
			// stays executable for the old key, and a point-in-time lookup cannot
			// retire it, so a new key could admit a second paid Task. Refuse until
			// the original cost returns; the user may create a new plan instead.
			return Plan{}, ErrReconcileFrozenChanged
		}
		if step.Failure == ReasonTaskCanceled {
			step.TaskID = ""
			step.IdempotencyKey = ""
			step.Retry++
		}
		if !step.SubmissionUncertain && !preserveFrozenInput {
			step.PreparedInputID, step.PreparedInputHash, step.AuthorizationRef = "", "", ""
			step.OperationID, step.PlanStepID = "", ""
		}
		if !step.SubmissionUncertain {
			step.Revision, step.EstimatedCredits, step.NativeQuote = offering.Revision, stepCredits, quote
		}
		step.State, step.Failure, step.Retryable, step.Resumable = Pending, "", false, false
		plan.State, plan.Failure, plan.UpdatedAt = Pending, "", time.Now().UTC()
		if err := s.savePlan(ctx, &plan); err != nil {
			return Plan{}, err
		}
		return plan, nil
	}
	return Plan{}, ErrConflict
}

// freezeAgain drops a paused step's frozen input, which names a source task
// the Provider no longer keeps, so the step is prepared again under today's
// rules (the saved model file) and waits for consent at the current price
// (#326 P). It is a new attempt with a new key: a grant someone approved for
// the old input can never run it.
func (s *Service) freezeAgain(ctx context.Context, plan Plan, index int, expectedRevision string) (Plan, error) {
	step := &plan.Steps[index]
	offering, ok := s.catalog.Get(step.OfferingID)
	if !ok || !offering.Enabled || !s.catalog.OfferingAvailable(step.OfferingID) ||
		offering.Domain != generation.DomainProcessing || offering.Capability != step.Capability ||
		offering.Revision != expectedRevision {
		return Plan{}, ErrConflict
	}
	credits, quote, priceErr := processingPrice(offering, step.Input)
	if priceErr != nil {
		return Plan{}, ErrConflict
	}
	step.TaskID, step.PreparedInputID, step.PreparedInputHash, step.AuthorizationRef = "", "", "", ""
	step.OperationID, step.PlanStepID, step.IdempotencyKey = "", "", ""
	step.Retry++
	step.Revision, step.EstimatedCredits, step.NativeQuote = offering.Revision, credits, quote
	step.State, step.Failure, step.Retryable, step.Resumable = Pending, "", false, false
	plan.State, plan.Failure, plan.UpdatedAt = Pending, "", time.Now().UTC()
	if err := s.savePlan(ctx, &plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.advanceAll(ctx); err != nil {
			s.logger.Error("推进处理计划失败", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// activePlanPageSize 只是单次查询的批量大小，不是推进上限：advanceAll
// 逐页读取直到活动计划耗尽。
const activePlanPageSize = 100

func (s *Service) advanceAll(ctx context.Context) error {
	after := ""
	for {
		plans, err := s.store.ListActiveProcessingPlans(ctx, after, activePlanPageSize)
		if err != nil {
			return err
		}
		for _, plan := range plans {
			if err := ctx.Err(); err != nil {
				return nil
			}
			if err := s.advance(ctx, &plan); err != nil {
				s.logger.Error("处理计划步骤失败", "plan_id", plan.ID, "error", err)
			}
		}
		if len(plans) < activePlanPageSize {
			return nil
		}
		after = plans[len(plans)-1].ID
	}
}

func (s *Service) advance(ctx context.Context, plan *Plan) error {
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.State == Succeeded {
			continue
		}
		if step.TaskID != "" {
			task, err := s.gateway.Get(ctx, plan.OwnerID, step.TaskID)
			if err != nil {
				return err
			}
			// Reconciliation may discover a paid Task with a different frozen
			// source or revision. Observe it, but do not interpret its success as
			// this step's success, offer a paid retry, or advance its successors.
			if step.Failure == ReasonReconciledMismatch {
				switch task.State {
				case generation.TaskSucceeded, generation.TaskCanceled, generation.TaskFailed:
					return s.stop(ctx, plan, step, Paused, ReasonReconciledMismatch)
				default:
					return nil
				}
			}
			switch task.State {
			case generation.TaskSucceeded:
				step.State = Succeeded
				if index == len(plan.Steps)-1 {
					plan.State = Succeeded
				}
			case generation.TaskCanceled:
				step.State, plan.State = Paused, Paused
				step.Failure, plan.Failure = ReasonTaskCanceled, ReasonTaskCanceled
				step.Resumable = true
			case generation.TaskFailed:
				step.State, plan.State = Failed, Failed
				step.Failure = ReasonTaskFailed
				step.Retryable = task.Failure == nil || !task.Failure.Ambiguous
				if task.Failure != nil && task.Failure.Class == generation.FailureSourceExpired {
					step.Failure, step.Retryable = ReasonSourceExpired, false
				}
				plan.Failure = step.Failure
			default:
				return nil
			}
			plan.UpdatedAt = time.Now().UTC()
			return s.savePlan(ctx, plan)
		}
		stepKey := stepIdempotencyKey(*plan, index)
		// Reconcile a committed Task before consulting the possibly changed
		// catalog. A lost Submit response must never create a second paid Task.
		recovered, lookupErr := s.gateway.LookupByIdempotency(ctx, plan.OwnerID, stepKey)
		switch {
		case lookupErr == nil:
			if err := s.verifyRecoveredStep(ctx, recovered, *plan, step, stepKey); err != nil {
				if !replayRejected(err) {
					return err
				}
				return s.stop(ctx, plan, step, Paused, ReasonSubmitUnreconciled)
			}
			step.TaskID, step.State, plan.State = recovered.ID, Running, Running
			step.Failure, step.SubmissionUncertain = "", false
			plan.Failure, plan.UpdatedAt = "", time.Now().UTC()
			return s.savePlan(ctx, plan)
		case generation.IsCode(lookupErr, generation.ErrNotFound):
			// A missing Task permits replay only with the original frozen
			// revision and price. If either changed, retain a non-resumable
			// pause rather than silently charging under a new offering.
		case generation.IsCode(lookupErr, generation.ErrConflict):
			return s.stop(ctx, plan, step, Paused, ReasonSubmitUnreconciled)
		default:
			return s.stop(ctx, plan, step, Paused, ReasonSubmitUnknown)
		}
		var source generation.Task
		var assetID generation.AssetID
		var err error
		if index == 0 {
			source, err = s.gateway.Get(ctx, plan.OwnerID, plan.SourceTaskID)
			assetID = plan.SourceAssetID
		} else {
			source, err = s.gateway.Get(ctx, plan.OwnerID, plan.Steps[index-1].TaskID)
		}
		if err != nil {
			return err
		}
		offering, ok := s.catalog.Get(step.OfferingID)
		if !ok || !offering.Enabled || !s.catalog.OfferingAvailable(step.OfferingID) {
			return s.stop(ctx, plan, step, Paused, ReasonOfferingUnavailable)
		}
		credits, quote, priceErr := processingPrice(offering, step.Input)
		if priceErr != nil || offering.Revision != step.Revision || credits != step.EstimatedCredits || !schema.SameNativeQuote(quote, step.NativeQuote) {
			if step.SubmissionUncertain {
				return s.stop(ctx, plan, step, Paused, ReasonSubmitUnreconciled)
			}
			return s.stop(ctx, plan, step, Paused, ReasonOfferingChanged)
		}
		input, err := buildInput(offering, source, assetID, s.sourcePreference, step.Input)
		if err != nil {
			if errors.Is(err, generation.ErrSourceExpired) {
				return s.stop(ctx, plan, step, Failed, ReasonRetrySourceExpired)
			}
			return s.stop(ctx, plan, step, Failed, ReasonSourceIncompatible)
		}
		if step.PreparedInputID == "" {
			prepared, prepareErr := s.gateway.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: plan.OwnerID, SessionID: plan.SessionID, Capability: step.Capability, OfferingID: step.OfferingID, Input: input})
			if prepareErr != nil {
				if errors.Is(prepareErr, generation.ErrSourceExpired) {
					return s.stop(ctx, plan, step, Failed, ReasonSourceExpired)
				}
				if submitRejected(prepareErr) || generation.IsCode(prepareErr, generation.ErrConflict) {
					return s.stop(ctx, plan, step, Failed, ReasonSubmitRejected)
				}
				// Preparation has not submitted a paid Task. A transient read or
				// storage failure may be explicitly resumed without marking the
				// submission uncertain or bypassing the later human decision.
				return s.stop(ctx, plan, step, Paused, ReasonPrepareUnavailable)
			}
			if !schema.SameNativeQuote(prepared.NativeQuote, step.NativeQuote) {
				return s.stop(ctx, plan, step, Paused, ReasonOfferingChanged)
			}
			step.PreparedInputID, step.PreparedInputHash = prepared.ID, prepared.InputHash
			step.OperationID, step.PlanStepID, step.IdempotencyKey = plan.ID, fmt.Sprintf("%d:%d", index, step.Retry), stepKey
		}
		if step.AuthorizationRef == "" {
			// The session approval policy may approve this step without a
			// person. The frozen input and the grant are persisted before any
			// submission, so a crash is reconciled by the step key, never by a
			// blind resubmission. A step that needs a human pauses as before.
			reference := s.approveStepByPolicy(ctx, plan, step)
			if reference == "" {
				return s.stop(ctx, plan, step, Paused, ReasonConsentRequired)
			}
			step.AuthorizationRef = reference
			plan.UpdatedAt = time.Now().UTC()
			if err := s.savePlan(ctx, plan); err != nil {
				return err
			}
		}
		accepted, err := s.gateway.SubmitPrepared(ctx, generation.SubmitPreparedCommand{Harness: plan.Harness, OwnerID: plan.OwnerID, SessionID: plan.SessionID, PreparedInputID: step.PreparedInputID, IdempotencyKey: stepKey, OperationID: step.OperationID, PlanStepID: step.PlanStepID, AuthorizationRef: step.AuthorizationRef})
		if err != nil {
			if errors.Is(err, generation.ErrConversionInProgress) {
				// Another active run of this exact conversion (a direct Agent
				// run or another step) owns the source and formats. Nothing
				// was submitted; pause and let the user continue after it ends.
				return s.stop(ctx, plan, step, Paused, ReasonPrepareUnavailable)
			}
			if generation.IsCode(err, generation.ErrForbidden) {
				step.AuthorizationRef = ""
				return s.stop(ctx, plan, step, Paused, ReasonConsentRequired)
			}
			if generation.IsCode(err, generation.ErrIdempotencyConflict) {
				return s.stop(ctx, plan, step, Paused, ReasonSubmitUnreconciled)
			}
			if errors.Is(err, generation.ErrSourceExpired) {
				return s.stop(ctx, plan, step, Failed, ReasonSourceExpired)
			}
			if submitRejected(err) {
				return s.stop(ctx, plan, step, Failed, ReasonSubmitRejected)
			}
			return s.stop(ctx, plan, step, Paused, ReasonSubmitUnknown)
		}
		step.TaskID, step.State, plan.State = accepted.TaskID, Running, Running
		step.Failure, step.SubmissionUncertain = "", false
		plan.UpdatedAt = time.Now().UTC()
		return s.savePlan(ctx, plan)
	}
	return nil
}

func matchesFrozenStep(task generation.Task, plan Plan, step *Step) bool {
	return task.ID != "" && task.OwnerID == plan.OwnerID && task.SessionID == plan.SessionID &&
		task.Capability == step.Capability && task.Offering.ID == step.OfferingID &&
		task.Offering.Revision == step.Revision
}

// A matching label/revision is insufficient: a same-key Task can carry another
// source or parameter set. Only the kernel can verify the frozen input and its
// bound grant. Legacy steps without that evidence remain paused for inspection;
// reconciliation never submits or prepares a replacement paid Task.
func (s *Service) verifyRecoveredStep(ctx context.Context, task generation.Task, plan Plan, step *Step, key string) error {
	if !matchesFrozenStep(task, plan, step) || step.PreparedInputID == "" || step.AuthorizationRef == "" || step.OperationID == "" {
		return &generation.Error{Code: generation.ErrConflict, Operation: "reconcile_processing_step"}
	}
	verified, err := s.gateway.VerifyPreparedTaskReplay(ctx, generation.SubmitPreparedCommand{
		Harness: plan.Harness,
		OwnerID: plan.OwnerID, SessionID: plan.SessionID, PreparedInputID: step.PreparedInputID,
		IdempotencyKey: key, OperationID: step.OperationID, PlanStepID: step.PlanStepID,
		AuthorizationRef: step.AuthorizationRef,
	})
	if err != nil {
		return err
	}
	if verified.ID != task.ID {
		return &generation.Error{Code: generation.ErrConflict, Operation: "reconcile_processing_step"}
	}
	return nil
}

func replayRejected(err error) bool {
	return submitRejected(err) || generation.IsCode(err, generation.ErrConflict) || generation.IsCode(err, generation.ErrIdempotencyConflict)
}

func (s *Service) stop(ctx context.Context, plan *Plan, step *Step, state, reason string) error {
	step.State, step.Failure, plan.State, plan.Failure = state, reason, state, reason
	if reason == ReasonSubmitUnknown {
		step.SubmissionUncertain = true
	}
	step.Resumable = state == Paused && resumableReason(reason)
	plan.UpdatedAt = time.Now().UTC()
	return s.savePlan(ctx, plan)
}

func (s *Service) savePlan(ctx context.Context, plan *Plan) error {
	if err := s.store.SaveProcessingPlan(ctx, *plan); err != nil {
		return err
	}
	// SaveProcessingPlan atomically advances exactly one version on success.
	// Keep the returned Retry/Resume representation in sync with persisted state.
	plan.Version++
	return nil
}

func resumableReason(reason string) bool {
	switch reason {
	case ReasonTaskCanceled, ReasonSubmitUnknown, ReasonOfferingChanged, ReasonOfferingUnavailable, ReasonConsentRequired, ReasonPrepareUnavailable, ReasonReconciledAbsent:
		return true
	}
	return false
}

// submitRejected 只把任务内核的确定性拒绝（校验、目录、所有权）视为终态
// 失败；幂等冲突说明同键请求已被受理，由调用方单独暂停；不可用、内部错误
// 或未知错误的结果不明，必须暂停。
func submitRejected(err error) bool {
	if errors.Is(err, harness.ErrStale) {
		return true
	}
	for _, code := range []generation.ErrorCode{
		generation.ErrInvalidCapability,
		generation.ErrInvalidInput,
		generation.ErrUnknownOffering,
		generation.ErrNoOffering,
		generation.ErrNotFound,
		generation.ErrForbidden,
	} {
		if generation.IsCode(err, code) {
			return true
		}
	}
	return false
}

// buildInput binds source into the step's source parameter. chosen is the
// plan's source asset for the first step and empty for later steps, whose
// file is picked from the previous result by prefer.
func buildInput(offering generation.Offering, source generation.Task, chosen generation.AssetID, prefer []string, params json.RawMessage) (json.RawMessage, error) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, err
	}
	binding, err := generation.BindModelSource(offering, source, chosen, prefer)
	if errors.Is(err, generation.ErrSourceExpired) {
		return nil, err
	}
	if err != nil {
		return nil, ErrInvalid
	}
	input[binding.Parameter], _ = json.Marshal(binding.Value)
	return json.Marshal(input)
}
