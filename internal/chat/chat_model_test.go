package chat_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func (store *memoryContextStore) SetSessionChatModel(_ context.Context, owner, session, model string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.owns(owner, session) {
		return false, nil
	}
	if store.models == nil {
		store.models = map[string]string{}
	}
	store.models[session] = model
	return true, nil
}

// switchableModels is the administrator's model set as the tests change it.
type switchableModels struct {
	mu  sync.Mutex
	set chat.ChatModelSet
}

func (models *switchableModels) source(context.Context) (chat.ChatModelSet, error) {
	models.mu.Lock()
	defer models.mu.Unlock()
	return models.set, nil
}

func (models *switchableModels) replace(set chat.ChatModelSet) {
	models.mu.Lock()
	defer models.mu.Unlock()
	models.set = set
}

var (
	deepseekOption = chat.ChatModelOption{ID: "LLM", Model: "deepseek", EffortLevels: []string{"low", "high"}, DefaultEffort: "high", Window: 1000000}
	glmOption      = chat.ChatModelOption{ID: "GLM", Model: "glm-5.3", EffortLevels: []string{"max"}, DefaultEffort: "max", Window: 200000}
	plainOption    = chat.ChatModelOption{ID: "PLAIN", Model: "plain-model", EffortLevels: []string{}}
)

func newModelFixture(t *testing.T) (*contextFixture, *switchableModels) {
	t.Helper()
	models := &switchableModels{set: chat.ChatModelSet{Options: []chat.ChatModelOption{deepseekOption, glmOption}, DefaultID: "LLM"}}
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek", Source: models.source})
	return fixture, models
}

// #322 Scope 5: the session follows the administrator's default model until
// the person picks another one; the levels, the window and every request of
// the next turn follow the picked model.
func TestSessionChatModelChoiceDrivesLevelsWindowAndRequests(t *testing.T) {
	ctx := context.Background()
	fixture, _ := newModelFixture(t)
	view, err := fixture.service.SessionChatModel(ctx, "owner-a", fixture.session)
	if err != nil || view.Selected != "LLM" || view.Chosen != "" || view.Default != "LLM" || view.Fallback || len(view.Options) != 2 {
		t.Fatalf("initial view = %+v err=%v", view, err)
	}
	reasoning, err := fixture.service.SessionReasoning(ctx, "owner-a", fixture.session)
	if err != nil || reasoning.Effort != "high" || reasoning.Default != "high" || !slices.Equal(reasoning.Levels, []string{"low", "high"}) {
		t.Fatalf("deepseek levels = %+v err=%v", reasoning, err)
	}
	// The administrator did not offer max for this model.
	if _, err := fixture.service.SetSessionReasoning(ctx, "owner-a", fixture.session, "max"); !errors.Is(err, chat.ErrInvalidReasoningEffort) {
		t.Fatalf("level the administrator closed = %v", err)
	}
	if _, err := fixture.service.SetSessionReasoning(ctx, "owner-a", fixture.session, "low"); err != nil {
		t.Fatal(err)
	}
	fixture.turn(t, "first")
	first := fixture.agent.lastStream()
	if first.Model != "deepseek" || first.Connection != "LLM" || first.ReasoningEffort != "low" {
		t.Fatalf("deepseek request = model %q connection %q effort %q", first.Model, first.Connection, first.ReasoningEffort)
	}

	view, err = fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "GLM")
	if err != nil || view.Selected != "GLM" || view.Chosen != "GLM" || view.Fallback {
		t.Fatalf("pick GLM = %+v err=%v", view, err)
	}
	// "low" is not a GLM level: the level falls back to the model's default.
	reasoning, _ = fixture.service.SessionReasoning(ctx, "owner-a", fixture.session)
	if reasoning.Effort != "max" || !slices.Equal(reasoning.Levels, []string{"max"}) {
		t.Fatalf("glm levels = %+v", reasoning)
	}
	window, _ := fixture.service.SessionContextView(ctx, "owner-a", fixture.session)
	if window.Window != 200000 {
		t.Fatalf("glm window = %d", window.Window)
	}
	fixture.turn(t, "second")
	second := fixture.agent.lastStream()
	if second.Model != "glm-5.3" || second.Connection != "GLM" || second.ReasoningEffort != "max" {
		t.Fatalf("glm request = model %q connection %q effort %q", second.Model, second.Connection, second.ReasoningEffort)
	}
	// Back on DeepSeek the stored "low" applies again.
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "LLM"); err != nil {
		t.Fatal(err)
	}
	if reasoning, _ := fixture.service.SessionReasoning(ctx, "owner-a", fixture.session); reasoning.Effort != "low" {
		t.Fatalf("back on deepseek = %+v", reasoning)
	}
}

