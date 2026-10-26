package generation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Local delivery completion (#330 third follow-ups) closes the one gap ordinary
// execution leaves: a Schema-declared format conversion failed (possibly
// before producing any upstream result), and a precisely related refetch or
// retry Task later delivered the same contract. The failed Task gets its own real
// SUCCEEDED state and its own registered assets, copied by immutable CAS bytes
// from the delivery Task. This is not a general resurrection path:
//
//   - only a Task whose frozen Offering and input are a declared conversion;
//   - only a failed Task with no registered asset;
//   - only the exact refetch binding or the immediately preceding plan retry;
//   - only identical input and output contracts, with no source or revision
//     change;
//   - every donor byte is opened and re-checked under the failed Task's
//     contract before any row is written;
//   - the new Attempt has no Job, Opaque, dispatch or observation, and the
//     original failed Attempt is never changed.

var (
	// ErrDeliveryCompletionNotApplicable means this Task pair has no local
	// delivery completion relation. Callers may skip it.
	ErrDeliveryCompletionNotApplicable = errors.New("任务没有可完成的本地交付关系")
	// ErrDeliveryCompletionRejected means a relation exists but its contract
	// or bytes do not permit completion. Nothing was written.
	ErrDeliveryCompletionRejected = errors.New("本地交付完成校验失败")
)

// DeliveryCompletionPreparation is the bounded operator view of one
// preparation. It is also what the maintenance command prints in dry-run.
type DeliveryCompletionPreparation struct {
	Kind              string             `json:"kind"`
	FailedTaskID      TaskID             `json:"failed_task_id"`
	DeliveryTaskID    TaskID             `json:"delivery_task_id"`
	DeliveryAttemptID AttemptID          `json:"delivery_attempt_id"`
	OriginalAttemptID AttemptID          `json:"original_attempt_id"`
	AssetCount        int                `json:"asset_count"`
	Bytes             int64              `json:"bytes"`
	ContractSHA256    string             `json:"contract_sha256"`
	AlreadyCompleted  bool               `json:"already_completed"`
	Provenance        DeliveryCompletion `json:"provenance"`
}

type preparedDeliveryCompletion struct {
	view   DeliveryCompletionPreparation
	commit ValidatedDeliveryCompletion
}

// PrepareDeliveryCompletion validates the exact failed/delivery pair without
// writing. The delivery Task must already be a stored SUCCEEDED Task.
func (m *Module) PrepareDeliveryCompletion(ctx context.Context, owner OwnerID, failedID, deliveryID TaskID) (DeliveryCompletionPreparation, error) {
	failed, err := m.repository.Load(ctx, failedID)
	if err != nil {
		return DeliveryCompletionPreparation{}, mapRepositoryError("delivery_completion", err)
	}
	delivery, err := m.repository.Load(ctx, deliveryID)
	if err != nil {
		return DeliveryCompletionPreparation{}, mapRepositoryError("delivery_completion", err)
	}
	if failed.Task.OwnerID != owner || delivery.Task.OwnerID != owner {
		return DeliveryCompletionPreparation{}, ErrDeliveryCompletionNotApplicable
	}
	prepared, err := m.prepareDeliveryCompletion(ctx, failed, delivery.Task, delivery.CurrentAttempt, delivery.Task.Assets)
	if err != nil {
		return DeliveryCompletionPreparation{}, err
	}
	return prepared.view, nil
}

// CompleteDeliveryCompletion validates and commits one local completion. It is
// idempotent for the same delivery Task; another completion of the same failed
// Task conflicts.
func (m *Module) CompleteDeliveryCompletion(ctx context.Context, owner OwnerID, failedID, deliveryID TaskID) (DeliveryCompletionPreparation, error) {
	failed, err := m.repository.Load(ctx, failedID)
	if err != nil {
		return DeliveryCompletionPreparation{}, mapRepositoryError("delivery_completion", err)
	}
	delivery, err := m.repository.Load(ctx, deliveryID)
	if err != nil {
		return DeliveryCompletionPreparation{}, mapRepositoryError("delivery_completion", err)
	}
	if failed.Task.OwnerID != owner || delivery.Task.OwnerID != owner {
		return DeliveryCompletionPreparation{}, ErrDeliveryCompletionNotApplicable
	}
	prepared, err := m.prepareDeliveryCompletion(ctx, failed, delivery.Task, delivery.CurrentAttempt, delivery.Task.Assets)
	if err != nil {
		return DeliveryCompletionPreparation{}, err
	}
	if prepared.view.AlreadyCompleted {
		return prepared.view, nil
	}
	store, ok := m.repository.(interface {
		CommitDeliveryCompletion(context.Context, ValidatedDeliveryCompletion) error
	})
	if !ok {
		return DeliveryCompletionPreparation{}, &Error{Code: ErrUnavailable, Operation: "delivery_completion", Detail: "本地交付完成存储未配置"}
	}
	if err := store.CommitDeliveryCompletion(ctx, prepared.commit); err != nil {
		if errors.Is(err, RepositoryConflict) {
			current, loadErr := m.repository.Load(ctx, failedID)
			if loadErr == nil && current.Task.State == TaskSucceeded && current.CurrentAttempt != nil &&
				current.CurrentAttempt.DeliveryCompletion != nil &&
				current.CurrentAttempt.DeliveryCompletion.DeliveryTaskID == delivery.Task.ID {
				prepared.view.AlreadyCompleted = true
				prepared.view.Provenance = *current.CurrentAttempt.DeliveryCompletion
				return prepared.view, nil
			}
		}
		return DeliveryCompletionPreparation{}, mapRepositoryError("delivery_completion", err)
	}
	return prepared.view, nil
}

// prepareDeliveryCompletionForMaterialization is the ordinary success path.
// It returns no completion when the Task pair has no exact relation; a
// deterministic rejection is logged and skipped by the caller, while a
// temporary read/DB/I/O failure is returned so the caller delays the delivery
// success commit and retries the same transaction.
func (m *Module) prepareDeliveryCompletionForMaterialization(ctx context.Context, delivery Task, attempt *Attempt, assets []Asset) (*ValidatedDeliveryCompletion, string, error) {
	if m.conversions == nil || delivery.SessionID == "" || attempt == nil {
		return nil, "", nil
	}
	if _, ok := m.repository.(interface {
		CommitDeliveryCompletion(context.Context, ValidatedDeliveryCompletion) error
	}); !ok {
		return nil, "", nil
	}
	failedID, kind, err := m.deliveryCompletionSource(ctx, delivery)
	if err != nil {
		if errors.Is(err, ErrDeliveryCompletionNotApplicable) {
			return nil, "", nil
		}
		return nil, "", err
	}
	failed, err := m.repository.Load(ctx, failedID)
	if err != nil {
		if errors.Is(err, RepositoryNotFound) {
			return nil, "", nil
		}
		return nil, kind, err
	}
	prepared, err := m.prepareDeliveryCompletion(ctx, failed, delivery, attempt, assets)
	if err != nil {
		return nil, kind, err
	}
	if prepared.view.AlreadyCompleted {
		return nil, kind, nil
	}
	return &prepared.commit, kind, nil
}

func (m *Module) prepareDeliveryCompletion(ctx context.Context, failed Aggregate, delivery Task, deliveryAttempt *Attempt, deliveryAssets []Asset) (preparedDeliveryCompletion, error) {
	var zero preparedDeliveryCompletion
	if m.conversions == nil || failed.Task.OwnerID == "" || failed.Task.SessionID == "" ||
		delivery.OwnerID != failed.Task.OwnerID || delivery.SessionID != failed.Task.SessionID ||
		deliveryAttempt == nil || deliveryAttempt.TaskID != delivery.ID {
		return zero, ErrDeliveryCompletionNotApplicable
	}
	if delivery.State != TaskSucceeded || !m.conversions.IsConversion(delivery.Offering.ID, delivery.Input) {
		return zero, ErrDeliveryCompletionNotApplicable
	}
	failedTask := failed.Task
	if failedTask.State == TaskSucceeded && failed.CurrentAttempt != nil && failed.CurrentAttempt.DeliveryCompletion != nil {
		provenance := failed.CurrentAttempt.DeliveryCompletion
		if provenance.DeliveryTaskID == delivery.ID {
			var total int64
			for _, asset := range failedTask.Assets {
				total += asset.Size
			}
			return preparedDeliveryCompletion{view: DeliveryCompletionPreparation{
				Kind: provenance.Kind, FailedTaskID: failedTask.ID, DeliveryTaskID: delivery.ID,
				DeliveryAttemptID: provenance.DeliveryAttemptID, OriginalAttemptID: provenance.OriginalAttemptID,
				AssetCount: len(provenance.Assets), Bytes: total, ContractSHA256: provenance.ContractSHA256,
				AlreadyCompleted: true, Provenance: *provenance,
			}}, nil
		}
		return zero, fmt.Errorf("%w: 该失败任务已由其他交付完成", ErrDeliveryCompletionRejected)
	}
	if failedTask.State != TaskFailed || failed.CurrentAttempt == nil || failed.CurrentAttempt.State != AttemptFailed {
		return zero, ErrDeliveryCompletionNotApplicable
	}
	if len(failedTask.Assets) != 0 {
		return zero, fmt.Errorf("%w: 原失败任务已有登记资产", ErrDeliveryCompletionRejected)
	}
	// A Plan retry may follow a conversion that failed before any upstream
	// result (a transport/Start failure with no claims). Only the refetch
	// relation below requires the original Attempt to have reached COMPLETED.
	if failed.CurrentAttempt.FinishedAt == nil || failedTask.Failure == nil ||
		!m.conversions.IsConversion(failedTask.Offering.ID, failedTask.Input) {
		return zero, ErrDeliveryCompletionNotApplicable
	}
	relation, err := m.deliveryCompletionRelation(ctx, failed, delivery)
	if err != nil {
		return zero, err
	}
	normalizedFailed, normalizedDelivery := contractOffering(failedTask.Offering, relation.kind == DeliveryCompletionRefetch), contractOffering(delivery.Offering, relation.kind == DeliveryCompletionRefetch)
	if failedTask.Capability != delivery.Capability || !reflect.DeepEqual(normalizedFailed, normalizedDelivery) ||
		!compactJSONEqual(failedTask.Input, delivery.Input) || !reflect.DeepEqual(failedTask.Offering.Outputs, delivery.Offering.Outputs) {
		return zero, fmt.Errorf("%w: 原任务与交付任务的输入或输出合同不一致", ErrDeliveryCompletionRejected)
	}

	now := m.clock.Now()
	attempt := Attempt{
		ID: m.ids.AttemptID(), TaskID: failedTask.ID, Ordinal: failed.CurrentAttempt.Ordinal + 1,
		Offering: failedTask.Offering, State: AttemptDeliveryCompleted,
		SubmissionKey: "delivery-completion:" + string(failed.CurrentAttempt.ID),
		StartedAt:     now, UpdatedAt: now, FinishedAt: &now,
	}

	written := make([]Asset, 0, len(deliveryAssets))
	mappings := make([]DeliveryAssetMapping, 0, len(deliveryAssets))
	claims := make([]ArtifactClaim, 0, len(deliveryAssets))
	for _, donor := range deliveryAssets {
		if donor.TaskID != delivery.ID || donor.AttemptID != deliveryAttempt.ID || !readyAsset(donor) {
			return zero, fmt.Errorf("%w: 交付资产不属于交付 Task 的当前 Attempt", ErrDeliveryCompletionRejected)
		}
		requirement, ok := requirementForClaim(failedTask.Offering.Outputs, donor.Role, donor.Variant)
		if !ok {
			continue
		}
		claims = append(claims, ArtifactClaim{ID: "delivery:" + string(donor.ID), Role: donor.Role, Variant: donor.Variant, MediaType: donor.MediaType, StoredAsset: &Asset{}})
		next := donor
		next.ID = m.ids.AssetID()
		next.TaskID = failedTask.ID
		next.AttemptID = attempt.ID
		next.Required = false
		next.CreatedAt = now
		claim := ArtifactClaim{ID: "delivery:" + string(donor.ID), Role: donor.Role, Variant: donor.Variant, MediaType: donor.MediaType, StoredAsset: &next}
		checked, err := m.checkStoredClaim(ctx, failedTask, &attempt, claim, requirement)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return zero, err
			}
			var rejected *storedClaimRejected
			if errors.As(err, &rejected) || isFidelityRejection(err) {
				return zero, fmt.Errorf("%w: 交付资产 %s 的大小、摘要、媒体类型或交付合同不符", ErrDeliveryCompletionRejected, donor.ID)
			}
			return zero, fmt.Errorf("交付资产 %s 暂不可读取: %w", donor.ID, err)
		}
		written = append(written, checked)
		mappings = append(mappings, DeliveryAssetMapping{SourceAssetID: donor.ID, AssetID: checked.ID})
	}
	if err := validateClaims(failedTask.Offering.Outputs, claims); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrDeliveryCompletionRejected, err)
	}
	if err := assignRequiredOutputs(failedTask.Offering.Outputs, written); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrDeliveryCompletionRejected, err)
	}

	contractSHA, err := deliveryContractDigest(failedTask, delivery, relation.kind == DeliveryCompletionRefetch, written, deliveryAssets, mappings)
	if err != nil {
		return zero, err
	}
	provenance := DeliveryCompletion{
		Kind: relation.kind, OriginalAttemptID: failed.CurrentAttempt.ID,
		DeliveryTaskID: delivery.ID, DeliveryAttemptID: deliveryAttempt.ID,
		OriginalFailureClass: failedTask.Failure.Class, OriginalFinishedAt: *failed.CurrentAttempt.FinishedAt,
		ChargedCredits: chargedCredits(failedTask), CreditsMetered: failedTask.Offering.CreditsMetered,
		ContractSHA256: contractSHA, Assets: mappings,
	}
	if relation.refetch != nil {
		provenance.Refetch = relation.refetch
	} else {
		provenance.PlanRetry = relation.plan
	}
	attempt.DeliveryCompletion = &provenance

	task := failedTask
	task.State = TaskSucceeded
	task.Progress = 100
	task.Failure = nil
	task.CurrentAttemptID = attempt.ID
	task.MaterializeFailures = 0
	task.NextActionAt = time.Time{}
	task.FinishedAt = &now
	task.UpdatedAt = now
	task.Version++
	task.DeliveryCompleted = true
	task.Resources = nil
	event := Event{
		TaskID: task.ID, Type: "task.succeeded",
		Data:      eventData(map[string]any{"asset_count": len(written)}),
		CreatedAt: now,
	}
	view := DeliveryCompletionPreparation{
		Kind: relation.kind, FailedTaskID: failedTask.ID, DeliveryTaskID: delivery.ID,
		DeliveryAttemptID: deliveryAttempt.ID, OriginalAttemptID: failed.CurrentAttempt.ID,
		AssetCount: len(written), ContractSHA256: contractSHA, Provenance: provenance,
	}
	for _, asset := range written {
		view.Bytes += asset.Size
	}
	return preparedDeliveryCompletion{view: view, commit: ValidatedDeliveryCompletion{
		Task: task, ExpectedVersion: failedTask.Version, Attempt: attempt,
		Assets: written, Events: []Event{event},
	}}, nil
}

