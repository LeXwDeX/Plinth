package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// sessionLookupRepository fails the session read of the approval mode on
// demand; everything else is the in-memory repository.
type sessionLookupRepository struct {
	*memoryChatRepository
	fail bool
}

func (repository *sessionLookupRepository) Session(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (chat.Session, bool, error) {
	if repository.fail {
		return chat.Session{}, false, errors.New("session store unavailable")
	}
	return repository.memoryChatRepository.Session(ctx, ownerID, sessionID)
}

// Texts of the shipped Schema (application.chat.approval_policy.prompt) that
// tell the two modes apart.
const (
	manualModeLabel   = "批准方式是「手工」"
	manualModeText    = "手工方式下，文生 3D、图生 3D、Gaussian Splat 生成、换贴图这几类步骤会先出批准卡"
	autoModeLabel     = "批准方式是「自动」"
	autoModeText      = "自动方式下，所有付费步骤都由系统按会话批准方式自动批准，不出批准卡"
	approvalBlockHead = "【批准方式】"
)

type approvalPromptCase struct {
	// mode is written to the session as stored ("" keeps the new-session
	// default); it may be a value the Schema does not offer.
	mode  string
	scope string
	// failLookup makes the session read fail from the turn on.
	failLookup bool
}

// approvalPromptSystem runs one turn of a session set up by c, with the
// shipped Schema texts, and returns the system context the model read on its
// first request.
func approvalPromptSystem(t *testing.T, c approvalPromptCase) string {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	shipped := taskCatalog.Application().Chat
	schema := configuredChatSchema(t)
	schema.Agent.SystemPrompt = shipped.Agent.SystemPrompt
	schema.AgentScope = shipped.AgentScope
	schema.ApprovalPolicy = shipped.ApprovalPolicy
	schema.Tools = shipped.Tools
	repository := &sessionLookupRepository{memoryChatRepository: newMemoryChatRepository()}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.AgentScope = agentscope.FromSchema(schema.AgentScope)
	})
	agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: &scopeToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if c.mode != "" {
		if _, _, err := repository.SetSessionApprovalMode(ctx, "owner-a", session.ID, c.mode); err != nil {
			t.Fatal(err)
		}
	}
	if c.scope != "" {
		if _, _, err := service.SetAgentScope(ctx, "owner-a", session.ID, c.scope); err != nil {
			t.Fatal(err)
		}
	}
	options := chat.TurnOptions{CreativeDomain: "image"}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID,
		Content: "hello", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	repository.fail = c.failLookup
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: assistant.ID, Options: options}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) == 0 {
		t.Fatal("the Agent was never called")
	}
	return agent.requests[0].Messages[0].Content
}

// #7: the Agent is told the session's real approval mode, whatever the
// conversation says, and the two modes never leak into each other's text.
func TestAgentSystemContextCarriesTheSessionApprovalMode(t *testing.T) {
	for name, testCase := range map[string]struct {
		session          approvalPromptCase
		want, wantAbsent []string
	}{
		"manual session":                  {approvalPromptCase{mode: "manual"}, []string{manualModeLabel, manualModeText}, []string{autoModeLabel, autoModeText}},
		"auto session":                    {approvalPromptCase{mode: "auto"}, []string{autoModeLabel, autoModeText}, []string{manualModeLabel, manualModeText}},
		"new session defaults to manual":  {approvalPromptCase{}, []string{manualModeLabel, manualModeText}, []string{autoModeLabel, autoModeText}},
		"a stored unknown mode is manual": {approvalPromptCase{mode: "yolo"}, []string{manualModeLabel, manualModeText}, []string{autoModeLabel, autoModeText}},
		"the Agent scope does not change it": {approvalPromptCase{mode: "auto", scope: "2d"},
			[]string{autoModeLabel, autoModeText}, []string{manualModeLabel, manualModeText}},
	} {
		system := approvalPromptSystem(t, testCase.session)
		for _, want := range testCase.want {
			if !strings.Contains(system, want) {
				t.Errorf("%s: the system context omits %q", name, want)
			}
		}
		for _, absent := range testCase.wantAbsent {
			if strings.Contains(system, absent) {
				t.Errorf("%s: the system context carries %q", name, absent)
			}
		}
	}
}

// The block states that the person alone sets the mode, so the Agent neither
// switches it nor sells a switch as the way to get approval cards (#150, #7),
// and it follows the Agent scope block it must not be confused with.
func TestApprovalModePromptKeepsTheAgentOutOfTheModeAndFollowsTheScope(t *testing.T) {
	system := approvalPromptSystem(t, approvalPromptCase{mode: "manual"})
	for _, want := range []string{"只由用户在顶栏切换", "你不能切换它", "不要主动建议用户切换它", "不要猜测", "与 Agent 范围"} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context omits %q", want)
		}
	}
	scope, approval := strings.Index(system, "【Agent 范围】"), strings.Index(system, approvalBlockHead)
	if scope < 0 || approval < scope {
		t.Errorf("the approval block (%d) does not follow the scope block (%d)", approval, scope)
	}
	if strings.Count(system, approvalBlockHead) != 1 {
		t.Errorf("the approval block appears %d times", strings.Count(system, approvalBlockHead))
	}
}

// A mode that cannot be read is left out: the Agent is never given a guess
// that could contradict the session.
func TestApprovalModePromptIsOmittedWhenTheSessionCannotBeRead(t *testing.T) {
	system := approvalPromptSystem(t, approvalPromptCase{mode: "manual", failLookup: true})
	if strings.Contains(system, approvalBlockHead) || strings.Contains(system, manualModeLabel) ||
		strings.Contains(system, autoModeLabel) {
		t.Errorf("a failed lookup still states a mode: %q", system)
	}
}
