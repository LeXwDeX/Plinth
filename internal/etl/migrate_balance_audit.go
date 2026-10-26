package etl

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type balanceMigrationReport struct {
	source  int64
	loaded  int
	skipped []string
}

func (report balanceMigrationReport) String(migrated int) string {
	var output strings.Builder
	fmt.Fprintf(
		&output,
		"余额快照域: 源 %d 行 / 迁移 %d 行 / 跳过 %d 行\n",
		report.source,
		migrated,
		len(report.skipped),
	)
	for _, issue := range report.skipped {
		fmt.Fprintf(&output, "余额快照跳过: %s\n", issue)
	}
	return output.String()
}

type auditMigrationReport struct {
	logSource    int64
	logLoaded    int
	fileSource   int64
	fileExisting int64
	logSkipped   []string
	fileSkipped  []string
}

func (report auditMigrationReport) String(
	migratedLogs int,
	migratedFiles int64,
) string {
	var output strings.Builder
	fmt.Fprintf(
		&output,
		"审计域: 日志源 %d 行 / 迁移 %d 行 / 跳过 %d 行；"+
			"文件源 %d 个 / 迁移 %d 个 / 重跑跳过 %d 个 / 失败 %d 个\n",
		report.logSource,
		migratedLogs,
		len(report.logSkipped),
		report.fileSource,
		migratedFiles,
		report.fileExisting,
		len(report.fileSkipped),
	)
	for _, issue := range report.logSkipped {
		fmt.Fprintf(&output, "审计日志跳过: %s\n", issue)
	}
	for _, issue := range report.fileSkipped {
		fmt.Fprintf(&output, "审计文件跳过: %s\n", issue)
	}
	return output.String()
}

func loadLegacyBalanceSnapshots(
	legacy legacyQuery,
) ([]administration.BalanceSnapshot, balanceMigrationReport, error) {
	var report balanceMigrationReport
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM balance_snapshots`,
	).Scan(&report.source); err != nil {
		return nil, report, fmt.Errorf(
			"统计旧 balance_snapshots: %w",
			err,
		)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		return nil, report, fmt.Errorf("加载 Product Schema: %w", err)
	}
	accountID := strings.TrimSpace(
		product.Application().Administration.Usage.Balance.AccountID,
	)
	if accountID == "" {
		return nil, report, fmt.Errorf(
			"Product Schema 未声明余额快照 account_id",
		)
	}
	rows, err := legacy.Query(`
		SELECT CAST(id AS TEXT), balance, capturedAt
		FROM balance_snapshots
		ORDER BY capturedAt, id`)
	if err != nil {
		return nil, report, fmt.Errorf("读取旧 balance_snapshots: %w", err)
	}
	defer rows.Close()
	snapshots := make([]administration.BalanceSnapshot, 0, report.source)
	for rows.Next() {
		report.loaded++
		var snapshot administration.BalanceSnapshot
		var capturedValue any
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.Balance,
			&capturedValue,
		); err != nil {
			return nil, report, fmt.Errorf("读取旧余额快照行: %w", err)
		}
		snapshot.AccountID = accountID
		snapshot.CapturedAt, err = parseLegacyTime(capturedValue)
		if err != nil {
			report.skipped = append(
				report.skipped,
				fmt.Sprintf(
					"%s: capturedAt 无法解析: %v",
					snapshot.ID,
					err,
				),
			)
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, report, fmt.Errorf("遍历旧 balance_snapshots: %w", err)
	}
	return snapshots, report, nil
}

func loadLegacyAuditEvents(
	ctx context.Context,
	legacy legacyQuery,
	sourceAuditRoot string,
	targetAuditRoot string,
) ([]administration.AuditEvent, auditMigrationReport, error) {
	var report auditMigrationReport
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM audit_logs`,
	).Scan(&report.logSource); err != nil {
		return nil, report, fmt.Errorf("统计旧 audit_logs: %w", err)
	}
	rows, err := legacy.QueryContext(ctx, `
		SELECT CAST(id AS TEXT), COALESCE(action, ''),
		       COALESCE(userId, ''), COALESCE(userName, ''),
		       COALESCE(resourceType, ''), COALESCE(resourceId, ''),
		       COALESCE(detail, '{}'), COALESCE(ipAddress, ''),
		       COALESCE(userAgent, ''), createdAt
		FROM audit_logs
		ORDER BY createdAt, id`)
	if err != nil {
		return nil, report, fmt.Errorf("读取旧 audit_logs: %w", err)
	}
	defer rows.Close()
	events := make([]administration.AuditEvent, 0, report.logSource)
	for rows.Next() {
		report.logLoaded++
		var event administration.AuditEvent
		var detailJSON string
		var createdValue any
		if err := rows.Scan(
			&event.ID,
			&event.Action,
			&event.ActorID,
			&event.ActorName,
			&event.ResourceType,
			&event.ResourceID,
			&detailJSON,
			&event.IPAddress,
			&event.UserAgent,
			&createdValue,
		); err != nil {
			return nil, report, fmt.Errorf("读取旧审计日志行: %w", err)
		}
		if err := json.Unmarshal([]byte(detailJSON), &event.Detail); err != nil {
			report.logSkipped = append(
				report.logSkipped,
				fmt.Sprintf("%s: detail 无法解析: %v", event.ID, err),
			)
			continue
		}
		rewriteLegacyAuditPaths(
			event.Detail,
			sourceAuditRoot,
			targetAuditRoot,
		)
		event.CreatedAt, err = parseLegacyTime(createdValue)
		if err != nil {
			report.logSkipped = append(
				report.logSkipped,
				fmt.Sprintf(
					"%s: createdAt 无法解析: %v",
					event.ID,
					err,
				),
			)
			continue
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, report, fmt.Errorf("遍历旧 audit_logs: %w", err)
	}
	return events, report, nil
}

