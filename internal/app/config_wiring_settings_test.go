package app

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestRuntimeWiringSettingsDeclaredInDefaultCatalog 守卫 Go 按名消费的
// 设置与默认 Product Schema 的声明一致：接线名单里的每个名字都必须在
// 默认 Catalog 中声明，防止消费面与 Schema 漂移。
func TestRuntimeWiringSettingsDeclaredInDefaultCatalog(t *testing.T) {
	bootstrap, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := bootstrap.Application()
	for _, name := range runtimeWiringSettingNames(schema) {
		if _, exists := schema.Runtime.Configuration.Settings[name]; !exists {
			t.Errorf("接线设置 %q 未在默认 Catalog 声明", name)
		}
	}
}
