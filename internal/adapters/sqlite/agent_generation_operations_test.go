package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestAgentOperationPersistsExactToolCallAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent-operations.db")
	r, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := r.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a",
		OwnerName: "owner", Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := r.CreatePreparedInput(ctx, generation.PreparedInput{ID: "prepared-a", OwnerID: "owner-a",
		SessionID: "session-a", OfferingID: "offering-a", OfferingRevision: "revision-a",
		InputHash: "hash-a", NormalizedInput: []byte(`{}`), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	candidate := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a",
		AssistantMessageID: "turn-a", CallID: "call-a", ToolName: "refine_image",
		CommandHash: "command-a", IdempotencyKey: "key-a", PreparedInputID: "prepared-a",
		PreparedInputHash: "hash-a", OfferingID: "offering-a", OfferingRevision: "revision-a"}
	first, err := r.InsertAgentOperation(ctx, candidate)
	if err != nil || first.OperationID == "" {
		t.Fatalf("insert: %+v %v", first, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	replayed, err := r.InsertAgentOperation(ctx, candidate)
	if err != nil || replayed.OperationID != first.OperationID {
		t.Fatalf("restart replay: %+v %v", replayed, err)
	}
	if _, err := r.LoadAgentOperation(ctx, "other-owner", "session-a", "turn-a", "call-a"); !errors.Is(err, generationtool.ErrOperationNotFound) {
		t.Fatalf("other owner read operation: %v", err)
	}
	changed := candidate
	changed.IdempotencyKey = "changed-key"
	if _, err := r.InsertAgentOperation(ctx, changed); !errors.Is(err, generationtool.ErrOperationConflict) {
		t.Fatalf("same call changed key: %v", err)
	}
	bound, err := r.AttachAgentGrantRequest(ctx, first, "grant-a")
	if err != nil || bound.GrantRequestID != "grant-a" {
		t.Fatalf("attach: %+v %v", bound, err)
	}
	if _, err := r.AttachAgentGrantRequest(ctx, first, "grant-b"); !errors.Is(err, generationtool.ErrOperationConflict) {
		t.Fatalf("grant replacement accepted: %v", err)
	}
}
