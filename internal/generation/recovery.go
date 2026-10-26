package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

const RecoveryReceiptVersion = 1
const RecoveryValidatorRevision = "asset-recovery-v1"

// RecoverySourceBinding contains only immutable identity and digests. Snapshot
// hashing also covers the source Task, Attempt, claims and registered assets;
// no upstream URL, opaque checkpoint or credential is copied to public data.
type RecoverySourceBinding struct {
	OwnerID          OwnerID    `json:"owner_id"`
	SessionID        string     `json:"session_id"`
	TaskID           TaskID     `json:"task_id"`
	AttemptID        AttemptID  `json:"attempt_id"`
	OfferingID       OfferingID `json:"offering_id"`
	OfferingRevision string     `json:"offering_revision"`
	OfferingSHA256   string     `json:"offering_sha256"`
	SnapshotSHA256   string     `json:"snapshot_sha256"`
}
type RecoverySource struct {
	RegisteredAssetCount int
	Binding              RecoverySourceBinding
	Offering             Offering
}
type RecoverySlot struct {
	SourceRole      string `json:"source_role"`
	SourceVariant   string `json:"source_variant"`
	Role            string `json:"role"`
	Variant         string `json:"variant"`
	Required        bool   `json:"required"`
	StorageKey      string `json:"storage_key"`
	SHA256          string `json:"sha256"`
	Size            int64  `json:"size"`
	MediaType       string `json:"media_type"`
	DeliveryProfile string `json:"delivery_profile,omitempty"`
}

func (s RecoverySlot) Asset() Asset {
	return Asset{StorageKey: s.StorageKey, SHA256: s.SHA256, Size: s.Size, MediaType: s.MediaType, Role: s.Role, Variant: s.Variant, Required: s.Required, DeliveryProfile: s.DeliveryProfile}
}

type RecoveryReceipt struct {
	ID                string                `json:"id"`
	Digest            string                `json:"digest"`
	Version           int                   `json:"version"`
	ValidatorRevision string                `json:"validator_revision"`
	Source            RecoverySourceBinding `json:"source"`
	ArchiveSHA256     string                `json:"archive_sha256"`
	ManifestSHA256    string                `json:"manifest_sha256"`
	OfferingID        OfferingID            `json:"offering_id"`
	OfferingRevision  string                `json:"offering_revision"`
	OfferingSHA256    string                `json:"offering_sha256"`
	Slots             []RecoverySlot        `json:"slots"`
}

// RecoveryStore is implemented by the durable repository. Receipt creation is
// intentionally absent: only the trusted offline collector can persist one.
type RecoveryStore interface {
	ReadRecoverySource(context.Context, OwnerID, TaskID) (RecoverySource, error)
	GetRecoveryReceipt(context.Context, OwnerID, string) (RecoveryReceipt, error)
	FindRecoveryReceipt(context.Context, OwnerID, TaskID) (RecoveryReceipt, error)
	RecoveryTaskBinding(context.Context, TaskID, AttemptID) (Task, error)
}

func RecoveryDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func validRecoveryDigest(s string) bool {
	decoded, err := hex.DecodeString(s)
	return err == nil && len(decoded) == 32 && s == strings.ToLower(s)
}
func receiptDigest(r RecoveryReceipt) (string, error) {
	r.ID = ""
	r.Digest = ""
	return RecoveryDigest(r)
}
func SealRecoveryReceipt(r RecoveryReceipt) (RecoveryReceipt, error) {
	r.Version = RecoveryReceiptVersion
	r.ValidatorRevision = RecoveryValidatorRevision
	digest, err := receiptDigest(r)
	if err != nil {
		return RecoveryReceipt{}, err
	}
	r.Digest = digest
	r.ID = "recovery_" + digest
	return r, r.Validate()
}
func (r RecoveryReceipt) Validate() error {
	digest, err := receiptDigest(r)
	if err != nil || r.Version != RecoveryReceiptVersion || r.ValidatorRevision != RecoveryValidatorRevision || r.ID != "recovery_"+digest || r.Digest != digest || r.Source.OwnerID == "" || r.Source.SessionID == "" || r.Source.TaskID == "" || r.Source.AttemptID == "" || r.Source.OfferingID == "" || r.Source.OfferingRevision == "" || r.OfferingID == "" || r.OfferingRevision == "" || len(r.Slots) == 0 || len(r.Slots) > 32 {
		return recoveryConflict()
	}
	for _, value := range []string{r.ArchiveSHA256, r.ManifestSHA256, r.OfferingSHA256, r.Source.OfferingSHA256, r.Source.SnapshotSHA256} {
		if !validRecoveryDigest(value) {
			return recoveryConflict()
		}
	}
	seen := map[string]bool{}
	for _, s := range r.Slots {
		key := s.SourceRole + "\x00" + s.SourceVariant
		if s.SourceRole == "" || s.Role == "" || s.Variant == "" || s.StorageKey == "" || s.Size <= 0 || s.Size > 1<<30 || s.MediaType == "" || !validRecoveryDigest(s.SHA256) || seen[key] {
			return recoveryConflict()
		}
		seen[key] = true
	}
	return nil
}
func recoveryConflict() error {
	return &Error{Code: ErrConflict, Operation: "asset_recovery", Detail: "恢复回执或来源校验失败"}
}

