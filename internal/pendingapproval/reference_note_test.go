package pendingapproval_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

const imageTo3DTool = "create_model_3d_from_image"

// referenceLimitNote is the one Schema text (#329 note 99647) for a model
// that takes one picture, and its English copy.
func referenceLimitNote(t *testing.T) (string, string) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	template := product.Application().Runtime.GenerationMessages.ReferenceLimitOne
	english := englishFields(t)["runtime.generation_messages.reference_limit_one"]
	if template == "" || english == "" || english == template {
		t.Fatalf("no one-picture reference limit text and English copy: %q %q", template, english)
	}
	return template, english
}

func fillNote(template, model string, count, total string) string {
	return strings.NewReplacer("{model}", model, "{count}", count, "{total}", total).Replace(template)
}

// #329 E (dev-test 0518727d): the approval card listed only the first of the
// two pictures the person gave Meshy 7.1, and nothing said why. The card now
// says, in the Schema's words and the reader's language, that only the first
// was used of the two given.
func TestTheApprovalCardSaysOnlyTheFirstPictureIsUsed(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "card.db"), time.Hour)
	template, english := referenceLimitNote(t)
	w.tool, w.referencesUsed, w.referencesGiven = imageTo3DTool, 1, 2
	w.pending("call-cut")
	w.referencesUsed, w.referencesGiven = 0, 0
	w.pending("call-whole")

	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 2 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	notes := map[string]string{}
	for _, item := range items {
		notes[item.MessageID] = item.ReferenceNote
	}
	if want := fillNote(template, "Fake Model", "1", "2"); notes["turn-call-cut"] != want {
		t.Fatalf("card note = %q, want %q", notes["turn-call-cut"], want)
	}
	if notes["turn-call-whole"] != "" {
		t.Fatalf("a step that uses every picture got a note: %q", notes["turn-call-whole"])
	}

	// The card reads in the language of the person looking at it.
	items, err = w.service.List(catalog.WithLocale(context.Background(), "en"), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.MessageID == "turn-call-cut" && item.ReferenceNote != fillNote(english, "Fake Model", "1", "2") {
			t.Fatalf("English card note = %q", item.ReferenceNote)
		}
	}
}

// After the approval, the message that reports the finished model says the
// same, so the conversation itself records which picture was used.
func TestTheFinishedResultSaysOnlyTheFirstPictureIsUsed(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "result.db"), time.Hour)
	template, englishNote := referenceLimitNote(t)
	messages := w.schema.PendingApprovals.Messages
	if !strings.Contains(messages.SucceededWithNote, "{note}") {
		t.Fatalf("pending_approvals.messages.succeeded_with_note = %q", messages.SucceededWithNote)
	}
	finish := func(callID string) string {
		t.Helper()
		_, grant := w.pending(callID)
		w.gateway.state = generation.TaskQueued
		outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
		if err != nil || !handled {
			t.Fatalf("approve %s = %+v %v %v", callID, outcome, handled, err)
		}
		if started, _ := w.message(outcome.MessageID); strings.Contains(started.Content, "{note}") {
			t.Fatalf("started = %q", started.Content)
		}
		w.gateway.state = generation.TaskSucceeded
		if _, err := w.service.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		result, ok := w.message(outcome.MessageID)
		if !ok {
			t.Fatalf("%s: no result message", callID)
		}
		return result.Content
	}

	w.tool, w.referencesUsed, w.referencesGiven = imageTo3DTool, 1, 2
	note := fillNote(template, "Fake Model", "1", "2")
	want := strings.NewReplacer("{model}", "Fake Model", "{note}", note).Replace(messages.SucceededWithNote)
	if got := finish("call-cut"); got != want || strings.Count(got, "Fake Model") != 1 {
		t.Fatalf("result = %q, want %q naming the model once", got, want)
	}

	w.referencesUsed, w.referencesGiven = 0, 0
	plain := strings.NewReplacer("{model}", "Fake Model").Replace(messages.Succeeded)
	if got := finish("call-whole"); got != plain {
		t.Fatalf("a step that used every picture = %q, want %q", got, plain)
	}

	// The English turn reads the English sentence.
	english := englishFields(t)
	w.locale = "en"
	w.referencesUsed, w.referencesGiven = 1, 3
	note = fillNote(englishNote, "Fake Model", "1", "3")
	want = strings.NewReplacer("{model}", "Fake Model", "{note}", note).Replace(english["chat.pending_approvals.messages.succeeded_with_note"])
	if got := finish("call-en"); got != want || strings.ContainsAny(got, "张图") || strings.Count(got, "Fake Model") != 1 {
		t.Fatalf("English result = %q, want %q", got, want)
	}
}

// #329 note 99647: a task frozen before the reference-limit text changed
// reports, once finished, in the current Schema text, as its card did.
func TestTheFinishedResultUsesTheCurrentReferenceLimitText(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "frozen.db"), time.Hour)
	template, _ := referenceLimitNote(t)
	w.gateway.frozenMessages = &generation.ExecutionMessages{ReferenceLimit: "{model} 旧文案 {count}/{total}"}
	w.tool, w.referencesUsed, w.referencesGiven = imageTo3DTool, 1, 2
	_, grant := w.pending("call-frozen")
	w.gateway.state = generation.TaskQueued
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil || !handled {
		t.Fatalf("approve = %+v %v %v", outcome, handled, err)
	}
	w.gateway.state = generation.TaskSucceeded
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, ok := w.message(outcome.MessageID)
	note := fillNote(template, "Fake Model", "1", "2")
	want := strings.NewReplacer("{note}", note).Replace(w.schema.PendingApprovals.Messages.SucceededWithNote)
	if !ok || result.Content != want {
		t.Fatalf("result = %q, want %q", result.Content, want)
	}
}

