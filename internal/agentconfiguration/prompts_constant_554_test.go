package agentconfiguration_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #554: the Agent prompts are product constants. Whatever text a stored
// version carries, the system prompt and the domain contexts read as the
// current Schema's, and a release that changes them reaches every version.

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// schemaPrompts is the Schema's prompts as a turn gets them: sections of
// capabilities without an enabled Offering removed. Computed from the Catalog
// directly, not through the service under test.
func schemaPrompts(t *testing.T, f followFixture) agentconfiguration.Configuration {
	t.Helper()
	return stripped(f, f.product.Application().Chat.Agent)
}

func stripped(f followFixture, agent catalog.ChatAgentSchema) agentconfiguration.Configuration {
	active := catalog.ActiveCapabilities(f.product.List(""))
	result := agentconfiguration.Configuration{SystemPrompt: catalog.StripInactivePromptSections(agent.SystemPrompt, agent.PromptSections, active), DomainContext: map[string]string{}}
	for key, text := range agent.DomainContext {
		result.DomainContext[key] = catalog.StripInactivePromptSections(text, agent.PromptSections, active)
	}
	return result
}

func samePrompts(got, want agentconfiguration.Configuration) bool {
	return got.SystemPrompt == want.SystemPrompt && maps.Equal(got.DomainContext, want.DomainContext)
}

// openUpgraded returns a service over the same store whose Schema ships
// different prompts, as a later release would.
func (f followFixture) openUpgraded(t *testing.T) (*agentconfiguration.Service, agentconfiguration.Configuration) {
	t.Helper()
	skills, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	application := f.product.Application()
	application.Chat.Agent.SystemPrompt += "\n- 升级后新增的一条规则。"
	contexts := map[string]string{}
	for key, text := range application.Chat.Agent.DomainContext {
		contexts[key] = text + "（升级后改写）"
	}
	application.Chat.Agent.DomainContext = contexts
	service, err := agentconfiguration.New(f.repo, application, skills, f.product.List(""), agentconfiguration.WithRetiredBuiltInSkills())
	if err != nil {
		t.Fatal(err)
	}
	return service, stripped(f, application.Chat.Agent)
}

