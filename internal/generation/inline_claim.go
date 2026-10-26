package generation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Inline response bodies are already bounded by the provider HTTP reader. Copy
// image payloads directly into CAS with a streaming hash/size limit, then drop
// them before committing the observation to SQLite. Interrupted commits leave
// ordinary orphan blobs for the offline collector, never another paid request.
func (m *Module) storeInlineImages(ctx context.Context, task Task, attempt *Attempt, claims []ArtifactClaim) ([]ArtifactClaim, error) {
	result := append([]ArtifactClaim(nil), claims...)
	if task.Offering.Domain != DomainImage {
		return result, nil
	}
	ordinals := map[string]int{}
	// B17：未声明 inline 产物在遍历中删除，索引不递增以免跳元素/越界。
	for index := 0; index < len(result); {
		claim := &result[index]
		ordinals[claim.Role]++
		if len(claim.Inline) == 0 || !strings.HasPrefix(claim.MediaType, "image/") {
			index++
			continue
		}
		requirement, contracted := requirementForClaim(
			task.Offering.Outputs,
			claim.Role,
			claim.Variant,
		)
		if !contracted {
			// B17 未声明 inline 产物跳过：Offering 契约未声明该
			// role/variant 时不得以无约束形态入库，直接丢弃该 claim。
			result = append(result[:index], result[index+1:]...)
			continue
		}
		detected := http.DetectContentType(claim.Inline[:min(512, len(claim.Inline))])
		if !strings.HasPrefix(detected, "image/") {
			m.logInlineStorageFailure(task, attempt, "detect", "not_image", len(claim.Inline), requirement.MaxBytes, detected, nil)
			return nil, fmt.Errorf("inline asset is not an image")
		}
		if contracted && !mediaTypeAllowed(requirement.MediaTypes, detected) {
			m.logInlineStorageFailure(task, attempt, "contract", "media_type", len(claim.Inline), requirement.MaxBytes, detected, nil)
			return nil, fmt.Errorf("产物 %q 的媒体类型不符合供给项约束", claim.Role)
		}
		claim.MediaType = detected
		if requirement.MaxBytes > 0 && int64(len(claim.Inline)) > requirement.MaxBytes {
			m.logInlineStorageFailure(task, attempt, "contract", "size_limit", len(claim.Inline), requirement.MaxBytes, detected, nil)
			return nil, fmt.Errorf("产物 %q 超过供给项大小上限", claim.Role)
		}
		write := AssetWrite{AssetID: m.ids.AssetID(), OwnerID: task.OwnerID, TaskID: task.ID, AttemptID: attempt.ID, Role: claim.Role, Variant: claim.Variant, Ordinal: ordinals[claim.Role], MediaType: claim.MediaType, DeliveryProfile: requirement.DeliveryProfile, MaxBytes: requirement.MaxBytes, Body: bytes.NewReader(claim.Inline)}
		asset, err := m.assets.Put(ctx, write)
		if err == nil {
			err = checkWrittenAsset(write, asset)
		}
		if err != nil {
			m.logInlineStorageFailure(task, attempt, "store", "store_failed", len(claim.Inline), requirement.MaxBytes, detected, err)
			return nil, err
		}
		claim.StoredAsset = &asset
		claim.Inline = nil
		index++
	}
	return result, nil
}

// storedClaimRejected marks a deterministic mismatch between a stored claim
// and the frozen output contract. I/O, cancellation and other temporary read
// failures stay unclassified so callers can retry them.
type storedClaimRejected struct{ cause error }

func (e *storedClaimRejected) Error() string { return e.cause.Error() }
func (e *storedClaimRejected) Unwrap() error { return e.cause }

