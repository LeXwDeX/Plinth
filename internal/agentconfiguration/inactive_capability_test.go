package agentconfiguration_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

const (
	creativeLabDesign = "create_creative_lab_design"
	creativeLabBuild  = "build_creative_lab_item"
	creativeLabLine   = "create_creative_lab_design"
	creativeLabScope  = "Creative Lab（照片做钥匙扣"
)

// withCreativeLab returns the enabled Offerings plus the Creative Lab pair
// the default Schema keeps disabled (#100), as a deployment enabling them would.
func withCreativeLab(t *testing.T, product *catalog.Catalog) []schemacontract.Offering {
	t.Helper()
	offerings := product.List("")
	for _, id := range []schemacontract.OfferingID{"meshy/creative-lab/keychain/prototype", "meshy/creative-lab/keychain/build"} {
		offering, found := product.Get(id)
		if !found {
			t.Fatalf("offering %s missing", id)
		}
		offering.Enabled = true
		offerings = append(offerings, offering)
	}
	return offerings
}

func openService(t *testing.T, product *catalog.Catalog, offerings []schemacontract.Offering) (*sqlite.Repository, *agentconfiguration.Service) {
	t.Helper()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentconfiguration.New(repo, product.Application(), skills, offerings)
	if err != nil {
		t.Fatal(err)
	}
	return repo, service
}

func toolNames(tools []agentconfiguration.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func TestInactiveCapabilityToolsAndPromptStayOutOfProjections(t *testing.T) {
	ctx := context.Background()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	repo, service := openService(t, product, product.List(""))

	// Defaults: no Creative Lab Offering is enabled.
	management, err := service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{creativeLabDesign, creativeLabBuild} {
		if slices.Contains(toolNames(management.Tools), name) || slices.Contains(management.Presets.EnabledToolNames, name) || slices.Contains(management.Configuration.EnabledToolNames, name) {
			t.Fatalf("%s is projected without an enabled Offering", name)
		}
		if slices.Contains(toolNames(service.ToolSummaries()), name) {
			t.Fatalf("%s is in the effective tool summaries", name)
		}
	}
	if strings.Contains(management.Configuration.SystemPrompt, creativeLabLine) || strings.Contains(management.Presets.SystemPrompt, creativeLabLine) {
		t.Fatal("system prompt still teaches the Creative Lab path")
	}
	if !slices.Contains(management.Configuration.EnabledToolNames, "create_model_3d") || !strings.Contains(management.Configuration.SystemPrompt, "- 处理已有 3D") {
		t.Fatalf("unrelated tools or prompt text were removed: %v", management.Configuration.EnabledToolNames)
	}

	// A version published before #115 froze the paragraph and both tools.
	_, enabledService := openService(t, product, withCreativeLab(t, product))
	enabledDefaults, err := enabledService.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(enabledDefaults.Configuration.SystemPrompt, creativeLabLine) || !slices.Contains(enabledDefaults.Configuration.EnabledToolNames, creativeLabDesign) || !slices.Contains(enabledDefaults.Configuration.EnabledToolNames, creativeLabBuild) {
		t.Fatal("enabling an Offering must bring the tools and the prompt section back")
	}
	frozen := enabledDefaults.Configuration
	if !strings.Contains(frozen.SystemPrompt, creativeLabLine) {
		t.Fatal("test setup: frozen configuration lacks the paragraph")
	}
	stored, err := repo.AppendAgentConfiguration(ctx, "0", agentconfiguration.Version{Snapshot: agentconfiguration.Snapshot{Configuration: frozen}, CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot.Configuration.SystemPrompt, creativeLabLine) || slices.Contains(snapshot.Configuration.EnabledToolNames, creativeLabDesign) || slices.Contains(snapshot.Configuration.EnabledToolNames, creativeLabBuild) {
		t.Fatal("a frozen version must not project tools or prompt without an enabled Offering")
	}
	if !strings.Contains(snapshot.Configuration.SystemPrompt, "- 处理已有 3D") || !slices.Contains(snapshot.Configuration.EnabledToolNames, "create_model_3d") {
		t.Fatal("projection removed more than the inactive capability")
	}
	// The published version itself stays as it was published.
	original, err := repo.AgentConfigurationVersion(ctx, stored.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(original.Configuration.SystemPrompt, creativeLabLine) || !slices.Contains(original.Configuration.EnabledToolNames, creativeLabDesign) {
		t.Fatal("the stored version was rewritten")
	}
	// The same version is complete again for a service that has an enabled Offering.
	again, err := enabledService.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again.Configuration.SystemPrompt, creativeLabLine) || !slices.Contains(again.Configuration.EnabledToolNames, creativeLabDesign) {
		t.Fatal("re-enabling an Offering must restore the published version's tools and prompt")
	}

	// Validate keeps accepting the declared tool; Publish never stores it.
	if err := service.Validate(frozen); err != nil {
		t.Fatalf("a declared tool must stay valid: %v", err)
	}
	published, err := service.Publish(ctx, stored.Revision, "admin", "publish", frozen)
	if err != nil {
		t.Fatal(err)
	}
	next, err := repo.AgentConfigurationVersion(ctx, published.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(next.Configuration.SystemPrompt, creativeLabLine) || slices.Contains(next.Configuration.EnabledToolNames, creativeLabDesign) || slices.Contains(next.Configuration.EnabledToolNames, creativeLabBuild) {
		t.Fatal("a new publish stored an inactive capability's tools or prompt")
	}
	if effective := service.Effective(frozen); strings.Contains(effective.SystemPrompt, creativeLabLine) {
		t.Fatal("Effective must strip the section")
	}
}

func TestInactivePromptTextIsNotSentToTheModel(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	if !strings.Contains(chat.Agent.SystemPrompt, creativeLabLine) {
		t.Fatal("the Schema keeps the section text")
	}
	stripped := chat.WithoutInactivePromptText(catalog.ActiveCapabilities(product.List("")))
	if strings.Contains(stripped.Agent.SystemPrompt, creativeLabLine) {
		t.Fatal("system prompt still teaches Creative Lab")
	}
	for _, mode := range stripped.AgentScope.Modes {
		if strings.Contains(mode.Description, creativeLabScope) || strings.Contains(mode.Description, "Creative Lab") {
			t.Fatalf("scope %q still mentions Creative Lab", mode.ID)
		}
	}
	// The input schema is not modified.
	if !strings.Contains(chat.Agent.SystemPrompt, creativeLabLine) {
		t.Fatal("WithoutInactivePromptText modified its receiver")
	}
	kept := chat.WithoutInactivePromptText(catalog.ActiveCapabilities(withCreativeLab(t, product)))
	if !strings.Contains(kept.Agent.SystemPrompt, creativeLabLine) {
		t.Fatal("an enabled Offering keeps the section")
	}
	found := false
	for _, mode := range kept.AgentScope.Modes {
		found = found || strings.Contains(mode.Description, creativeLabScope)
	}
	if !found {
		t.Fatal("an enabled Offering keeps the scope text")
	}
}
