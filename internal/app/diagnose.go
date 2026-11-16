package app

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/diagbundle"
	"github.com/LeXwDeX/Plinth/internal/logging"
)

// `art-ai diagnose` (#234): export a diagnostics bundle (see internal/diagbundle)
// from a running or stopped stack. It reads the same runtime settings the
// service reads, but it does not validate them the way a start does: a stack
// that refuses to start over one bad setting is exactly the one that needs a
// bundle, so a setting that cannot be read is noted in the manifest and the
// export goes on with what it has.

// Exit codes of `art-ai diagnose`.
const (
	DiagnoseExitOK      = 0
	DiagnoseExitFailed  = 1
	DiagnoseExitUsage   = 2
	DiagnoseExitRefused = 3 // the privacy scan found something; no file was written
)

// diagnoseClock is the export's clock; tests replace it.
var diagnoseClock = time.Now

// RunDiagnose runs the subcommand with the process environment given as lookup
// and returns the exit code. Nothing is written but the one bundle file, and
// only when the whole bundle passed the privacy scan.
func RunDiagnose(ctx context.Context, args []string, lookup func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("art-ai diagnose", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "", "输出文件（必填）；写成已存在的目录则在其中按默认名生成 plinth-diagnose-<时间>.tar.gz，已存在的文件不会被覆盖")
	since := flags.String("since", diagbundle.DefaultSince, "时间范围：回溯多久（24h、90m、7d），或 RFC 3339 时间点")
	maxBytes := flags.String("max-bytes", diagbundle.DefaultMaxBytesText, "包内全部内容（未压缩）的体积上限，如 32m、512k；超出时按类别从最旧的开始截断，并记入清单")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法：art-ai diagnose --out <文件> [--since 24h] [--max-bytes 32m]")
		fmt.Fprintln(stderr, "导出诊断包：运行版本与 Catalog、近期日志、脱敏的上游录制、失败任务摘要、运行设置名称。只读运行，不含密钥、提示词、媒体与用户标识。")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "退出码：0 成功；1 失败；2 参数错误；3 隐私扫描命中（未写出任何文件）")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return DiagnoseExitOK
		}
		return DiagnoseExitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "不接受位置参数")
		flags.Usage()
		return DiagnoseExitUsage
	}
	now := diagnoseClock()
	rangeStart, err := diagbundle.ParseSince(*since, now)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return DiagnoseExitUsage
	}
	limit, err := diagbundle.ParseByteSize(*maxBytes)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return DiagnoseExitUsage
	}
	if *out == "" {
		fmt.Fprintln(stderr, "需要用 --out 指定输出文件")
		flags.Usage()
		return DiagnoseExitUsage
	}
	sources, err := diagnoseSources(lookup)
	if err != nil {
		fmt.Fprintln(stderr, "无法读取运行配置:", err)
		return DiagnoseExitFailed
	}
	bundle, written, err := diagbundle.Export(ctx, sources, diagbundle.Options{Since: rangeStart, MaxBytes: limit, Now: diagnoseClock}, *out)
	var refused *diagbundle.RefusedError
	switch {
	case errors.As(err, &refused):
		fmt.Fprintln(stderr, refused)
		fmt.Fprintln(stderr, "没有写出任何文件。先处理上面列出的文件（日志之外的内容不会被改写），或缩小 --since 后重试。")
		return DiagnoseExitRefused
	case err != nil:
		fmt.Fprintln(stderr, "导出失败:", err)
		return DiagnoseExitFailed
	}
	printDiagnoseSummary(stdout, bundle, written)
	return DiagnoseExitOK
}

