package agentconfiguration_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// v180Tools are the tools the v1.8.0 Schema declared, in the order its default
// configuration listed them. Written out here, not read from the packaged
// baseline, so a mistaken edit of that baseline fails these tests.
var v180Tools = []string{
	"refine_image", "remove_background", "create_model_3d", "create_model_3d_from_image",
	"create_model_3d_from_views", "retexture_model", "process_model", "plan_model_processing",
	"convert_model_format", "create_motion_clip", "create_gaussian_splat", "create_multiview_images",
	"edit_multiview_images", "create_creative_lab_design", "build_creative_lab_item", "create_concept_sketch",
	"set_params", "update_session_notes", "update_production_brief", "ask_choice",
	"propose_production_plan", "revise_processing_plan", "find_history_images", "inspect_image",
	"create_selection_mask", "get_production_step", "list_offerings", "get_processing_plan",
	"list_session_models", "inspect_model", "render_model_views", "compare_models",
	"compare_model_reference", "record_self_check", "get_task", "observe_task",
	"recover_task_result", "request_consent", "create_three_view", "set_visual_anchor", "list_project_assets",
}

// addedAfterV180 are tools declared by the current default Schema that v1.8.0
// did not have.
var addedAfterV180 = []string{"analyze_model", "crop_image_regions", "propose_asset_list", "get_asset_list"}

type followFixture struct {
	repo    *sqlite.Repository
	service *agentconfiguration.Service
	open    func(offerings []schemacontract.Offering) *agentconfiguration.Service
	product *catalog.Catalog
}

