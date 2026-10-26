package generationtool_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// sessionTasks is the recharge guard's view of the session's Tasks.
type sessionTasks struct {
	tasks     []generation.Task
	recovered map[generation.TaskID]generation.TaskID // recovery task -> its source
	refetches map[generation.TaskID]generation.TaskID // upstream refetch task -> its source
	lists     int
}

func (s *sessionTasks) SessionRefetchSources(_ context.Context, _ generation.OwnerID, _ string) (map[generation.TaskID]generation.TaskID, error) {
	result := map[generation.TaskID]generation.TaskID{}
	for task, source := range s.refetches {
		result[task] = source
	}
	return result, nil
}

func (s *sessionTasks) List(_ context.Context, scope generation.TaskListScope, query generation.ListQuery) (generation.TaskPage, error) {
	s.lists++
	var page []generation.Task
	for _, task := range s.tasks {
		if task.OwnerID == scope.OwnerID() && task.SessionID == query.SessionID {
			summary := task
			summary.Offering = generation.Offering{ID: task.Offering.ID} // the list omits billing
			page = append(page, summary)
		}
	}
	sort.Slice(page, func(i, j int) bool { return page[i].CreatedAt.After(page[j].CreatedAt) })
	return generation.TaskPage{Tasks: page, Total: len(page)}, nil
}

func (s *sessionTasks) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	for _, task := range s.tasks {
		if task.ID == id && task.OwnerID == owner {
			return task, nil
		}
	}
	return generation.Task{}, &generation.Error{Code: generation.ErrNotFound}
}

func (s *sessionTasks) TaskRecoveryProvenance(_ context.Context, task generation.Task) (*generation.RecoveryProvenance, error) {
	if source, ok := s.recovered[task.ID]; ok {
		return &generation.RecoveryProvenance{SourceTaskID: source}, nil
	}
	return nil, nil
}

// unstoredTask failed after the upstream finished and charged: a credits-metered
// Offering, the Schema's metered integrity class, its result not stored.
func unstoredTask(id string, capability string, finished time.Time) generation.Task {
	return generation.Task{ID: generation.TaskID(id), OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.Capability(capability), State: generation.TaskFailed,
		Offering:  generation.Offering{ID: "meshy/fake", CreditsMetered: true},
		Failure:   &generation.Failure{Class: generation.FailureIntegrity, Retryable: true},
		CreatedAt: finished.Add(-time.Minute), UpdatedAt: finished, FinishedAt: &finished}
}

func runRechargeCase(t *testing.T, mode, toolName, capability string, tasks *sessionTasks) (string, *grantGateway, string) {
	t.Helper()
	executor, command, gateway, repo := policyHarnessWithTasks(t, sessionMode{mode: mode}, toolName, capability, nil, tasks)
	ctx := context.Background()
	result, err := executor.Execute(ctx, command, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil {
		t.Fatalf("operation: %v", err)
	}
	if op.RechargeTaskID == "" {
		return result.AgentContent, gateway, ""
	}
	if !result.NoSubmission || result.Suspend || gateway.submitCount != 0 || !op.Detached || !op.PolicyEvaluated {
		t.Fatalf("a repeat of a charged attempt must wait on its card: result=%+v submits=%d op=%+v", result, gateway.submitCount, op)
	}
	grant, err := repo.GetExecutionGrant(ctx, "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v err=%v", grant, err)
	}
	return result.AgentContent, gateway, op.RechargeTaskID
}

// The automatic approval mode, and the 2D steps that manual mode approves by
// itself, would submit at once: a repeat of a charged-but-unstored attempt still
// waits for the person on its card.
func TestRechargeGuardForcesTheCardWhateverTheApprovalMode(t *testing.T) {
	for _, c := range []struct{ mode, tool, capability string }{
		{"auto", "create_model_3d", "text-to-3d"},
		{"auto", "refine_image", "text-to-image"},
		{"manual", "refine_image", "text-to-image"},
		{"manual", "create_model_3d", "text-to-3d"}, // already manual: the card still says it charges again
	} {
		t.Run(c.mode+"/"+c.tool, func(t *testing.T) {
			tasks := &sessionTasks{tasks: []generation.Task{unstoredTask("task_old", c.capability, time.Now().Add(-time.Hour))}}
			content, _, recharge := runRechargeCase(t, c.mode, c.tool, c.capability, tasks)
			if recharge != "task_old" {
				t.Fatalf("recharge task = %q", recharge)
			}
			if !strings.Contains(content, "task_old") || !strings.Contains(content, "charges again") {
				t.Fatalf("the Agent is not told why the card waits: %q", content)
			}
		})
	}
}

