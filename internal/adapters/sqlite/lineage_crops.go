package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

// SessionCrops lists the newest crops a session's chat shows (#587): owned
// list_crop input assets named by a crop attachment of one of the session's
// messages, with the source their provenance records. The crop's only tie to
// the session is that attachment; the asset row itself has no session. It
// reads stored rows only and never creates a Task.
func (r *Repository) SessionCrops(ctx context.Context, owner, session string, limit int) ([]lineage.SessionCrop, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(session) == "" || limit < 1 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT asset.id, asset.owner_id, asset.storage_key, asset.original_filename, asset.asset_type,
		       asset.media_type, asset.size_bytes, asset.sha256, asset.created_at,
		       COALESCE(crop.source_asset_id, '')
		FROM generation_input_assets asset
		LEFT JOIN input_asset_crops crop
		       ON crop.crop_asset_id = asset.id AND crop.owner_id = asset.owner_id
		WHERE asset.owner_id = ? AND asset.asset_type = ?
		  AND asset.id IN (
		      SELECT json_extract(attachment.value, '$.asset_id')
		      FROM go_chat_messages message
		      JOIN go_chat_sessions owned ON owned.id = message.session_id AND owned.owner_id = ?,
		           json_each(CASE WHEN json_valid(message.attachments) THEN message.attachments ELSE '[]' END) attachment
		      WHERE message.session_id = ?
		        AND json_extract(attachment.value, '$.role') = ?)
		ORDER BY rtrim(asset.created_at, 'Z') DESC, asset.id DESC
		LIMIT ?`,
		owner, listcrop.CropAssetType, owner, session, chat.AttachmentRoleCrop, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list session crops: %w", err)
	}
	defer rows.Close()
	var result []lineage.SessionCrop
	for rows.Next() {
		var crop lineage.SessionCrop
		var createdAt string
		if err := rows.Scan(
			&crop.Asset.ID, &crop.Asset.OwnerID, &crop.Asset.StorageKey, &crop.Asset.OriginalFilename,
			&crop.Asset.AssetType, &crop.Asset.MediaType, &crop.Asset.Size, &crop.Asset.SHA256, &createdAt,
			&crop.SourceAssetID,
		); err != nil {
			return nil, err
		}
		if crop.Asset.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		result = append(result, crop)
	}
	return result, rows.Err()
}

var _ lineage.CropSource = (*Repository)(nil)
