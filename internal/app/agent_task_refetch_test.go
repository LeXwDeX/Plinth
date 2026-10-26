package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// fakeRefetcher is the kernel's upstream refetch (#295) for the Agent tools.
type fakeRefetcher struct {
	fakeAgentTaskReader
	view     generation.RefetchEligibility
	result   generation.RefetchResult
	err      error
	requests int
}

func (f *fakeRefetcher) RefetchEligibility(context.Context, generation.OwnerID, generation.TaskID) (generation.RefetchEligibility, error) {
	return f.view, nil
}

func (f *fakeRefetcher) RefetchUpstreamResult(_ context.Context, _ generation.OwnerID, _ generation.TaskID, by generation.RefetchRequester) (generation.RefetchResult, error) {
	f.requests++
	if by != generation.RefetchByAgent {
		return generation.RefetchResult{}, errors.New("wrong requester")
	}
	return f.result, f.err
}

func TestAgentTaskToolsOfferAndStartTheNoChargeRefetch(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := c.Application().Chat
	messages := c.Application().Content.Messages
	failed := generation.Task{ID: "task_old", OwnerID: "owner_a", SessionID: "session_1", Capability: "text-to-3d",
		State: generation.TaskFailed, Offering: generation.Offering{ID: "meshy/x", CreditsMetered: true},
		Failure: &generation.Failure{Class: generation.FailureIntegrity, Retryable: true}}
	ctx := context.Background()

	// get_task tells whether the result can be fetched again, and why not.
	tasks := &fakeRefetcher{fakeAgentTaskReader: fakeAgentTaskReader{task: failed}, view: generation.RefetchEligibility{SourceTaskID: "task_old", Eligible: true}}
	raw, err := newAgentTaskReader(tasks, chatSchema, messages, nil)(ctx, "owner_a", "task_old")
	if err != nil || !strings.Contains(string(raw), `"result_recovery":{"available":true}`) {
		t.Fatalf("get_task = %s, %v", raw, err)
	}
	tasks.view = generation.RefetchEligibility{SourceTaskID: "task_old", Reason: generation.RefetchExpired}
	raw, _ = newAgentTaskReader(tasks, chatSchema, messages, nil)(ctx, "owner_a", "task_old")
	if !strings.Contains(string(raw), `"available":false`) || !strings.Contains(string(raw), chatSchema.Agent.RefetchUnavailableContent["expired"]) {
		t.Fatalf("expired get_task = %s", raw)
	}
	tasks.view = generation.RefetchEligibility{SourceTaskID: "task_old", Reason: generation.RefetchNotApplicable}
	raw, _ = newAgentTaskReader(tasks, chatSchema, messages, nil)(ctx, "owner_a", "task_old")
	if strings.Contains(string(raw), "result_recovery") {
		t.Fatalf("nothing to fetch again but get_task offers it: %s", raw)
	}

	recoverTask := newAgentTaskRecoverer(tasks, chatSchema, messages, nil)
	// Only a Task of the command's own session.
	if _, err := recoverTask(ctx, "owner_a", "session_other", "task_old"); err == nil || tasks.requests != 0 {
		t.Fatalf("another session's task was recovered: %v requests=%d", err, tasks.requests)
	}
	tasks.result = generation.RefetchResult{TaskID: "task_refetch", Refetch: generation.RefetchEligibility{RefetchTaskID: "task_refetch", RefetchState: generation.TaskQueued}}
	raw, err = recoverTask(ctx, "owner_a", "session_1", "task_old")
	started := strings.ReplaceAll(chatSchema.Agent.RefetchStartedContent, "{task_id}", "task_refetch")
	if err != nil || !strings.Contains(string(raw), `"recovery_task_id":"task_refetch"`) || !strings.Contains(string(raw), `"started":true`) ||
		!strings.Contains(string(raw), started) {
		t.Fatalf("recover = %s, %v", raw, err)
	}
	tasks.result = generation.RefetchResult{Refetch: generation.RefetchEligibility{Reason: generation.RefetchNotSupported}}
	tasks.err = &generation.Error{Code: generation.ErrConflict}
	raw, err = recoverTask(ctx, "owner_a", "session_1", "task_old")
	if err != nil || !strings.Contains(string(raw), `"started":false`) || !strings.Contains(string(raw), chatSchema.Agent.RefetchUnavailableContent["not_supported"]) {
		t.Fatalf("unsupported recover = %s, %v", raw, err)
	}

	// A refetch that repeated the source's failure closes it; the Agent gets
	// the Schema reason with the public text of the failure class as the cause.
	repeated := generation.RefetchEligibility{SourceTaskID: "task_old", Reason: generation.RefetchRepeatedFailure, FailureClass: generation.FailureIntegrity}
	cause := messages["failure.integrity_failure"]
	want := strings.ReplaceAll(chatSchema.Agent.RefetchUnavailableContent["repeated_failure"], "{cause}", cause)
	if cause == "" || want == chatSchema.Agent.RefetchUnavailableContent["repeated_failure"] {
		t.Fatalf("repeated_failure text must name its cause: %q / %q", cause, want)
	}
	tasks.view = repeated
	raw, _ = newAgentTaskReader(tasks, chatSchema, messages, nil)(ctx, "owner_a", "task_old")
	var read struct {
		ResultRecovery *agentResultRecoveryView `json:"result_recovery"`
	}
	if err := json.Unmarshal(raw, &read); err != nil || read.ResultRecovery == nil || read.ResultRecovery.Available || read.ResultRecovery.Reason != want {
		t.Fatalf("repeated get_task = %s, %v", raw, err)
	}
	tasks.result = generation.RefetchResult{Refetch: repeated}
	raw, err = recoverTask(ctx, "owner_a", "session_1", "task_old")
	var recovered map[string]any
	if err != nil || json.Unmarshal(raw, &recovered) != nil || recovered["started"] != false || recovered["reason"] != want {
		t.Fatalf("repeated recover = %s, %v", raw, err)
	}
}

