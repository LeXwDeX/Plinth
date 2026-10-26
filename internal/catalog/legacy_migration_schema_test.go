package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyMigrationRuntimeIsPrivateAndValidated(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()

	privateRuntime, err := json.Marshal(application.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(privateRuntime), `"legacy_migration"`) ||
		!strings.Contains(string(privateRuntime), `"deploy-check"`) ||
		!strings.Contains(
			string(privateRuntime),
			`"task_output_excluded_roles":["*"]`,
		) ||
		!strings.Contains(
			string(privateRuntime),
			`"source_cache_subdirectory":"legacy-thumbnail-backfill"`,
		) ||
		!strings.Contains(
			string(privateRuntime),
			`"legacy_provider_id":"Tripo/Tripo-H3.1"`,
		) ||
		!strings.Contains(
			string(privateRuntime),
			`"task_id":"019f367b-f7ed-7ccb-9a98-fd37366f5b31"`,
		) ||
		!strings.Contains(
			string(privateRuntime),
			`"{task_id}_{source_stem}{source_extension}"`,
		) {
		t.Fatalf(
			"私有 runtime 缺少历史迁移声明: %s",
			privateRuntime,
		)
	}

	publicRuntime, err := json.Marshal(application.Public().Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicRuntime), `"legacy_migration"`) ||
		strings.Contains(string(publicRuntime), `"deploy-check"`) ||
		strings.Contains(string(publicRuntime), `"audit_asset_recovery"`) ||
		strings.Contains(string(publicRuntime), `"Tripo/Tripo-H3.1"`) ||
		strings.Contains(
			string(publicRuntime),
			`"019f367b-f7ed-7ccb-9a98-fd37366f5b31"`,
		) {
		t.Fatalf(
			"公开 Product Schema 泄露历史迁移声明: %s",
			publicRuntime,
		)
	}

	first := application.Runtime.LegacyMigration.CacheOnlyTaskExclusions
	if len(first) != 1 {
		t.Fatalf("默认迁移排除声明数量=%d want=1", len(first))
	}
	first[0].Reason = "mutated"
	second := taskCatalog.Application().Runtime.LegacyMigration.
		CacheOnlyTaskExclusions
	if len(second) != 1 ||
		second[0].Reason == "mutated" {
		t.Fatalf("历史迁移声明未防御性克隆: %#v", second)
	}
	roles := application.Runtime.LegacyMigration.TaskOutputExcludedRoles
	roles[0] = "mutated"
	if got := taskCatalog.Application().Runtime.LegacyMigration.
		TaskOutputExcludedRoles; len(got) != 1 || got[0] != "*" {
		t.Fatalf("迁移产物排除角色未防御性克隆: %#v", got)
	}
	recovery := application.Runtime.LegacyMigration.AuditAssetRecovery
	if recovery.RootSubdirectory != "audit" ||
		recovery.FilenameTemplate !=
			"{task_id}_{source_stem}{source_extension}" {
		t.Fatalf("默认审计副本恢复声明错误: %#v", recovery)
	}
	backfill := application.Runtime.LegacyMigration.ThumbnailBackfill
	if backfill.SourceCacheSubdirectory != "legacy-thumbnail-backfill" ||
		backfill.BatchSize != 50 ||
		backfill.MaximumAssetBytes != 52428800 ||
		len(backfill.OfferingMappings) != 14 ||
		len(backfill.AcknowledgedSourceLosses) != 14 {
		t.Fatalf("默认截图补迁移声明错误: %#v", backfill)
	}
	backfill.OfferingMappings[0].LegacyProviderID = "mutated"
	secondBackfill := taskCatalog.Application().Runtime.LegacyMigration.
		ThumbnailBackfill
	if len(secondBackfill.OfferingMappings) != 14 ||
		secondBackfill.OfferingMappings[0].LegacyProviderID == "mutated" {
		t.Fatalf("截图 Offering 映射未防御性克隆: %#v", secondBackfill)
	}
	backfill.AcknowledgedSourceLosses[0].Reason = "mutated"
	secondBackfill = taskCatalog.Application().Runtime.LegacyMigration.
		ThumbnailBackfill
	if len(secondBackfill.AcknowledgedSourceLosses) != 14 ||
		secondBackfill.AcknowledgedSourceLosses[0].Reason == "mutated" {
		t.Fatalf("截图来源缺失声明未防御性克隆: %#v", secondBackfill)
	}

	payload, err := json.Marshal(application)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	runtime := document["runtime"].(map[string]any)
	runtime["legacy_migration"] = map[string]any{
		"thumbnail_backfill": map[string]any{
			"source_cache_subdirectory": "legacy-thumbnail-backfill",
			"batch_size":                50,
			"maximum_asset_bytes":       52428800,
		},
		"task_output_excluded_roles": []any{"*"},
		"audit_asset_recovery": map[string]any{
			"root_subdirectory": "audit",
			"filename_template": "{task_id}_{source_stem}{source_extension}",
		},
		"cache_only_task_exclusions": []any{
			map[string]any{
				"task_id":       "deploy-check",
				"external_type": "test",
				"reason":        "",
			},
		},
	}
	invalidPayload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var invalid catalog.ApplicationSchema
	if err := json.Unmarshal(invalidPayload, &invalid); err != nil {
		t.Fatal(err)
	}
	err = catalog.ValidateApplicationSchema(invalid)
	if err == nil || !strings.Contains(err.Error(), "legacy_migration") {
		t.Fatalf("非法迁移排除声明未被拒绝: %v", err)
	}

	runtime["legacy_migration"] = map[string]any{
		"thumbnail_backfill": map[string]any{
			"source_cache_subdirectory": "legacy-thumbnail-backfill",
			"batch_size":                50,
			"maximum_asset_bytes":       52428800,
		},
		"task_output_excluded_roles": []any{"*"},
		"audit_asset_recovery": map[string]any{
			"root_subdirectory": "audit",
			"filename_template": "../{task_id}_{source_stem}{source_extension}",
		},
		"cache_only_task_exclusions": []any{
			map[string]any{
				"task_id":       "deploy-check",
				"external_type": "test",
				"reason":        "test",
			},
		},
	}
	invalidPayload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(invalidPayload, &invalid); err != nil {
		t.Fatal(err)
	}
	err = catalog.ValidateApplicationSchema(invalid)
	if err == nil ||
		!strings.Contains(err.Error(), "audit_asset_recovery") {
		t.Fatalf("非法审计副本恢复声明未被拒绝: %v", err)
	}
}