// #80: a frozen front source is the only fact that may produce the three-view
// sentence on the card and in the finished message. An operation from before
// the fact was kept makes no source claim at all.
func TestImageTo3DApprovalUsesTheFrozenFrontSource(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "front.db"), time.Hour)
	w.tool, w.capability = imageTo3DTool, generation.ImageTo3D
	w.referenceSource = generationtool.ReferenceSourceFact{Version: 1, Selected: 2, Resolved: 1, Frozen: 1,
		Replaced: true, AllFront: true, Prefix: false, FrontParameter: "reference_image", FrontIndex: 0,
		FrontTaskID: "task-front", FrontAssetID: "asset-front", Identity: "front-source"}
	_, grant := w.pending("call-front")
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	frontNote := w.schema.ThreeView.ModelSourceNote
	if items[0].ReferenceNote != frontNote {
		t.Fatalf("card note = %q, want the front source %q", items[0].ReferenceNote, frontNote)
	}

	w.gateway.state = generation.TaskQueued
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil || !handled {
		t.Fatalf("approve = %+v %v %v", outcome, handled, err)
	}
	w.gateway.state = generation.TaskSucceeded
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, ok := w.message(outcome.MessageID)
	want := strings.NewReplacer("{model}", "Fake Model", "{note}", frontNote).Replace(w.schema.PendingApprovals.Messages.SucceededWithNote)
	if !ok || result.Content != want {
		t.Fatalf("result = %q, want %q", result.Content, want)
	}
}

func TestImageTo3DWithoutAFrozenSourceMakesNoClaim(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "legacy.db"), time.Hour)
	w.tool, w.capability = imageTo3DTool, generation.ImageTo3D
	w.referencesUsed, w.referencesGiven = 1, 2
	_, grant := w.pending("call-legacy")
	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 || items[0].ReferenceNote != "" {
		t.Fatalf("legacy card = %+v err=%v", items, err)
	}

	w.gateway.state = generation.TaskQueued
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID, executiongrant.Approve)
	if err != nil || !handled {
		t.Fatalf("approve = %+v %v %v", outcome, handled, err)
	}
	w.gateway.state = generation.TaskSucceeded
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, ok := w.message(outcome.MessageID)
	want := strings.NewReplacer("{model}", "Fake Model").Replace(w.schema.PendingApprovals.Messages.Succeeded)
	if !ok || result.Content != want {
		t.Fatalf("legacy result = %q, want %q", result.Content, want)
	}
}

func TestImageTo3DSourceNoteSurvivesRestarts(t *testing.T) {
	for _, known := range []bool{true, false} {
		name := "legacy"
		if known {
			name = "frozen-front"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "restart.db")
			first := openWorld(t, path, time.Hour)
			first.tool, first.capability = imageTo3DTool, generation.ImageTo3D
			first.referencesUsed, first.referencesGiven = 1, 2
			if known {
				first.referenceSource = generationtool.ReferenceSourceFact{Version: 1, Selected: 2, Resolved: 1, Frozen: 1,
					Replaced: true, AllFront: true, FrontParameter: "reference_image", FrontIndex: 0,
					FrontTaskID: "task-front", FrontAssetID: "asset-front", Identity: "front-source"}
			}
			_, grant := first.pending("call-restart")
			if err := first.repo.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := openWorld(t, path, time.Hour)
			note := ""
			if known {
				note = reopened.schema.ThreeView.ModelSourceNote
				if note == "" {
					t.Fatal("schema has no three-view source note")
				}
			}
			items, err := reopened.service.List(ctx, "owner-a", "session-a")
			if err != nil || len(items) != 1 || items[0].RequestID != grant.RequestID || items[0].ReferenceNote != note {
				t.Fatalf("card after restart = %+v, want note %q, err=%v", items, note, err)
			}
			if _, err := reopened.service.List(ctx, "owner-b", "session-a"); !errors.Is(err, pendingapproval.ErrSessionNotFound) {
				t.Fatalf("foreign list after restart err=%v", err)
			}
			if reopened.gateway.submits != 0 || reopened.gateway.distinctTasks() != 0 {
				t.Fatal("reading pending approvals created a task")
			}
			outcome, handled, err := reopened.service.Decide(ctx, reopened.operator, grant.RequestID, executiongrant.Approve)
			if err != nil || !handled || outcome.TaskID == "" {
				t.Fatalf("approve after restart = %+v %v %v", outcome, handled, err)
			}
			if err := reopened.repo.Close(); err != nil {
				t.Fatal(err)
			}

			again := openWorld(t, path, time.Hour)
			again.gateway.state = generation.TaskSucceeded
			again.gateway.tasks = reopened.gateway.tasks
			if settled, err := again.service.Sweep(ctx); err != nil || settled != 1 {
				t.Fatalf("sweep after second restart settled=%d err=%v", settled, err)
			}
			template := again.schema.PendingApprovals.Messages.Succeeded
			if known {
				template = again.schema.PendingApprovals.Messages.SucceededWithNote
			}
			want := strings.NewReplacer("{model}", "Fake Model", "{note}", note).Replace(template)
			result, ok := again.message(outcome.MessageID)
			if !ok || result.Content != want {
				t.Fatalf("result after second restart = %q, want %q (found=%v)", result.Content, want, ok)
			}
		})
	}
}
