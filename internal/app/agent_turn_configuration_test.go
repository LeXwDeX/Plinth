package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
// must not offer the completion tool; proposing and reading a list need no run
// and stay. Turning execution on puts the completion tool back with no other
// change.
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
	// Only the completion tool needs a run: it is the one the default table
	// must not offer while asset_list.execution is off.
	if names(t, off)["submit_asset_list_item"] {
		t.Fatal("the default tool table still offers submit_asset_list_item while asset_list.execution is off")
	}
	// Proposing and reading a list need no run, so the default table keeps them.
	for _, name := range []string{"propose_asset_list", "get_asset_list"} {
		if !names(t, off)[name] {
			t.Fatalf("the default tool table lost %s while asset_list.execution is off", name)
		}
	}
	// Turning execution on brings the completion tool back with no other change.
	enabled := names(t, on)
	for _, name := range []string{"propose_asset_list", "get_asset_list", "submit_asset_list_item"} {
		if !enabled[name] {
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

// #500: a configuration published before #491 still enables observe_task and
// request_consent. A turn must still start, and its tool table must not offer
// the two names the Catalog no longer declares.
func TestAgentTurnConfigurationSkipsToolsTheCatalogRetired(t *testing.T) {
	ctx := context.Background()
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
	service, err := agentconfiguration.New(repository, product.Application(), skills, product.List(""), agentconfiguration.WithRetiredBuiltInSkills())
	if err != nil {
		t.Fatal(err)
	}
	mcpService, err := buildWorkspaceMCP(product, &generation.Module{}, &chat.Service{})
	if err != nil {
		t.Fatal(err)
	}
	base, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy := base.Configuration
	legacy.EnabledToolNames = append(append([]string{}, legacy.EnabledToolNames...), "observe_task", "request_consent")
	if _, err := repository.AppendAgentConfiguration(ctx, base.Revision, agentconfiguration.Version{Snapshot: agentconfiguration.Snapshot{Configuration: legacy}, CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"}); err != nil {
		t.Fatal(err)
	}
	check := func(label string) {
		t.Helper()
		frozen, err := agentTurnConfiguration(ctx, "owner-a", service, product.Application(), mcpService)
		if err != nil {
			t.Fatalf("%s: turn configuration failed: %v", label, err)
		}
		names := map[string]bool{}
		for _, tool := range frozen.AgentTools {
			names[tool.Name] = true
		}
		if names["observe_task"] || names["request_consent"] {
			t.Fatalf("%s: tool table offers a retired tool: %v", label, names)
		}
		if !names["get_task"] {
			t.Fatalf("%s: tool table lost a declared tool: %v", label, names)
		}
	}
	check("stored version")
	if err := service.ReconcileRetiredBuiltInSkills(ctx); err != nil {
		t.Fatalf("startup reconcile: %v", err)
	}
	check("reconciled version")
}

// #525: a configuration a v1.8.0 build published froze the tool list. A turn
// must offer the tools added since (analyze_model, which process_model's
// description points the Agent at) and still leave out what the administrator
// turned off.
func TestAgentTurnConfigurationOffersToolsAddedAfterThePublishedVersion(t *testing.T) {
	ctx := context.Background()
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
	service, err := agentconfiguration.New(repository, product.Application(), skills, product.List(""), agentconfiguration.WithRetiredBuiltInSkills())
	if err != nil {
		t.Fatal(err)
	}
	mcpService, err := buildWorkspaceMCP(product, &generation.Module{}, &chat.Service{})
	if err != nil {
		t.Fatal(err)
	}
	base, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// What the v1.8.0 default configuration enabled, minus two tools an
	// administrator turned off, and plus the two it still declared.
	added := map[string]bool{"analyze_model": true, "crop_image_regions": true, "propose_asset_list": true, "get_asset_list": true}
	legacy := base.Configuration
	legacy.EnabledToolNames = []string{"observe_task", "request_consent"}
	for _, name := range base.Configuration.EnabledToolNames {
		if !added[name] && name != "create_motion_clip" && name != "compare_models" {
			legacy.EnabledToolNames = append(legacy.EnabledToolNames, name)
		}
	}
	if _, err := repository.AppendAgentConfiguration(ctx, base.Revision, agentconfiguration.Version{Snapshot: agentconfiguration.Snapshot{Configuration: legacy}, CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"}); err != nil {
		t.Fatal(err)
	}
	frozen, err := agentTurnConfiguration(ctx, "owner-a", service, product.Application(), mcpService)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range frozen.Tools {
		names[tool.Name] = true
	}
	for _, tool := range frozen.AgentTools {
		names[tool.Name] = true
	}
	for name := range added {
		if !names[name] {
			t.Errorf("tool table lacks %s, added after the published version", name)
		}
	}
	for _, name := range []string{"create_motion_clip", "compare_models", "observe_task", "request_consent"} {
		if names[name] {
			t.Errorf("tool table offers %s", name)
		}
	}
	if !names["process_model"] {
		t.Fatal("tool table lost a tool the administrator kept")
	}
}