// The Agent may switch between a tool's capabilities when it tries again: a
// failed text-to-image is repeated by refine_image with a reference picture.
func TestRechargeGuardCoversTheToolsOtherCapabilities(t *testing.T) {
	tasks := &sessionTasks{tasks: []generation.Task{unstoredTask("task_old", "text-to-image", time.Now().Add(-time.Hour))}}
	if _, _, recharge := runRechargeCase(t, "auto", "refine_image", "image-to-image", tasks); recharge != "task_old" {
		t.Fatalf("recharge task = %q", recharge)
	}
}

func TestRechargeGuardStaysOffWithoutAChargedUnstoredFailure(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	notMetered := unstoredTask("task_free", "text-to-image", past)
	notMetered.Offering.CreditsMetered = false
	rejected := unstoredTask("task_rejected", "text-to-image", past)
	rejected.Failure = &generation.Failure{Class: generation.FailureRejected}
	otherSession := unstoredTask("task_elsewhere", "text-to-image", past)
	otherSession.SessionID = "session-b"
	otherOwner := unstoredTask("task_other", "text-to-image", past)
	otherOwner.OwnerID = "owner-b"
	otherKind := unstoredTask("task_3d", "text-to-3d", past)
	for name, tasks := range map[string][]generation.Task{
		"none":             nil,
		"not metered":      {notMetered},
		"other class":      {rejected},
		"other session":    {otherSession},
		"other owner":      {otherOwner},
		"other kind":       {otherKind},
		"succeeded so far": {{ID: "task_ok", OwnerID: "owner-a", SessionID: "session-a", Capability: "text-to-image", State: generation.TaskSucceeded, CreatedAt: past}},
	} {
		t.Run(name, func(t *testing.T) {
			_, gateway, recharge := runRechargeCase(t, "auto", "refine_image", "text-to-image", &sessionTasks{tasks: tasks})
			if recharge != "" || gateway.submitCount != 1 {
				t.Fatalf("the guard fired without a charged unstored failure: recharge=%q submits=%d", recharge, gateway.submitCount)
			}
		})
	}
}

func TestRechargeGuardClearsOnceTheFailureWasAnswered(t *testing.T) {
	failedAt := time.Now().Add(-time.Hour)
	failed := unstoredTask("task_old", "text-to-3d", failedAt)
	// A same-kind Task started after the failure was known: the person retried it,
	// ran it from the workspace, or approved the card this guard forced.
	later := generation.Task{ID: "task_new", OwnerID: "owner-a", SessionID: "session-a", Capability: "image-to-3d",
		State: generation.TaskRunning, CreatedAt: failedAt.Add(time.Minute)}
	_, gateway, recharge := runRechargeCase(t, "auto", "create_model_3d_from_image", "image-to-3d",
		&sessionTasks{tasks: []generation.Task{failed, later}})
	if recharge != "" || gateway.submitCount != 1 {
		t.Fatalf("a later same-kind task did not clear the guard: recharge=%q submits=%d", recharge, gateway.submitCount)
	}
	// An asset recovery answers the failure only after it actually succeeded.
	// While it is queued, running or materializing, or after it failed or was
	// canceled, the result is still not in hand and the repeat still waits.
	recovery := generation.Task{ID: "task_recovery", OwnerID: "owner-a", SessionID: "session-a", Capability: schema.AssetRecovery,
		State: generation.TaskSucceeded, CreatedAt: failedAt.Add(time.Minute)}
	recoveries := map[generation.TaskID]generation.TaskID{"task_recovery": "task_old"}
	for _, state := range []generation.TaskState{
		generation.TaskQueued, generation.TaskRunning, generation.TaskMaterializing,
		generation.TaskFailed, generation.TaskCanceled,
	} {
		outstanding := recovery
		outstanding.State = state
		_, gateway, recharge := runRechargeCase(t, "auto", "create_model_3d_from_image", "image-to-3d",
			&sessionTasks{tasks: []generation.Task{failed, outstanding}, recovered: recoveries})
		if recharge != "task_old" || gateway.submitCount != 0 {
			t.Fatalf("%s asset recovery cleared the guard: recharge=%q submits=%d", state, recharge, gateway.submitCount)
		}
	}
	_, gateway, recharge = runRechargeCase(t, "auto", "create_model_3d_from_image", "image-to-3d",
		&sessionTasks{tasks: []generation.Task{failed, recovery}, recovered: recoveries})
	if recharge != "" || gateway.submitCount != 1 {
		t.Fatalf("a succeeded asset recovery did not clear the guard: recharge=%q submits=%d", recharge, gateway.submitCount)
	}
	// A same-kind Task that was already running when the failure happened (two
	// steps of one turn) answers nothing.
	parallel := later
	parallel.CreatedAt = failedAt.Add(-30 * time.Second)
	if _, _, recharge := runRechargeCase(t, "auto", "create_model_3d_from_image", "image-to-3d",
		&sessionTasks{tasks: []generation.Task{failed, parallel}}); recharge != "task_old" {
		t.Fatalf("a parallel task cleared the guard: %q", recharge)
	}
}

