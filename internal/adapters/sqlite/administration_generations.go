package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// nativeAdministrationGenerationSource lists every native generation record;
// charged says whether its estimate counts toward spent credits
// (taskMayBeChargedSQL, #327) for Tasks run on this platform; imported legacy
// usage counts as recorded. Records still list every Task.
var nativeAdministrationGenerationSource = `
	SELECT
		ledger.id AS record_id,
		ledger.user_id AS user_id,
		ledger.user_name AS user_name,
		ledger.user_email AS user_email,
		ledger.task_type AS task_type,
		ledger.task_id AS task_id,
		ledger.estimated_credits AS estimated_credits,
		ledger_task.state AS task_status,
		ledger.params_json AS params_json,
		ledger.result_urls_json AS result_urls_json,
		'' AS thumbnail_url,
		CAST(ledger.created_at AS TEXT) AS created_at,
		CAST(ledger.finished_at AS TEXT) AS finished_at,
		unixepoch(ledger.created_at, 'subsec') AS created_unix,
		'native' AS record_source, CASE WHEN ledger_task.native_quote_required=1 THEN COALESCE(ledger_task.native_quote_json,'null') ELSE ledger_task.native_quote_json END AS native_quote_json,
		json_object('id', json_extract(ledger_task.offering_json, '$.id'), 'provider_id', json_extract(ledger_task.offering_json, '$.provider_id'),
			'model_id', json_extract(ledger_task.offering_json, '$.model_id'), 'capability', json_extract(ledger_task.offering_json, '$.capability')) AS offering_ref_json,
		NULL AS estimate_offering_json,
		-- Imported legacy usage (only the ETL import writes this ledger) has no
		-- upstream-phase evidence, so it counts as recorded.
		1 AS charged
	FROM generation_usage_ledger AS ledger
	JOIN generation_tasks AS ledger_task ON ledger_task.id = ledger.task_id
	WHERE NOT EXISTS (
		SELECT 1
		FROM generation_task_tombstones AS tombstone
		WHERE tombstone.task_id = ledger.task_id
	)
	UNION ALL
	SELECT
		task.id AS record_id,
		task.owner_id AS user_id,
		COALESCE(
			NULLIF(user.nickname, ''),
			NULLIF(user.name, ''),
			user.username,
			''
		) AS user_name,
		COALESCE(user.email, '') AS user_email,
		task.capability AS task_type,
		task.id AS task_id,
		COALESCE(
			CAST(json_extract(task.offering_json, '$.estimated_credits') AS INTEGER),
			0
		) AS estimated_credits,
		task.state AS task_status,
		task.input_json AS params_json,
		'[]' AS result_urls_json,
		'' AS thumbnail_url,
		CAST(task.created_at AS TEXT) AS created_at,
		CAST(task.finished_at AS TEXT) AS finished_at,
		unixepoch(task.created_at, 'subsec') AS created_unix,
		'native' AS record_source, CASE WHEN task.native_quote_required=1 THEN COALESCE(task.native_quote_json,'null') ELSE task.native_quote_json END AS native_quote_json,
		json_object('id', json_extract(task.offering_json, '$.id'), 'provider_id', json_extract(task.offering_json, '$.provider_id'),
			'model_id', json_extract(task.offering_json, '$.model_id'), 'capability', json_extract(task.offering_json, '$.capability')) AS offering_ref_json,
		` + nativeEstimateOfferingExpression + ` AS estimate_offering_json,
		CASE WHEN ` + taskMayBeChargedSQL("task") + ` THEN 1 ELSE 0 END AS charged
	FROM generation_tasks AS task
	LEFT JOIN authentication_users AS user ON user.id = task.owner_id
	WHERE NOT EXISTS (
		SELECT 1
		FROM generation_usage_ledger AS ledger
		WHERE ledger.task_id = task.id
	) AND NOT EXISTS (
		SELECT 1
		FROM generation_task_tombstones AS tombstone
		WHERE tombstone.task_id = task.id
	)`

// administrationGenerationColumns are the source columns
// scanAdministrationGeneration reads, in its order.
const administrationGenerationColumns = `record_id, user_id, user_name, user_email, task_type, task_id,
	estimated_credits, task_status, params_json, result_urls_json,
	thumbnail_url, created_at, finished_at, record_source, native_quote_json, offering_ref_json,
	estimate_offering_json`