func readyAsset(asset Asset) bool {
	return asset.StorageKey != "" && asset.Size > 0 && asset.SHA256 != ""
}

func compactJSONEqual(left, right json.RawMessage) bool {
	var a, b bytes.Buffer
	if json.Compact(&a, left) != nil || json.Compact(&b, right) != nil {
		return false
	}
	return bytes.Equal(a.Bytes(), b.Bytes())
}

// contractOffering removes only the declarations the existing refetch route
// explicitly changes; execution messages never decide delivery. The plan
// retry route must keep its full paid contract.
func contractOffering(offering Offering, refetch bool) Offering {
	if refetch {
		offering = refetchOffering(offering)
	}
	offering.ExecutionMessages = ExecutionMessages{}
	return offering
}

func deliveryContractDigest(failed, delivery Task, refetch bool, written, donors []Asset, mappings []DeliveryAssetMapping) (string, error) {
	type assetFact struct {
		Role      string `json:"role"`
		Variant   string `json:"variant,omitempty"`
		SHA256    string `json:"sha256"`
		Size      int64  `json:"size"`
		MediaType string `json:"media_type"`
	}
	facts := make([]assetFact, 0, len(written))
	byID := map[AssetID]Asset{}
	for _, donor := range donors {
		byID[donor.ID] = donor
	}
	for _, mapping := range mappings {
		donor, ok := byID[mapping.SourceAssetID]
		if !ok {
			return "", ErrDeliveryCompletionRejected
		}
		facts = append(facts, assetFact{Role: donor.Role, Variant: donor.Variant, SHA256: donor.SHA256, Size: donor.Size, MediaType: donor.MediaType})
	}
	payload := struct {
		FailedOffering   Offering
		DeliveryOffering Offering
		FailedInput      string
		DeliveryInput    string
		Assets           []assetFact
	}{
		FailedOffering: contractOffering(failed.Offering, refetch), DeliveryOffering: contractOffering(delivery.Offering, refetch),
		FailedInput: string(failed.Input), DeliveryInput: string(delivery.Input), Assets: facts,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type deliveryRelation struct {
	kind    string
	refetch *DeliveryRefetch
	plan    *DeliveryPlanRetry
}

// previousPlanTask resolves the preceding retry Task by its deterministic
// key. A missing row or a key held only by a tombstoned Task is a
// deterministic "no source": the failed Task was deleted and no completion
// may be written. Other conflicts and repository errors stay errors.
func (m *Module) previousPlanTask(ctx context.Context, owner OwnerID, key string) (Task, bool, error) {
	task, err := m.repository.GetByIdempotency(ctx, owner, key)
	if err == nil {
		return task, true, nil
	}
	if errors.Is(err, RepositoryNotFound) {
		return Task{}, false, nil
	}
	if errors.Is(err, RepositoryConflict) {
		if reader, ok := m.repository.(interface {
			TaskTombstonedByIdempotency(context.Context, OwnerID, string) (bool, error)
		}); ok {
			tombstoned, checkErr := reader.TaskTombstonedByIdempotency(ctx, owner, key)
			if checkErr != nil {
				return Task{}, false, checkErr
			}
			if tombstoned {
				return Task{}, false, nil
			}
		}
	}
	return Task{}, false, err
}

// deliveryCompletionSource finds the failed Task of a delivery candidate
// without reading the full history: a refetch binding or the preceding plan
// retry key.
func (m *Module) deliveryCompletionSource(ctx context.Context, delivery Task) (TaskID, string, error) {
	if binding, ok, err := m.upstreamRefetchOf(ctx, delivery.ID); err != nil {
		return "", "", err
	} else if ok {
		return binding.SourceTaskID, DeliveryCompletionRefetch, nil
	}
	reader, ok := m.repository.(TaskGrantReader)
	if !ok {
		return "", "", ErrDeliveryCompletionNotApplicable
	}
	grant, ok, err := reader.BoundGrantForTask(ctx, delivery.OwnerID, delivery.ID)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", ErrDeliveryCompletionNotApplicable
	}
	planID, index, retry, ok := parseProcessingKey(grant.IdempotencyKey)
	if !ok || retry <= 0 || grant.OperationID != planID || grant.PlanStepID != fmt.Sprintf("%d:%d", index, retry) {
		return "", "", ErrDeliveryCompletionNotApplicable
	}
	previous, found, err := m.previousPlanTask(ctx, delivery.OwnerID, fmt.Sprintf("processing:%s:%d:%d", planID, index, retry-1))
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", ErrDeliveryCompletionNotApplicable
	}
	return previous.ID, DeliveryCompletionPlanRetry, nil
}