// ValidateRecoveryReceipt binds every original slot and actual byte identity to
// the frozen replacement contract. It neither relaxes failed Task dependencies
// nor establishes any source Task as successful.
func ValidateRecoveryReceipt(receipt RecoveryReceipt, source RecoverySource, offering Offering) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if receipt.Source != source.Binding {
		return recoveryConflict()
	}
	options, err := schema.DecodeRecoveryOptions(offering)
	if err != nil {
		return recoveryConflict()
	}
	digest, err := RecoveryDigest(offering)
	if err != nil || receipt.OfferingID != offering.ID || receipt.OfferingRevision != offering.Revision || receipt.OfferingSHA256 != digest {
		return recoveryConflict()
	}
	var mappings []schema.RecoverySlotMapping
	for _, contract := range options.Sources {
		if contract.OfferingID == source.Binding.OfferingID && contract.OfferingRevision == source.Binding.OfferingRevision {
			mappings = contract.Slots
			break
		}
	}
	if len(mappings) != len(receipt.Slots) || len(mappings) != len(source.Offering.Outputs) {
		return recoveryConflict()
	}
	original := map[string]schema.AssetRequirement{}
	for _, out := range source.Offering.Outputs {
		if out.VariantPrefix != "" || out.VariantFrom != "" {
			return recoveryConflict()
		}
		key := out.Role + "\x00" + out.Variant
		if _, ok := original[key]; ok {
			return recoveryConflict()
		}
		original[key] = out
	}
	outputs := map[string]schema.AssetRequirement{}
	for _, out := range offering.Outputs {
		outputs[out.Role+"\x00"+out.Variant] = out
	}
	for i, mapping := range mappings {
		slot := receipt.Slots[i]
		out, ok := outputs[mapping.Role+"\x00"+mapping.Variant]
		old, oldOK := original[mapping.SourceRole+"\x00"+mapping.SourceVariant]
		required := old.Delivery == "" || old.Delivery == schema.AssetDeliveryRequired
		if !ok || !oldOK || slot.SourceRole != mapping.SourceRole || slot.SourceVariant != mapping.SourceVariant || slot.Role != mapping.Role || slot.Variant != mapping.Variant || slot.Required != (out.Delivery == schema.AssetDeliveryRequired) || slot.Required != required || slot.Size > out.MaxBytes || slot.MediaType != out.MediaTypes[0] || slot.DeliveryProfile != out.DeliveryProfile {
			return recoveryConflict()
		}
	}
	return nil
}

func RecoveryInput(offering Offering, receipt RecoveryReceipt) (json.RawMessage, error) {
	options, err := schema.DecodeRecoveryOptions(offering)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{options.ReceiptParameter: receipt.ID, options.DigestParameter: receipt.Digest})
}
func ParseRecoveryInput(offering Offering, input json.RawMessage) (id, digest string, err error) {
	options, e := schema.DecodeRecoveryOptions(offering)
	if e != nil {
		return "", "", recoveryConflict()
	}
	var values map[string]string
	if json.Unmarshal(input, &values) != nil || len(values) != 2 {
		return "", "", recoveryConflict()
	}
	id, digest = values[options.ReceiptParameter], values[options.DigestParameter]
	if !validRecoveryDigest(digest) || id != "recovery_"+digest {
		return "", "", recoveryConflict()
	}
	return id, digest, nil
}
func RecoveryIdempotencyKey(receiptID string) string { return "asset-recovery:" + receiptID }
func validateRecoverySubmission(offering Offering, input json.RawMessage, key string, admission *ExecutionAdmission) error {
	if offering.Capability != schema.AssetRecovery {
		return nil
	}
	id, _, err := ParseRecoveryInput(offering, input)
	if err != nil {
		return err
	}
	if key != RecoveryIdempotencyKey(id) || admission == nil || admission.RequestID == "" || admission.PreparedInputID == "" || admission.PreparedInputHash == "" || admission.OperationID == "" {
		return &Error{Code: ErrForbidden, Operation: "asset_recovery", Detail: "恢复需要与冻结回执绑定的执行授权"}
	}
	return nil
}

