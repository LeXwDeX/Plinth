package etl

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/LeXwDeX/Plinth/internal/app"
)

func runBackfillThumbnails(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "backfill-thumbnails 不接受参数")
		return 2
	}
	config, err := app.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "读取 Go 运行配置失败: %v\n", err)
		return 1
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	application, err := app.New(config, logger)
	if err != nil {
		fmt.Fprintf(stderr, "初始化 Go 应用失败: %v\n", err)
		return 1
	}
	report, runErr := application.BackfillLegacyThumbnails(
		context.Background(),
	)
	closeErr := application.Close()
	fmt.Fprintf(
		stdout,
		"历史模型截图补迁移: 扫描 %d / 原 Next 声明 %d / "+
			"新增 %d / 已存在 %d / 原站已缺失 %d / 异常未迁移 %d\n",
		report.Scanned,
		report.Eligible,
		report.Migrated,
		report.AlreadyPresent,
		report.AcknowledgedUnavailable,
		report.Unavailable,
	)
	for _, acknowledgement := range report.Acknowledgements {
		fmt.Fprintf(
			stdout,
			"- 已确认原站缺失 task=%s: %s\n",
			acknowledgement.TaskID,
			acknowledgement.Detail,
		)
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(
			stdout,
			"- 异常未迁移 task=%s: %s\n",
			issue.TaskID,
			issue.Detail,
		)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "补迁移执行失败: %v\n", runErr)
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "关闭 Go 应用失败: %v\n", closeErr)
		return 1
	}
	if report.Unavailable != 0 {
		fmt.Fprintln(
			stderr,
			"仍有原 Next 已声明的模型截图未迁移，拒绝报告成功",
		)
		return 1
	}
	return 0
}
