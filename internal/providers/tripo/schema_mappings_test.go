package tripo_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// declaredTripoMappings 从默认 Product Schema 读取 tripo 连接声明的
// 错误/状态词表。测试通过声明（而非代码内置表）驱动适配器，保证
// Schema 是执行数据唯一真源。
func declaredTripoMappings(
	t *testing.T,
) (*generation.ErrorMapping, *generation.StatusMapping) {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range taskCatalog.Application().Runtime.
		ProviderConnections {
		if connection.AdapterKey == "tripo.v3" {
			return connection.ErrorMapping, connection.StatusMapping
		}
	}
	t.Fatal("默认 Catalog 缺少 tripo.v3 Provider 连接声明")
	return nil, nil
}