type RecoverySlotView struct {
	SourceRole      string `json:"source_role"`
	SourceVariant   string `json:"source_variant"`
	Role            string `json:"role"`
	Variant         string `json:"variant"`
	Required        bool   `json:"required"`
	Size            int64  `json:"size"`
	SHA256          string `json:"sha256"`
	MediaType       string `json:"media_type"`
	DeliveryProfile string `json:"delivery_profile,omitempty"`
}
type RecoveryEligibility struct {
	SourceTaskID     TaskID             `json:"source_task_id"`
	Eligible         bool               `json:"eligible"`
	ReceiptID        string             `json:"receipt_id,omitempty"`
	ReceiptDigest    string             `json:"receipt_digest,omitempty"`
	OfferingID       OfferingID         `json:"offering_id,omitempty"`
	OfferingRevision string             `json:"offering_revision,omitempty"`
	Capability       Capability         `json:"capability,omitempty"`
	SessionID        string             `json:"session_id,omitempty"`
	IdempotencyKey   string             `json:"idempotency_key,omitempty"`
	RecoveryTaskID   TaskID             `json:"recovery_task_id,omitempty"`
	Slots            []RecoverySlotView `json:"slots,omitempty"`
}

func (m *Module) RecoveryEligibility(ctx context.Context, owner OwnerID, taskID TaskID) (RecoveryEligibility, error) {
	view := RecoveryEligibility{SourceTaskID: taskID}
	sourceTask, err := m.repository.Get(ctx, owner, taskID)
	if err != nil {
		return view, mapRepositoryError("asset_recovery", err)
	}
	store, ok := m.repository.(RecoveryStore)
	if !ok || sourceTask.State != TaskFailed || sourceTask.SessionID == "" {
		return view, nil
	}
	receipt, err := store.FindRecoveryReceipt(ctx, owner, taskID)
	if errors.Is(err, RepositoryNotFound) {
		return view, nil
	}
	if err != nil {
		return view, mapRepositoryError("asset_recovery", err)
	}
	source, err := store.ReadRecoverySource(ctx, owner, taskID)
	if err != nil {
		return view, mapRepositoryError("asset_recovery", err)
	}
	offering, ok := m.catalog.Get(receipt.OfferingID)
	if !ok || !offering.Enabled {
		return view, nil
	}
	if _, ok := m.providers.Provider(offering); !ok {
		return view, nil
	}
	if err := ValidateRecoveryReceipt(receipt, source, offering); err != nil {
		return view, err
	}
	input, err := RecoveryInput(offering, receipt)
	if err != nil {
		return view, err
	}
	idempotencyKey := RecoveryIdempotencyKey(receipt.ID)
	existing, err := m.repository.GetByIdempotency(ctx, owner, idempotencyKey)
	if err == nil {
		if existing.SessionID != source.Binding.SessionID || existing.Offering.ID != offering.ID || !reflect.DeepEqual(existing.Input, input) {
			return view, recoveryConflict()
		}
	} else if !errors.Is(err, RepositoryNotFound) {
		return view, mapRepositoryError("asset_recovery", err)
	} else if err := m.validateProviderInput(ctx, owner, source.Binding.SessionID, offering, input, nil, nil); err != nil {
		return view, err
	}
	// An already-bound Task is a navigation target, not a new recovery attempt.
	// New preparation, submission and materialization retain their byte checks.
	view.Eligible = true
	view.ReceiptID = receipt.ID
	view.ReceiptDigest = receipt.Digest
	view.OfferingID = offering.ID
	view.OfferingRevision = offering.Revision
	view.Capability = offering.Capability
	view.SessionID = source.Binding.SessionID
	view.IdempotencyKey = idempotencyKey
	for _, s := range receipt.Slots {
		view.Slots = append(view.Slots, RecoverySlotView{s.SourceRole, s.SourceVariant, s.Role, s.Variant, s.Required, s.Size, s.SHA256, s.MediaType, s.DeliveryProfile})
	}
	if err == nil {
		view.RecoveryTaskID = existing.ID
	}
	return view, nil
}
func (m *Module) PrepareRecovery(ctx context.Context, owner OwnerID, taskID TaskID, expectedDigest string) (PreparedInput, RecoveryEligibility, error) {
	view, err := m.RecoveryEligibility(ctx, owner, taskID)
	if err != nil {
		return PreparedInput{}, view, err
	}
	if !view.Eligible || expectedDigest != view.ReceiptDigest {
		return PreparedInput{}, view, recoveryConflict()
	}
	if view.RecoveryTaskID != "" {
		return PreparedInput{}, view, nil
	}
	offering, ok := m.catalog.Get(view.OfferingID)
	if !ok {
		return PreparedInput{}, view, recoveryConflict()
	}
	input, err := RecoveryInput(offering, RecoveryReceipt{ID: view.ReceiptID, Digest: view.ReceiptDigest})
	if err != nil {
		return PreparedInput{}, view, err
	}
	prepared, err := m.PrepareInput(ctx, PrepareInputCommand{OwnerID: owner, SessionID: view.SessionID, Capability: view.Capability, OfferingID: view.OfferingID, Input: input})
	return prepared, view, err
}

