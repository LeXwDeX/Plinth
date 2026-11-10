package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type promptSelfCheckStore struct {
	records map[string]SelfCheckRecord
}

func (store *promptSelfCheckStore) ListSelfChecks(_ context.Context, owner, session string) ([]SelfCheckRecord, error) {
	records := make([]SelfCheckRecord, 0, len(store.records))
	for key, record := range store.records {
		if strings.HasPrefix(key, owner+"\x00"+session+"\x00") {
			records = append(records, record)
		}
	}
	return records, nil
}

func (store *promptSelfCheckStore) SelfCheck(_ context.Context, owner, session, taskID string) (SelfCheckRecord, bool, error) {
	record, found := store.records[owner+"\x00"+session+"\x00"+taskID]
	return record, found, nil
}

func (store *promptSelfCheckStore) SaveSelfCheck(_ context.Context, owner, session string,
	record SelfCheckRecord, _ int64) (SelfCheckRecord, bool, error) {
	if store.records == nil {
		store.records = map[string]SelfCheckRecord{}
	}
	store.records[owner+"\x00"+session+"\x00"+record.TaskID] = record
	return record, false, nil
}

// promptOnlyRepository answers the ownership check; the embedded nil
// interface panics if a method the prompt does not use is called.
type promptOnlyRepository struct {
	Repository
	owned bool
}

func (repository promptOnlyRepository) OwnsSession(context.Context, string, string) (bool, error) {
	return repository.owned, nil
}

func testCatalogChatSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Chat
}

func selfCheckPromptFixture(t *testing.T, schema catalog.ChatSchema) (*Orchestrator, *promptSelfCheckStore) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	store := &promptSelfCheckStore{records: map[string]SelfCheckRecord{}}
	service := &Service{
		repository: promptOnlyRepository{owned: true},
		selfChecks: NewSelfCheckService(store, schema.SelfCheck),
	}
	orchestrator := &Orchestrator{schema: schema, service: service, texts: texts}
	return orchestrator, store
}

func TestSelfCheckPromptListsEveryFinishedResultAndTheUncheckedRule(t *testing.T) {
	schema := testCatalogChatSchema(t)
	orchestrator, store := selfCheckPromptFixture(t, schema)
	now := time.Now().UTC()
	store.records["owner-a\x00session-a\x00task-a"] = SelfCheckRecord{
		TaskID: "task-a", UpdatedAt: now,
		Checks: []SelfCheckCheck{
			{Kind: SelfCheckKindInspect, Tool: "inspect_model", OK: true},
			{Kind: SelfCheckKindRender, Tool: "render_model_views", OK: true},
		},
		Conclusion: &SelfCheckConclusion{Status: "passed", Summary: "matches the reference"},
	}
	orchestrator.selfCheckTasks = func(context.Context, string, string) ([]SelfCheckTask, error) {
		return []SelfCheckTask{
			{TaskID: "task-a", Model: "meshy-6", ResultAt: now},
			{TaskID: "task-b", Model: "tripo-3", ResultAt: now.Add(time.Minute), Background: true},
		}, nil
	}
	history := []Message{{ToolCalls: []ToolCall{{TaskID: "task-b", Background: true}}}}
	prompt := orchestrator.selfCheckPrompt(context.Background(), "owner-a", "session-a", history)
	if prompt == "" {
		t.Fatal("no self-check block")
	}
	passedStatus, _ := SelfCheckStatusByRole(schema.SelfCheck, SelfCheckRolePassed)
	uncheckedStatus, _ := SelfCheckStatusByRole(schema.SelfCheck, SelfCheckRoleUnchecked)
	for _, needle := range []string{"task-a", "task-b", passedStatus.Label, uncheckedStatus.Label,
		schema.SelfCheck.Context.BackgroundText, "不得说成已检查"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt lacks %q:\n%s", needle, prompt)
		}
	}
	if !strings.Contains(prompt, "1/") || !strings.Contains(prompt, schema.SelfCheck.Context.UncheckedRuleText) {
		t.Fatalf("prompt lacks counters or the rule:\n%s", prompt)
	}
}

func TestSelfCheckPromptKeepsUncheckedResultsWithinTheLimits(t *testing.T) {
	schema := testCatalogChatSchema(t)
	schema.SelfCheck.Context.MaxResults = 1
	orchestrator, store := selfCheckPromptFixture(t, schema)
	now := time.Now().UTC()
	store.records["owner-a\x00session-a\x00task-a"] = SelfCheckRecord{
		TaskID: "task-a", UpdatedAt: now.Add(time.Hour),
		Conclusion: &SelfCheckConclusion{Status: "passed", Summary: "passed recently"},
	}
	orchestrator.selfCheckTasks = func(context.Context, string, string) ([]SelfCheckTask, error) {
		return []SelfCheckTask{
			{TaskID: "task-a", ResultAt: now.Add(time.Hour)},
			{TaskID: "task-b", ResultAt: now.Add(-time.Hour)},
		}, nil
	}
	prompt := orchestrator.selfCheckPrompt(context.Background(), "owner-a", "session-a", nil)
	if !strings.Contains(prompt, "task-b") || strings.Contains(prompt, "task-a") {
		t.Fatalf("the unchecked result must survive the limit:\n%s", prompt)
	}
}

func TestSelfCheckPromptIsIndependentOfTheMessageWindow(t *testing.T) {
	schema := testCatalogChatSchema(t)
	orchestrator, store := selfCheckPromptFixture(t, schema)
	store.records["owner-a\x00session-a\x00task-a"] = SelfCheckRecord{
		TaskID: "task-a", UpdatedAt: time.Now().UTC(),
		Conclusion: &SelfCheckConclusion{Status: "has_issues", Summary: "cracked corner", Remaining: []string{"crack"}},
	}
	orchestrator.selfCheckTasks = func(context.Context, string, string) ([]SelfCheckTask, error) {
		return []SelfCheckTask{{TaskID: "task-a"}}, nil
	}
	// An empty history is what remains after a context compaction; the record
	// comes from the store, so the block and the conclusion survive.
	prompt := orchestrator.selfCheckPrompt(context.Background(), "owner-a", "session-a", nil)
	if !strings.Contains(prompt, "task-a") || !strings.Contains(prompt, "cracked corner") {
		t.Fatalf("record lost without history:\n%s", prompt)
	}
}

func TestSelfCheckPromptIsEmptyWithoutTasksOrSchema(t *testing.T) {
	schema := testCatalogChatSchema(t)
	orchestrator, _ := selfCheckPromptFixture(t, schema)
	orchestrator.selfCheckTasks = func(context.Context, string, string) ([]SelfCheckTask, error) {
		return nil, nil
	}
	if prompt := orchestrator.selfCheckPrompt(context.Background(), "owner-a", "session-a", nil); prompt != "" {
		t.Fatalf("empty task list produced %q", prompt)
	}
	orchestrator.selfCheckTasks = nil
	if prompt := orchestrator.selfCheckPrompt(context.Background(), "owner-a", "session-a", nil); prompt != "" {
		t.Fatalf("no task source produced %q", prompt)
	}
}
