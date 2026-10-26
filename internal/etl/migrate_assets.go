package etl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/assets/legacygenerated"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const maximumAssetReportIssues = 20

type inputAssetMigrationReport struct {
	sourceCount        int64
	loaded             int
	materialized       int
	recovered          int
	unavailable        []string
	sha256Samples      []string
	sourceLosses       []inputAssetSourceLoss
	acknowledgedAssets int
	acknowledgementSHA string
	recoveryIssues     []legacyInputAssetRecoveryIssue
}

type taskOutputMigrationReport struct {
	declared            int
	materialized        int
	auditRecovered      int
	unavailable         []string
	optionalUnavailable []string
	sha256Samples       []string
	sourceLosses        []taskOutputSourceLoss
	acknowledgedTasks   int
	acknowledgedIssues  int
	acknowledgementSHA  string
}

type taskOutputSourceLoss struct {
	TaskID      string `json:"task_id"`
	Capability  string `json:"capability"`
	OfferingID  string `json:"offering_id"`
	ProviderID  string `json:"provider_id"`
	ModelID     string `json:"model_id"`
	IssueCount  int    `json:"issue_count"`
	IssueSHA256 string `json:"issue_sha256"`
}

type inputAssetSourceLoss struct {
	AssetID      string `json:"asset_id"`
	OwnerID      string `json:"owner_id"`
	AssetType    string `json:"asset_type"`
	MediaType    string `json:"media_type"`
	FileSize     int64  `json:"file_size"`
	StoredSHA256 string `json:"stored_sha256"`
	StoragePath  string `json:"storage_path"`
	IssueSHA256  string `json:"issue_sha256"`
}

func (r taskOutputMigrationReport) String() string {
	var report strings.Builder
	fmt.Fprintf(
		&report,
		"任务产物域: 声明 %d 个 / 固化 %d 个 / 不可固化 %d 个 / 可选产物未固化 %d 个 / 审计副本恢复 %d 个\n",
		r.declared,
		r.materialized,
		len(r.unavailable),
		len(r.optionalUnavailable),
		r.auditRecovered,
	)
	if len(r.sourceLosses) > 0 {
		fmt.Fprintf(
			&report,
			"历史源损失确认: 任务 %d 个 / 明细 %d 条 / 未确认 %d 条",
			r.acknowledgedTasks,
			r.acknowledgedIssues,
			r.unacknowledgedIssueCount(),
		)
		if r.acknowledgementSHA != "" {
			fmt.Fprintf(
				&report,
				" / 清单 SHA-256 %s",
				r.acknowledgementSHA,
			)
		}
		report.WriteByte('\n')
	}
	if len(r.sha256Samples) > 0 {
		fmt.Fprintf(
			&report,
			"任务产物 SHA-256 样本: %s\n",
			strings.Join(r.sha256Samples, ", "),
		)
	}
	for index, issue := range r.unavailable {
		if index == maximumAssetReportIssues {
			fmt.Fprintf(
				&report,
				"任务产物不可固化明细: 另有 %d 条省略\n",
				len(r.unavailable)-maximumAssetReportIssues,
			)
			break
		}
		fmt.Fprintf(&report, "任务产物不可固化: %s\n", issue)
	}
	for index, issue := range r.optionalUnavailable {
		if index == maximumAssetReportIssues {
			fmt.Fprintf(
				&report,
				"可选产物未固化明细: 另有 %d 条省略\n",
				len(r.optionalUnavailable)-maximumAssetReportIssues,
			)
			break
		}
		fmt.Fprintf(&report, "可选产物未固化: %s\n", issue)
	}
	return report.String()
}

