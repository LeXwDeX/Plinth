package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// publishedConfigurationFixture is an Orchestrator whose own registry carries
// the runtime sources the server injects (tool availability, Offering
// availability, Offering knowledge), plus the context of a turn that runs on a
// published Agent configuration: the snapshot freezes the tool declarations,
// but never the runtime Offerings (#379). The returned map is the live Offering
// list; editing it changes what the sources report on the next call.
func publishedConfigurationFixture(t *testing.T) (*Orchestrator, context.Context, map[string][]generation.Offering) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	registry, err := compileToolRegistry(chatSchema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	offering := func(provider, model string, enabled bool, strengths, weaknesses []string) generation.Offering {
		return generation.Offering{
			ID: generation.OfferingID(provider + "/" + model + "/text-to-3d"), ProviderID: provider,
			ModelID: model, ModelName: model, Capability: schema.TextTo3D, Enabled: enabled,
			Parameters: []schema.Parameter{{Name: "prompt", Semantic: schema.SemanticPrompt}},
			Knowledge:  &schema.OfferingKnowledge{Strengths: strengths, Weaknesses: weaknesses},
		}
	}
	offerings := map[string][]generation.Offering{
		string(schema.TextTo3D): {
			offering("tripo", "tripo-p2-text", true, []string{"low poly"}, []string{"thin detail"}),
			offering("meshy", "meshy-7.1", false, []string{"never shown"}, nil),
			offering("hyper3d", "rodin-gen-2.5", true, []string{"clean topology"}, []string{"slow"}),
		},
	}
	registry.offerings = func(capability string) []generation.Offering { return offerings[capability] }
	registry.offeringAllowed = func(capability, modelID string) bool {
		for _, candidate := range offerings[capability] {
			if candidate.ModelID == modelID && candidate.Enabled {
				return true
			}
		}
		return false
	}
	orchestrator := &Orchestrator{schema: chatSchema, registry: registry, agentScope: registry.scope}
	snapshot := &AgentConfigurationSnapshot{
		Revision:   "published-1",
		Tools:      append([]catalog.ChatToolSchema(nil), chatSchema.Tools...),
		AgentTools: append([]catalog.ChatAgentToolSchema(nil), chatSchema.AgentTools...),
	}
	return orchestrator, context.WithValue(context.Background(), configurationContextKey{}, snapshot), offerings
}

func requestModelEnum(t *testing.T, tools []agents.Tool, name string) ([]string, bool) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		var parameters struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
		return parameters.Properties["model"].Enum, true
	}
	return nil, false
}

// TestPublishedConfigurationRegistryFollowsRuntimeOfferings is #379: a turn on
// a published Agent configuration recompiles its tool registry from the frozen
// declarations, and must still follow the runtime Offerings. The model enum of
// a tool with model_choices and the craft brief's Offering knowledge are the
// same as on the live (unfrozen) path.
func TestPublishedConfigurationRegistryFollowsRuntimeOfferings(t *testing.T) {
	orchestrator, frozenContext, offerings := publishedConfigurationFixture(t)
	live, err := orchestrator.registryFor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := orchestrator.registryFor(frozenContext)
	if err != nil {
		t.Fatal(err)
	}

	wantEnum := []string{"tripo-p2-text", "rodin-gen-2.5"}
	if got, found := describedModelEnum(t, live, "create_model_3d"); !found || !slices.Equal(got, wantEnum) {
		t.Fatalf("live registry model enum = %v (found %v), want %v", got, found, wantEnum)
	}
	if got, found := describedModelEnum(t, frozen, "create_model_3d"); !found || !slices.Equal(got, wantEnum) {
		t.Errorf("published-configuration registry model enum = %v (found %v), want the live %v", got, found, wantEnum)
	}

	var craft catalog.ChatAgentCraftPrompt
	for _, prompt := range orchestrator.schema.Agent.CraftPrompts {
		if prompt.ID == "model3d" {
			craft = prompt
		}
	}
	wantLines := live.offeringTradeoffs(craft)
	if len(wantLines) != 2 {
		t.Fatalf("live tradeoff lines = %v, want one per available Offering", wantLines)
	}
	if got := frozen.offeringTradeoffs(craft); !slices.Equal(got, wantLines) {
		t.Errorf("published-configuration tradeoff lines = %v, want the live %v", got, wantLines)
	}
	liveBrief := orchestrator.craftPrompt(live, "3d", "model-3d", nil)
	frozenBrief := orchestrator.craftPrompt(frozen, "3d", "model-3d", nil)
	if frozenBrief != liveBrief {
		t.Errorf("published-configuration craft brief differs from the live one:\n%q\nwant\n%q", frozenBrief, liveBrief)
	}
	for _, line := range wantLines {
		if !strings.Contains(frozenBrief, line) {
			t.Errorf("craft brief of a published configuration lacks %q", line)
		}
	}

	// The turn itself: the request the model receives carries both.
	request := orchestrator.agentRequest(frozenContext, nil,
		TurnOptions{AgentScope: "auto", CreativeDomain: "model-3d"}, "owner", "")
	if got, found := requestModelEnum(t, request.Tools, "create_model_3d"); !found || !slices.Equal(got, wantEnum) {
		t.Errorf("request model enum = %v (found %v), want %v", got, found, wantEnum)
	}
	for _, line := range wantLines {
		if !strings.Contains(request.Messages[0].Content, line) {
			t.Errorf("system prompt of a published-configuration turn lacks %q", line)
		}
	}

	// With no Offering left for the capability both paths withhold the tool:
	// the frozen path must not offer a tool nothing can run.
	delete(offerings, string(schema.TextTo3D))
	if _, found := describedModelEnum(t, live, "create_model_3d"); found {
		t.Fatal("live registry still offers a model tool with no available Offering")
	}
	if _, found := describedModelEnum(t, frozen, "create_model_3d"); found {
		t.Error("published-configuration registry offers a model tool with no available Offering")
	}
}

// TestPublishedConfigurationRegistryKeepsEveryRuntimeSource guards the next
// field: any func the Orchestrator's own registry carries is injected by the
// server, not declared by the Schema, so the registry recompiled from a frozen
// configuration must carry it too. A new func field fails here until the
// fixture sets it and registryFor passes it on.
func TestPublishedConfigurationRegistryKeepsEveryRuntimeSource(t *testing.T) {
	orchestrator, frozenContext, _ := publishedConfigurationFixture(t)
	frozen, err := orchestrator.registryFor(frozenContext)
	if err != nil {
		t.Fatal(err)
	}
	base, got := reflect.ValueOf(*orchestrator.registry), reflect.ValueOf(*frozen)
	for index := 0; index < base.NumField(); index++ {
		field := base.Type().Field(index)
		if field.Type.Kind() != reflect.Func {
			continue
		}
		if base.Field(index).IsNil() {
			t.Errorf("toolRegistry.%s is not set by publishedConfigurationFixture; set it so this guard covers it", field.Name)
			continue
		}
		if got.Field(index).IsNil() {
			t.Errorf("registry of a published configuration lost the runtime source toolRegistry.%s", field.Name)
		}
	}
}
