package chat_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// localeAgent records the turn's request and the title request.
type localeAgent struct {
	mu     sync.Mutex
	stream []agents.Request
	title  []agents.Request
}

func (a *localeAgent) Stream(_ context.Context, request agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	a.mu.Lock()
	a.stream = append(a.stream, request)
	a.mu.Unlock()
	if emit != nil {
		if err := emit(agents.Delta{Content: "reply"}); err != nil {
			return agents.Completion{}, err
		}
	}
	return agents.Completion{Content: "reply"}, nil
}

func (a *localeAgent) Complete(_ context.Context, request agents.Request) (agents.Completion, error) {
	a.mu.Lock()
	a.title = append(a.title, request)
	a.mu.Unlock()
	return agents.Completion{Content: "A robot cat adventure in the city"}, nil
}

func runLocaleTurn(t *testing.T, locale string) (*localeAgent, catalog.ApplicationSchema, string) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	agent := &localeAgent{}
	service := newChatService(t, newMemoryChatRepository())
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		// The test service writes the configured roles; the copy is the catalog's.
		Service: service, Schema: configuredChatSchema(t), Model: "configured-model", Agent: agent,
		Tools: noToolExecutor{}, Texts: texts,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "draw a robot cat",
		Options: chat.TurnOptions{Locale: locale},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The command carries no locale: the one frozen with the turn is used.
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
	orchestrator.WaitForTitles()
	stored, found, err := service.Session(ctx, "owner-a", session.ID)
	if err != nil || !found {
		t.Fatalf("session = %v %v", found, err)
	}
	return agent, application, stored.Title
}

func systemPrompt(t *testing.T, request agents.Request) string {
	t.Helper()
	if len(request.Messages) == 0 {
		t.Fatal("request without messages")
	}
	return request.Messages[0].Content
}

func TestEnglishTurnAsksForAnEnglishReplyAndTitle(t *testing.T) {
	agent, application, title := runLocaleTurn(t, "en")
	english := application.Localization.Bundles["en"]
	directive := strings.ReplaceAll(english.Fields["chat.agent.reply_language_template"], "{language}", "English")
	if len(agent.stream) == 0 || !strings.HasSuffix(systemPrompt(t, agent.stream[0]), directive) {
		t.Fatalf("system prompt does not end with the English reply instruction %q", directive)
	}
	// The configured system prompt itself is unchanged.
	if !strings.HasPrefix(systemPrompt(t, agent.stream[0]), configuredChatSchema(t).Agent.SystemPrompt) {
		t.Fatal("system prompt no longer starts with the configured prompt")
	}
	if len(agent.title) != 1 {
		t.Fatalf("title requests = %d", len(agent.title))
	}
	maximum := application.Chat.Agent.TitleMaxCharsByLocale["en"]
	want := strings.NewReplacer("{maximum}", "40", "{content}", "draw a robot cat").Replace(english.Fields["chat.agent.title_prompt"])
	if maximum != 40 || systemPrompt(t, agent.title[0]) != want {
		t.Fatalf("title prompt = %q", systemPrompt(t, agent.title[0]))
	}
	if title != "A robot cat adventure in the city" {
		t.Fatalf("English title clipped to %q", title)
	}
}

func TestDefaultLocaleTurnKeepsThePrompts(t *testing.T) {
	agent, application, title := runLocaleTurn(t, "zh-CN")
	for _, request := range agent.stream {
		if strings.Contains(systemPrompt(t, request), "{language}") ||
			strings.Contains(systemPrompt(t, request), strings.ReplaceAll(application.Chat.Agent.ReplyLanguageTemplate, "{language}", "简体中文")) {
			t.Fatal("default-locale turn got a reply-language instruction")
		}
	}
	if len(agent.title) != 1 || !strings.HasPrefix(systemPrompt(t, agent.title[0]), strings.Split(application.Chat.Agent.TitlePrompt, "{maximum}")[0]) {
		t.Fatal("default-locale title prompt changed")
	}
	if got := []rune(title); len(got) > application.Chat.Agent.TitleMaxChars {
		t.Fatalf("default title not clipped: %q", title)
	}
}