func (r inputAssetMigrationReport) String() string {
	var report strings.Builder
	fmt.Fprintf(
		&report,
		"输入资产域: 源 %d 行 / 固化 %d 行 / SHA-256 副本恢复 %d 行 / 不可固化 %d 行\n",
		r.sourceCount,
		r.materialized,
		r.recovered,
		len(r.unavailable),
	)
	for _, issue := range r.recoveryIssues {
		encoded, _ := json.Marshal(issue)
		fmt.Fprintf(&report, "输入资产恢复候选跳过: %s\n", encoded)
	}
	if len(r.sha256Samples) > 0 {
		fmt.Fprintf(
			&report,
			"输入资产 SHA-256 样本: %s\n",
			strings.Join(r.sha256Samples, ", "),
		)
	}
	if len(r.sourceLosses) > 0 {
		fmt.Fprintf(
			&report,
			"历史输入资产源损失确认: 资产 %d 个 / 未确认 %d 个",
			r.acknowledgedAssets,
			r.unacknowledgedSourceLossCount(),
		)
		if r.acknowledgementSHA != "" {
			fmt.Fprintf(
				&report,
				" / 清单 SHA-256 %s",
				r.acknowledgementSHA,
			)
		}
		report.WriteByte('\n')
	}
	for index, issue := range r.unavailable {
		if index == maximumAssetReportIssues {
			fmt.Fprintf(
				&report,
				"输入资产不可固化明细: 另有 %d 条省略\n",
				len(r.unavailable)-maximumAssetReportIssues,
			)
			break
		}
		fmt.Fprintf(&report, "输入资产不可固化: %s\n", issue)
	}
	return report.String()
}