// storeRaw appends a stored configuration given as JSON, the way an older
// build wrote it, without going through the service.
func (f followFixture) storeRaw(t *testing.T, configuration map[string]any, source string) agentconfiguration.Version {
	t.Helper()
	ctx := context.Background()
	base, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var stored agentconfiguration.Configuration
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	version, err := f.repo.AppendAgentConfiguration(ctx, base.Revision, agentconfiguration.Version{Snapshot: agentconfiguration.Snapshot{Configuration: stored}, CreatedAt: time.Now().UTC(), ActorID: "admin", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestStoredPromptTextIsIgnoredWhateverTheFormat(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	want := schemaPrompts(t, f)
	cases := map[string]map[string]any{
		// A version published before #525 with a prompt an administrator wrote.
		"format 1 custom": {
			"system_prompt": "完全由管理员重写的提示词。", "domain_context": map[string]string{"image": "自定义图像上下文", "model-3d": "自定义三维上下文"},
			"skills": []any{}, "enabled_tool_names": without(v180Tools, "compare_models"), "enabled_mcp_preset_ids": []string{},
		},
		// The release's own default text of v1.8.0.
		"format 1 old default": {
			"system_prompt": readTestdata(t, "v1.8.0-system-prompt.txt"), "domain_context": map[string]string{"image": readTestdata(t, "v1.8.0-domain-image.txt")},
			"skills": []any{}, "enabled_tool_names": without(v180Tools, "compare_models"), "enabled_mcp_preset_ids": []string{},
		},
		// A #525 version that froze a customized prompt (not in follows_schema),
		// with a domain context key the Schema does not declare.
		"format 2 customized": {
			"format": 2, "follows_schema": []string{"domain_context.image"},
			"system_prompt": "#525 时代冻结的自定义提示词", "domain_context": map[string]string{"image": "旧", "model-3d": "冻结的三维上下文", "retired": "未声明的键"},
			"skills": []any{}, "enabled_tool_names": []string{}, "disabled_tool_names": []string{"compare_models"}, "enabled_mcp_preset_ids": []string{},
		},
		// An empty prompt an older reader would have rejected.
		"format 2 empty": {
			"format": 2, "system_prompt": "", "domain_context": map[string]string{},
			"skills": []any{}, "enabled_tool_names": []string{}, "disabled_tool_names": []string{"compare_models"}, "enabled_mcp_preset_ids": []string{},
		},
	}
	for name, configuration := range cases {
		stored := f.storeRaw(t, configuration, "publish")
		snapshot, err := f.service.Snapshot(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if snapshot.Revision != stored.Revision || !samePrompts(snapshot.Configuration, want) {
			t.Fatalf("%s: Snapshot prompts are not the Schema's: %q", name, snapshot.Configuration.SystemPrompt)
		}
		if slices.Contains(snapshot.Configuration.EnabledToolNames, "compare_models") || !slices.Contains(snapshot.Configuration.EnabledToolNames, "analyze_model") {
			t.Fatalf("%s: the tool choices are not read as before: %v", name, snapshot.Configuration.EnabledToolNames)
		}
		// What a turn freezes.
		owner, err := f.service.SnapshotForOwner(ctx, "owner-a")
		if err != nil || !samePrompts(owner.Configuration, want) {
			t.Fatalf("%s: a turn does not get the Schema prompts: %v", name, err)
		}
		management, err := f.service.Management(ctx)
		if err != nil || !samePrompts(management.Configuration, want) || !management.PromptsReadonly {
			t.Fatalf("%s: management prompts: readonly=%v %v", name, management.PromptsReadonly, err)
		}
		// Reading never rewrites the stored version.
		row, err := f.repo.AgentConfigurationVersion(ctx, stored.Revision)
		if err != nil || row.Configuration.SystemPrompt != configuration["system_prompt"] {
			t.Fatalf("%s: reading rewrote the stored version: %v", name, err)
		}
	}
}

func TestPublishIgnoresSubmittedPromptsAndKeepsToolChoices(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	want := schemaPrompts(t, f)
	current, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	submitted := current.Configuration
	submitted.SystemPrompt = "网页上改写的提示词"
	submitted.DomainContext = map[string]string{"image": "改写的图像上下文", "unknown-domain": "x"}
	submitted.EnabledToolNames = without(submitted.EnabledToolNames, "compare_models")
	if err := f.service.Validate(submitted); err != nil {
		t.Fatalf("a submission carrying prompts must stay valid: %v", err)
	}
	published, err := f.service.Publish(ctx, current.Revision, "admin", "publish", submitted)
	if err != nil {
		t.Fatal(err)
	}
	if !samePrompts(published.Configuration, want) {
		t.Fatalf("Publish returned the submitted prompt: %q", published.Configuration.SystemPrompt)
	}
	read, err := f.service.Snapshot(ctx)
	if err != nil || !samePrompts(read.Configuration, want) || slices.Contains(read.Configuration.EnabledToolNames, "compare_models") || !slices.Contains(read.Configuration.EnabledToolNames, "process_model") {
		t.Fatalf("after publish: %v %v", read.Configuration.EnabledToolNames, err)
	}
	// The stored text is the Schema's, a mirror an older build can read.
	row, err := f.repo.AgentConfigurationVersion(ctx, published.Revision)
	if err != nil || row.Configuration.SystemPrompt != want.SystemPrompt || strings.Contains(row.Configuration.SystemPrompt, "网页上改写") || row.Configuration.DomainContext["unknown-domain"] != "" {
		t.Fatalf("stored prompts: %q %v", row.Configuration.SystemPrompt, err)
	}
	// A client that no longer sends prompts at all can still validate and publish.
	bare := read.Configuration
	bare.SystemPrompt, bare.DomainContext = "", nil
	if err := f.service.Validate(bare); err != nil {
		t.Fatalf("a submission without prompts was rejected: %v", err)
	}
	if _, err := f.service.Publish(ctx, read.Revision, "admin", "publish", bare); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaPromptChangeReachesEveryVersionWithoutARepublish(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	f.storeRaw(t, map[string]any{
		"system_prompt": "旧版本自定义", "domain_context": map[string]string{"image": "旧"},
		"skills": []any{}, "enabled_tool_names": v180Tools, "enabled_mcp_preset_ids": []string{},
	}, "publish")
	before, err := f.service.SnapshotForOwner(ctx, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	upgraded, want := f.openUpgraded(t)
	if samePrompts(before.Configuration, want) {
		t.Fatal("test setup: the upgraded Schema did not change the prompts")
	}
	for _, read := range []func() (agentconfiguration.Configuration, error){
		func() (agentconfiguration.Configuration, error) {
			s, err := upgraded.Snapshot(ctx)
			return s.Configuration, err
		},
		func() (agentconfiguration.Configuration, error) {
			m, err := upgraded.Management(ctx)
			return m.Configuration, err
		},
	} {
		got, err := read()
		if err != nil || !samePrompts(got, want) {
			t.Fatalf("the upgraded prompts did not reach the stored version: %v", err)
		}
	}
	after, err := upgraded.SnapshotForOwner(ctx, "owner-a")
	if err != nil || !samePrompts(after.Configuration, want) || after.Revision == before.Revision {
		t.Fatalf("a turn's revision must change with the prompts: %s %v", after.Revision, err)
	}
	if !strings.HasPrefix(after.Revision, strings.SplitN(before.Revision, ":", 2)[0]+":") {
		t.Fatalf("an upgrade published a version: %s -> %s", before.Revision, after.Revision)
	}
}

func TestRestoreAndResetUseTheSchemaPrompts(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	want := schemaPrompts(t, f)
	old := f.storeRaw(t, map[string]any{
		"system_prompt": "历史版本里的自定义提示词", "domain_context": map[string]string{"model-3d": "历史三维上下文"},
		"skills": []any{}, "enabled_tool_names": without(v180Tools, "inspect_model"), "enabled_mcp_preset_ids": []string{},
	}, "publish")
	latest, err := f.service.Restore(ctx, old.Revision, "0", "admin")
	if err != nil || !samePrompts(latest.Configuration, want) {
		t.Fatalf("reset: %v", err)
	}
	restored, err := f.service.Restore(ctx, latest.Revision, old.Revision, "admin")
	if err != nil || !samePrompts(restored.Configuration, want) || slices.Contains(restored.Configuration.EnabledToolNames, "inspect_model") {
		t.Fatalf("restoring an old version brought back its prompt or lost its tools: %v", err)
	}
}
