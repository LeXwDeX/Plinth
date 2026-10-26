// Package assetcorrection applies one-off, idempotent corrections to stored
// asset records whose label no longer matches the declaration of the bytes.
//
// TripoNativeOBJArchive (#185, 2026-10-22): Tripo's native OBJ export is a ZIP
// holding the OBJ, its material library and textures. Before revision
// 2026-10-22.185.5 (geometry) and 2026-10-22.1 (Texture) it was declared and
// saved as model/obj, so the format list offered a paid conversion for an OBJ
// the user already had. The correction relabels such assets application/zip,
// but only after the stored bytes pass the same size, digest and format checks
// that delivery applies to an OBJ archive (at least one .obj member). Assets
// whose bytes fail keep their label and are recorded; unreadable files are
// left for the next start. Every decision is recorded per asset, so a later
// start skips decided assets and a corrected asset can be restored from its
// record.
package assetcorrection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const (
	// TripoNativeOBJArchive names the #185 correction in its records.
	TripoNativeOBJArchive = "2026-10-22.tripo-native-obj-zip"

	OutcomeCorrected = "corrected"
	OutcomeKept      = "kept"

	objRole         = "converted_model"
	objVariant      = "obj"
	objLegacyMedia  = "model/obj"
	objArchiveMedia = "application/zip"
	maxDetailLength = 300
)

// RollbackSQL restores every label a correction changed, from its records
// (parameter: the correction name). Run it on a stopped instance; the records
// stay so the decision history remains visible, and the next start does not
// redo them. To redo a correction instead, delete its records first.
const RollbackSQL = `UPDATE generation_assets
SET media_type = (
    SELECT c.previous_media_type FROM asset_media_type_corrections c
    WHERE c.correction = ?1 AND c.asset_id = generation_assets.id AND c.outcome = 'corrected')
WHERE id IN (
    SELECT c.asset_id FROM asset_media_type_corrections c
    WHERE c.correction = ?1 AND c.outcome = 'corrected')`

// Candidate is a saved asset still labelled with the legacy media type, with
// the frozen Offering of its task.
type Candidate struct {
	Asset    generation.Asset
	Offering json.RawMessage
}

// Record is one decision about one asset. PreviousMediaType is the label the
// asset had before; restoring it undoes a correction.
type Record struct {
	Correction        string
	AssetID           generation.AssetID
	TaskID            generation.TaskID
	Outcome           string
	PreviousMediaType string
	MediaType         string
	SHA256            string
	Detail            string
	RecordedAt        time.Time
}

// Repository lists undecided candidates and records a decision. Recording a
// correction relabels the asset in the same transaction, and only when it
// still carries PreviousMediaType.
type Repository interface {
	ListMediaTypeCorrectionCandidates(ctx context.Context, correction, role, variant, mediaType string) ([]Candidate, error)
	RecordMediaTypeCorrection(ctx context.Context, record Record) error
}

// Verifier checks stored bytes as if the asset carried another media type.
// A rejection wraps ErrRejected; any other error means the bytes could not be
// read now.
type Verifier interface {
	VerifyAs(ctx context.Context, asset generation.Asset, mediaType string) error
}

// Report summarises one run.
type Report struct {
	Candidates int // assets still labelled model/obj in a converted_model/obj slot
	Corrected  int
	Kept       int // bytes are not an OBJ archive; label unchanged and recorded
	Deferred   int // file missing or unreadable; retried on the next start
	Ignored    int // not a Tripo native OBJ export; never touched
}

// CorrectTripoNativeOBJ runs the #185 correction once over every undecided
// candidate. It is safe to run on every start: decided assets are skipped and
// a corrected asset no longer matches the candidate query.
func CorrectTripoNativeOBJ(ctx context.Context, repository Repository, verifier Verifier, rejected error, now func() time.Time, logger *slog.Logger) (Report, error) {
	var report Report
	candidates, err := repository.ListMediaTypeCorrectionCandidates(ctx, TripoNativeOBJArchive, objRole, objVariant, objLegacyMedia)
	if err != nil {
		return report, fmt.Errorf("列出待修正资产: %w", err)
	}
	report.Candidates = len(candidates)
	for _, candidate := range candidates {
		asset := candidate.Asset
		if !tripoNativeOBJExport(candidate.Offering) {
			report.Ignored++
			continue
		}
		record := Record{
			Correction:        TripoNativeOBJArchive,
			AssetID:           asset.ID,
			TaskID:            asset.TaskID,
			PreviousMediaType: asset.MediaType,
			MediaType:         objArchiveMedia,
			SHA256:            asset.SHA256,
			RecordedAt:        now().UTC(),
		}
		switch verifyErr := verifier.VerifyAs(ctx, asset, objArchiveMedia); {
		case verifyErr == nil:
			record.Outcome = OutcomeCorrected
		case errors.Is(verifyErr, rejected):
			record.Outcome = OutcomeKept
			record.MediaType = asset.MediaType
			record.Detail = bounded(verifyErr.Error())
		default:
			report.Deferred++
			logger.Warn("Tripo OBJ 资产暂不可读，下次启动再修正", "asset_id", asset.ID, "task_id", asset.TaskID, "error", verifyErr)
			continue
		}
		if err := repository.RecordMediaTypeCorrection(ctx, record); err != nil {
			return report, fmt.Errorf("记录资产 %s 的修正: %w", asset.ID, err)
		}
		if record.Outcome == OutcomeCorrected {
			report.Corrected++
			logger.Info("Tripo OBJ 资产已按实际字节改为 application/zip", "asset_id", asset.ID, "task_id", asset.TaskID)
		} else {
			report.Kept++
			logger.Warn("Tripo OBJ 资产字节不是 OBJ 压缩包，保持原标签", "asset_id", asset.ID, "task_id", asset.TaskID, "detail", record.Detail)
		}
	}
	return report, nil
}

// tripoNativeOBJExport reports whether the task's frozen Offering declares the
// Tripo native export child that produced this slot (adapter tripo.v3, a
// native_conversion OBJ format for converted_model/obj). Other producers of
// the same role and variant are not touched.
func tripoNativeOBJExport(raw json.RawMessage) bool {
	var offering struct {
		AdapterKey      string `json:"adapter_key"`
		ProviderOptions struct {
			NativeConversion *struct {
				Formats []struct {
					Format  string `json:"format"`
					Role    string `json:"role"`
					Variant string `json:"variant"`
				} `json:"formats"`
			} `json:"native_conversion"`
		} `json:"provider_options"`
	}
	if json.Unmarshal(raw, &offering) != nil || offering.AdapterKey != "tripo.v3" || offering.ProviderOptions.NativeConversion == nil {
		return false
	}
	for _, format := range offering.ProviderOptions.NativeConversion.Formats {
		if format.Format == "OBJ" && format.Role == objRole && format.Variant == objVariant {
			return true
		}
	}
	return false
}

func bounded(detail string) string {
	detail = strings.TrimSpace(detail)
	if len([]rune(detail)) > maxDetailLength {
		return string([]rune(detail)[:maxDetailLength])
	}
	return detail
}
