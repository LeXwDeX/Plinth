package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// standingSession is a session the steps mode governs.
type standingSession struct{}

func (standingSession) SessionApprovalMode(context.Context, string, string) (string, error) {
	return approvalpolicy.ModeSteps, nil
}

func (standingSession) SessionStepEntry(context.Context, string, string) (string, error) {
	return approvalpolicy.StepEntryList, nil
}

// standingStore is the data layer of the step standing, with a fixed answer
// and a count of reads so a test can tell a read from a skipped one.
type standingStore struct {
	executiongrant.Repository
	summary executiongrant.StepSummary
	err     error
	reads   int
}

func (s *standingStore) ApproveExecutionGrantWithSteps(context.Context, string, string, executiongrant.StepRequest) (executiongrant.Grant, executiongrant.StepDecision, error) {
	return executiongrant.Grant{}, executiongrant.StepDecision{}, nil
}

func (s *standingStore) StepUsage(context.Context, string, string, executiongrant.StepRequest) (executiongrant.StepSummary, error) {
	s.reads++
	return s.summary, s.err
}

// standingExecutor is an executor with only the step-standing dependencies.
func standingExecutor(t *testing.T, summary executiongrant.StepSummary, readErr error) (*Executor, chat.ToolCommand, *standingStore) {
	t.Helper()
	store := &standingStore{summary: summary, err: readErr}
	policy := approvalpolicy.Policy{Steps: &approvalpolicy.StepsPolicy{Counted: []string{"image-to-3d"}}}
	executor := &Executor{
		grants: &executiongrant.Service{Grants: store, Policy: policy, Modes: standingSession{}},
		stepPolicy: &catalog.StepPolicySchema{
			CountedCapabilities: []schemacontract.Capability{"image-to-3d"},
		},
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a",
		Tool: catalog.ChatToolSchema{Name: "create_model_3d_from_image", Capability: "image-to-3d",
			ResultFields: catalog.ChatToolResultFields{Success: "success", Error: "error", Steps: "steps"}}}
	return executor, command, store
}

func agentStanding() executiongrant.StepSummary {
	return executiongrant.StepSummary{Entry: approvalpolicy.StepEntryList, Mode: approvalpolicy.StepModeAgent, Used: 2}
}

// #330 step 3B review: a pure failure has no agent content of its own; the
// orchestrator hands the model its Error text, so the failure receipt is built
// with the steps object inside instead of leaving the model without it.
func TestAttachStepStandingWritesIntoAPureErrorResult(t *testing.T) {
	executor, command, store := standingExecutor(t, agentStanding(), nil)
	result := chat.ToolResult{Error: "the model report is not readable", NoSubmission: true}
	executor.attachStepStanding(context.Background(), command, &result, nil)

	if store.reads != 1 || result.Error != "the model report is not readable" {
		t.Fatalf("reads=%d error=%q", store.reads, result.Error)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["success"] != false || content["error"] != "the model report is not readable" {
		t.Fatalf("failure receipt = %v", content)
	}
	steps, _ := content["steps"].(map[string]any)
	if steps[catalog.StepResultMode] != approvalpolicy.StepModeAgent || steps[catalog.StepResultUsed] != float64(2) {
		t.Fatalf("steps object = %v", content["steps"])
	}
}

// A wait has no receipt for the model yet, and a result with no text at all
// has nothing to attach to.
func TestAttachStepStandingLeavesWaitResultsAlone(t *testing.T) {
	for name, result := range map[string]chat.ToolResult{
		"suspend":    {AgentContent: `{"success":true,"error":"waiting"}`, Suspend: true},
		"await-task": {AgentContent: `{"success":true,"error":"waiting"}`, AwaitTask: true},
		"no text":    {NoSubmission: true},
	} {
		t.Run(name, func(t *testing.T) {
			executor, command, store := standingExecutor(t, agentStanding(), nil)
			before := result.AgentContent
			executor.attachStepStanding(context.Background(), command, &result, nil)
			if result.AgentContent != before || store.reads != 0 {
				t.Fatalf("wait result changed: %q reads=%d", result.AgentContent, store.reads)
			}
		})
	}
}

// A result that already owns the key keeps its value, and one whose content is
// not a JSON object is left as it is; neither needs a read of the standing.
func TestAttachStepStandingLeavesContentItCannotExtend(t *testing.T) {
	for name, content := range map[string]string{
		"existing key":  `{"success":true,"steps":{"mode":"cap","used":9}}`,
		"not an object": "not json",
	} {
		t.Run(name, func(t *testing.T) {
			executor, command, store := standingExecutor(t, agentStanding(), nil)
			result := chat.ToolResult{AgentContent: content}
			executor.attachStepStanding(context.Background(), command, &result, nil)
			if result.AgentContent != content || store.reads != 0 {
				t.Fatalf("content changed: %q reads=%d", result.AgentContent, store.reads)
			}
		})
	}
}

// An unreadable standing never guesses a number: a session outside the steps
// mode answers ErrInvalid and any other failure leaves the result unchanged.
func TestAttachStepStandingLeavesTheResultWhenTheStandingIsUnreadable(t *testing.T) {
	for name, readErr := range map[string]error{
		"outside the steps mode": executiongrant.ErrInvalid,
		"read failure":           errors.New("step store unavailable"),
	} {
		t.Run(name, func(t *testing.T) {
			executor, command, store := standingExecutor(t, executiongrant.StepSummary{}, readErr)
			content := `{"success":true}`
			result := chat.ToolResult{AgentContent: content}
			executor.attachStepStanding(context.Background(), command, &result, nil)
			if result.AgentContent != content || store.reads != 1 {
				t.Fatalf("result changed: %q reads=%d", result.AgentContent, store.reads)
			}
		})
	}
}