func TestSessionChatModelRejectsUnknownModelsAndForeignSessions(t *testing.T) {
	ctx := context.Background()
	fixture, _ := newModelFixture(t)
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "NOPE"); !errors.Is(err, chat.ErrInvalidChatModel) {
		t.Fatalf("unknown model = %v", err)
	}
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-b", fixture.session, "GLM"); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign write = %v", err)
	}
	if _, err := fixture.service.SessionChatModel(ctx, "owner-b", fixture.session); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign read = %v", err)
	}
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "GLM"); err != nil {
		t.Fatal(err)
	}
	// "" follows the default again.
	view, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "")
	if err != nil || view.Chosen != "" || view.Selected != "LLM" {
		t.Fatalf("follow default = %+v err=%v", view, err)
	}
}

// The person's choice wins over the default; when the administrator takes
// the chosen model offline the session falls back to the default and says so,
// and turns run on the default without asking.
func TestSessionChatModelFallsBackWhenTheChosenModelGoesOffline(t *testing.T) {
	ctx := context.Background()
	fixture, models := newModelFixture(t)
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "GLM"); err != nil {
		t.Fatal(err)
	}
	models.replace(chat.ChatModelSet{Options: []chat.ChatModelOption{deepseekOption, glmOption}, DefaultID: "GLM"})
	models.replace(chat.ChatModelSet{Options: []chat.ChatModelOption{deepseekOption}, DefaultID: "LLM"})
	view, err := fixture.service.SessionChatModel(ctx, "owner-a", fixture.session)
	if err != nil || view.Selected != "LLM" || view.Chosen != "GLM" || !view.Fallback || len(view.Options) != 1 {
		t.Fatalf("offline choice = %+v err=%v", view, err)
	}
	fixture.turn(t, "after offline")
	request := fixture.agent.lastStream()
	if request.Connection != "LLM" || request.Model != "deepseek" || request.ReasoningEffort != "high" {
		t.Fatalf("fallback request = %q %q %q", request.Connection, request.Model, request.ReasoningEffort)
	}
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "GLM"); !errors.Is(err, chat.ErrInvalidChatModel) {
		t.Fatalf("pick an offline model = %v", err)
	}
	// Back online, the choice applies again.
	models.replace(chat.ChatModelSet{Options: []chat.ChatModelOption{deepseekOption, glmOption}, DefaultID: "LLM"})
	if view, _ := fixture.service.SessionChatModel(ctx, "owner-a", fixture.session); view.Selected != "GLM" || view.Fallback {
		t.Fatalf("back online = %+v", view)
	}
}

// A turn keeps the model it started with even when the session switches
// before it is processed (or resumed after a restart).
func TestTurnKeepsTheModelFrozenAtItsStart(t *testing.T) {
	ctx := context.Background()
	fixture, _ := newModelFixture(t)
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "GLM"); err != nil {
		t.Fatal(err)
	}
	assistant, err := fixture.service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: fixture.session, Content: "frozen"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "LLM"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: fixture.session, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	fixture.orchestrator.WaitForTitles()
	request := fixture.agent.lastStream()
	if request.Connection != "GLM" || request.Model != "glm-5.3" {
		t.Fatalf("frozen turn = %q %q", request.Connection, request.Model)
	}
	turn, err := fixture.service.GetTurn(ctx, "owner-a", fixture.session, assistant.ID)
	if err != nil || turn.Options.ModelConnection != "GLM" || turn.Options.Model != "glm-5.3" {
		t.Fatalf("stored turn options = %+v err=%v", turn.Options, err)
	}
}