func (m *Module) stableRecoveryPrepared(ctx context.Context, prepared PreparedInput) (PreparedInput, error) {
	prepared.ID = PreparedInputID("prepared_recovery_" + prepared.InputHash)
	existing, err := m.GetPreparedInput(ctx, prepared.OwnerID, prepared.ID)
	if err == nil {
		return existing, nil
	}
	if !IsCode(err, ErrNotFound) {
		return PreparedInput{}, err
	}
	if err = m.preparedStore.CreatePreparedInput(ctx, prepared); err == nil {
		return prepared, nil
	}
	existing, readErr := m.GetPreparedInput(ctx, prepared.OwnerID, prepared.ID)
	if readErr == nil && existing.InputHash == prepared.InputHash {
		return existing, nil
	}
	return PreparedInput{}, fmt.Errorf("persist recovery prepared input: %w", err)
}

// RecoveryProvenance is an owner-only link back to the unchanged failed source.
// Storage keys, source checkpoints and upstream URLs are never projected.
type RecoveryProvenance struct {
	// Kind says how the result was recovered: asset_recovery (verified local
	// bytes) or upstream_refetch (the accepted upstream job read again, #295).
	Kind                   string     `json:"kind"`
	SourceTaskID           TaskID     `json:"source_task_id"`
	SourceAttemptID        AttemptID  `json:"source_attempt_id"`
	SourceOfferingID       OfferingID `json:"source_offering_id"`
	SourceOfferingRevision string     `json:"source_offering_revision"`
	ReceiptDigest          string     `json:"receipt_digest,omitempty"`
}

const (
	RecoveryKindAssetRecovery   = "asset_recovery"
	RecoveryKindUpstreamRefetch = "upstream_refetch"
)

func (m *Module) TaskRecoveryProvenance(ctx context.Context, task Task) (*RecoveryProvenance, error) {
	if task.Offering.Capability != schema.AssetRecovery {
		return m.refetchProvenance(ctx, task)
	}
	store, ok := m.repository.(RecoveryStore)
	if !ok {
		return nil, recoveryConflict()
	}
	id, digest, err := ParseRecoveryInput(task.Offering, task.Input)
	if err != nil {
		return nil, err
	}
	receipt, err := store.GetRecoveryReceipt(ctx, task.OwnerID, id)
	if err != nil {
		return nil, mapRepositoryError("asset_recovery", err)
	}
	if receipt.Digest != digest || receipt.Source.OwnerID != task.OwnerID || receipt.Source.SessionID != task.SessionID {
		return nil, recoveryConflict()
	}
	return &RecoveryProvenance{Kind: RecoveryKindAssetRecovery, SourceTaskID: receipt.Source.TaskID, SourceAttemptID: receipt.Source.AttemptID, SourceOfferingID: receipt.Source.OfferingID, SourceOfferingRevision: receipt.Source.OfferingRevision, ReceiptDigest: receipt.Digest}, nil
}