func (m *Module) deliveryCompletionRelation(ctx context.Context, failed Aggregate, delivery Task) (deliveryRelation, error) {
	var result deliveryRelation
	binding, refetch, err := m.upstreamRefetchOf(ctx, delivery.ID)
	if err != nil {
		return result, err
	}
	reader, hasGrants := m.repository.(TaskGrantReader)
	var grant TaskGrant
	hasGrant := false
	if hasGrants {
		grant, hasGrant, err = reader.BoundGrantForTask(ctx, delivery.OwnerID, delivery.ID)
		if err != nil {
			return result, err
		}
	}
	if refetch && hasGrant {
		return result, fmt.Errorf("%w: 交付任务同时带有 refetch 与 plan retry 关系", ErrDeliveryCompletionRejected)
	}
	if refetch {
		// A refetch is a read of a completed provider job; a Plan retry is a
		// fresh paid step and may follow a pre-result failure.
		if binding.OwnerID != failed.Task.OwnerID || binding.SessionID != failed.Task.SessionID ||
			binding.SourceTaskID != failed.Task.ID || binding.SourceAttemptID != failed.CurrentAttempt.ID ||
			failed.CurrentAttempt.LastObservationPhase != UpstreamCompleted {
			return result, fmt.Errorf("%w: refetch 绑定不是这个失败任务的原始已完成上游 Attempt", ErrDeliveryCompletionRejected)
		}
		result.kind = DeliveryCompletionRefetch
		result.refetch = &DeliveryRefetch{Ordinal: binding.Ordinal, SourceAttemptID: binding.SourceAttemptID}
		return result, nil
	}
	if !hasGrant {
		return result, ErrDeliveryCompletionNotApplicable
	}
	planID, index, retry, ok := parseProcessingKey(grant.IdempotencyKey)
	if !ok || retry <= 0 || delivery.IdempotencyKey != grant.IdempotencyKey ||
		grant.OperationID != planID || grant.PlanStepID != fmt.Sprintf("%d:%d", index, retry) ||
		grant.TaskID != delivery.ID || grant.OfferingID != delivery.Offering.ID ||
		grant.OfferingRevision != delivery.Offering.Revision || grant.Status != "bound" {
		return result, fmt.Errorf("%w: 交付任务的 bound Grant 不是同一步骤的重试", ErrDeliveryCompletionRejected)
	}
	previous, found, err := m.previousPlanTask(ctx, delivery.OwnerID, fmt.Sprintf("processing:%s:%d:%d", planID, index, retry-1))
	if err != nil {
		return result, err
	}
	if !found || previous.ID != failed.Task.ID {
		return result, ErrDeliveryCompletionNotApplicable
	}
	previousGrant, ok, err := reader.BoundGrantForTask(ctx, failed.Task.OwnerID, failed.Task.ID)
	if err != nil {
		return result, err
	}
	if !ok || previousGrant.TaskID != failed.Task.ID || previousGrant.OperationID != planID ||
		previousGrant.PlanStepID != fmt.Sprintf("%d:%d", index, retry-1) ||
		previousGrant.IdempotencyKey != previous.IdempotencyKey ||
		previousGrant.OfferingID != delivery.Offering.ID || previousGrant.OfferingRevision != delivery.Offering.Revision ||
		previousGrant.Status != "bound" {
		return result, fmt.Errorf("%w: 原失败任务的重试 Grant 与交付任务不一致", ErrDeliveryCompletionRejected)
	}
	if err := m.validateDeliveryGrant(ctx, grant, delivery); err != nil {
		return result, err
	}
	if err := m.validateDeliveryGrant(ctx, previousGrant, failed.Task); err != nil {
		return result, err
	}
	result.kind = DeliveryCompletionPlanRetry
	result.plan = &DeliveryPlanRetry{
		PlanID: planID, StepIndex: index, Retry: retry, GrantRequestID: grant.RequestID,
		PreparedInputID: grant.PreparedInputID, PreparedInputHash: grant.PreparedInputHash,
	}
	return result, nil
}

