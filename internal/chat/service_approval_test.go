package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestServiceApprovalModeDefaultsToManualAndSwitchesPerOwner(t *testing.T) {
	ctx := context.Background()
	service := newChatService(t, newMemoryChatRepository())
	session, err := service.CreateSession(ctx, "owner-a", "Owner A", "")
	if err != nil {
		t.Fatal(err)
	}
	if session.ApprovalMode != "manual" {
		t.Fatalf("new session mode = %q, want manual", session.ApprovalMode)
	}
	mode, err := service.SessionApprovalMode(ctx, "owner-a", session.ID)
	if err != nil || mode != "manual" {
		t.Fatalf("mode = %q err=%v", mode, err)
	}
	updated, previous, err := service.SetApprovalMode(ctx, "owner-a", session.ID, "auto")
	if err != nil || updated.ApprovalMode != "auto" || previous != "manual" {
		t.Fatalf("switch: %+v previous=%q err=%v", updated, previous, err)
	}
	same, previous, err := service.SetApprovalMode(ctx, "owner-a", session.ID, "auto")
	if err != nil || same.ApprovalMode != "auto" || previous != "auto" {
		t.Fatalf("repeat: %+v previous=%q err=%v", same, previous, err)
	}
	if _, _, err := service.SetApprovalMode(ctx, "owner-a", session.ID, "yolo"); !errors.Is(err, chat.ErrInvalidApprovalMode) {
		t.Fatalf("invalid mode: %v", err)
	}
	if _, _, err := service.SetApprovalMode(ctx, "owner-b", session.ID, "manual"); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	if _, err := service.SessionApprovalMode(ctx, "owner-b", session.ID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign owner read: %v", err)
	}
	if got, _ := service.SessionApprovalMode(ctx, "owner-a", session.ID); got != "auto" {
		t.Fatalf("mode after rejected changes = %q", got)
	}
}

func TestServiceNormalizeApprovalModeFailsClosed(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	for _, mode := range []string{"", "bogus", "AUTO"} {
		if got := service.NormalizeApprovalMode(mode); got != "manual" {
			t.Fatalf("NormalizeApprovalMode(%q) = %q", mode, got)
		}
	}
	if got := service.NormalizeApprovalMode("auto"); got != "auto" {
		t.Fatalf("auto = %q", got)
	}
}
