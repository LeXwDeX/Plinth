package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// receiptSchema is the fixture's three-view declaration with both started
// receipts, so a test can tell which one the tool chose.
func receiptSchema() catalog.ChatThreeViewSchema {
	return catalog.ChatThreeViewSchema{
		RecipeID: "image-three-view", ModelSourceStepKey: "front", Label: "views", MaximumListed: 5,
		ModelSourceNote: "front", DefaultSubjectType: "generic", SubjectArgument: "subject_type",
		SubjectTypes: []catalog.ChatThreeViewSubjectType{
			{ID: "generic", Label: "Generic"}, {ID: "humanoid", Label: "Humanoid"}},
		Messages: catalog.ChatThreeViewMessages{
			AgentStarted:           "waits-on-card {run} {view} {approval} {credits} {task}",
			AgentStartedContinuing: "server-continues {run} {view} {approval} {credits} {task}",
			ApprovalHuman:          "person", ApprovalPolicy: "policy", ReferenceRequired: "one image",
			AgentCompleted: "completed {run}: {views}", AgentPaused: "paused {run} {view} {reason}",
			ConsistencyRetry:  "retry {view} {attempt}/{maximum} {reasons}",
			ConsistencyFailed: "failed {view} {reasons}", SubjectInvalid: "bad {subject} {choices}",
		},
		Continuation: catalog.ChatThreeViewContinuationSchema{Enabled: true,
			Reasons: map[string]string{"budget_not_covered": "budget", "view_not_finished": "unfinished",
				"view_already_started": "busy", "consistency_retries_exhausted": "exhausted"}},
		Multiview: catalog.ChatThreeViewMultiviewSchema{ViewOrder: []string{"front", "left", "back", "right"},
			ViewSeparator: " / "},
	}
}

// #665: the front view's receipt says what happens next. Where the server goes
// on with the views (auto mode, or a plan covering them) it says so; where they
// wait on the card (strict mode, or budget mode without such a plan) it says
// that, instead of always claiming the strict behaviour.
func TestThreeViewStartedReceiptFollowsTheContinuation(t *testing.T) {
	for _, test := range []struct {
		name     string
		withPlan bool
		mode     string
		want     string
	}{
		{"auto mode", false, "auto", "server-continues"},
		{"budget mode with a covering plan", true, "budget", "server-continues"},
		// #702: strict cards the front view itself; the budget ("approve")
		// mode without a plan is the one whose views wait on the card.
		{"budget mode without a plan", false, "budget", "waits-on-card"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newContinuationFixtureWithMode(t, test.withPlan, test.mode)
			executor, command := f.toolFor(t, `{"subject_type":"humanoid"}`)
			executor.SetThreeView(receiptSchema())
			result, err := executor.Execute(context.Background(), command, func(chat.ToolObservation) error { return nil })
			if err != nil || !strings.HasPrefix(result.AgentContent, test.want) {
				t.Fatalf("receipt = %q err=%v, want %q", result.AgentContent, err, test.want)
			}
		})
	}
}