// #330 second follow-ups, observation 4: after a refetch delivered, the
// eligibility said eligible:true with refetch_state SUCCEEDED, so an Agent
// reading only available could offer another refetch. get_task now says it is
// not available and names the recovered Task; recover_task_result reports the
// recovered Task instead of "started".
func TestAgentSeesADeliveredRefetchAsDone330(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := c.Application().Chat
	messages := c.Application().Content.Messages
	text := chatSchema.Agent.RefetchUnavailableContent["delivered"]
	if !strings.Contains(text, "{task_id}") {
		t.Fatalf("delivered text must name the recovered task: %q", text)
	}
	failed := generation.Task{ID: "task_old", OwnerID: "owner_a", SessionID: "session_1", Capability: "format-conversion",
		State: generation.TaskFailed, Offering: generation.Offering{ID: "meshy/processing/convert", CreditsMetered: true},
		Failure: &generation.Failure{Class: generation.FailureIntegrity, Retryable: true}}
	delivered := generation.RefetchEligibility{SourceTaskID: "task_old", Reason: generation.RefetchDelivered, RefetchTaskID: "task_refetch", RefetchState: generation.TaskSucceeded}
	tasks := &fakeRefetcher{fakeAgentTaskReader: fakeAgentTaskReader{task: failed}, view: delivered}
	ctx := context.Background()
	raw, err := newAgentTaskReader(tasks, chatSchema, messages, nil)(ctx, "owner_a", "task_old")
	var read struct {
		ResultRecovery *agentResultRecoveryView `json:"result_recovery"`
	}
	want := strings.ReplaceAll(text, "{task_id}", "task_refetch")
	if err != nil || json.Unmarshal(raw, &read) != nil || read.ResultRecovery == nil || read.ResultRecovery.Available ||
		read.ResultRecovery.RecoveryTaskID != "task_refetch" || read.ResultRecovery.Reason != want {
		t.Fatalf("get_task after a delivered refetch = %s, %v", raw, err)
	}
	tasks.result = generation.RefetchResult{TaskID: "task_refetch", Replayed: true, Refetch: delivered}
	raw, err = newAgentTaskRecoverer(tasks, chatSchema, messages, nil)(ctx, "owner_a", "session_1", "task_old")
	var recovered map[string]any
	if err != nil || json.Unmarshal(raw, &recovered) != nil || recovered["started"] != false ||
		recovered["recovery_task_id"] != "task_refetch" || recovered["reason"] != want {
		t.Fatalf("recover after a delivered refetch = %s, %v", raw, err)
	}
}
