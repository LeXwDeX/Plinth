package agentconfiguration_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #445: a fresh configuration does not enable the preset whose tools repeat
// built-in reads, while an administrator's published selection is kept as
// written (no migration) and an older Catalog without the field keeps the
// historical "enable every preset" behavior.
func TestMCPPresetDefaultSelectionAndPublishedChoice(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offers := product.List("")
	repository, service := openService(t, product, offers)
	ctx := context.Background()
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Configuration.EnabledMCPPresetIDs) != 0 {
		t.Fatalf("default configuration enables MCP presets: %v", snapshot.Configuration.EnabledMCPPresetIDs)
	}
	managed, err := service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed.MCPPresets) != 1 || managed.MCPPresets[0].ID != "workspace-read" || managed.MCPPresets[0].DefaultEnabled {
		t.Fatalf("management default selection = %#v", managed.MCPPresets)
	}

	snapshot.Configuration.EnabledMCPPresetIDs = []string{"workspace-read"}
	published, err := service.Publish(ctx, snapshot.Revision, "test-admin", "publish", snapshot.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := agentconfiguration.New(repository, product.Application(), skills, offers)
	if err != nil {
		t.Fatal(err)
	}
	read, err := restarted.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if read.Revision != published.Revision || !slices.Contains(read.Configuration.EnabledMCPPresetIDs, "workspace-read") {
		t.Fatalf("published preset selection was not kept: revision %q selection %v", read.Revision, read.Configuration.EnabledMCPPresetIDs)
	}
}

// An external Catalog written before #445 does not declare the field; every
// declared preset stays enabled there, as it was.
func TestMCPPresetWithoutDefaultFieldStaysEnabled(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	if len(application.AgentConfiguration.MCPPresets) == 0 {
		t.Fatal("default catalog declares no MCP preset")
	}
	application.AgentConfiguration.MCPPresets[0].DefaultEnabled = nil
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentconfiguration.New(repository, application, skills, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(snapshot.Configuration.EnabledMCPPresetIDs, "workspace-read") {
		t.Fatalf("legacy preset without the field is no longer enabled: %v", snapshot.Configuration.EnabledMCPPresetIDs)
	}
}
