package sqlite

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

func (r *Repository) QueryAdministrationUserAssets(
	ctx context.Context,
	query administration.UserAssetsQuery,
) (administration.UserAssetsOverview, bool, error) {
	storedUser, found, err := r.FindAdministrationUser(ctx, query.UserID)
	if err != nil {
		return administration.UserAssetsOverview{}, false, err
	}
	if !found {
		return administration.UserAssetsOverview{}, false, nil
	}
	uploads, uploadTotal, storageTotal, err :=
		r.queryAdministrationUserUploads(ctx, query)
	if err != nil {
		return administration.UserAssetsOverview{}, false, err
	}
	generationRecords, generationTotal, err :=
		r.QueryAdministrationGenerations(
			ctx,
			administration.GenerationQuery{
				Page:     query.GenerationsPage,
				PageSize: query.PageSize,
				UserID:   query.UserID,
				From:     query.From,
				To:       query.To,
			},
		)
	if err != nil {
		return administration.UserAssetsOverview{}, false, err
	}
	generations := make(
		[]administration.UserAssetGeneration,
		0,
		len(generationRecords),
	)
	for _, record := range generationRecords {
		generations = append(
			generations,
			administration.UserAssetGeneration{
				ID:               record.ID,
				TaskType:         record.TaskType,
				TaskID:           record.TaskID,
				TaskStatus:       record.TaskStatus,
				ResultURLs:       append([]string(nil), record.ResultURLs...),
				Params:           append([]byte(nil), record.Params...),
				EstimatedCredits: record.EstimatedCredits, NativeQuote: record.NativeQuote, Offering: record.Offering,
				ThumbnailURL: record.ThumbnailURL,
				CreatedAt:    record.CreatedAt,
				FinishedAt:   record.FinishedAt,
				Assets: append(
					[]administration.GenerationAsset(nil),
					record.Assets...,
				),
			},
		)
	}
	downloads, downloadTotal, err :=
		r.queryAdministrationUserDownloads(ctx, query)
	if err != nil {
		return administration.UserAssetsOverview{}, false, err
	}
	return administration.UserAssetsOverview{
		User: storedUser.User,
		Summary: administration.UserAssetsSummary{
			TotalUploads:      uploadTotal,
			TotalGenerations:  generationTotal,
			TotalDownloads:    downloadTotal,
			TotalStorageBytes: storageTotal,
		},
		Uploads: administration.UserAssetsPage[administration.UserAssetUpload]{
			Data:  uploads,
			Total: uploadTotal,
		},
		Generations: administration.UserAssetsPage[administration.UserAssetGeneration]{
			Data:  generations,
			Total: generationTotal,
		},
		Downloads: administration.UserAssetsPage[administration.UserAssetDownload]{
			Data:  downloads,
			Total: downloadTotal,
		},
	}, true, nil
}

func (r *Repository) queryAdministrationUserUploads(
	ctx context.Context,
	query administration.UserAssetsQuery,
) ([]administration.UserAssetUpload, int, int64, error) {
	source, err := r.administrationUserUploadSource(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	clauses, args := administrationUserAssetRange(
		"user_id",
		query.UserID,
		query.From,
		query.To,
	)
	where := strings.Join(clauses, " AND ")
	var total int
	var storageTotal int64
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*), COALESCE(SUM(file_size), 0)
		FROM (`+source+`) AS upload
		WHERE `+where,
		args...,
	).Scan(&total, &storageTotal); err != nil {
		return nil, 0, 0, fmt.Errorf(
			"统计管理用户上传资产: %w",
			err,
		)
	}
	pageArgs := append([]any(nil), args...)
	pageArgs = append(
		pageArgs,
		query.PageSize,
		(query.UploadsPage-1)*query.PageSize,
	)
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, asset_type, original_filename, stored_filename,
			mime_type, file_size, storage_path, created_at, record_source
		FROM (`+source+`) AS upload
		WHERE `+where+`
		ORDER BY created_unix DESC, id DESC
		LIMIT ? OFFSET ?`,
		pageArgs...,
	)
	if err != nil {
		return nil, 0, 0, fmt.Errorf(
			"查询管理用户上传资产: %w",
			err,
		)
	}
	defer rows.Close()
	uploads := make(
		[]administration.UserAssetUpload,
		0,
		query.PageSize,
	)
	for rows.Next() {
		var upload administration.UserAssetUpload
		var createdAt string
		var sourceName string
		if err := rows.Scan(
			&upload.ID,
			&upload.AssetType,
			&upload.OriginalFilename,
			&upload.StoredFilename,
			&upload.MimeType,
			&upload.FileSize,
			&upload.StoragePath,
			&createdAt,
			&sourceName,
		); err != nil {
			return nil, 0, 0, fmt.Errorf(
				"扫描管理用户上传资产: %w",
				err,
			)
		}
		upload.CreatedAt, err = parseDatabaseTime(createdAt)
		if err != nil {
			return nil, 0, 0, err
		}
		if sourceName == "native" {
			upload.StoredFilename = path.Base(upload.StoredFilename)
		}
		uploads = append(uploads, upload)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf(
			"遍历管理用户上传资产: %w",
			err,
		)
	}
	return uploads, total, storageTotal, nil
}

