package pendingapproval_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

func englishFields(t *testing.T) map[string]string {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Localization.Bundles["en"].Fields
}

// What the service posts into a conversation reads in the language of the
// turn that asked for the approval, also when the background sweep posts it.
func TestPostedMessagesFollowTheTurnLocale(t *testing.T) {
	english := englishFields(t)
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Second)
	w.locale = "en"
	_, denied := w.pending("call-denied-en")
	outcome, handled, err := w.service.Decide(context.Background(), w.operator, denied.RequestID, executiongrant.Deny)
	if err != nil || !handled {
		t.Fatalf("deny = %+v %v %v", outcome, handled, err)
	}
	if message, ok := w.message(outcome.MessageID); !ok || message.Content != english["chat.pending_approvals.messages.denied"] {
		t.Fatalf("denial = %q ok=%v", message.Content, ok)
	}

	_, expiring := w.pending("call-expiring-en")
	time.Sleep(1100 * time.Millisecond)
	if _, err := w.service.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if message, ok := w.message(pendingapproval.MessageID(expiring.RequestID)); !ok || message.Content != english["chat.pending_approvals.messages.expired"] {
		t.Fatalf("expiry = %q ok=%v", message.Content, ok)
	}

	// A turn in the default locale keeps the Schema's own copy.
	w.locale = ""
	_, plain := w.pending("call-denied-zh")
	outcome, _, err = w.service.Decide(context.Background(), w.operator, plain.RequestID, executiongrant.Deny)
	if err != nil {
		t.Fatal(err)
	}
	if message, _ := w.message(outcome.MessageID); message.Content != w.schema.PendingApprovals.Messages.Denied {
		t.Fatalf("default denial = %q", message.Content)
	}
}

// #358: an empty frozen turn locale is the Schema default, not the language
// of the request that happens to sweep the approval. A new turn keeps its own
// frozen English when the request is Chinese. Denial and the finished report
// both use the frozen value.
func TestEmptyTurnLocaleUsesTheDefaultNotTheRequest(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "empty-locale.db"), time.Hour)
	english := englishFields(t)

	// Stored turn has no locale; the request asks for English.
	w.locale = ""
	_, plain := w.pending("call-empty-en")
	requestEnglish := catalog.WithLocale(context.Background(), "en")
	outcome, _, err := w.service.Decide(requestEnglish, w.operator, plain.RequestID, executiongrant.Deny)
	if err != nil {
		t.Fatal(err)
	}
	if message, _ := w.message(outcome.MessageID); message.Content != w.schema.PendingApprovals.Messages.Denied {
		t.Fatalf("empty turn locale got %q, want the default denial", message.Content)
	}

	// Stored turn is English; the request asks for Chinese.
	w.locale = "en"
	_, frozenEnglish := w.pending("call-en-zh")
	requestChinese := catalog.WithLocale(context.Background(), "zh-CN")
	outcome, _, err = w.service.Decide(requestChinese, w.operator, frozenEnglish.RequestID, executiongrant.Deny)
	if err != nil {
		t.Fatal(err)
	}
	if message, _ := w.message(outcome.MessageID); message.Content != english["chat.pending_approvals.messages.denied"] {
		t.Fatalf("English turn got %q", message.Content)
	}

	// The finished report follows the frozen turn too.
	op, approved := w.pending("call-report-en")
	outcome, _, err = w.service.Decide(requestChinese, w.operator, approved.RequestID, executiongrant.Approve)
	if err != nil {
		t.Fatal(err)
	}
	w.gateway.state = generation.TaskSucceeded
	if err := w.service.Continue(requestChinese, op); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("{model}", "Fake Model").Replace(english["chat.pending_approvals.messages.succeeded"])
	if message, _ := w.message(outcome.MessageID); message.Content != want {
		t.Fatalf("finished report = %q, want %q", message.Content, want)
	}
}
