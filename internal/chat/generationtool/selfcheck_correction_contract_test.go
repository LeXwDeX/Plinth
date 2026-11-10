package generationtool_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// orderedListGateway gives the session model list a deterministic order, so a
// regression in the pre-truncation sorting cannot hide behind map iteration.
type orderedListGateway struct {
	*processingGateway
	order map[generation.TaskID]int
}

func (gateway *orderedListGateway) List(ctx context.Context, scope generation.TaskListScope,
	query generation.ListQuery) (generation.TaskPage, error) {
	page, err := gateway.processingGateway.List(ctx, scope, query)
	if err != nil {
		return page, err
	}
	rank := func(id generation.TaskID) int {
		if position, found := gateway.order[id]; found {
			return position
		}
		return len(gateway.order)
	}
	sort.SliceStable(page.Tasks, func(left, right int) bool {
		return rank(page.Tasks[left].ID) < rank(page.Tasks[right].ID)
	})
	return page, nil
}

// missFirstOperationRead hides a stored operation from the turn's resume read
// once. The processing path then reaches prepareAgentOperation, whose insert
// conflict recovers the same operation; that is the path on which a bound Task
// can be observed without a submission.
type missFirstOperationRead struct {
	generationtool.OperationStore
	misses *int
}

func (store *missFirstOperationRead) LoadAgentOperation(ctx context.Context, owner, session, turn, call string) (generationtool.Operation, error) {
	if *store.misses > 0 {
		*store.misses--
		return generationtool.Operation{}, generationtool.ErrOperationNotFound
	}
	return store.OperationStore.LoadAgentOperation(ctx, owner, session, turn, call)
}

// TestBoundProcessingObservationDoesNotCountTheCorrectionAgain is the #246
// regression: once the kernel really accepted the paid step, a replay observes
// the bound Task. The observation shows the counters but must not increment
// corrections_used a second time.
func TestBoundProcessingObservationDoesNotCountTheCorrectionAgain(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	store := newReviewSelfCheckStore()
	selfChecks := chat.NewSelfCheckService(store, h.app.Chat.SelfCheck)
	h.config.SelfChecks = selfChecks
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
	ctx := context.Background()
	if _, err := selfChecks.AppendCheck(ctx, "owner-a", "session-a", "model-1", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := selfChecks.AppendCheck(ctx, "owner-a", "session-a", "model-1", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindRender, Tool: "render_model_views", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := selfChecks.RecordConclusion(ctx, "owner-a", "session-a", "model-1", "",
		chat.SelfCheckConclusion{Status: "passed", Summary: "looks right"}); err != nil {
		t.Fatal(err)
	}

	const arguments = `{"source_task_id":"model-1","prompt":"mossy stone"}`
	first := h.runCall("call-bound", "retexture_model", arguments)
	if first.Error != "" || first.TaskID == "" {
		t.Fatalf("first submission = %+v", first)
	}
	record, found, err := store.SelfCheck(ctx, "owner-a", "session-a", "model-1")
	if err != nil || !found || record.CorrectionsUsed != 1 {
		t.Fatalf("first correction = %+v found=%v err=%v", record, found, err)
	}
	block, ok := h.content(first)["self_check"].(map[string]any)
	if !ok || block["corrections_used"] != float64(1) || !strings.Contains(fmt.Sprint(block["message"]), "1 of 2 corrections remain") {
		t.Fatalf("first receipt = %v", h.content(first))
	}

	// The real kernel binds the grant when it accepts the Task. This fake has
	// no generation_tasks row, so bind the stored grant directly, exactly the
	// state a restarted replay sees.
	operation, err := h.gateway.grantGateway.repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-bound")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", h.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `UPDATE execution_grants SET status='bound', task_id=?, updated_at=?
		WHERE owner_id=? AND request_id=?`, "accepted-agent-task", time.Now().UTC().Format(time.RFC3339Nano),
		"owner-a", operation.GrantRequestID); err != nil {
		t.Fatal(err)
	}

	// Force the turn past its first resume read, so the call reaches the
	// processing path and recovers the same bound operation there. This is the
	// exact path the old unconditional increment miscounted.
	misses := 1
	h.config.Operations = &missFirstOperationRead{OperationStore: h.gateway.grantGateway.repo, misses: &misses}
	executor, err = generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
	replay := h.runCall("call-bound", "retexture_model", arguments)
	if misses != 0 {
		t.Fatalf("the resume read was not bypassed")
	}
	if replay.Error != "" || replay.TaskID != "accepted-agent-task" {
		t.Fatalf("bound observation = %+v", replay)
	}
	if h.gateway.submitCount != 1 {
		t.Fatalf("bound observation submitted again: %d", h.gateway.submitCount)
	}
	record, found, err = store.SelfCheck(ctx, "owner-a", "session-a", "model-1")
	if err != nil || !found || record.CorrectionsUsed != 1 {
		t.Fatalf("observation counted the correction again: %+v found=%v err=%v", record, found, err)
	}
	block, ok = h.content(replay)["self_check"].(map[string]any)
	if !ok || block["corrections_used"] != float64(1) {
		t.Fatalf("observation receipt = %v", h.content(replay))
	}
}

// TestSelfCheckTaskCandidatesAreOrderedBeforeTruncation is the #246
// regression: an old unchecked result must reach the prompt even when newer
// results already filled the limit. The limit applies after the unchecked-first
// ordering, not to the raw Task list.
func TestSelfCheckTaskCandidatesAreOrderedBeforeTruncation(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	store := newReviewSelfCheckStore()
	selfChecks := chat.NewSelfCheckService(store, h.app.Chat.SelfCheck)
	h.config.SelfChecks = selfChecks
	ctx := context.Background()
	now := time.Now().UTC()

	limit := h.app.Chat.SelfCheck.Context.MaxResults
	if limit <= 0 {
		t.Fatalf("the shipped Schema must bound the prompt list")
	}
	order := make([]generation.TaskID, 0, limit+1)
	for index := 0; index < limit; index++ {
		id := generation.TaskID(fmt.Sprintf("checked-%d", index))
		task := modelTask(string(id), "owner-a", "session-a", h.meshy, generation.TextTo3D, "glb")
		h.gateway.tasks[id] = task
		order = append(order, id)
	}
	// The unchecked target is the oldest and sits last in the list read.
	target := modelTask("old-unchecked", "owner-a", "session-a", h.meshy, generation.TextTo3D, "glb")
	h.gateway.tasks[target.ID] = target
	order = append(order, target.ID)

	for index, id := range order[:len(order)-1] {
		key := store.key("owner-a", "session-a", string(id))
		store.records[key] = chat.SelfCheckRecord{
			TaskID: string(id), UpdatedAt: now.Add(time.Duration(index) * time.Minute),
			Conclusion: &chat.SelfCheckConclusion{Status: "passed", Summary: "checked"},
		}
	}
	h.config.Gateway = &orderedListGateway{processingGateway: h.gateway, order: func() map[generation.TaskID]int {
		positions := make(map[generation.TaskID]int, len(order))
		for position, id := range order {
			positions[id] = position
		}
		return positions
	}()}
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}

	tasks, err := executor.SessionModelTasks(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != limit {
		t.Fatalf("tasks = %d, want the declared limit %d", len(tasks), limit)
	}
	found := false
	for _, task := range tasks {
		if task.TaskID == string(target.ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the old unchecked result was truncated away: %+v", tasks)
	}
}
