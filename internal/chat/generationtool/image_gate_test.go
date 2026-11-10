package generationtool

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

type fakeGate struct {
	checks []imageinspect.Check
}

func (f fakeGate) ReviewAttachment(context.Context, string, chat.Attachment, string, string) (imageinspect.Report, []imageinspect.Check, error) {
	return imageinspect.Report{Width: 100, Height: 100}, f.checks, nil
}

func gateExecutor(reviewer ImageReviewer) *Executor {
	return &Executor{
		imageReview: reviewer,
		logger:      slog.New(slog.DiscardHandler),
		chat: catalog.ChatSchema{ImageReview: catalog.ChatImageReviewSchema{
			OverrideArgument: "input_override_reason",
			RefusedTemplate:  "not a 3D source: {reasons}; use {override_argument} to proceed",
		}},
	}
}

func gateCommand(arguments string) chat.ToolCommand {
	return chat.ToolCommand{
		Call: agents.ToolCall{ID: "call_1", Arguments: []byte(arguments)},
		History: []chat.Message{{
			Role: "assistant", ImageURLs: []string{"/api/v1/tasks/t/assets/a"},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage,
				TaskID: "task_1", AssetID: "a1", ImageSlot: 1}},
		}},
	}
}

func TestReview3DInputRefusesAFailedPicture(t *testing.T) {
	executor := gateExecutor(fakeGate{checks: []imageinspect.Check{
		{Name: "background", Status: imageinspect.StatusFail, Actual: "alpha=false, spread 80"},
	}})
	_, refusal := executor.review3DInput(context.Background(), gateCommand(`{}`),
		[]string{"/api/v1/tasks/t/assets/a"})
	if !strings.Contains(refusal, "@img1 background") || !strings.Contains(refusal, "input_override_reason") {
		t.Fatalf("refusal = %q", refusal)
	}
}

func TestReview3DInputAcceptsAnOverrideReason(t *testing.T) {
	executor := gateExecutor(fakeGate{checks: []imageinspect.Check{
		{Name: "background", Status: imageinspect.StatusFail, Actual: "alpha=false"},
	}})
	override, refusal := executor.review3DInput(context.Background(),
		gateCommand(`{"input_override_reason":"the person asked to keep the photo background"}`),
		[]string{"/api/v1/tasks/t/assets/a"})
	if refusal != "" || !strings.Contains(override, "photo background") {
		t.Fatalf("override = %q, refusal = %q", override, refusal)
	}
}

func TestReview3DInputPassesACleanPicture(t *testing.T) {
	executor := gateExecutor(fakeGate{checks: []imageinspect.Check{
		{Name: "background", Status: imageinspect.StatusPass},
	}})
	override, refusal := executor.review3DInput(context.Background(), gateCommand(`{}`),
		[]string{"/api/v1/tasks/t/assets/a"})
	if refusal != "" || override != "" {
		t.Fatalf("override = %q, refusal = %q", override, refusal)
	}
}

func TestGateInputsPairsTheURLWithItsAttachment(t *testing.T) {
	inputs := gateInputs(gateCommand(`{}`), []string{"/api/v1/tasks/t/assets/a", "data:image/png;base64,AA"})
	if len(inputs) != 2 || inputs[0].attachment.Handle != "@img1" || inputs[1].attachment.Handle != "" {
		t.Fatalf("inputs = %+v", inputs)
	}
}