func printDiagnoseSummary(out io.Writer, bundle *diagbundle.Bundle, written string) {
	size := int64(0)
	if info, err := os.Stat(written); err == nil {
		size = info.Size()
	}
	manifest := bundle.Manifest
	fmt.Fprintf(out, "诊断包已写出：%s（%d 字节，权限 0600）\n", written, size)
	fmt.Fprintf(out, "时间范围：%s 至 %s；版本 %s\n", manifest.Range.Since, manifest.Range.Until, manifest.Revision)
	labels := map[string]string{
		diagbundle.CategoryRuntime: "版本与 Catalog", diagbundle.CategorySettings: "运行设置名称", diagbundle.CategoryFailures: "失败任务摘要",
		diagbundle.CategoryLogs: "日志行", diagbundle.CategoryCaptures: "上游脱敏录制",
	}
	reasons := map[string]string{
		diagbundle.ReasonNotConfigured: "未配置", diagbundle.ReasonNotFound: "不存在", diagbundle.ReasonUnreadable: "无法读取",
	}
	for _, report := range manifest.Categories {
		line := fmt.Sprintf("  %-9s %s：%d 条", report.Category, labels[report.Category], report.Entries)
		switch {
		case report.Status == diagbundle.StatusUnavailable:
			line = fmt.Sprintf("  %-9s %s：不可用（%s，%s）", report.Category, labels[report.Category], reasons[report.Reason], report.Reason)
		case report.Truncated:
			line += fmt.Sprintf("（时间范围内共 %d 条，超出体积上限，已从最旧的开始截断）", report.Available)
		}
		if dropped := droppedTotal(report.Dropped); dropped > 0 {
			line += fmt.Sprintf("，另有 %d 条因无法安全放入而丢弃（见 manifest.json）", dropped)
		}
		fmt.Fprintln(out, line)
	}
	for _, warning := range manifest.Warnings {
		fmt.Fprintln(out, "  注意:", warning)
	}
}

func droppedTotal(dropped map[string]int) int {
	total := 0
	for _, count := range dropped {
		total += count
	}
	return total
}

// diagnoseSources resolves where the stack keeps what the bundle collects, from
// the Catalog the process is configured with and the environment.
func diagnoseSources(lookup func(string) string) (diagbundle.Sources, error) {
	bootstrap, err := catalog.LoadDefault()
	if err != nil {
		return diagbundle.Sources{}, err
	}
	var warnings []string
	read := func(schema catalog.ApplicationSchema, name, environment string) string {
		value, err := runtimeSettingValue(schema, name, environment, lookup)
		if err != nil {
			warnings = append(warnings, "setting_unreadable:"+name)
			return ""
		}
		return value
	}
	schema := bootstrap.Application()
	environment := read(schema, "application_environment", "")
	catalogFile := read(schema, "catalog_file", environment)
	document := catalog.DefaultDocument()
	if catalogFile != "" {
		// The Catalog the stack runs is the file, not the built-in one: its
		// digest and its settings are what the bundle must describe.
		raw, err := os.ReadFile(catalogFile)
		switch {
		case err != nil:
			warnings = append(warnings, "catalog_file_unreadable")
			document = nil
		default:
			document = raw
			if loaded, err := catalog.Load(bytes.NewReader(raw)); err != nil {
				warnings = append(warnings, "catalog_file_invalid")
			} else {
				schema = loaded.Application()
			}
		}
	}
	settings, retired := diagbundle.SettingsFromCatalog(schema)
	// Read first, then build: the warnings are collected by the reads.
	logDirectory := read(schema, "log_directory", environment)
	captureDirectory := read(schema, "upstream_capture_directory", environment)
	databasePath := read(schema, "database_path", environment)
	sources := diagbundle.Sources{
		Revision: runtimeRevision(), CatalogRevision: schema.Revision, Environment: environment,
		LogDirectory: logDirectory, LogFileName: logging.FileName, CaptureDirectory: captureDirectory, DatabasePath: databasePath,
		Settings: settings, RetiredEnvironment: retired, Lookup: lookup, Warnings: warnings,
	}
	if document != nil {
		sources.CatalogSHA256 = diagbundle.DocumentSHA256(document)
	}
	return sources, nil
}