// An upstream refetch (#295) has the source's capability. It answers the
// failure only after it actually succeeded; while it is queued, running or
// materializing, and after it failed or was canceled, the earlier result is
// still not in hand. A refetch is free (#295), so it is never the later
// same-kind paid decision that clears the guard.
func TestRechargeGuardClearsOnlyAfterUpstreamRefetchSucceeded(t *testing.T) {
	failedAt := time.Now().Add(-time.Hour)
	failed := unstoredTask("task_old", "text-to-3d", failedAt)
	refetch := generation.Task{ID: "task_refetch", OwnerID: "owner-a", SessionID: "session-a", Capability: "text-to-3d",
		State: generation.TaskMaterializing, CreatedAt: failedAt.Add(time.Minute), Offering: generation.Offering{ID: "meshy/fake"}}
	bindings := map[generation.TaskID]generation.TaskID{"task_refetch": "task_old"}
	for _, state := range []generation.TaskState{
		generation.TaskQueued, generation.TaskRunning, generation.TaskMaterializing,
		generation.TaskFailed, generation.TaskCanceled,
	} {
		outstanding := refetch
		outstanding.State = state
		_, gateway, recharge := runRechargeCase(t, "auto", "create_model_3d", "text-to-3d",
			&sessionTasks{tasks: []generation.Task{failed, outstanding}, refetches: bindings})
		if recharge != "task_old" || gateway.submitCount != 0 {
			t.Fatalf("%s refetch cleared the guard: recharge=%q submits=%d", state, recharge, gateway.submitCount)
		}
	}
	succeeded := refetch
	succeeded.State = generation.TaskSucceeded
	_, gateway, recharge := runRechargeCase(t, "auto", "create_model_3d", "text-to-3d",
		&sessionTasks{tasks: []generation.Task{failed, succeeded}, refetches: bindings})
	if recharge != "" || gateway.submitCount != 1 {
		t.Fatalf("a succeeded refetch did not clear the guard: recharge=%q submits=%d", recharge, gateway.submitCount)
	}
}

// The decision is made once, when the step is created: a replay of the turn
// (restart, late wake-up) keeps the card and never lets the policy approve it.
func TestRechargeGuardDecisionSurvivesAReplay(t *testing.T) {
	tasks := &sessionTasks{tasks: []generation.Task{unstoredTask("task_old", "text-to-3d", time.Now().Add(-time.Hour))}}
	executor, command, gateway, repo := policyHarnessWithTasks(t, sessionMode{mode: "auto"}, "create_model_3d", "text-to-3d", nil, tasks)
	ctx := context.Background()
	if _, err := executor.Execute(ctx, command, nil); err != nil {
		t.Fatal(err)
	}
	tasks.tasks = nil // whatever the session looks like now
	replayed, err := executor.Execute(ctx, command, nil)
	if err != nil || !replayed.NoSubmission || gateway.submitCount != 0 || !strings.Contains(replayed.AgentContent, "task_old") {
		t.Fatalf("replay = %+v submits=%d err=%v", replayed, gateway.submitCount, err)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if err != nil || op.RechargeTaskID != "task_old" {
		t.Fatalf("operation = %+v err=%v", op, err)
	}
}