// nativeEstimateOfferingExpression selects the frozen Offering of a native Task
// whose estimate depends on its frozen input (extra credits or a unit count);
// for every other Task the bare estimated_credits already is the estimate, so
// its large Offering document is not read back.
const nativeEstimateOfferingExpression = `CASE WHEN COALESCE(CAST(json_extract(task.offering_json, '$.cost.extra_credits') AS INTEGER), 0) > 0
			OR COALESCE(json_extract(task.offering_json, '$.estimated_credits_unit_parameter'), '') <> ''
		THEN task.offering_json END`

// frozenTaskEstimate is the estimate of a native Task as the user side states it
// (task detail, credits_charged, session total): the frozen Offering's
// EstimateTotalCredits of the frozen input, base plus unit count plus extra
// credits (#324). Without such an Offering the stored number stands.
func frozenTaskEstimate(stored int, offeringJSON sql.NullString, input string) int {
	if !offeringJSON.Valid || offeringJSON.String == "" {
		return stored
	}
	var offering schema.Offering
	if err := json.Unmarshal([]byte(offeringJSON.String), &offering); err != nil {
		return stored
	}
	return offering.EstimateTotalCredits(json.RawMessage(input))
}

type administrationGenerationRow struct {
	record administration.GenerationRecord
	source string
}