func loadLegacyInputAssets(
	ctx context.Context,
	legacy legacyQuery,
	legacyStorageRoot string,
	blobs *local.Store,
) ([]generation.InputAsset, inputAssetMigrationReport, error) {
	var report inputAssetMigrationReport
	recovery, err := newLegacyInputAssetRecovery(ctx, legacy, legacyStorageRoot)
	if err != nil {
		return nil, report, err
	}
	report.recoveryIssues = recovery.issues
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM asset_registry`,
	).Scan(&report.sourceCount); err != nil {
		return nil, report, fmt.Errorf("统计旧 asset_registry 表行数: %w", err)
	}
	rows, err := legacy.QueryContext(ctx, `
		SELECT CAST(id AS TEXT),
		       COALESCE(assetType, ''),
		       COALESCE(originalFilename, ''),
		       COALESCE(mimeType, ''),
		       COALESCE(fileSize, 0),
		       COALESCE(storedSha256, ''),
		       COALESCE(storagePath, ''),
		       COALESCE(uploaderUserId, ''),
		       createdAt
		FROM asset_registry
		ORDER BY id`)
	if err != nil {
		return nil, report, fmt.Errorf("读取旧 asset_registry: %w", err)
	}
	defer rows.Close()

	assets := make([]generation.InputAsset, 0, report.sourceCount)
	for rows.Next() {
		report.loaded++
		var (
			id               string
			assetType        string
			originalFilename string
			mediaType        string
			declaredSize     int64
			expectedSHA256   string
			storagePath      string
			ownerID          string
			createdValue     any
		)
		if err := rows.Scan(
			&id,
			&assetType,
			&originalFilename,
			&mediaType,
			&declaredSize,
			&expectedSHA256,
			&storagePath,
			&ownerID,
			&createdValue,
		); err != nil {
			return nil, report, fmt.Errorf("读取旧输入资产行: %w", err)
		}
		createdAt, err := parseLegacyTime(createdValue)
		if err != nil {
			report.unavailable = append(
				report.unavailable,
				fmt.Sprintf("%s: createdAt 无法解析: %v", id, err),
			)
			continue
		}
		body, actualSize, err := openLegacyStorageFile(
			filepath.Join(legacyStorageRoot, "assets"),
			storagePath,
		)
		if err != nil {
			body, actualSize, err = recovery.Open(expectedSHA256)
			if err != nil {
				issue := fmt.Sprintf("%s: %v", id, err)
				report.unavailable = append(
					report.unavailable,
					issue,
				)
				report.sourceLosses = append(
					report.sourceLosses,
					newInputAssetSourceLoss(
						id,
						ownerID,
						assetType,
						mediaType,
						declaredSize,
						expectedSHA256,
						storagePath,
						issue,
					),
				)
				continue
			}
			report.recovered++
		}
		hasher := sha256.New()
		if _, err := io.Copy(hasher, body); err != nil {
			_ = body.Close()
			report.unavailable = append(
				report.unavailable,
				fmt.Sprintf("%s: 计算 SHA-256: %v", id, err),
			)
			continue
		}
		actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
		expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
		if expectedSHA256 != "" && actualSHA256 != expectedSHA256 {
			closeErr := body.Close()
			if closeErr != nil {
				return nil, report, fmt.Errorf(
					"关闭旧输入资产 %s: %w",
					id,
					closeErr,
				)
			}
			body, actualSize, err = recovery.Open(expectedSHA256)
			if err != nil {
				issue := fmt.Sprintf(
					"%s: SHA-256 不一致（登记 %s，实际 %s）",
					id,
					expectedSHA256,
					actualSHA256,
				)
				report.unavailable = append(
					report.unavailable,
					issue,
				)
				report.sourceLosses = append(
					report.sourceLosses,
					newInputAssetSourceLoss(
						id,
						ownerID,
						assetType,
						mediaType,
						declaredSize,
						expectedSHA256,
						storagePath,
						issue,
					),
				)
				continue
			}
			report.recovered++
		}
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			_ = body.Close()
			report.unavailable = append(
				report.unavailable,
				fmt.Sprintf("%s: 重置文件读取位置: %v", id, err),
			)
			continue
		}
		maxBytes := actualSize
		if declaredSize > maxBytes {
			maxBytes = declaredSize
		}
		blob, putErr := blobs.PutBlob(ctx, body, mediaType, maxBytes)
		closeErr := body.Close()
		if putErr != nil {
			report.unavailable = append(
				report.unavailable,
				fmt.Sprintf("%s: 写入 CAS: %v", id, putErr),
			)
			continue
		}
		if closeErr != nil {
			return nil, report, fmt.Errorf("关闭旧输入资产 %s: %w", id, closeErr)
		}
		assets = append(assets, generation.InputAsset{
			ID:               generation.AssetID(id),
			OwnerID:          generation.OwnerID(ownerID),
			StorageKey:       blob.StorageKey,
			OriginalFilename: originalFilename,
			AssetType:        assetType,
			MediaType:        blob.MediaType,
			Size:             blob.Size,
			SHA256:           blob.SHA256,
			CreatedAt:        createdAt,
		})
		report.materialized++
		if len(report.sha256Samples) < 3 {
			report.sha256Samples = append(
				report.sha256Samples,
				id+"="+blob.SHA256,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, report, fmt.Errorf("遍历旧 asset_registry: %w", err)
	}
	return assets, report, nil
}

func newInputAssetSourceLoss(
	assetID string,
	ownerID string,
	assetType string,
	mediaType string,
	fileSize int64,
	storedSHA256 string,
	storagePath string,
	issue string,
) inputAssetSourceLoss {
	storedSHA256 = strings.ToLower(strings.TrimSpace(storedSHA256))
	identity := []string{
		assetID,
		ownerID,
		assetType,
		mediaType,
		fmt.Sprintf("%d", fileSize),
		storedSHA256,
		storagePath,
		issue,
	}
	digest := sha256.Sum256([]byte(strings.Join(identity, "\x00")))
	return inputAssetSourceLoss{
		AssetID:      assetID,
		OwnerID:      ownerID,
		AssetType:    assetType,
		MediaType:    mediaType,
		FileSize:     fileSize,
		StoredSHA256: storedSHA256,
		StoragePath:  storagePath,
		IssueSHA256:  hex.EncodeToString(digest[:]),
	}
}

func (r inputAssetMigrationReport) unacknowledgedSourceLossCount() int {
	unacknowledged := len(r.sourceLosses) - r.acknowledgedAssets
	if unacknowledged < 0 {
		return 0
	}
	return unacknowledged
}

func (r inputAssetMigrationReport) unacknowledgedIssueCount() int {
	unacknowledged := len(r.unavailable) - r.acknowledgedAssets
	if unacknowledged < 0 {
		return 0
	}
	return unacknowledged
}

type legacyInputAssetRecovery struct {
	bySHA256 map[string]string
	issues   []legacyInputAssetRecoveryIssue
}

type legacyInputAssetRecoveryIssue struct {
	Category        string   `json:"category"`
	PathFingerprint string   `json:"path_sha256"`
	Reason          string   `json:"reason"`
	Size            int64    `json:"candidate_size"`
	AssetIDs        []string `json:"possible_asset_ids,omitempty"`
}

func newLegacyInputAssetRecovery(
	ctx context.Context,
	legacy legacyQuery,
	legacyStorageRoot string,
) (legacyInputAssetRecovery, error) {
	expectedSizes := make(map[int64][]string)
	rows, err := legacy.QueryContext(ctx, `
		SELECT CAST(id AS TEXT), COALESCE(fileSize, 0)
		FROM asset_registry
		WHERE LENGTH(TRIM(COALESCE(storedSha256, ''))) = 64
		ORDER BY id`)
	if err != nil {
		return legacyInputAssetRecovery{}, fmt.Errorf(
			"读取旧输入资产恢复尺寸: %w",
			err,
		)
	}
	for rows.Next() {
		var id string
		var size int64
		if err := rows.Scan(&id, &size); err != nil {
			_ = rows.Close()
			return legacyInputAssetRecovery{}, fmt.Errorf(
				"读取旧输入资产恢复尺寸行: %w",
				err,
			)
		}
		if size > 0 {
			expectedSizes[size] = append(expectedSizes[size], id)
		}
	}
	if err := rows.Close(); err != nil {
		return legacyInputAssetRecovery{}, err
	}
	if err := rows.Err(); err != nil {
		return legacyInputAssetRecovery{}, fmt.Errorf(
			"遍历旧输入资产恢复尺寸: %w",
			err,
		)
	}

	recovery := legacyInputAssetRecovery{bySHA256: make(map[string]string)}
	for _, directory := range []string{
		"assets",
		"cache",
		"audit",
		"generated",
		"task-inputs",
	} {
		root := filepath.Join(legacyStorageRoot, directory)
		recordIssue := func(filePath, reason string, size int64) {
			relative, _ := filepath.Rel(legacyStorageRoot, filePath)
			fingerprint := sha256.Sum256([]byte(filepath.ToSlash(relative)))
			recovery.issues = append(recovery.issues, legacyInputAssetRecoveryIssue{
				Category: directory, PathFingerprint: hex.EncodeToString(fingerprint[:]),
				Reason: reason, Size: size, AssetIDs: expectedSizes[size],
			})
		}
		err := filepath.WalkDir(root, func(
			filePath string,
			entry os.DirEntry,
			walkErr error,
		) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if filePath == root && errors.Is(walkErr, os.ErrNotExist) {
					return nil
				}
				recordIssue(filePath, "walk_failed", 0)
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				recordIssue(filePath, "stat_failed", 0)
				return nil
			}
			if !info.Mode().IsRegular() || len(expectedSizes[info.Size()]) == 0 {
				return nil
			}
			body, size, err := openVerifiedRegularFile(filePath)
			if err != nil || size != info.Size() {
				if body != nil {
					_ = body.Close()
				}
				recordIssue(filePath, "open_failed", info.Size())
				return nil
			}
			hasher := sha256.New()
			copied, copyErr := copyWithContext(ctx, hasher, body)
			closeErr := body.Close()
			if err := ctx.Err(); err != nil {
				return err
			}
			if copyErr != nil || closeErr != nil || copied != size {
				recordIssue(filePath, "read_failed", info.Size())
				return nil
			}
			digest := hex.EncodeToString(hasher.Sum(nil))
			if current := recovery.bySHA256[digest]; current == "" ||
				filePath < current {
				recovery.bySHA256[digest] = filePath
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return legacyInputAssetRecovery{}, fmt.Errorf(
				"建立旧输入资产 SHA-256 恢复索引: %w",
				err,
			)
		}
	}
	return recovery, nil
}

func (recovery legacyInputAssetRecovery) Open(
	expectedSHA256 string,
) (*os.File, int64, error) {
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if !validSHA256(expectedSHA256) {
		return nil, 0, errors.New("旧输入资产缺失且没有有效 SHA-256 可供恢复")
	}
	filePath := recovery.bySHA256[expectedSHA256]
	if filePath == "" {
		return nil, 0, errors.New("旧输入资产缺失且未找到 SHA-256 一致副本")
	}
	body, size, err := openVerifiedRegularFile(filePath)
	if err != nil {
		return nil, 0, errors.New("旧输入资产 SHA-256 副本无法安全读取")
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, body); err != nil {
		_ = body.Close()
		return nil, 0, errors.New("旧输入资产 SHA-256 副本无法安全读取")
	}
	if hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
		_ = body.Close()
		return nil, 0, errors.New("旧输入资产 SHA-256 副本读取期间发生变化")
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		_ = body.Close()
		return nil, 0, errors.New("旧输入资产 SHA-256 副本无法安全读取")
	}
	return body, size, nil
}

func materializeLegacyTaskOutputs(
	ctx context.Context,
	legacy legacyQuery,
	legacyStorageRoot string,
	blobs *local.Store,
	tasks []sqlite.GenerationTaskImport,
) (
	[]sqlite.GenerationTaskImport,
	[]generation.Asset,
	taskOutputMigrationReport,
	error,
) {
	var report taskOutputMigrationReport
	resultURLs, err := loadLegacyTaskResultURLs(ctx, legacy)
	if err != nil {
		return nil, nil, report, err
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		return nil, nil, report, fmt.Errorf("加载产物 Product Schema: %w", err)
	}
	application := product.Application()
	sourceResolver, err := legacygeneration.NewSourceResolver(
		application.Runtime.HTTP.LegacyRoutes.MeshyProxyAsset,
		application.Runtime.AssetDelivery.Proxy.API.Query.URL,
	)
	if err != nil {
		return nil, nil, report, fmt.Errorf(
			"创建历史产物来源解析器: %w",
			err,
		)
	}
	projector, err := legacygeneration.NewProjector(
		legacygeneration.ProjectorConfig{
			Capabilities: application.Capabilities,
			Aliases:      application.TaskStream.Aliases,
			ExcludedRoles: application.Runtime.LegacyMigration.
				TaskOutputExcludedRoles,
		},
	)
	if err != nil {
		return nil, nil, report, fmt.Errorf("创建历史产物投影器: %w", err)
	}
	generatedImages, err := legacygenerated.New(
		filepath.Join(legacyStorageRoot, "generated"),
	)
	if err != nil {
		return nil, nil, report, fmt.Errorf("打开旧生成图片目录: %w", err)
	}
	assetSchema := application.Administration.Generations.Assets
	auditRecovery, err := newLegacyAuditAssetRecovery(
		legacyStorageRoot,
		application.Runtime.LegacyMigration.AuditAssetRecovery,
		assetSchema,
	)
	if err != nil {
		return nil, nil, report, fmt.Errorf(
			"打开历史审计副本恢复源: %w",
			err,
		)
	}
	reader, err := legacygeneration.NewReader(
		legacygeneration.ReaderConfig{
			Native: administration.GenerationAssetReaderFunc(func(
				context.Context,
				administration.GenerationAssetRequest,
			) (administration.GenerationAssetContent, error) {
				return administration.GenerationAssetContent{},
					errors.New("ETL 不读取目标库原生产物")
			}),
			GeneratedImages:     generatedImages,
			GeneratedImageRoute: application.Runtime.HTTP.LegacyRoutes.GeneratedImage,
			CacheRoot:           filepath.Join(legacyStorageRoot, "cache"),
			CachePrefixCharacters: assetSchema.
				LegacyCachePrefixCharacters,
			CacheMetadataSuffix: assetSchema.
				LegacyCacheMetadataSuffix,
			CacheContentTypeField: assetSchema.
				LegacyCacheContentTypeField,
			AllowedHosts: legacygeneration.CollectAssetHosts(
				product.List(""),
			),
			DefaultMediaType: assetSchema.DefaultMediaType,
			UnknownExtension: assetSchema.UnknownExtension,
			MaximumBytes:     assetSchema.MaximumPackageBytes,
			RemoteTimeoutMilliseconds: assetSchema.
				RemoteDownloadTimeoutMilliseconds,
			MaximumRedirects: assetSchema.MaximumRemoteRedirects,
			MediaTypeExtensions: assetSchema.
				MediaTypeExtensions,
			AssetDownloads: legacygeneration.CollectAssetDownloads(
				application.Capabilities,
			),
			HTTPClient: &http.Client{Transport: offlineRoundTripper{}},
		},
	)
	if err != nil {
		return nil, nil, report, fmt.Errorf("创建历史产物读取器: %w", err)
	}

	imports := append([]sqlite.GenerationTaskImport(nil), tasks...)
	assets := make([]generation.Asset, 0)
	for index := range imports {
		item := &imports[index]
		currentAttempt := item.CurrentAttempt()
		if currentAttempt == nil {
			return nil, nil, report, fmt.Errorf(
				"任务 %s 缺少历史 Attempt，无法固化产物",
				item.Task.ID,
			)
		}
		if item.Task.State != generation.TaskSucceeded {
			continue
		}
		projected := projector.ProjectLegacyGenerationAssets(
			string(item.Task.Capability),
			currentAttempt.Opaque,
			resultURLs[item.Task.ID],
		)
		if len(projected) == 0 {
			report.declared++
			issues := []string{fmt.Sprintf(
				"%s: 未找到可固化的历史产物声明",
				item.Task.ID,
			)}
			report.unavailable = append(report.unavailable, issues...)
			report.sourceLosses = append(
				report.sourceLosses,
				newTaskOutputSourceLoss(item, issues),
			)
			markLegacyMaterializationFailed(item)
			continue
		}
		report.declared += len(projected)
		roleOrdinals := map[string]int{}
		taskAssetStart := len(assets)
		taskIssues := make([]string, 0)
		for _, projectedAsset := range projected {
			projectedAsset.SourceURL = sourceResolver.Resolve(
				projectedAsset.SourceURL,
			)
			content, openErr := reader.OpenGenerationAsset(
				ctx,
				administration.GenerationAssetRequest{
					OwnerID: string(item.Task.OwnerID),
					TaskID:  string(item.Task.ID),
					Asset:   projectedAsset,
				},
			)
			recoveredFromAudit := false
			if errors.Is(
				openErr,
				legacygeneration.ErrLegacyAssetUnavailable,
			) {
				auditContent, auditErr := auditRecovery.Open(
					ctx,
					string(item.Task.ID),
					projectedAsset,
				)
				if auditErr == nil {
					content = auditContent
					openErr = nil
					recoveredFromAudit = true
				} else if !errors.Is(
					auditErr,
					legacygeneration.ErrLegacyAssetUnavailable,
				) {
					openErr = auditErr
				}
			}
			if openErr != nil {
				taskIssues = append(
					taskIssues,
					fmt.Sprintf(
						"%s/%s[%s]: %s",
						item.Task.ID,
						projectedAsset.Role,
						projectedAsset.Variant,
						describeLegacyAssetReadFailure(openErr),
					),
				)
				continue
			}
			blob, putErr := blobs.PutBlob(
				ctx,
				content.Body,
				content.MediaType,
				assetSchema.MaximumPackageBytes,
			)
			closeErr := content.Body.Close()
			if putErr == nil && closeErr != nil {
				putErr = closeErr
			}
			if putErr != nil {
				taskIssues = append(
					taskIssues,
					fmt.Sprintf(
						"%s/%s[%s]: 写入 CAS: %v",
						item.Task.ID,
						projectedAsset.Role,
						projectedAsset.Variant,
						putErr,
					),
				)
				continue
			}
			roleOrdinals[projectedAsset.Role]++
			createdAt := item.Task.UpdatedAt
			if item.Task.FinishedAt != nil {
				createdAt = *item.Task.FinishedAt
			}
			asset := generation.Asset{
				ID: generation.AssetID(fmt.Sprintf(
					"%s-asset-%d",
					item.Task.ID,
					projectedAsset.Ordinal+1,
				)),
				TaskID:     item.Task.ID,
				AttemptID:  item.CurrentAttemptID(),
				Role:       projectedAsset.Role,
				Variant:    projectedAsset.Variant,
				Required:   false,
				Ordinal:    roleOrdinals[projectedAsset.Role],
				StorageKey: blob.StorageKey,
				MediaType:  blob.MediaType,
				Size:       blob.Size,
				SHA256:     blob.SHA256,
				CreatedAt:  createdAt,
			}
			assets = append(assets, asset)
			report.materialized++
			if recoveredFromAudit {
				report.auditRecovered++
			}
			if len(report.sha256Samples) < 3 {
				report.sha256Samples = append(
					report.sha256Samples,
					string(asset.ID)+"="+asset.SHA256,
				)
			}
		}
		requirementIssues := assignLegacyRequiredOutputs(
			item.Task.Offering.Outputs,
			assets[taskAssetStart:],
		)
		if len(requirementIssues) > 0 {
			var sourceLossIssues []string
			if len(taskIssues) > 0 {
				report.unavailable = append(
					report.unavailable,
					taskIssues...,
				)
				sourceLossIssues = taskIssues
			} else {
				for _, issue := range requirementIssues {
					sourceLossIssues = append(
						sourceLossIssues,
						fmt.Sprintf("%s: %s", item.Task.ID, issue),
					)
				}
				report.unavailable = append(
					report.unavailable,
					sourceLossIssues...,
				)
			}
			report.sourceLosses = append(
				report.sourceLosses,
				newTaskOutputSourceLoss(item, sourceLossIssues),
			)
			markLegacyMaterializationFailed(item)
		} else {
			report.optionalUnavailable = append(
				report.optionalUnavailable,
				taskIssues...,
			)
		}
	}
	return imports, assets, report, nil
}

func newTaskOutputSourceLoss(
	item *sqlite.GenerationTaskImport,
	issues []string,
) taskOutputSourceLoss {
	sortedIssues := append([]string(nil), issues...)
	sort.Strings(sortedIssues)
	identity := []string{
		string(item.Task.ID),
		string(item.Task.Capability),
		string(item.Task.Offering.ID),
		item.Task.Offering.ProviderID,
		item.Task.Offering.ModelID,
		fmt.Sprintf("%d", len(sortedIssues)),
	}
	identity = append(identity, sortedIssues...)
	digest := sha256.Sum256([]byte(strings.Join(identity, "\x00")))
	return taskOutputSourceLoss{
		TaskID:      string(item.Task.ID),
		Capability:  string(item.Task.Capability),
		OfferingID:  string(item.Task.Offering.ID),
		ProviderID:  item.Task.Offering.ProviderID,
		ModelID:     item.Task.Offering.ModelID,
		IssueCount:  len(sortedIssues),
		IssueSHA256: hex.EncodeToString(digest[:]),
	}
}

func (r taskOutputMigrationReport) unacknowledgedIssueCount() int {
	unacknowledged := len(r.unavailable) - r.acknowledgedIssues
	if unacknowledged < 0 {
		return 0
	}
	return unacknowledged
}

func describeLegacyAssetReadFailure(err error) string {
	switch {
	case errors.Is(err, legacygeneration.ErrLegacyAssetUnavailable):
		return "历史缓存中无可用字节"
	case errors.Is(err, legacygeneration.ErrLegacyAssetRejected):
		return "历史产物来源不符合安全策略"
	case errors.Is(err, legacygeneration.ErrLegacyAssetTooLarge):
		return "历史产物超过大小限制"
	case errors.Is(err, errLegacyAuditAssetConflict):
		return "历史审计副本存在内容冲突"
	case errors.Is(err, errLegacyAuditAssetUnreadable):
		return "历史审计副本无法安全读取"
	case errors.Is(err, context.Canceled):
		return "历史产物读取已取消"
	case errors.Is(err, context.DeadlineExceeded):
		return "历史产物读取超时"
	default:
		return "历史产物读取失败"
	}
}

func assignLegacyRequiredOutputs(
	requirements []generation.AssetRequirement,
	assets []generation.Asset,
) []string {
	issues := make([]string, 0)
	for _, requirement := range requirements {
		// background/on-demand 声明（W6 起）不阻断历史固化：按定义是
		// 可选槽位，缺失走 optionalUnavailable，不制造源损失。
		if requirement.Delivery == generation.AssetDeliveryBackground ||
			requirement.Delivery == generation.AssetDeliveryOnDemand {
			continue
		}
		// 旧路径保持 role-only 强制转换语义（nil/<=0 等效 1）；
		// 历史固化不消费 W1 变体声明（§3.4 旧快照等价）。
		minimum := 1
		if requirement.Minimum != nil && *requirement.Minimum > 0 {
			minimum = *requirement.Minimum
		}
		matched := 0
		for index := range assets {
			if assets[index].Role != requirement.Role {
				continue
			}
			if matched < minimum {
				assets[index].Required = true
			}
			matched++
		}
		if matched < minimum {
			issues = append(issues, fmt.Sprintf(
				"必需产物 %q 至少需要 %d 个，实际固化 %d 个",
				requirement.Role,
				minimum,
				matched,
			))
		}
	}
	return issues
}

func loadLegacyTaskResultURLs(
	ctx context.Context,
	legacy legacyQuery,
) (map[generation.TaskID][]string, error) {
	rows, err := legacy.QueryContext(ctx, `
		SELECT CAST(taskId AS TEXT), COALESCE(resultUrls, '')
		FROM usage_ledger
		ORDER BY createdAt, id`)
	if err != nil {
		return nil, fmt.Errorf("读取旧任务 resultUrls: %w", err)
	}
	defer rows.Close()
	result := map[generation.TaskID][]string{}
	seen := map[generation.TaskID]map[string]bool{}
	for rows.Next() {
		var taskID, raw string
		if err := rows.Scan(&taskID, &raw); err != nil {
			return nil, fmt.Errorf("读取旧任务 resultUrls 行: %w", err)
		}
		var urls []string
		if strings.TrimSpace(raw) == "" ||
			json.Unmarshal([]byte(raw), &urls) != nil {
			continue
		}
		id := generation.TaskID(taskID)
		if seen[id] == nil {
			seen[id] = map[string]bool{}
		}
		for _, source := range urls {
			source = strings.TrimSpace(source)
			if source == "" || seen[id][source] {
				continue
			}
			seen[id][source] = true
			result[id] = append(result[id], source)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历旧任务 resultUrls: %w", err)
	}
	return result, nil
}

func markLegacyMaterializationFailed(item *sqlite.GenerationTaskImport) {
	item.Task.State = generation.TaskFailed
	item.Task.Progress = 0
	item.Task.Failure = &generation.Failure{
		Class:      generation.FailureIntegrity,
		Retryable:  false,
		SafeDetail: "历史任务产物不可固化",
	}
	if attempt := item.CurrentAttempt(); attempt != nil {
		attempt.State = generation.AttemptFailed
		attempt.Failure = item.Task.Failure
	}
}

type offlineRoundTripper struct{}

func (offlineRoundTripper) RoundTrip(
	*http.Request,
) (*http.Response, error) {
	return nil, errors.New("ETL 禁止访问远端产物")
}

func openLegacyStorageFile(
	root string,
	storageKey string,
) (*os.File, int64, error) {
	if strings.TrimSpace(storageKey) == "" ||
		strings.ContainsRune(storageKey, '\x00') ||
		filepath.IsAbs(storageKey) {
		return nil, 0, errors.New("storagePath 非法")
	}
	cleanKey := filepath.Clean(filepath.FromSlash(storageKey))
	if cleanKey == "." ||
		cleanKey == ".." ||
		strings.HasPrefix(cleanKey, ".."+string(filepath.Separator)) {
		return nil, 0, errors.New("storagePath 越界")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, 0, fmt.Errorf("解析旧资产根目录: %w", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(
		filepath.Join(resolvedRoot, cleanKey),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("解析旧资产文件: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, 0, errors.New("storagePath 越界")
	}
	body, err := os.Open(resolvedTarget)
	if err != nil {
		return nil, 0, err
	}
	info, err := body.Stat()
	if err != nil {
		_ = body.Close()
		return nil, 0, err
	}
	if info.IsDir() {
		_ = body.Close()
		return nil, 0, errors.New("storagePath 不是文件")
	}
	return body, info.Size(), nil
}
