package chat_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// titleAgent answers the turn itself with a fixed reply and the title request
// (recognised by the configured title prompt prefix) through titleReply. When
// gate is set the title call waits for it, so a test can act while the title
// is being generated.
type titleAgent struct {
	mu         sync.Mutex
	titleCalls int
	titleReply func() (agents.Completion, error)
	entered    chan struct{}
	gate       chan struct{}
}

func (a *titleAgent) Stream(
	_ context.Context,
	_ agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	if emit != nil {
		if err := emit(agents.Delta{Content: "reply"}); err != nil {
			return agents.Completion{}, err
		}
	}
	return agents.Completion{Content: "reply"}, nil
}

func (a *titleAgent) Complete(ctx context.Context, _ agents.Request) (agents.Completion, error) {
	a.mu.Lock()
	a.titleCalls++
	a.mu.Unlock()
	if a.entered != nil {
		a.entered <- struct{}{}
	}
	if a.gate != nil {
		select {
		case <-a.gate:
		case <-ctx.Done():
			return agents.Completion{}, ctx.Err()
		}
	}
	return a.titleReply()
}

func (a *titleAgent) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.titleCalls
}

func titleFixture(t *testing.T, agent *titleAgent) (*chat.Service, *chat.Orchestrator) {
	t.Helper()
	service := newChatService(t, newMemoryChatRepository())
	schema := configuredChatSchema(t)
	schema.Agent.TitleMaxChars = 6
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: noToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, orchestrator
}

func runTitleTurn(
	t *testing.T,
	service *chat.Service,
	orchestrator *chat.Orchestrator,
	owner, sessionID, content string,
) {
	t.Helper()
	ctx := context.Background()
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: owner, SessionID: sessionID, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: owner, SessionID: sessionID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}
}

func sessionTitle(t *testing.T, service *chat.Service, owner, id string) string {
	t.Helper()
	session, found, err := service.Session(context.Background(), owner, id)
	if err != nil || !found {
		t.Fatalf("session = %v %v", found, err)
	}
	return session.Title
}

func TestAutoTitleIsCleanedClippedAndDoesNotBlockTheTurn(t *testing.T) {
	agent := &titleAgent{
		entered: make(chan struct{}, 1),
		gate:    make(chan struct{}),
		titleReply: func() (agents.Completion, error) {
			return agents.Completion{Content: "「\"银色\n机械猫咪的冒险\"」。"}, nil
		},
	}
	service, orchestrator := titleFixture(t, agent)
	session, _ := service.CreateSession(context.Background(), "owner-a", "Owner", "")
	// The turn returns while the title call is still blocked on the gate.
	runTitleTurn(t, service, orchestrator, "owner-a", session.ID, "画一只机械猫")
	<-agent.entered
	if got := sessionTitle(t, service, "owner-a", session.ID); got != "configured default title" {
		t.Fatalf("title before generation = %q", got)
	}
	close(agent.gate)
	orchestrator.WaitForTitles()
	if got := sessionTitle(t, service, "owner-a", session.ID); got != "银色机械猫咪" {
		t.Fatalf("title = %q", got)
	}
}

func TestAutoTitleNeverOverwritesAManualRenameEvenWhenItRacesGeneration(t *testing.T) {
	agent := &titleAgent{
		entered:    make(chan struct{}, 1),
		gate:       make(chan struct{}),
		titleReply: func() (agents.Completion, error) { return agents.Completion{Content: "自动标题"}, nil },
	}
	service, orchestrator := titleFixture(t, agent)
	session, _ := service.CreateSession(context.Background(), "owner-a", "Owner", "")
	runTitleTurn(t, service, orchestrator, "owner-a", session.ID, "hello")
	<-agent.entered
	if _, err := service.RenameSession(context.Background(), "owner-a", session.ID, "我的标题"); err != nil {
		t.Fatal(err)
	}
	close(agent.gate)
	orchestrator.WaitForTitles()
	if got := sessionTitle(t, service, "owner-a", session.ID); got != "我的标题" {
		t.Fatalf("manual title overwritten: %q", got)
	}
}

func TestAutoTitleSkipsSessionsCreatedWithAnExplicitTitle(t *testing.T) {
	agent := &titleAgent{titleReply: func() (agents.Completion, error) { return agents.Completion{Content: "自动"}, nil }}
	service, orchestrator := titleFixture(t, agent)
	session, _ := service.CreateSession(context.Background(), "owner-a", "Owner", "named")
	runTitleTurn(t, service, orchestrator, "owner-a", session.ID, "hello")
	orchestrator.WaitForTitles()
	if agent.calls() != 0 || sessionTitle(t, service, "owner-a", session.ID) != "named" {
		t.Fatalf("calls = %d", agent.calls())
	}
}

func TestAutoTitleFailureKeepsTheCurrentTitle(t *testing.T) {
	for name, reply := range map[string]func() (agents.Completion, error){
		"error": func() (agents.Completion, error) { return agents.Completion{}, errors.New("upstream down") },
		"empty": func() (agents.Completion, error) { return agents.Completion{Content: " \"。\" "}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			agent := &titleAgent{titleReply: reply}
			service, orchestrator := titleFixture(t, agent)
			session, _ := service.CreateSession(context.Background(), "owner-a", "Owner", "")
			runTitleTurn(t, service, orchestrator, "owner-a", session.ID, "hello")
			orchestrator.WaitForTitles()
			if got := sessionTitle(t, service, "owner-a", session.ID); got != "configured default title" {
				t.Fatalf("title = %q", got)
			}
		})
	}
}

func TestAutoTitleRunsOnlyForTheFirstTurn(t *testing.T) {
	agent := &titleAgent{titleReply: func() (agents.Completion, error) { return agents.Completion{Content: "第一"}, nil }}
	service, orchestrator := titleFixture(t, agent)
	session, _ := service.CreateSession(context.Background(), "owner-a", "Owner", "")
	runTitleTurn(t, service, orchestrator, "owner-a", session.ID, "one")
	orchestrator.WaitForTitles()
	runTitleTurn(t, service, orchestrator, "owner-a", session.ID, "two")
	orchestrator.WaitForTitles()
	if agent.calls() != 1 || sessionTitle(t, service, "owner-a", session.ID) != "第一" {
		t.Fatalf("calls = %d", agent.calls())
	}
}

func TestApplyAutoTitleIsOwnerScopedAndOnlyWritesOnce(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	ctx := context.Background()
	session, _ := service.CreateSession(ctx, "owner-a", "Owner", "")
	if applied, err := service.ApplyAutoTitle(ctx, "owner-b", session.ID, "stolen"); err != nil || applied {
		t.Fatalf("foreign apply = %v %v", applied, err)
	}
	if applied, err := service.ApplyAutoTitle(ctx, "owner-a", session.ID, "first"); err != nil || !applied {
		t.Fatalf("apply = %v %v", applied, err)
	}
	if applied, err := service.ApplyAutoTitle(ctx, "owner-a", session.ID, "second"); err != nil || applied {
		t.Fatalf("second apply = %v %v", applied, err)
	}
	if got := sessionTitle(t, service, "owner-a", session.ID); got != "first" {
		t.Fatalf("title = %q", got)
	}
}
