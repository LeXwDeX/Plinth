package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestDefaultCatalogOwnsLegacyInputAssetStorageConfiguration(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	settingName := application.Runtime.InputAssets.LegacyStorageRootSetting
	if settingName != "legacy_input_assets_root" {
		t.Fatalf("legacy storage root setting = %q", settingName)
	}
	setting, exists := application.Runtime.Configuration.Settings[settingName]
	if !exists {
		t.Fatalf("runtime setting %q is missing", settingName)
	}
	if setting.Default != "./storage/assets" {
		t.Fatalf("legacy input assets default = %#v", setting.Default)
	}
	if len(setting.Environment) != 2 ||
		setting.Environment[0].Name != "LEGACY_INPUT_ASSETS_ROOT" ||
		setting.Environment[1].Name != "ART_ASSET_ROOT_DIR" {
		t.Fatalf("legacy input assets environment = %#v", setting.Environment)
	}
}

func TestLegacyInputAssetStorageConfigurationRejectsUnknownSetting(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Runtime.InputAssets.LegacyStorageRootSetting = "missing_setting"
	if err := catalog.ValidateApplicationSchema(application); err == nil {
		t.Fatal("expected unknown legacy input asset storage setting to fail")
	}
}
