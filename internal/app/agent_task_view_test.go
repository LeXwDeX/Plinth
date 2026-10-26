package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fakeAgentTaskReader struct {
	task generation.Task
}

func (reader fakeAgentTaskReader) Get(
	_ context.Context,
	owner generation.OwnerID,
	id generation.TaskID,
) (generation.Task, error) {
	if owner != reader.task.OwnerID || id != reader.task.ID {
		return generation.Task{}, errors.New("not found")
	}
	return reader.task, nil
}

func TestAgentTaskReaderProjectsPublicFactsOnly(t *testing.T) {
	task := generation.Task{
		ID:         "task_1",
		OwnerID:    "owner_a",
		SessionID:  "session_1",
		Capability: "text-to-image",
		Offering: generation.Offering{
			ID:         "openai/gpt/text-to-image",
			ProviderID: "openai",
			ModelID:    "gpt",
		},
		Input:        json.RawMessage(`{"prompt":"secret prompt"}`),
		State:        generation.TaskFailed,
		Progress:     40,
		ProviderData: json.RawMessage(`{"remote_job":"upstream-123"}`),
		Failure: &generation.Failure{
			Class:           "provider_rejected",
			Retryable:       true,
			SafeDetail:      "internal detail",
			UpstreamCode:    "UPSTREAM_CODE",
			UpstreamExcerpt: "raw upstream body",
		},
		Assets: []generation.Asset{{
			ID:         "asset_1",
			Role:       "image",
			MediaType:  "image/png",
			Size:       12,
			StorageKey: "storage/key",
		}},
	}
	read := newAgentTaskReader(fakeAgentTaskReader{task: task}, catalog.ChatSchema{}, nil, nil)

	raw, err := read(context.Background(), "owner_a", "task_1")
	if err != nil {
		t.Fatalf("read own task: %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{
		"upstream-123", "UPSTREAM_CODE", "raw upstream body",
		"internal detail", "storage/key", "secret prompt", "owner_a",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("agent task view leaks %q: %s", forbidden, text)
		}
	}
	var view agentTaskView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if view.TaskID != "task_1" || view.State != string(generation.TaskFailed) ||
		view.Failure == nil || !view.Failure.Retryable ||
		len(view.Assets) != 1 || view.Assets[0].AssetID != "asset_1" || view.Assets[0].Size != "0.02 KB" {
		t.Fatalf("view = %+v", view)
	}

	if _, err := read(context.Background(), "owner_b", "task_1"); err == nil {
		t.Fatal("cross-owner read must fail")
	}
}

func TestAgentTaskViewForbidsAutoRetryOfMeteredIntegrityFailure(t *testing.T) {
	chatSchema := catalog.ChatSchema{}
	chatSchema.Presentation.Messages.GenerationFailedMeteredByClass = map[string]string{string(generation.FailureIntegrity): "no auto retry"}
	failed := func(metered bool, class generation.FailureClass) generation.Task {
		return generation.Task{
			ID: "task_1", State: generation.TaskFailed,
			Offering: generation.Offering{CreditsMetered: metered},
			Failure:  &generation.Failure{Class: class, Retryable: true, UpstreamCode: "asset_store_rejected"},
		}
	}
	view := buildAgentTaskView(failed(true, generation.FailureIntegrity), chatSchema)
	if view.Failure.Retryable || !view.Failure.UserDecisionRequired || view.Failure.Reason != "no auto retry" {
		t.Fatalf("metered integrity failure = %+v", view.Failure)
	}
	for name, task := range map[string]generation.Task{
		"unmetered":   failed(false, generation.FailureIntegrity),
		"other class": failed(true, generation.FailureRejected),
	} {
		got := buildAgentTaskView(task, chatSchema).Failure
		if !got.Retryable || got.UserDecisionRequired || got.Reason != "" {
			t.Errorf("%s: failure = %+v", name, got)
		}
	}
}

// #358: get_task's metered no-auto-retry reason is Schema copy and follows
// the turn locale. The retry decision itself is made on the canonical text.
func TestAgentTaskViewMeteredReasonFollowsTheTurnLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	task := generation.Task{
		ID: "task_1", OwnerID: "owner_a", SessionID: "session_1",
		State:    generation.TaskFailed,
		Offering: generation.Offering{CreditsMetered: true},
		Failure:  &generation.Failure{Class: generation.FailureIntegrity, Retryable: true},
	}
	canonical := generationtool.MeteredFailureMessage(application.Chat, true, task.Failure)
	english := texts.Text("en", canonical)
	if canonical == "" || english == canonical {
		t.Fatalf("metered integrity text needs distinct default and English copy: %q / %q", canonical, english)
	}
	for _, test := range []struct{ name, locale, want string }{
		{"English", "en", english},
		{"default", "", canonical},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.locale != "" {
				ctx = catalog.WithLocale(ctx, test.locale)
			}
			read := newAgentTaskReader(fakeAgentTaskReader{task: task}, application.Chat,
				application.Content.Messages, texts)
			raw, err := read(ctx, "owner_a", "task_1")
			if err != nil {
				t.Fatal(err)
			}
			var view agentTaskView
			if err := json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}
			if view.Failure == nil || view.Failure.Reason != test.want || !view.Failure.UserDecisionRequired || view.Failure.Retryable {
				t.Fatalf("failure = %+v, want reason %q", view.Failure, test.want)
			}
		})
	}
}

