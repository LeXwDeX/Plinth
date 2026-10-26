package meshy_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// newMeshyAdapter 从默认 Product Schema 读取 meshy 连接声明的错误/状态
// 词表并构造适配器。测试通过声明（而非代码内置表）驱动适配器，保证
// Schema 是执行数据唯一真源。
func newMeshyAdapter(t *testing.T, config meshy.Config) *meshy.Adapter {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range taskCatalog.Application().Runtime.
		ProviderConnections {
		if connection.AdapterKey != "meshy.v2" {
			continue
		}
		config.ErrorMapping = connection.ErrorMapping
		config.StatusMapping = connection.StatusMapping
		adapter, err := meshy.New(config)
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	}
	t.Fatal("默认 Catalog 缺少 meshy.v2 Provider 连接声明")
	return nil
}
