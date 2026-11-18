package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #330: an external MCP asset session derives the internal steps mode and the
// mcp step entry; a plain session keeps its stored mode and names no entry.
func TestMCPStepSessionsDeriveStepsAndTheMCPEntry(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, nil)
	ctx := context.Background()
	mcpSession, err := service.CreateSession(ctx, "owner-a", "Owner", "mcp asset")
	if err != nil {
		t.Fatal(err)
	}
	plainSession, err := service.CreateSession(ctx, "owner-a", "Owner", "plain")
	if err != nil {
		t.Fatal(err)
	}
	service.SetMCPStepSessions(stepMCPAssets{mark: map[string]bool{mcpSession.ID: true}})

	mode, err := service.SessionApprovalMode(ctx, "owner-a", mcpSession.ID)
	if err != nil || mode != approvalpolicy.ModeSteps {
		t.Fatalf("mcp session mode = %q err=%v; want %q", mode, err, approvalpolicy.ModeSteps)
	}
	entry, err := service.SessionStepEntry(ctx, "owner-a", mcpSession.ID)
	if err != nil || entry != approvalpolicy.StepEntryMCP {
		t.Fatalf("mcp session entry = %q err=%v; want %q", entry, err, approvalpolicy.StepEntryMCP)
	}
	if mode, err := service.SessionApprovalMode(ctx, "owner-a", plainSession.ID); err != nil || mode == approvalpolicy.ModeSteps {
		t.Fatalf("plain session mode = %q err=%v; want the stored mode", mode, err)
	}
	if entry, err := service.SessionStepEntry(ctx, "owner-a", plainSession.ID); err != nil || entry != "" {
		t.Fatalf("plain session entry = %q err=%v; want none", entry, err)
	}
}

// #330: a person cannot flip the derived mode of an external MCP asset
// session, the same rule the asset-list item sessions have.
func TestMCPStepSessionRefusesAnApprovalModeWrite(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, nil)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "mcp asset")
	if err != nil {
		t.Fatal(err)
	}
	service.SetMCPStepSessions(stepMCPAssets{mark: map[string]bool{session.ID: true}})
	if _, _, err := service.SetApprovalMode(ctx, "owner-a", session.ID, "auto"); !errors.Is(err, chat.ErrMCPSessionApprovalMode) {
		t.Fatalf("approval-mode write = %v; want ErrMCPSessionApprovalMode", err)
	}
}

// #330: a failing asset read is an error, never a guessed plain session.
func TestMCPStepSessionLookupFailureIsAnError(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, nil)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	expected := errors.New("asset store unavailable")
	service.SetMCPStepSessions(stepMCPAssets{err: expected})
	if _, err := service.SessionApprovalMode(ctx, "owner-a", session.ID); !errors.Is(err, expected) {
		t.Fatalf("mode read = %v; want the asset store error", err)
	}
	if _, err := service.SessionStepEntry(ctx, "owner-a", session.ID); !errors.Is(err, expected) {
		t.Fatalf("entry read = %v; want the asset store error", err)
	}
}