func (r *Repository) QueryAdministrationGenerations(
	ctx context.Context,
	query administration.GenerationQuery,
) ([]administration.GenerationRecord, int, error) {
	source, err := r.administrationGenerationSource(ctx)
	if err != nil {
		return nil, 0, err
	}
	if query.PageSize <= 0 || query.Page <= 0 {
		return nil, 0, fmt.Errorf("invalid administration pagination")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	clauses := []string{"1 = 1"}
	args := []any{}
	if query.UserID != "" {
		clauses = append(clauses, "user_id = ?")
		args = append(args, query.UserID)
	}
	if query.TaskType != "" {
		clauses = append(clauses, "task_type = ?")
		args = append(args, query.TaskType)
	}
	if query.Status != "" {
		clauses = append(clauses, "task_status = ?")
		args = append(args, query.Status)
	}
	if query.From != nil {
		clauses = append(clauses, "created_unix >= ?")
		args = append(args, unixSeconds(*query.From))
	}
	if query.To != nil {
		clauses = append(clauses, "created_unix <= ?")
		args = append(args, unixSeconds(*query.To))
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := tx.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM ("+source+") AS generation WHERE "+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计管理生成记录: %w", err)
	}
	idSQL, pageArgs := administrationGenerationIDsSQL(query, strings.Contains(source, "FROM usage_ledger AS ledger"))
	rows, err := tx.QueryContext(ctx, idSQL, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询管理生成记录: %w", err)
	}
	type identifier struct {
		recordID, taskID, source string
		created                  float64
	}
	identifiers := []identifier{}
	for rows.Next() {
		var id identifier
		if err := rows.Scan(&id.recordID, &id.taskID, &id.source, &id.created); err != nil {
			rows.Close()
			return nil, 0, err
		}
		identifiers = append(identifiers, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, fmt.Errorf("遍历管理生成记录: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("关闭管理生成记录结果集: %w", err)
	}
	records := make(
		[]administration.GenerationRecord,
		0,
		len(identifiers),
	)
	for _, id := range identifiers {
		row, err := scanAdministrationGeneration(tx.QueryRowContext(ctx, `SELECT `+administrationGenerationColumns+`
		 FROM (`+source+`) WHERE record_id = ? AND task_id = ? AND record_source = ?`, id.recordID, id.taskID, id.source))
		if err != nil {
			return nil, 0, err
		}
		if row.source == "native" {
			row.record.Assets, err =
				r.loadAdministrationGenerationAssetsFrom(
					ctx,
					tx,
					row.record.TaskID,
				)
			if err != nil {
				return nil, 0, err
			}
		}
		records = append(records, row.record)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func (r *Repository) GetAdministrationGeneration(
	ctx context.Context,
	taskID string,
) (administration.GenerationDetail, bool, error) {
	source, err := r.administrationGenerationSource(ctx)
	if err != nil {
		return administration.GenerationDetail{}, false, err
	}
	row, err := scanAdministrationGeneration(r.db.QueryRowContext(
		ctx,
		`SELECT `+administrationGenerationColumns+`
		FROM (`+source+`) AS generation
		WHERE task_id = ?
		ORDER BY CASE record_source WHEN 'native' THEN 0 ELSE 1 END,
			created_unix DESC, record_id DESC
		LIMIT 1`,
		taskID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return administration.GenerationDetail{}, false, nil
	}
	if err != nil {
		return administration.GenerationDetail{}, false, err
	}
	if row.source == "native" {
		row.record.Assets, err = r.loadAdministrationGenerationAssets(
			ctx,
			taskID,
		)
		if err != nil {
			return administration.GenerationDetail{}, false, err
		}
		cache, cacheErr := r.nativeAdministrationGenerationCache(
			ctx,
			taskID,
		)
		if cacheErr != nil {
			return administration.GenerationDetail{}, false, cacheErr
		}
		return administration.GenerationDetail{
			Ledger:    row.record,
			TaskCache: cache,
		}, true, nil
	}
	cache, err := r.legacyAdministrationGenerationCache(ctx, taskID)
	if err != nil {
		return administration.GenerationDetail{}, false, err
	}
	return administration.GenerationDetail{
		Ledger:    row.record,
		TaskCache: cache,
	}, true, nil
}

func (r *Repository) administrationGenerationSource(
	ctx context.Context,
) (string, error) {
	exists, err := r.tableExists(ctx, "usage_ledger")
	if err != nil {
		return "", fmt.Errorf("检查旧生成记录表: %w", err)
	}
	if !exists {
		return nativeAdministrationGenerationSource, nil
	}
	taskCacheExists, err := r.tableExists(ctx, "task_cache")
	if err != nil {
		return "", fmt.Errorf("检查旧任务缓存表: %w", err)
	}
	cacheJoin := ""
	thumbnailExpression := "''"
	if taskCacheExists {
		cacheJoin = `
			LEFT JOIN task_cache AS cache ON cache.taskId = ledger.taskId`
		thumbnailExpression = `CASE
			WHEN json_valid(cache.data) THEN COALESCE(
				json_extract(cache.data, '$.thumbnail_url'),
				json_extract(cache.data, '$.thumbnailUrl'),
				''
			)
			ELSE ''
		END`
	}
	legacySource := `
		SELECT
			CAST(ledger.id AS TEXT) AS record_id,
			ledger.userId AS user_id,
			ledger.userName AS user_name,
			ledger.userEmail AS user_email,
			ledger.taskType AS task_type,
			ledger.taskId AS task_id,
			ledger.estimatedCredits AS estimated_credits,
			ledger.taskStatus AS task_status,
			ledger.params AS params_json,
			ledger.resultUrls AS result_urls_json,
			` + thumbnailExpression + ` AS thumbnail_url,
			CAST(ledger.createdAt AS TEXT) AS created_at,
			CAST(ledger.finishedAt AS TEXT) AS finished_at,
			` + databaseUnixExpression("ledger.createdAt") + ` AS created_unix,
			'legacy' AS record_source, NULL AS native_quote_json, NULL AS offering_ref_json,
			NULL AS estimate_offering_json,
			1 AS charged
		FROM usage_ledger AS ledger
		` + cacheJoin + `
		WHERE NOT EXISTS (
			SELECT 1
			FROM generation_tasks AS native
			WHERE native.id = ledger.taskId
		)`
	return nativeAdministrationGenerationSource +
		"\nUNION ALL\n" +
		legacySource, nil
}

func (r *Repository) loadAdministrationGenerationAssets(ctx context.Context, taskID string) ([]administration.GenerationAsset, error) {
	return r.loadAdministrationGenerationAssetsFrom(ctx, r.db, taskID)
}

func (r *Repository) loadAdministrationGenerationAssetsFrom(
	ctx context.Context,
	reader queryer,
	taskID string,
) ([]administration.GenerationAsset, error) {
	rows, err := reader.QueryContext(ctx, `
		SELECT id, role, variant, media_type, ordinal
		FROM generation_assets
		WHERE task_id = ?
		ORDER BY ordinal ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询管理生成资产: %w", err)
	}
	defer rows.Close()
	assets := []administration.GenerationAsset{}
	for rows.Next() {
		var asset administration.GenerationAsset
		if err := rows.Scan(
			&asset.ID,
			&asset.Role,
			&asset.Variant,
			&asset.MediaType,
			&asset.Ordinal,
		); err != nil {
			return nil, fmt.Errorf("扫描管理生成资产: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历管理生成资产: %w", err)
	}
	return assets, nil
}

func (r *Repository) nativeAdministrationGenerationCache(
	ctx context.Context,
	taskID string,
) (*administration.GenerationCache, error) {
	var cache administration.GenerationCache
	var data sql.NullString
	var cachedAt string
	var updatedAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT
			task.id,
			task.capability,
			task.state,
			COALESCE(json_extract(task.offering_json, '$.provider_id'), ''),
			attempt.opaque_json,
			CAST(task.created_at AS TEXT),
			CAST(task.updated_at AS TEXT)
		FROM generation_tasks AS task
		LEFT JOIN generation_attempts AS attempt
			ON attempt.id = task.current_attempt_id
		WHERE task.id = ?`,
		taskID,
	).Scan(
		&cache.TaskID,
		&cache.TaskType,
		&cache.Status,
		&cache.ProviderID,
		&data,
		&cachedAt,
		&updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("查询原生管理任务缓存: %w", err)
	}
	cache.Data = json.RawMessage(`{}`)
	if data.Valid && json.Valid([]byte(data.String)) {
		cache.Data = json.RawMessage(data.String)
	}
	cache.CachedAt, err = parseDatabaseTime(cachedAt)
	if err != nil {
		return nil, err
	}
	cache.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	return &cache, nil
}

func (r *Repository) legacyAdministrationGenerationCache(
	ctx context.Context,
	taskID string,
) (*administration.GenerationCache, error) {
	exists, err := r.tableExists(ctx, "task_cache")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	var cache administration.GenerationCache
	var data string
	var cachedAt string
	var updatedAt string
	err = r.db.QueryRowContext(ctx, `
		SELECT
			taskId,
			taskType,
			status,
			providerId,
			data,
			CAST(cachedAt AS TEXT),
			CAST(updatedAt AS TEXT)
		FROM task_cache
		WHERE taskId = ?`,
		taskID,
	).Scan(
		&cache.TaskID,
		&cache.TaskType,
		&cache.Status,
		&cache.ProviderID,
		&data,
		&cachedAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询旧管理任务缓存: %w", err)
	}
	cache.Data = json.RawMessage(`{}`)
	if json.Valid([]byte(data)) {
		cache.Data = json.RawMessage(data)
	}
	cache.CachedAt, err = parseDatabaseTime(cachedAt)
	if err != nil {
		return nil, err
	}
	cache.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	return &cache, nil
}

func scanAdministrationGeneration(
	row rowScanner,
) (administrationGenerationRow, error) {
	var result administrationGenerationRow
	var params string
	var resultURLs string
	var createdAt string
	var finishedAt sql.NullString
	var quote sql.NullString
	var offering sql.NullString
	var estimateOffering sql.NullString
	err := row.Scan(
		&result.record.ID,
		&result.record.UserID,
		&result.record.UserName,
		&result.record.UserEmail,
		&result.record.TaskType,
		&result.record.TaskID,
		&result.record.EstimatedCredits,
		&result.record.TaskStatus,
		&params,
		&resultURLs,
		&result.record.ThumbnailURL,
		&createdAt,
		&finishedAt,
		&result.source, &quote, &offering, &estimateOffering,
	)
	if err != nil {
		return administrationGenerationRow{}, err
	}
	if quote.Valid {
		if err := json.Unmarshal([]byte(quote.String), &result.record.NativeQuote); err != nil {
			return administrationGenerationRow{}, err
		}
		if err := result.record.NativeQuote.Validate(); err != nil {
			return administrationGenerationRow{}, err
		}
	}
	if offering.Valid {
		if err := json.Unmarshal([]byte(offering.String), &result.record.Offering); err != nil {
			return administrationGenerationRow{}, err
		}
	}
	result.record.Params = json.RawMessage(`{}`)
	if json.Valid([]byte(params)) {
		result.record.Params = json.RawMessage(params)
	}
	if result.record.NativeQuote == nil {
		result.record.EstimatedCredits = frozenTaskEstimate(result.record.EstimatedCredits, estimateOffering, params)
	}
	result.record.ResultURLs = []string{}
	if json.Valid([]byte(resultURLs)) {
		_ = json.Unmarshal(
			[]byte(resultURLs),
			&result.record.ResultURLs,
		)
	}
	result.record.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return administrationGenerationRow{}, err
	}
	if finishedAt.Valid && strings.TrimSpace(finishedAt.String) != "" {
		value, parseErr := parseDatabaseTime(finishedAt.String)
		if parseErr != nil {
			return administrationGenerationRow{}, parseErr
		}
		result.record.FinishedAt = &value
	}
	result.record.Assets = []administration.GenerationAsset{}
	return result, nil
}

func (r *Repository) tableExists(
	ctx context.Context,
	name string,
) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?`,
		name,
	).Scan(&count)
	return count > 0, err
}

func databaseUnixExpression(column string) string {
	return `CASE
		WHEN typeof(` + column + `) IN ('integer', 'real') THEN
			CASE
				WHEN ` + column + ` > 100000000000
				THEN ` + column + ` / 1000.0
				ELSE ` + column + `
			END
		ELSE unixepoch(` + column + `, 'subsec')
	END`
}
