package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestRuntimeSettingsContractSeparatesWiringFromProviders 验证运行设置
// 契约的三条边界：
//  1. Go 接线名单内的核心设置缺失 → 拒绝加载；
//  2. Provider 家族（连接 + 其设置）整体退役 → 纯 Schema 变更即可加载，
//     不再被代码侧固定清单阻塞；
//  3. 连接仍引用已删除的设置 → 引用校验兜底拒绝。
func TestRuntimeSettingsContractSeparatesWiringFromProviders(t *testing.T) {
	base, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	baseSchema := base.Application()

	mutate := func(t *testing.T, removeSettings []string, dropConnections []string) catalog.ApplicationSchema {
		t.Helper()
		schema := baseSchema
		schema.Runtime.Configuration.Settings = map[string]catalog.RuntimeSettingSchema{}
		for name, setting := range baseSchema.Runtime.Configuration.Settings {
			if slices.Contains(removeSettings, name) {
				continue
			}
			schema.Runtime.Configuration.Settings[name] = setting
		}
		schema.Runtime.ProviderConnections = nil
		for _, connection := range baseSchema.Runtime.ProviderConnections {
			if slices.Contains(dropConnections, connection.ID) {
				continue
			}
			schema.Runtime.ProviderConnections = append(
				schema.Runtime.ProviderConnections, connection,
			)
		}
		return schema
	}

	t.Run("core wiring settings stay mandatory", func(t *testing.T) {
		schema := mutate(t, []string{"address"}, nil)
		err := catalog.ValidateApplicationSchema(schema)
		if err == nil || !strings.Contains(err.Error(), "缺少必需运行设置") {
			t.Fatalf("缺少 address 应拒绝加载, got %v", err)
		}
	})

	t.Run("provider family retires via schema only", func(t *testing.T) {
		schema := mutate(t,
			[]string{"matting_base_url", "matting_api_key"},
			[]string{"matting"},
		)
		if err := catalog.ValidateApplicationSchema(schema); err != nil {
			t.Fatalf("退役 matting 家族后应可加载, got %v", err)
		}
	})

	t.Run("connection referencing removed setting is rejected", func(t *testing.T) {
		schema := mutate(t, []string{"tripo_api_key"}, nil)
		err := catalog.ValidateApplicationSchema(schema)
		if err == nil || !strings.Contains(err.Error(), "tripo_api_key") {
			t.Fatalf("连接引用已删除设置应拒绝加载, got %v", err)
		}
	})
}
