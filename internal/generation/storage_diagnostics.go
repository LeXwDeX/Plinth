package generation

import (
	"errors"
	"fmt"
	"mime"
)

// AssetStorageDiagnostic is an allowlisted, payload-free description for server
// logs. It never carries a path, upstream address, response body or raw error.
type AssetStorageDiagnostic struct {
	Stage  string
	Reason string
	Errno  string
}

// DiagnosedAssetStorageError optionally enriches an AssetStore error. The
// coordinator still rejects the asset and uses the existing public SafeDetail.
type DiagnosedAssetStorageError interface {
	error
	StorageDiagnostic() AssetStorageDiagnostic
}

func safeStorageDiagnostic(value AssetStorageDiagnostic) AssetStorageDiagnostic {
	fidelityReason := diagnosticValue(value.Reason, []string{"fidelity_unsupported", "fidelity_invalid_structure", "fidelity_skeleton_missing", "fidelity_animation_missing", "fidelity_inspection_unavailable"})
	value.Stage = diagnosticValue(value.Stage, []string{"identity", "input", "directory_check", "temp_create", "write", "temp_sync", "temp_close", "inspect_open", "inspect", "inspect_close", "cas_path", "root_open", "cas_directory", "cas_verify", "temp_cleanup", "cas_rename", "cas_sync"})
	value.Reason = diagnosticValue(value.Reason, []string{"invalid_identity", "empty_content", "io_failure", "size_limit", "content_rejected", "png_inspection_size", "png_invalid_structure", "png_invalid_metadata", "png_invalid_pixels", "png_metadata_checksum", "png_metadata_limit", "png_metadata_structure", "jpeg_invalid_pixels", "jpeg_invalid_structure", "raster_decode_budget", "raster_inspection_size", "raster_invalid_dimensions", "raster_invalid_structure", "raster_trailing_data", "raster_unsupported_format", "fbx_invalid_structure", "fbx_invalid_array", "archive_rejected", "usdc_rejected", "glb_rejected", "gltf_rejected", "stl_rejected", "3mf_rejected", "blend_rejected", "blend_magic_missing", "external_address"})
	value.Errno = diagnosticValue(value.Errno, []string{"canceled", "deadline", "permission", "not_found", "no_space", "read_only", "io", "unexpected_eof", "other"})
	if fidelityReason != "unknown" {
		value.Reason = fidelityReason
	}
	return value
}
func diagnosticValue(value string, allowed []string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "unknown"
}

// AssetStorageDiagnosticOf returns the allowlisted storage diagnostic an
// AssetStore error carries, for adapters that stream their own output into
// the store (#330 local conversions). The second result is false when err
// carries none.
func AssetStorageDiagnosticOf(err error) (AssetStorageDiagnostic, bool) {
	var diagnosed DiagnosedAssetStorageError
	if !errors.As(err, &diagnosed) {
		return AssetStorageDiagnostic{}, false
	}
	return safeStorageDiagnostic(diagnosed.StorageDiagnostic()), true
}

// AssetStoreRejection is the failure of an adapter whose own output the
// AssetStore refused (#330: a local conversion streams its package straight
// into the store). It matches what the kernel records when it stores a
// downloaded result: integrity_failure with the allowlisted role/format,
// stage and reason as internal forensics, so attempt.failed names the cause.
// A content verdict (stage inspect) is deterministic and not retryable. The
// second result is false when err carries no storage diagnostic.
func AssetStoreRejection(err error, role, variant string) (*Failure, bool) {
	diagnostic, ok := AssetStorageDiagnosticOf(err)
	if !ok {
		return nil, false
	}
	return &Failure{
		Class:           FailureIntegrity,
		Retryable:       diagnostic.Stage != "inspect",
		SafeDetail:      "资产写入或完整性校验失败",
		UpstreamCode:    "asset_store_rejected",
		UpstreamExcerpt: assetStoreForensics(ArtifactClaim{Role: role, Variant: variant}, diagnostic),
	}, true
}

func storageDiagnosticOf(err error) AssetStorageDiagnostic {
	diagnostic := AssetStorageDiagnostic{Stage: "unknown", Reason: "unknown", Errno: "unknown"}
	var diagnosed DiagnosedAssetStorageError
	if errors.As(err, &diagnosed) {
		diagnostic = safeStorageDiagnostic(diagnosed.StorageDiagnostic())
	}
	return diagnostic
}

// safeClaimLabel keeps a claim role/variant usable as a log token. Adapters
// already restrict variants to [A-Za-z0-9_.-]; anything else is not echoed.
func safeClaimLabel(value string) string {
	if value == "" {
		return "-"
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return "unknown"
	}
	return value
}

// safeMediaTypeLabel logs an upstream-declared media type only when it parses
// as one; arbitrary header text is not echoed.
func safeMediaTypeLabel(value string) string {
	parsed, _, err := mime.ParseMediaType(value)
	if err != nil || parsed == "" {
		return "unknown"
	}
	return parsed
}

func assetStoreForensics(claim ArtifactClaim, diagnostic AssetStorageDiagnostic) string {
	return fmt.Sprintf("role=%s format=%s stage=%s reason=%s errno=%s",
		safeClaimLabel(claim.Role), safeClaimLabel(claim.Variant),
		diagnostic.Stage, diagnostic.Reason, diagnostic.Errno)
}

// logAssetStoreRejection names the failing role/format and the allowlisted
// storage reason for operators. It never logs bytes, locators or err.Error().
func (m *Module) logAssetStoreRejection(task Task, attempt *Attempt, claim ArtifactClaim, mediaType string, blocking bool, diagnostic AssetStorageDiagnostic) {
	m.logger.Warn("asset materialization rejected",
		"event", "materialization.asset_store_rejected",
		"task_id", string(task.ID),
		"attempt_id", string(attempt.ID),
		"provider_id", task.Offering.ProviderID,
		"offering_id", string(task.Offering.ID),
		"role", safeClaimLabel(claim.Role),
		"format", safeClaimLabel(claim.Variant),
		"media_type", safeMediaTypeLabel(mediaType),
		"blocking", blocking,
		"storage_stage", diagnostic.Stage,
		"storage_reason", diagnostic.Reason,
		"errno", diagnostic.Errno)
}