// #358: the refetch note and the repeated-failure cause are Schema copy; the
// Task ID is an identifier and stays unchanged.
func TestAgentTaskRefetchReasonFollowsTheTurnLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	causeZH := application.Content.Messages["failure."+string(generation.FailureIntegrity)]
	causeEN := texts.Text("en", causeZH)
	if causeZH == "" || causeEN == causeZH {
		t.Fatalf("integrity failure text needs distinct default and English copy")
	}
	failed := generation.Task{ID: "task_old", OwnerID: "owner_a", SessionID: "session_1",
		State: generation.TaskFailed, Offering: generation.Offering{CreditsMetered: true},
		Failure: &generation.Failure{Class: generation.FailureIntegrity}}
	tasks := &fakeRefetcher{fakeAgentTaskReader: fakeAgentTaskReader{task: failed},
		view: generation.RefetchEligibility{SourceTaskID: "task_old",
			Reason: generation.RefetchRepeatedFailure, FailureClass: generation.FailureIntegrity}}
	ctx := catalog.WithLocale(context.Background(), "en")
	raw, err := newAgentTaskReader(tasks, application.Chat, application.Content.Messages, texts)(ctx, "owner_a", "task_old")
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		ResultRecovery *agentResultRecoveryView `json:"result_recovery"`
	}
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	template := texts.Text("en", application.Chat.Agent.RefetchUnavailableContent["repeated_failure"])
	want := strings.ReplaceAll(template, "{cause}", causeEN)
	if read.ResultRecovery == nil || read.ResultRecovery.Reason != want || !strings.Contains(want, causeEN) {
		t.Fatalf("refetch reason = %+v, want %q", read.ResultRecovery, want)
	}

	tasks.result = generation.RefetchResult{TaskID: "task_refetch",
		Refetch: generation.RefetchEligibility{RefetchTaskID: "task_refetch", RefetchState: generation.TaskQueued}}
	raw, err = newAgentTaskRecoverer(tasks, application.Chat, application.Content.Messages, texts)(ctx, "owner_a", "session_1", "task_old")
	if err != nil {
		t.Fatal(err)
	}
	var recovered map[string]any
	if err := json.Unmarshal(raw, &recovered); err != nil {
		t.Fatal(err)
	}
	note, _ := recovered["note"].(string)
	if !strings.Contains(note, "task_refetch") || strings.Contains(note, "\ufffd") {
		t.Fatalf("recover note = %q", note)
	}
}

type noopGenerate struct{}

func (noopGenerate) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	return chat.ToolResult{}, nil
}

// #358: recover_task_result reaches the app through the real agent tool
// executor. The note follows the locale the orchestrator wrote into the tool
// context (command.Options.Locale); an English turn and the default turn
// get different text.
func TestRecoverTaskResultUsesTheTurnLocaleThroughTheTool(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	failed := generation.Task{ID: "task_old", OwnerID: "owner_a", SessionID: "session_1",
		State: generation.TaskFailed, Offering: generation.Offering{CreditsMetered: true},
		Failure: &generation.Failure{Class: generation.FailureIntegrity}}
	tasks := &fakeRefetcher{fakeAgentTaskReader: fakeAgentTaskReader{task: failed},
		result: generation.RefetchResult{TaskID: "task_refetch",
			Refetch: generation.RefetchEligibility{RefetchTaskID: "task_refetch", RefetchState: generation.TaskQueued}}}
	toolExecutor, err := agenttools.New(agenttools.Config{
		Params:      agentparams.NewStore(),
		Generate:    noopGenerate{},
		RecoverTask: newAgentTaskRecoverer(tasks, application.Chat, application.Content.Messages, texts),
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{
		OwnerID: "owner_a", SessionID: "session_1",
		Tool: catalog.ChatToolSchema{Name: agenttools.ToolRecoverTaskResult},
		Call: agents.ToolCall{ID: "call-a", Name: agenttools.ToolRecoverTaskResult,
			Arguments: json.RawMessage(`{"task_id":"task_old"}`)},
	}
	englishNote := texts.Text("en", application.Chat.Agent.RefetchStartedContent)
	for _, test := range []struct {
		name, locale, want string
	}{
		{"English turn", "en", englishNote},
		{"default turn", "", application.Chat.Agent.RefetchStartedContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.locale != "" {
				ctx = catalog.WithLocale(ctx, test.locale)
			}
			result, err := toolExecutor.Execute(ctx, command, nil)
			if err != nil || !result.NoSubmission {
				t.Fatalf("recover = %+v, %v", result, err)
			}
			var recovered map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &recovered); err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(test.want, "{task_id}", "task_refetch")
			if recovered["note"] != want {
				t.Fatalf("note = %v, want %q", recovered["note"], want)
			}
			if test.locale == "" && recovered["note"] == strings.ReplaceAll(englishNote, "{task_id}", "task_refetch") {
				t.Fatal("default turn got the English note")
			}
		})
	}
}