func materializeLegacyAuditFiles(
	sourceRoot string,
	targetRoot string,
	report *auditMigrationReport,
) (int64, error) {
	sourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return 0, fmt.Errorf("解析旧审计目录: %w", err)
	}
	targetRoot, err = filepath.Abs(targetRoot)
	if err != nil {
		return 0, fmt.Errorf("解析目标审计目录: %w", err)
	}
	sameRoot := filepath.Clean(sourceRoot) == filepath.Clean(targetRoot)
	if !sameRoot && pathWithin(sourceRoot, targetRoot) {
		return 0, fmt.Errorf(
			"目标审计目录不能位于旧审计目录内部: %s",
			targetRoot,
		)
	}
	var migrated int64
	err = filepath.WalkDir(
		sourceRoot,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			report.fileSource++
			relative, err := filepath.Rel(sourceRoot, path)
			if err != nil || !safeRelativePath(relative) {
				report.fileSkipped = append(
					report.fileSkipped,
					fmt.Sprintf("%s: 路径越界", path),
				)
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				report.fileSkipped = append(
					report.fileSkipped,
					fmt.Sprintf("%s: 不迁移符号链接", relative),
				)
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				report.fileSkipped = append(
					report.fileSkipped,
					fmt.Sprintf("%s: 读取元数据失败: %v", relative, err),
				)
				return nil
			}
			if !info.Mode().IsRegular() {
				report.fileSkipped = append(
					report.fileSkipped,
					fmt.Sprintf("%s: 不是普通文件", relative),
				)
				return nil
			}
			if !sameRoot {
				destination := filepath.Join(targetRoot, relative)
				existing, err := auditFilesEqual(path, destination)
				if err != nil {
					report.fileSkipped = append(
						report.fileSkipped,
						fmt.Sprintf("%s: 比较目标文件: %v", relative, err),
					)
					return nil
				}
				if existing {
					report.fileExisting++
					return nil
				}
				if err := copyAuditFile(path, destination, info.Mode()); err != nil {
					report.fileSkipped = append(
						report.fileSkipped,
						fmt.Sprintf("%s: %v", relative, err),
					)
					return nil
				}
			}
			migrated++
			return nil
		},
	)
	if err != nil {
		return migrated, fmt.Errorf("遍历旧审计目录: %w", err)
	}
	return migrated, nil
}

func auditFilesEqual(source string, destination string) (bool, error) {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	destinationInfo, err := os.Stat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !destinationInfo.Mode().IsRegular() ||
		sourceInfo.Size() != destinationInfo.Size() {
		return false, nil
	}
	if os.SameFile(sourceInfo, destinationInfo) {
		return true, nil
	}
	sourceDigest, err := fileSHA256(source)
	if err != nil {
		return false, err
	}
	destinationDigest, err := fileSHA256(destination)
	if err != nil {
		return false, err
	}
	return sourceDigest == destinationDigest, nil
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return result, err
	}
	copy(result[:], hasher.Sum(nil))
	return result, nil
}

func copyAuditFile(source string, destination string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("创建目标目录: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("打开源文件: %w", err)
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(destination), ".audit-migrate-*")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tempPath := output.Name()
	committed := false
	defer func() {
		_ = output.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	permissions := mode.Perm() & 0o666
	if permissions == 0 {
		permissions = 0o640
	}
	if err := output.Chmod(permissions); err != nil {
		return fmt.Errorf("设置文件权限: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("复制文件: %w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("同步文件: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("提交目标文件: %w", err)
	}
	committed = true
	return nil
}

func rewriteLegacyAuditPaths(
	value any,
	sourceRoot string,
	targetRoot string,
) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if text, ok := nested.(string); ok {
				if rewritten, matched := mapLegacyAuditPath(
					text,
					sourceRoot,
					targetRoot,
				); matched {
					typed[key] = rewritten
					continue
				}
			}
			rewriteLegacyAuditPaths(nested, sourceRoot, targetRoot)
		}
	case []any:
		for index, nested := range typed {
			if text, ok := nested.(string); ok {
				if rewritten, matched := mapLegacyAuditPath(
					text,
					sourceRoot,
					targetRoot,
				); matched {
					typed[index] = rewritten
					continue
				}
			}
			rewriteLegacyAuditPaths(nested, sourceRoot, targetRoot)
		}
	}
}

func mapLegacyAuditPath(
	value string,
	sourceRoot string,
	targetRoot string,
) (string, bool) {
	sourceRoot, sourceErr := filepath.Abs(sourceRoot)
	targetRoot, targetErr := filepath.Abs(targetRoot)
	if sourceErr != nil || targetErr != nil {
		return value, false
	}
	candidate := filepath.Clean(filepath.FromSlash(value))
	if filepath.IsAbs(candidate) {
		relative, err := filepath.Rel(sourceRoot, candidate)
		if err == nil && safeRelativePath(relative) {
			return filepath.Join(targetRoot, relative), true
		}
		return value, false
	}
	slashed := strings.TrimPrefix(filepath.ToSlash(candidate), "./")
	for _, prefix := range []string{"storage/audit/", "audit/"} {
		if strings.HasPrefix(slashed, prefix) {
			relative := filepath.FromSlash(strings.TrimPrefix(slashed, prefix))
			if safeRelativePath(relative) {
				return filepath.Join(targetRoot, relative), true
			}
		}
	}
	return value, false
}

func safeRelativePath(path string) bool {
	cleaned := filepath.Clean(path)
	return cleaned != "." &&
		cleaned != "" &&
		!filepath.IsAbs(cleaned) &&
		cleaned != ".." &&
		!strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func pathWithin(parent string, candidate string) bool {
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && safeRelativePath(relative)
}
