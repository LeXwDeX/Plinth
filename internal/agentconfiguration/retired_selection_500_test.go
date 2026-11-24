package agentconfiguration_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// retiredToolNames were declared Agent tools until #491 removed them from the
// Catalog. Every deployment that published an Agent configuration before then
// stored both names in enabled_tool_names.
var retiredToolNames = []string{"observe_task", "request_consent"}

const retiredPresetID = "retired-preset-500"

// #500: a stored version keeps names the current Schema no longer declares.
// Reading it (Snapshot, Management, Restore, startup reconcile) drops them
// instead of failing; a new submission with such a name is still rejected.
func TestStoredConfigurationWithRetiredSelectionsStaysReadable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range product.Application().Chat.AgentTools {
		if slices.Contains(retiredToolNames, tool.Name) {
			t.Fatalf("test setup: %s is declared again", tool.Name)
		}
	}
	repo, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	open := func() *agentconfiguration.Service {
		service, err := agentconfiguration.New(repo, product.Application(), skills, product.List(""), agentconfiguration.WithRetiredBuiltInSkills())
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	service := open()
	base, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy := base.Configuration
	legacy.EnabledToolNames = append(append([]string{}, legacy.EnabledToolNames...), retiredToolNames...)
	legacy.EnabledMCPPresetIDs = append(append([]string{}, legacy.EnabledMCPPresetIDs...), retiredPresetID)

	// A new submission keeps strict validation.
	if err := service.Validate(legacy); err == nil {
		t.Fatal("Validate accepted a tool name the Schema does not declare")
	}
	if _, err := service.Publish(ctx, base.Revision, "admin", "publish", legacy); err == nil {
		t.Fatal("Publish accepted a tool name the Schema does not declare")
	}

	// The version an older build published, written as it was stored.
	stored, err := repo.AppendAgentConfiguration(ctx, base.Revision, agentconfiguration.Version{Snapshot: agentconfiguration.Snapshot{Configuration: legacy}, CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	assertClean := func(label string, c agentconfiguration.Configuration) {
		t.Helper()
		for _, name := range retiredToolNames {
			if slices.Contains(c.EnabledToolNames, name) {
				t.Fatalf("%s still enables retired tool %s: %v", label, name, c.EnabledToolNames)
			}
		}
		if slices.Contains(c.EnabledMCPPresetIDs, retiredPresetID) {
			t.Fatalf("%s still enables retired preset: %v", label, c.EnabledMCPPresetIDs)
		}
		if !slices.Equal(c.EnabledToolNames, base.Configuration.EnabledToolNames) {
			t.Fatalf("%s dropped more than the retired tools: %v", label, c.EnabledToolNames)
		}
	}

	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot of a stored version with retired tools: %v", err)
	}
	if snapshot.Revision != stored.Revision {
		t.Fatalf("Snapshot read another version: %s != %s", snapshot.Revision, stored.Revision)
	}
	assertClean("Snapshot", snapshot.Configuration)
	if _, err := service.SnapshotForOwner(ctx, "owner-a"); err != nil {
		t.Fatalf("SnapshotForOwner: %v", err)
	}
	management, err := service.Management(ctx)
	if err != nil {
		t.Fatalf("Management of a stored version with retired tools: %v", err)
	}
	assertClean("Management", management.Configuration)

	// Startup publishes one cleaned version and leaves the stored one intact.
	if err := service.ReconcileRetiredBuiltInSkills(ctx); err != nil {
		t.Fatalf("startup reconcile: %v", err)
	}
	reconciled, err := repo.LatestAgentConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Revision == stored.Revision {
		t.Fatal("startup reconcile did not publish a cleaned version")
	}
	assertClean("reconciled version", reconciled.Configuration)
	original, err := repo.AgentConfigurationVersion(ctx, stored.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(original.Configuration.EnabledToolNames, retiredToolNames[0]) {
		t.Fatal("the stored version was rewritten")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service = open()
	if err := service.ReconcileRetiredBuiltInSkills(ctx); err != nil {
		t.Fatalf("startup reconcile after restart: %v", err)
	}
	again, err := repo.LatestAgentConfiguration(ctx)
	if err != nil || again.Revision != reconciled.Revision {
		t.Fatalf("startup reconcile was not idempotent: %#v %v", again.Revision, err)
	}

	// Restoring the old version publishes it without the retired names.
	restored, err := service.Restore(ctx, reconciled.Revision, stored.Revision, "admin")
	if err != nil {
		t.Fatalf("Restore of a version with retired tools: %v", err)
	}
	assertClean("restored version", restored.Configuration)
}