// A model without thinking levels offers none and sends none; a compaction
// runs on the session's model and connection, and its summary stays valid
// after a switch.
func TestModelWithoutLevelsAndCompactionUseTheSessionModel(t *testing.T) {
	ctx := context.Background()
	fixture, models := newModelFixture(t)
	models.replace(chat.ChatModelSet{Options: []chat.ChatModelOption{deepseekOption, plainOption}, DefaultID: "LLM"})
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "PLAIN"); err != nil {
		t.Fatal(err)
	}
	reasoning, err := fixture.service.SessionReasoning(ctx, "owner-a", fixture.session)
	if err != nil || reasoning.Effort != "" || len(reasoning.Levels) != 0 {
		t.Fatalf("plain levels = %+v err=%v", reasoning, err)
	}
	if _, err := fixture.service.SetSessionReasoning(ctx, "owner-a", fixture.session, "low"); !errors.Is(err, chat.ErrReasoningUnavailable) {
		t.Fatalf("level on a model without levels = %v", err)
	}
	if window, _ := fixture.service.SessionContextView(ctx, "owner-a", fixture.session); window.Window != 0 {
		t.Fatalf("plain window = %d", window.Window)
	}
	for range fixture.schema.ContextCompaction.KeepRecentRounds + 2 {
		fixture.turn(t, "round")
	}
	if request := fixture.agent.lastStream(); request.Connection != "PLAIN" || request.ReasoningEffort != "" {
		t.Fatalf("plain request = %q %q", request.Connection, request.ReasoningEffort)
	}
	if _, err := fixture.orchestrator.CompactContext(ctx, "owner-a", fixture.session); err != nil {
		t.Fatal(err)
	}
	fixture.agent.mu.Lock()
	summary := fixture.agent.summaries[len(fixture.agent.summaries)-1]
	fixture.agent.mu.Unlock()
	if summary.Connection != "PLAIN" || summary.Model != "plain-model" || summary.Purpose != agents.PurposeSummary {
		t.Fatalf("summary request = %q %q", summary.Connection, summary.Model)
	}
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "LLM"); err != nil {
		t.Fatal(err)
	}
	fixture.turn(t, "after switch")
	if text := requestText(fixture.agent.lastStream()); !strings.Contains(text, fixture.agent.summary) {
		t.Fatalf("summary dropped after the switch: %s", text)
	}
}

// Without a model source the deployment's single model is the only option,
// and a session cannot pick another.
func TestWithoutModelSourceTheDeploymentModelIsTheOnlyOption(t *testing.T) {
	ctx := context.Background()
	fixture := newContextFixture(t, chat.ModelPolicy{Model: "deepseek"})
	view, err := fixture.service.SessionChatModel(ctx, "owner-a", fixture.session)
	if err != nil || len(view.Options) != 1 || view.Options[0].Model != "deepseek" || view.Fallback {
		t.Fatalf("single model view = %+v err=%v", view, err)
	}
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "GLM"); !errors.Is(err, chat.ErrChatModelUnavailable) {
		t.Fatalf("pick without a source = %v", err)
	}
}

// DSML tool-call reading follows the turn's model (#322 Scope 5): a model
// whose profile does not write DSML keeps such text as plain content.
func TestDSMLReadingFollowsTheTurnModel(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.DSML = catalog.ChatDSMLSchema{
		Enabled: true, Markers: []string{"configured-marker-"}, ToolCallsElements: []string{"calls"},
		InvokeElement: "run", ParameterElement: "arg", NameAttribute: "key", StringAttribute: "text",
		TrueValue: "yes", FalseValue: "no", CallIDTemplate: "configured-dsml-{index}",
	}
	raw := "visible<configured-marker-calls>" + `<configured-marker-run key="` + schema.Tools[0].Name + `">` +
		`<configured-marker-arg key="prompt" text="yes">paint</configured-marker-arg></configured-marker-run></configured-marker-calls>`
	agent := &fragmentedDSMLAgent{raw: raw, chunks: []string{raw}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: scriptedToolExecutor{},
		DSMLFor: func(model string) bool { return model == "deepseek" },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored := messages[1]; len(stored.ToolCalls) != 0 || !strings.Contains(stored.Content, "configured-marker-calls") {
		t.Fatalf("a model without DSML had its text parsed: %#v", stored)
	}
}