// validateDeliveryGrant checks each grant against its own frozen PreparedInput
// and Task. A/B PreparedInputHash need not be equal: they can be different
// preparations of the same normalized input under different Schema revisions
// or contexts. What may not happen is a grant whose own PreparedInput/hash no
// longer matches the Task it is bound to.
func (m *Module) validateDeliveryGrant(ctx context.Context, grant TaskGrant, task Task) error {
	if grant.TaskID != task.ID || grant.OfferingID != task.Offering.ID ||
		grant.OfferingRevision != task.Offering.Revision || grant.PreparedInputID == "" ||
		grant.PreparedInputHash == "" {
		return fmt.Errorf("%w: Grant 与 Task 或 Offering 不一致", ErrDeliveryCompletionRejected)
	}
	prepared, err := m.GetPreparedInput(ctx, task.OwnerID, grant.PreparedInputID)
	if err != nil {
		if IsCode(err, ErrNotFound) || IsCode(err, ErrConflict) || IsCode(err, ErrInvalidInput) {
			return fmt.Errorf("%w: Grant 的 PreparedInput 缺失或已变化", ErrDeliveryCompletionRejected)
		}
		return err
	}
	if prepared.InputHash != grant.PreparedInputHash || prepared.OwnerID != task.OwnerID ||
		prepared.SessionID != task.SessionID || prepared.Capability != task.Capability ||
		prepared.OfferingID != task.Offering.ID || prepared.OfferingRevision != task.Offering.Revision ||
		!compactJSONEqual(prepared.NormalizedInput, task.Input) {
		return fmt.Errorf("%w: Grant 的 PreparedInput 与 Task 绑定不一致", ErrDeliveryCompletionRejected)
	}
	return nil
}

func parseProcessingKey(key string) (string, int, int, bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 4 || parts[0] != "processing" || parts[1] == "" {
		return "", 0, 0, false
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil || index < 0 {
		return "", 0, 0, false
	}
	retry, err := strconv.Atoi(parts[3])
	if err != nil || retry < 0 {
		return "", 0, 0, false
	}
	return parts[1], index, retry, true
}
