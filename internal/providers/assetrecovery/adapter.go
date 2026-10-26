// Package assetrecovery implements the local receipt protocol. It has no
// network client and cannot generate, convert, charge, or import old task state.
package assetrecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type Adapter struct {
	receipts generation.RecoveryStore
	assets   generation.AssetStore
}

func New(receipts generation.RecoveryStore, assets generation.AssetStore) (*Adapter, error) {
	if receipts == nil || assets == nil {
		return nil, errors.New("local recovery requires receipts and assets")
	}
	return &Adapter{receipts, assets}, nil
}
func ValidateOffering(offering generation.Offering) error {
	_, err := schema.DecodeRecoveryOptions(offering)
	return err
}
func rejected(cause error) error {
	return &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureIntegrity, SafeDetail: "恢复回执或文件校验失败"}, Cause: cause}
}
func (a *Adapter) receipt(ctx context.Context, owner generation.OwnerID, session string, offering generation.Offering, input json.RawMessage) (generation.RecoveryReceipt, error) {
	id, digest, err := generation.ParseRecoveryInput(offering, input)
	if err != nil {
		return generation.RecoveryReceipt{}, err
	}
	receipt, err := a.receipts.GetRecoveryReceipt(ctx, owner, id)
	if err != nil {
		return receipt, err
	}
	if session == "" || receipt.Source.OwnerID != owner || receipt.Source.SessionID != session || receipt.Digest != digest {
		return receipt, errors.New("recovery scope mismatch")
	}
	source, err := a.receipts.ReadRecoverySource(ctx, owner, receipt.Source.TaskID)
	if err != nil {
		return receipt, err
	}
	if err = generation.ValidateRecoveryReceipt(receipt, source, offering); err != nil {
		return receipt, err
	}
	return receipt, nil
}
func verifyBody(ctx context.Context, body io.ReadCloser, slot generation.RecoverySlot) error {
	defer body.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: body}, slot.Size+1))
	if err != nil {
		return err
	}
	if size != slot.Size || hex.EncodeToString(hash.Sum(nil)) != slot.SHA256 {
		return errors.New("recovery content mismatch")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func (a *Adapter) verifySlots(ctx context.Context, receipt generation.RecoveryReceipt) error {
	for _, slot := range receipt.Slots {
		body, err := a.assets.Open(ctx, slot.Asset())
		if err != nil {
			return err
		}
		if err := verifyBody(ctx, body, slot); err != nil {
			return err
		}
	}
	return nil
}
func (a *Adapter) ValidateInput(ctx context.Context, request generation.InputValidationRequest) error {
	if len(request.InputAssets) != 0 || len(request.TaskDependencies) != 0 {
		return rejected(errors.New("recovery cannot accept external input or Task dependencies"))
	}
	receipt, err := a.receipt(ctx, request.OwnerID, request.SessionID, request.Offering, request.Input)
	if err != nil {
		return rejected(err)
	}
	if err := a.verifySlots(ctx, receipt); err != nil {
		return rejected(err)
	}
	return nil
}

type claimBinding struct {
	ReceiptID string               `json:"receipt_id"`
	Digest    string               `json:"digest"`
	Slot      int                  `json:"slot"`
	TaskID    generation.TaskID    `json:"task_id"`
	AttemptID generation.AttemptID `json:"attempt_id"`
}

func claimID(task generation.TaskID, attempt generation.AttemptID, index int) string {
	return fmt.Sprintf("recovery:%s:%s:%d", task, attempt, index)
}
func (a *Adapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	if request.TaskID == "" || request.AttemptID == "" || request.SubmissionKey == "" || len(request.InputAssets) != 0 || len(request.TaskDependencies) != 0 {
		return generation.Observation{}, rejected(errors.New("invalid recovery start"))
	}
	receipt, err := a.receipt(ctx, request.OwnerID, request.SessionID, request.Offering, request.Input)
	if err != nil {
		return generation.Observation{}, rejected(err)
	}
	if err := a.verifySlots(ctx, receipt); err != nil {
		return generation.Observation{}, rejected(err)
	}
	observation := generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, DedupeKey: receipt.Digest}
	for i, slot := range receipt.Slots {
		metadata, _ := json.Marshal(claimBinding{receipt.ID, receipt.Digest, i, request.TaskID, request.AttemptID})
		observation.Artifacts = append(observation.Artifacts, generation.ArtifactClaim{ID: claimID(request.TaskID, request.AttemptID, i), Role: slot.Role, Variant: slot.Variant, MediaType: slot.MediaType, Locator: claimID(request.TaskID, request.AttemptID, i), Metadata: metadata})
	}
	return observation, nil
}
func (a *Adapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, rejected(errors.New("local recovery has no upstream job"))
}
func (a *Adapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	invalid := func() (io.ReadCloser, generation.AssetMetadata, error) {
		return nil, generation.AssetMetadata{}, rejected(errors.New("recovery claim binding mismatch"))
	}
	if request.Job != nil || request.Claim.StoredAsset != nil || len(request.Claim.Inline) != 0 {
		return invalid()
	}
	task, err := a.receipts.RecoveryTaskBinding(ctx, request.TaskID, request.AttemptID)
	if err != nil {
		return nil, generation.AssetMetadata{}, rejected(err)
	}
	if task.ID != request.TaskID || task.CurrentAttemptID != request.AttemptID || !reflect.DeepEqual(task.Offering, request.Offering) {
		return invalid()
	}
	receipt, err := a.receipt(ctx, task.OwnerID, task.SessionID, request.Offering, task.Input)
	if err != nil {
		return nil, generation.AssetMetadata{}, rejected(err)
	}
	var binding claimBinding
	if json.Unmarshal(request.Claim.Metadata, &binding) != nil || binding.TaskID != request.TaskID || binding.AttemptID != request.AttemptID || binding.ReceiptID != receipt.ID || binding.Digest != receipt.Digest || binding.Slot < 0 || binding.Slot >= len(receipt.Slots) || request.Claim.ID != claimID(task.ID, request.AttemptID, binding.Slot) || request.Claim.Locator != request.Claim.ID {
		return invalid()
	}
	slot := receipt.Slots[binding.Slot]
	if request.Claim.Role != slot.Role || request.Claim.Variant != slot.Variant || request.Claim.MediaType != slot.MediaType {
		return invalid()
	}
	body, err := a.assets.Open(ctx, slot.Asset())
	if err != nil {
		return nil, generation.AssetMetadata{}, rejected(err)
	}
	// Hash again while the kernel writes. A mutation between initial validation
	// and materialization reaches EOF as an error, so AssetStore.Put cannot commit.
	return &verifiedReader{body: body, ctx: ctx, slot: slot, hash: sha256.New()}, generation.AssetMetadata{MediaType: slot.MediaType, Size: slot.Size}, nil
}

type verifiedReader struct {
	body io.ReadCloser
	ctx  context.Context
	slot generation.RecoverySlot
	hash interface {
		io.Writer
		Sum([]byte) []byte
	}
	size int64
	done bool
}

func (r *verifiedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.done {
		return 0, io.EOF
	}
	n, err := r.body.Read(p)
	r.size += int64(n)
	if n > 0 {
		_, _ = r.hash.Write(p[:n])
	}
	if r.size > r.slot.Size {
		return n, errors.New("recovery byte limit changed")
	}
	if err == io.EOF {
		r.done = true
		if r.size != r.slot.Size || hex.EncodeToString(r.hash.Sum(nil)) != r.slot.SHA256 {
			return n, errors.New("recovery digest changed")
		}
	}
	return n, err
}
func (r *verifiedReader) Close() error { return r.body.Close() }
