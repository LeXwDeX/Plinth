package etl

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// legacyTables 是旧 Next.js 生产库的全部 11 张表
// （真源：prisma/schema.prisma，DDL 见 prisma/baseline.sql）。
var legacyTables = []string{
	"users",
	"asset_registry",
	"usage_ledger",
	"balance_snapshots",
	"chat_sessions",
	"chat_messages",
	"task_cache",
	"task_input_snapshot",
	"audit_logs",
	"canvas_projects",
	"canvas_nodes",
}

// legacyStorageDirs 是旧 storage 根下必须存在的子目录
// （真源：src/lib/storage-paths.ts；data 单独经 prod.db 校验）。
var legacyStorageDirs = []string{
	"assets",
	"generated",
	"cache",
	"audit",
	"thumbnails",
	"task-inputs",
}

// legacyLayout 按旧项目根目录和可选的独立数据库路径描述源布局。
type legacyLayout struct {
	root         string
	databasePath string
	storageRoot  string
}

func resolveLegacyLayout(root string) legacyLayout {
	return resolveLegacyLayoutWithDatabase(root, "")
}

func resolveLegacyLayoutWithDatabase(root, databasePath string) legacyLayout {
	storageRoot := filepath.Join(root, "storage")
	if databasePath == "" {
		databasePath = filepath.Join(storageRoot, "data", "prod.db")
	}
	return legacyLayout{
		root:         root,
		databasePath: databasePath,
		storageRoot:  storageRoot,
	}
}

func runPrecheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("precheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	legacyRoot := flags.String(
		"legacy-root",
		"",
		"旧 Next.js 版本项目根目录（必填）",
	)
	legacyDatabase := flags.String(
		"legacy-database",
		"",
		"旧 SQLite 数据库路径（可选，默认 <旧根>/storage/data/prod.db）",
	)
	if err := flags.Parse(args); err != nil {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	if *legacyRoot == "" {
		fmt.Fprintln(stderr, "缺少必填参数 --legacy-root，不做任何猜测。")
		fmt.Fprint(stderr, usageText)
		return 2
	}
	report, failures := precheck(
		resolveLegacyLayoutWithDatabase(*legacyRoot, *legacyDatabase),
	)
	if len(failures) > 0 {
		reportPrecheckFailures(stderr, failures)
		return 1
	}
	fmt.Fprint(stdout, report)
	return 0
}

// reportPrecheckFailures 输出预检失败明细（precheck 与 migrate 共用）。
func reportPrecheckFailures(stderr io.Writer, failures []string) {
	fmt.Fprintln(stderr, "预检未通过，拒绝执行迁移：")
	for _, failure := range failures {
		fmt.Fprintf(stderr, "  - %s\n", failure)
	}
}

// precheck 只读校验旧项目目录，返回通过时的摘要报告与失败明细。
// 数据库与 storage 两个域各自失败不互相中断，一次输出全部问题；
// 仅当旧库本体打不开时，逐表校验无从谈起，该域降级为单条失败。
func precheck(layout legacyLayout) (report string, failures []string) {
	if info, err := os.Stat(layout.root); err != nil || !info.IsDir() {
		return "", []string{fmt.Sprintf(
			"旧项目根目录不存在或不是目录: %s", layout.root,
		)}
	}
	tableReport, tableFailures := precheckDatabase(layout.databasePath)
	failures = append(failures, tableFailures...)
	storageReport, storageFailures := precheckStorage(layout.storageRoot)
	failures = append(failures, storageFailures...)
	if len(failures) > 0 {
		return "", failures
	}
	report = fmt.Sprintf(
		"预检通过: %s\n\n旧库表行数:\n%s\nstorage 目录:\n%s",
		layout.root,
		tableReport,
		storageReport,
	)
	return report, nil
}

func precheckDatabase(path string) (report string, failures []string) {
	if _, err := os.Stat(path); err != nil {
		return "", []string{fmt.Sprintf("旧库文件不存在: %s", path)}
	}
	// mode=ro 保证预检绝不写入旧库（连 WAL 副产物都不产生）。
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return "", databaseUnreadable("旧库无法打开", err)
	}
	defer func() { _ = db.Close() }()
	existing := map[string]bool{}
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table'`,
	)
	if err != nil {
		return "", databaseUnreadable("旧库不可读", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", databaseUnreadable("旧库不可读", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return "", databaseUnreadable("旧库不可读", err)
	}
	for _, table := range legacyTables {
		if !existing[table] {
			failures = append(failures, fmt.Sprintf(
				"旧库缺少表: %s", table,
			))
			continue
		}
		var count int64
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM "` + table + `"`,
		).Scan(&count); err != nil {
			failures = append(failures, fmt.Sprintf(
				"旧库表 %s 不可读: %v", table, err,
			))
			continue
		}
		report += fmt.Sprintf("  %s: %d 行\n", table, count)
	}
	return report, failures
}

func precheckStorage(storageRoot string) (report string, failures []string) {
	for _, directory := range legacyStorageDirs {
		path := filepath.Join(storageRoot, directory)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			failures = append(failures, fmt.Sprintf(
				"storage 缺少子目录: %s", directory,
			))
			continue
		}
		files, bytes, err := measureDirectory(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf(
				"storage 子目录 %s 不可读: %v", directory, err,
			))
			continue
		}
		report += fmt.Sprintf(
			"  %s: %d 个文件, %d 字节\n", directory, files, bytes,
		)
	}
	return report, failures
}

// readOnlyDSN 构造只读 SQLite URI；路径中的 URI 保留字符须转义，
// 否则含 %、? 或 # 的旧目录路径会被 URI 解析破坏。
func readOnlyDSN(path string) string {
	escaped := strings.NewReplacer(
		"%", "%25",
		"?", "%3F",
		"#", "%23",
	).Replace(filepath.ToSlash(path))
	return "file:" + escaped + "?mode=ro"
}

// databaseUnreadable 生成旧库级失败明细；生产切换场景下 TS 刚停机的
// prod.db 可能带未 checkpoint 的 -wal，只读打开会因无法恢复 WAL 而失败，
// 明细里提示这条排查线索。
func databaseUnreadable(prefix string, err error) []string {
	return []string{fmt.Sprintf(
		"%s: %v（若旧库仍带 -wal 文件，可能需先在旧栈侧执行 "+
			"wal_checkpoint 合并后重试）", prefix, err,
	)}
}

func measureDirectory(root string) (files int64, bytes int64, err error) {
	err = filepath.WalkDir(root, func(
		_ string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}
