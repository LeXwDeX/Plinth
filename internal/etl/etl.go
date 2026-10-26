// Package etl 提供一次性迁移命令：把旧 Next.js 生产数据
// （SQLite 旧表 + storage 文件）转换为 Go 原生表与内容寻址 Blob。
// 该命令只能人工执行，不进任何自动启动钩子。
package etl

import (
	"fmt"
	"io"
)

const usageText = `用法: art-ai-etl <子命令> [参数]

子命令:
  verify-snapshot --evidence <白名单采集证据> --scope-bindings <显式只读树映射>
                  --legacy-database <独立 SQLite 快照> --snapshot-storage <storage>
                  --snapshot-manifest <备份manifest> --inventory <实际inventory>
                  --report <全新JSON> --generated-at-utc <RFC3339>
      核对D1-D4/C1-C6；证据缺失或差异即STOP，不采集现场或批准源损失。

  diagnose-assets --legacy-database <独立只读 SQLite 快照> --storage-root <快照 storage>
                  --report <全新诊断 JSON> --generated-at-utc <RFC3339>
                  [--catalog <Product Schema>] [--protected-root <CAS/目标等只读保护根>]
      只读重放资产解析，所有私有引用默认哈希；不联网、不迁移、不批准源损失。

  precheck --legacy-root <旧项目根目录>
           [--legacy-database <旧 SQLite 数据库路径>]
      校验旧版本项目目录布局、旧表存在性与关键文件可读性。
      校验不通过即拒绝执行（退出码非零），不做任何写入。

  migrate --legacy-root <旧项目根目录> --target-db <Go 库文件>
          [--legacy-database <旧 SQLite 数据库路径>]
          [--target-storage <Go 资产目录>] [--report <报告文件>]
          [--manifest <机器可读 manifest 文件>]
          [--source-loss-template <候选清单>]
          [--source-loss-acknowledgement <已批准清单>]
      预检通过后执行全部业务域迁移并落盘对账报告。
      按稳定 ID 幂等处理，重跑安全，不产生重复数据。
	源损失候选清单默认不放行；已批准清单必须精确匹配旧数据库指纹、
	任务产物或输入资产身份及问题指纹，且不会伪造缺失 Blob 或成功任务。

  map-env --legacy-env <旧生产 .env> --output <Go 生产 env>
          [--catalog <Product Schema 文件>] [--public-base-url <HTTPS URL>]
      按 Product Schema 环境声明映射旧接线，补齐 Go 生产拓扑值，
      原子生成权限 0600 的环境文件；不在日志中回显任何变量值。

  rebind-source-loss-acknowledgement
          --approved <已批准清单> --candidate <最终候选清单>
          --output <最终确认清单>
      仅当最终停写快照的问题集与已批准问题集逐项完全一致时，
      生成绑定最终数据库指纹的新确认清单。

  backfill-thumbnails
      扫描已迁移成功任务；仅对原 Next 明确含 thumbnail_url、
      但 Go 原生资产缺少模型截图的任务执行幂等补迁移。
      只读取/刷新既有 Provider 任务，不重新生成、不产生扣费。

说明:
  --legacy-root 必须显式指定，指向旧 Next.js 版本的项目根目录；
  --legacy-database 可用于显式指定独立挂载的旧 SQLite 文件。
  --target-storage 省略时使用 <target-db 所在目录>/storage-go。
  --report 省略时使用 <target-db>.migration-report.txt。
`

// Main 是 ETL 命令的可测试入口：解析参数并分发子命令，
// 返回进程退出码。测试只断言退出码与输出，不触及内部实现。
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "verify-snapshot":
		return runVerifySnapshot(args[1:], stdout, stderr)
	case "diagnose-assets":
		return runDiagnoseAssets(args[1:], stdout, stderr)
	case "precheck":
		return runPrecheck(args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(args[1:], stdout, stderr)
	case "map-env":
		return runMapEnv(args[1:], stdout, stderr)
	case "rebind-source-loss-acknowledgement":
		return runRebindSourceLossAcknowledgement(
			args[1:],
			stdout,
			stderr,
		)
	case "backfill-thumbnails":
		return runBackfillThumbnails(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知子命令 %q\n\n", args[0])
		fmt.Fprint(stderr, usageText)
		return 2
	}
}