func (m *Module) checkStoredClaim(ctx context.Context, task Task, attempt *Attempt, claim ArtifactClaim, requirement AssetRequirement) (Asset, error) {
	asset := *claim.StoredAsset
	if err := checkAssetProfile(asset, requirement); err != nil {
		return Asset{}, &storedClaimRejected{cause: err}
	}
	if requirement.DeliveryProfile != "" && asset.Variant != claim.Variant {
		return Asset{}, &storedClaimRejected{cause: fmt.Errorf("stored asset variant differs from frozen claim")}
	}
	if asset.TaskID != task.ID || asset.AttemptID != attempt.ID || asset.Role != claim.Role || asset.Size <= 0 || (requirement.MaxBytes > 0 && asset.Size > requirement.MaxBytes) || !mediaTypeAllowed(requirement.MediaTypes, asset.MediaType) {
		return Asset{}, &storedClaimRejected{cause: fmt.Errorf("本地产物引用与任务或输出约束不一致")}
	}
	body, err := m.assets.Open(ctx, asset)
	if err != nil {
		return Asset{}, err
	}
	hasher := sha256.New()
	n, err := io.Copy(hasher, io.LimitReader(body, asset.Size+1))
	closeErr := body.Close()
	if err != nil {
		return Asset{}, err
	}
	if closeErr != nil {
		return Asset{}, closeErr
	}
	if n != asset.Size || hex.EncodeToString(hasher.Sum(nil)) != asset.SHA256 {
		return Asset{}, &storedClaimRejected{cause: fmt.Errorf("本地产物大小或摘要校验失败")}
	}
	if err := m.checkAssetFidelity(ctx, asset, requirement); err != nil {
		return Asset{}, err
	}
	return asset, nil
}

// Storage adapters must explicitly preserve the frozen profile. An older
// adapter that silently ignores AssetWrite.DeliveryProfile fails closed.
func checkWrittenAsset(write AssetWrite, asset Asset) error {
	if err := checkAssetProfile(asset, AssetRequirement{DeliveryProfile: write.DeliveryProfile, Role: write.Role, Variant: write.Variant, MediaTypes: []string{write.MediaType}, MaxBytes: write.MaxBytes}); err != nil {
		return err
	}
	if write.DeliveryProfile == "" {
		return nil
	}
	if asset.ID != write.AssetID || asset.TaskID != write.TaskID || asset.AttemptID != write.AttemptID || asset.Role != write.Role || asset.Ordinal != write.Ordinal || asset.Required != write.Required || asset.StorageKey == "" || len(asset.SHA256) != 64 {
		return fmt.Errorf("stored asset identity differs from requested write")
	}
	if _, err := hex.DecodeString(asset.SHA256); err != nil {
		return fmt.Errorf("stored asset digest is invalid")
	}
	return nil
}
func checkAssetProfile(asset Asset, requirement AssetRequirement) error {
	if asset.DeliveryProfile != requirement.DeliveryProfile {
		return fmt.Errorf("stored asset delivery profile differs from frozen output")
	}
	if requirement.DeliveryProfile == "" {
		return nil
	}
	variant, mime, known := schema.DeliveryProfileFormat(requirement.DeliveryProfile)
	if role := schema.DeliveryProfileRole(requirement.DeliveryProfile); role != "" && (asset.Role != role || requirement.Role != role) {
		return fmt.Errorf("stored asset differs from frozen representation role")
	}
	if !known || requirement.Variant != variant || len(requirement.MediaTypes) != 1 || requirement.MediaTypes[0] != mime || asset.Variant != variant || asset.MediaType != mime || requirement.MaxBytes <= 0 || asset.Size <= 0 || asset.Size > requirement.MaxBytes {
		return fmt.Errorf("stored asset differs from frozen delivery format")
	}
	return nil
}

func (m *Module) executionMessages(task Task) ExecutionMessages {
	if task.Offering.ExecutionMessages.SubmissionUnknown != "" {
		return task.Offering.ExecutionMessages
	}
	if current, ok := m.catalog.Get(task.Offering.ID); ok {
		return current.ExecutionMessages
	}
	return ExecutionMessages{}
}

// Diagnostics deliberately exclude err.Error(), claim metadata/locator and bytes.
// A storage adapter can only contribute allowlisted stage/reason/errno values.
func (m *Module) logInlineStorageFailure(task Task, attempt *Attempt, stage, reason string, byteCount int, maxBytes int64, mime string, err error) {
	diagnostic := AssetStorageDiagnostic{Stage: "unknown", Reason: "unknown", Errno: "unknown"}
	var diagnosed DiagnosedAssetStorageError
	if errors.As(err, &diagnosed) {
		diagnostic = safeStorageDiagnostic(diagnosed.StorageDiagnostic())
	}
	m.logger.Warn("inline asset materialization rejected", "event", "materialization.inline_storage_diagnostic", "task_id", string(task.ID), "attempt_id", string(attempt.ID), "stage", stage, "reason", reason, "bytes", byteCount, "max_bytes", maxBytes, "mime", mime, "storage_stage", diagnostic.Stage, "storage_reason", diagnostic.Reason, "errno", diagnostic.Errno)
}
