// art-ai-etl 是一次性人工执行的迁移命令：
// 把旧 Next.js 生产数据迁入 Go 原生表与内容寻址 Blob。
// 设计与执行约束见 docs/production-etl-design.md、docs/production-migration-runbook.md。
package main

import (
	"os"

	"github.com/LeXwDeX/Plinth/internal/etl"
)

func main() {
	os.Exit(etl.Main(os.Args[1:], os.Stdout, os.Stderr))
}
