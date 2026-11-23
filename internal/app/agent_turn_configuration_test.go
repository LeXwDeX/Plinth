package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #445: the model's tool table is where the duplicate reads showed up. A fresh
// configuration must offer one tool per read query: the built-in one, with the
// MCP preset that declares the equivalence left out. An administrator who
// selects the preset explicitly still gets its tools, with no migration of the
// published configuration.
func TestAgentTurnConfigurationDoesNotRepeatBuiltInReadsByDefault(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentconfiguration.New(repository, product.Application(), skills, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	// Construction-only collaborators: this test inspects the compiled tool
	// table, never executes a preset tool.
	mcpService, err := buildWorkspaceMCP(product, &generation.Module{}, &chat.Service{})
	if err != nil {
		t.Fatal(err)
	}
	names := func() map[string]bool {
		frozen, err := agentTurnConfiguration(context.Background(), "owner-a", service, product.Application(), mcpService)
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[string]bool, len(frozen.AgentTools))
		for _, tool := range frozen.AgentTools {
			result[tool.Name] = true
		}
		return result
	}
	defaults := names()
	for _, pair := range [][2]string{
		{"list_offerings", "mcp_list_offerings"},
		{"get_task", "mcp_get_task_assets"},
	} {
		if !defaults[pair[0]] {
			t.Fatalf("default tool table lost built-in read %s: %v", pair[0], defaults)
		}
		if defaults[pair[1]] {
			t.Fatalf("default tool table repeats %s with %s: %v", pair[0], pair[1], defaults)
		}
	}
	for _, presetTool := range []string{"mcp_list_offerings", "mcp_list_session_tasks", "mcp_get_task_assets"} {
		if defaults[presetTool] {
			t.Fatalf("default tool table enables %s from a preset that is off by default", presetTool)
		}
	}

	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Configuration.EnabledMCPPresetIDs = []string{"workspace-read"}
	if _, err := service.Publish(context.Background(), snapshot.Revision, "test-admin", "publish", snapshot.Configuration); err != nil {
		t.Fatal(err)
	}
	enabled := names()
	for _, presetTool := range []string{"mcp_list_offerings", "mcp_list_session_tasks", "mcp_get_task_assets"} {
		if !enabled[presetTool] {
			t.Fatalf("explicitly published preset tool %s absent from the tool table: %v", presetTool, enabled)
		}
	}
}

// #494: the production-list tools are declared in the shipped Catalog but the
// execution section that could advance a list is off, so the default tool table
// must not offer them; turning execution on puts them back with no other change.
func TestAgentTurnConfigurationOffersAssetListToolsOnlyWithExecution(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	off := product.Application()
	if off.AssetListExecutionEnabled() {
		t.Fatal("the shipped Catalog already enables asset_list.execution")
	}
	on := product.Application()
	on.AssetList.Execution.Enabled = true
	if !on.AssetListExecutionEnabled() {
		t.Fatal("the switched execution section does not read as enabled")
	}
	mcpService, err := buildWorkspaceMCP(product, &generation.Module{}, &chat.Service{})
	if err != nil {
		t.Fatal(err)
	}
	names := func(t *testing.T, application catalog.ApplicationSchema) map[string]bool {
		t.Helper()
		repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer repository.Close()
		service, err := agentconfiguration.New(repository, application, skills, product.List(""))
		if err != nil {
			t.Fatal(err)
		}
		frozen, err := agentTurnConfiguration(context.Background(), "owner-a", service, application, mcpService)
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[string]bool, len(frozen.AgentTools))
		for _, tool := range frozen.AgentTools {
			result[tool.Name] = true
		}
		return result
	}
	declared := []string{"propose_asset_list", "get_asset_list", "submit_asset_list_item"}
	for _, name := range declared {
		if names(t, off)[name] {
			t.Fatalf("the default tool table still offers %s while asset_list.execution is off", name)
		}
		if !names(t, on)[name] {
			t.Fatalf("the tool table lost %s although asset_list.execution is on", name)
		}
	}
	// An unrelated Agent tool is untouched by the gate, in both directions.
	for _, application := range []catalog.ApplicationSchema{off, on} {
		if !names(t, application)["get_task"] {
			t.Fatal("the gate removed an unrelated Agent tool")
		}
	}
}