func newFollowFixture(t *testing.T) followFixture {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	open := func(offerings []schemacontract.Offering) *agentconfiguration.Service {
		service, err := agentconfiguration.New(repo, product.Application(), skills, offerings, agentconfiguration.WithRetiredBuiltInSkills())
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	return followFixture{repo: repo, service: open(product.List("")), open: open, product: product}
}

// storeV180 writes a version the way a v1.8.0 build stored it: no format
// marker, the enabled list frozen, the default prompt of that release.
func (f followFixture) storeV180(t *testing.T, enabled []string, source string) agentconfiguration.Version {
	t.Helper()
	ctx := context.Background()
	base, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy := base.Configuration
	legacy.EnabledToolNames = slices.Clone(enabled)
	stored, err := f.repo.AppendAgentConfiguration(ctx, base.Revision, agentconfiguration.Version{Snapshot: agentconfiguration.Snapshot{Configuration: legacy}, CreatedAt: time.Now().UTC(), ActorID: "admin", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func without(names []string, drop ...string) []string {
	result := []string{}
	for _, name := range names {
		if !slices.Contains(drop, name) {
			result = append(result, name)
		}
	}
	return result
}

// #525: a configuration published by v1.8.0 froze the tool list, so a tool a
// later release adds never reached it. It now reads as "everything the
// Schema offers except what the administrator turned off".
func TestV180PublishedConfigurationGainsNewToolsAndKeepsAdministratorChoices(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	turnedOff := []string{"create_motion_clip", "compare_models"}
	stored := f.storeV180(t, without(v180Tools, turnedOff...), "publish")

	snapshot, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != stored.Revision {
		t.Fatalf("Snapshot read another version: %s", snapshot.Revision)
	}
	for _, name := range addedAfterV180 {
		if !slices.Contains(snapshot.Configuration.EnabledToolNames, name) {
			t.Errorf("%s added after v1.8.0 did not reach the published configuration", name)
		}
	}
	for _, name := range turnedOff {
		if slices.Contains(snapshot.Configuration.EnabledToolNames, name) {
			t.Errorf("%s was turned off by the administrator and came back", name)
		}
	}
	for _, name := range []string{"observe_task", "request_consent", "submit_asset_list_item"} {
		if slices.Contains(snapshot.Configuration.EnabledToolNames, name) {
			t.Errorf("%s is not offered by the current Schema but is enabled", name)
		}
	}
	if !slices.Contains(snapshot.Configuration.EnabledToolNames, "process_model") {
		t.Fatal("a tool the administrator kept was lost")
	}

	// A turn and the admin page read the same thing through the other entries.
	owner, err := f.service.SnapshotForOwner(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(owner.Configuration.EnabledToolNames, "analyze_model") {
		t.Fatal("SnapshotForOwner (what a turn freezes) lacks analyze_model")
	}
	management, err := f.service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(management.Configuration.EnabledToolNames, snapshot.Configuration.EnabledToolNames) {
		t.Fatal("Management and Snapshot disagree about the enabled tools")
	}
	if management.Configuration.Format != 0 || len(management.Configuration.DisabledToolNames) != 0 {
		t.Fatalf("stored-only fields leaked into the effective configuration: %#v", management.Configuration)
	}
	// Nothing was rewritten and nothing was published on read.
	latest, err := f.repo.LatestAgentConfiguration(ctx)
	if err != nil || latest.Revision != stored.Revision || latest.Configuration.Format != 0 {
		t.Fatalf("reading changed the stored version: %#v %v", latest, err)
	}
	if slices.Contains(latest.Configuration.EnabledToolNames, "analyze_model") {
		t.Fatal("the stored version was rewritten")
	}

	// Publishing what the page shows keeps the administrator's choices exactly.
	published, err := f.service.Publish(ctx, stored.Revision, "admin", "publish", management.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	row, err := f.repo.AgentConfigurationVersion(ctx, published.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if row.Configuration.Format != agentconfiguration.ConfigurationFormat {
		t.Fatalf("a new publish is not stored in the current format: %d", row.Configuration.Format)
	}
	wantOff := slices.Clone(turnedOff)
	gotOff := slices.Clone(row.Configuration.DisabledToolNames)
	slices.Sort(wantOff)
	slices.Sort(gotOff)
	if !slices.Equal(gotOff, wantOff) {
		t.Fatalf("disabled tools = %v, want %v", gotOff, wantOff)
	}
	after, err := f.service.Snapshot(ctx)
	if err != nil || !slices.Equal(after.Configuration.EnabledToolNames, snapshot.Configuration.EnabledToolNames) {
		t.Fatalf("republishing changed the effective tools: %v %v", after.Configuration.EnabledToolNames, err)
	}

	// The administrator now turns analyze_model off: it stays off, while a
	// tool this version has never heard of would still arrive on.
	off := management.Configuration
	off.EnabledToolNames = without(off.EnabledToolNames, "analyze_model")
	next, err := f.service.Publish(ctx, published.Revision, "admin", "publish", off)
	if err != nil {
		t.Fatal(err)
	}
	read, err := f.service.Snapshot(ctx)
	if err != nil || read.Revision != next.Revision || slices.Contains(read.Configuration.EnabledToolNames, "analyze_model") {
		t.Fatalf("a tool turned off after the upgrade came back: %v %v", read.Configuration.EnabledToolNames, err)
	}
}

// Rolling back to a build that does not know the format must not break: the
// stored format 2 version still has an enabled_tool_names an older reader
// accepts, and nothing in it names a tool that build does not declare.
func TestFormatTwoVersionStaysReadableByAnOlderBuild(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	base := f.storeV180(t, v180Tools, "publish")
	snapshot, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := snapshot.Configuration
	config.EnabledToolNames = without(config.EnabledToolNames, "create_motion_clip")
	published, err := f.service.Publish(ctx, base.Revision, "admin", "publish", config)
	if err != nil {
		t.Fatal(err)
	}
	row, err := f.repo.AgentConfigurationVersion(ctx, published.Revision)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(row.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	// The v1.8.0 reader: five fields, unknown JSON ignored, strict names.
	var older struct {
		SystemPrompt        string   `json:"system_prompt"`
		EnabledToolNames    []string `json:"enabled_tool_names"`
		EnabledMCPPresetIDs []string `json:"enabled_mcp_preset_ids"`
	}
	if err := json.Unmarshal(encoded, &older); err != nil {
		t.Fatal(err)
	}
	if older.SystemPrompt == "" {
		t.Fatal("an older build requires a system prompt")
	}
	seen := map[string]bool{}
	for _, name := range older.EnabledToolNames {
		if !slices.Contains(v180Tools, name) || seen[name] {
			t.Fatalf("an older build would report an unknown or duplicate selection %q", name)
		}
		seen[name] = true
	}
	if slices.Contains(older.EnabledToolNames, "create_motion_clip") || !slices.Contains(older.EnabledToolNames, "process_model") {
		t.Fatalf("the compatibility list lost the administrator's choice: %v", older.EnabledToolNames)
	}
	if slices.Contains(older.EnabledToolNames, "analyze_model") {
		t.Fatal("the compatibility list names a tool v1.8.0 does not declare")
	}
	// Reading an older build's own later publish (no format marker) is the
	// legacy path again: turned-off tools stay off, newer tools are on.
	legacy := f.storeV180(t, without(v180Tools, "create_motion_clip", "inspect_model"), "publish")
	read, err := f.service.Snapshot(ctx)
	if err != nil || read.Revision != legacy.Revision || slices.Contains(read.Configuration.EnabledToolNames, "inspect_model") || !slices.Contains(read.Configuration.EnabledToolNames, "analyze_model") {
		t.Fatalf("a version an older build published after a rollback: %v %v", read.Configuration.EnabledToolNames, err)
	}
}

// A tool whose capability has no enabled Offering is not part of the disabled
// list the administrator saved, so enabling the Offering later brings it back.
func TestToolOfAnInactiveCapabilityComesBackWhenTheOfferingIsEnabled(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	snapshot, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{creativeLabDesign, creativeLabBuild} {
		if slices.Contains(snapshot.Configuration.EnabledToolNames, name) {
			t.Fatalf("test setup: %s is offered without an enabled Offering", name)
		}
	}
	config := snapshot.Configuration
	config.EnabledToolNames = without(config.EnabledToolNames, "compare_models")
	if _, err := f.service.Publish(ctx, snapshot.Revision, "admin", "publish", config); err != nil {
		t.Fatal(err)
	}
	enabled := f.open(withCreativeLab(t, f.product))
	read, err := enabled.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{creativeLabDesign, creativeLabBuild} {
		if !slices.Contains(read.Configuration.EnabledToolNames, name) {
			t.Errorf("%s did not come back after its Offering was enabled", name)
		}
	}
	if slices.Contains(read.Configuration.EnabledToolNames, "compare_models") {
		t.Fatal("a tool the administrator turned off came back")
	}
}

// The submitted configuration never carries the stored-only fields.
func TestSubmittedConfigurationRejectsStoredOnlyFields(t *testing.T) {
	f := newFollowFixture(t)
	snapshot, err := f.service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	withFormat := snapshot.Configuration
	withFormat.Format = agentconfiguration.ConfigurationFormat
	withDisabled := snapshot.Configuration
	withDisabled.DisabledToolNames = []string{"create_motion_clip"}
	for name, config := range map[string]agentconfiguration.Configuration{"format": withFormat, "disabled": withDisabled} {
		if err := f.service.Validate(config); err == nil {
			t.Errorf("Validate accepted a submitted %s field", name)
		}
	}
}

// A revision a turn froze must change when an upgrade changes what the same
// stored version resolves to.
func TestSnapshotForOwnerRevisionTracksWhatTheVersionResolvesTo(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	f.storeV180(t, v180Tools, "publish")
	before, err := f.service.SnapshotForOwner(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	enabled := f.open(withCreativeLab(t, f.product))
	after, err := enabled.SnapshotForOwner(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision == after.Revision {
		t.Fatalf("one revision names two different tool sets: %s", before.Revision)
	}
	again, err := f.service.SnapshotForOwner(ctx, "owner-a")
	if err != nil || again.Revision != before.Revision {
		t.Fatalf("the revision is not stable for the same content: %s != %s (%v)", again.Revision, before.Revision, err)
	}
}