func (r *Repository) administrationUserUploadSource(
	ctx context.Context,
) (string, error) {
	const nativeSource = `
		SELECT id, owner_id AS user_id, asset_type,
			original_filename, storage_key AS stored_filename,
			media_type AS mime_type, size_bytes AS file_size,
			storage_key AS storage_path, created_at,
			unixepoch(created_at, 'subsec') AS created_unix,
			'native' AS record_source
		FROM generation_input_assets`
	exists, err := r.tableExists(ctx, "asset_registry")
	if err != nil {
		return "", fmt.Errorf("检查旧上传资产表: %w", err)
	}
	if !exists {
		return nativeSource, nil
	}
	legacySource := `
		SELECT CAST(legacy.id AS TEXT) AS id,
			legacy.uploaderUserId AS user_id,
			COALESCE(legacy.assetType, '') AS asset_type,
			COALESCE(legacy.originalFilename, '') AS original_filename,
			COALESCE(legacy.storedFilename, '') AS stored_filename,
			COALESCE(legacy.mimeType, '') AS mime_type,
			COALESCE(legacy.fileSize, 0) AS file_size,
			COALESCE(legacy.storagePath, '') AS storage_path,
			CAST(legacy.createdAt AS TEXT) AS created_at,
			` + databaseUnixExpression("legacy.createdAt") + ` AS created_unix,
			'legacy' AS record_source
		FROM asset_registry AS legacy
		WHERE NOT EXISTS (
			SELECT 1
			FROM generation_input_assets AS native
			WHERE native.id = legacy.id
		)`
	return nativeSource + "\nUNION ALL\n" + legacySource, nil
}

func (r *Repository) queryAdministrationUserDownloads(
	ctx context.Context,
	query administration.UserAssetsQuery,
) ([]administration.UserAssetDownload, int, error) {
	source, err := r.administrationAuditSource(ctx)
	if err != nil {
		return nil, 0, err
	}
	clauses, args := administrationUserAssetRange(
		"actor_id",
		query.UserID,
		query.From,
		query.To,
	)
	if len(query.DownloadActions) == 0 {
		clauses = append(clauses, "0 = 1")
	} else {
		placeholders := make([]string, len(query.DownloadActions))
		for index, action := range query.DownloadActions {
			placeholders[index] = "?"
			args = append(args, action)
		}
		clauses = append(
			clauses,
			"action IN ("+strings.Join(placeholders, ",")+")",
		)
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := r.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM ("+source+") AS audit WHERE "+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf(
			"统计管理用户下载记录: %w",
			err,
		)
	}
	pageArgs := append([]any(nil), args...)
	pageArgs = append(
		pageArgs,
		query.PageSize,
		(query.DownloadsPage-1)*query.PageSize,
	)
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, action, resource_type, resource_id,
			CAST(detail_json AS TEXT), CAST(created_at AS TEXT)
		FROM (`+source+`) AS audit
		WHERE `+where+`
		ORDER BY created_unix DESC, id DESC
		LIMIT ? OFFSET ?`,
		pageArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf(
			"查询管理用户下载记录: %w",
			err,
		)
	}
	defer rows.Close()
	downloads := make(
		[]administration.UserAssetDownload,
		0,
		query.PageSize,
	)
	for rows.Next() {
		var download administration.UserAssetDownload
		var createdAt string
		if err := rows.Scan(
			&download.ID,
			&download.Action,
			&download.ResourceType,
			&download.ResourceID,
			&download.Detail,
			&createdAt,
		); err != nil {
			return nil, 0, fmt.Errorf(
				"扫描管理用户下载记录: %w",
				err,
			)
		}
		download.CreatedAt, err = parseDatabaseTime(createdAt)
		if err != nil {
			return nil, 0, err
		}
		downloads = append(downloads, download)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf(
			"遍历管理用户下载记录: %w",
			err,
		)
	}
	return downloads, total, nil
}

func administrationUserAssetRange(
	userColumn string,
	userID string,
	from *time.Time,
	to *time.Time,
) ([]string, []any) {
	clauses := []string{userColumn + " = ?"}
	args := []any{userID}
	if from != nil {
		clauses = append(clauses, "created_unix >= ?")
		args = append(
			args,
			float64(from.UnixNano())/float64(1_000_000_000),
		)
	}
	if to != nil {
		clauses = append(clauses, "created_unix <= ?")
		args = append(
			args,
			float64(to.UnixNano())/float64(1_000_000_000),
		)
	}
	return clauses, args
}
