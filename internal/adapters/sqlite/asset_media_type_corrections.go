package sqlite

import (
	"context"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/assetcorrection"
)

// ListMediaTypeCorrectionCandidates lists assets of a role and variant that
// still carry mediaType and have no decision recorded for correction, with
// the frozen Offering of their task.
func (r *Repository) ListMediaTypeCorrectionCandidates(ctx context.Context, correction, role, variant, mediaType string) ([]assetcorrection.Candidate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.task_id, a.attempt_id, a.role, a.variant, a.storage_key,
		       a.media_type, a.size_bytes, a.sha256, a.delivery_profile, t.offering_json
		FROM generation_assets a
		JOIN generation_tasks t ON t.id = a.task_id
		WHERE a.role = ? AND a.variant = ? AND a.media_type = ?
		  AND NOT EXISTS (
		      SELECT 1 FROM asset_media_type_corrections c
		      WHERE c.correction = ? AND c.asset_id = a.id)
		ORDER BY a.id`,
		role, variant, mediaType, correction,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []assetcorrection.Candidate
	for rows.Next() {
		var candidate assetcorrection.Candidate
		var offering string
		asset := &candidate.Asset
		if err := rows.Scan(&asset.ID, &asset.TaskID, &asset.AttemptID, &asset.Role, &asset.Variant, &asset.StorageKey,
			&asset.MediaType, &asset.Size, &asset.SHA256, &asset.DeliveryProfile, &offering); err != nil {
			return nil, err
		}
		candidate.Offering = []byte(offering)
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

// RecordMediaTypeCorrection records one decision. A correction relabels the
// asset in the same transaction and only while it still carries the previous
// label; a concurrent change aborts the whole record.
func (r *Repository) RecordMediaTypeCorrection(ctx context.Context, record assetcorrection.Record) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if record.Outcome == assetcorrection.OutcomeCorrected {
		result, err := tx.ExecContext(ctx,
			`UPDATE generation_assets SET media_type = ? WHERE id = ? AND media_type = ?`,
			record.MediaType, record.AssetID, record.PreviousMediaType)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected != 1 {
			return fmt.Errorf("资产 %s 的媒体类型已不是 %s", record.AssetID, record.PreviousMediaType)
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_media_type_corrections
		    (correction, asset_id, task_id, outcome, previous_media_type, media_type, sha256, detail, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.Correction, record.AssetID, record.TaskID, record.Outcome, record.PreviousMediaType,
		record.MediaType, record.SHA256, record.Detail, formatTime(record.RecordedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// MediaTypeCorrections returns the recorded decisions of a correction in
// asset order (operations evidence and tests).
func (r *Repository) MediaTypeCorrections(ctx context.Context, correction string) ([]assetcorrection.Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, task_id, outcome, previous_media_type, media_type, sha256, detail, recorded_at
		FROM asset_media_type_corrections WHERE correction = ? ORDER BY asset_id`, correction)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []assetcorrection.Record
	for rows.Next() {
		record := assetcorrection.Record{Correction: correction}
		var recordedAt string
		if err := rows.Scan(&record.AssetID, &record.TaskID, &record.Outcome, &record.PreviousMediaType,
			&record.MediaType, &record.SHA256, &record.Detail, &recordedAt); err != nil {
			return nil, err
		}
		if record.RecordedAt, err = parseTime(recordedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

var _ assetcorrection.Repository = (*Repository)(nil)
