package generationtool_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func withCall(command chat.ToolCommand, id, arguments string) chat.ToolCommand {
	command.Call.ID = id
	command.Call.Arguments = []byte(arguments)
	return command
}

func pendingCards(t *testing.T, repo *sqlite.Repository, session string, since time.Time) int {
	t.Helper()
	ops, err := repo.ListPendingDetached(context.Background(), "owner-a", session, 50, since)
	if err != nil {
		t.Fatal(err)
	}
	return len(ops)
}

// Defect 3: a real agent may call the same tool again for the same thing. While
// the first card is still pending the second call must not create another.
func TestSameToolWithSameInputReturnsTheExistingPendingCard(t *testing.T) {
	ctx := context.Background()
	executor, command, gateway, repo := policyHarness(t, sessionMode{mode: "manual"}, "create_model_3d_from_image", "image-to-3d")
	waiting, err := executor.Execute(ctx, withCall(command, "call-1", `{"prompt":"a fox"}`), nil)
	if err != nil || !waiting.NoSubmission || waiting.AgentContent == "" {
		t.Fatalf("first call: %+v %v", waiting, err)
	}
	first, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-1")
	if err != nil || !first.Detached {
		t.Fatalf("first card: %+v %v", first, err)
	}

	again, err := executor.Execute(ctx, withCall(command, "call-2", `{"prompt":"a fox"}`), nil)
	if err != nil || !again.NoSubmission || again.Suspend || again.TaskID != "" ||
		again.AgentContent != chatSchema(t).Agent.ConsentDuplicateContent || again.AgentContent == waiting.AgentContent {
		t.Fatalf("second call must be told the card already exists: %+v %v", again, err)
	}
	if got := pendingCards(t, repo, "session-a", time.Time{}); got != 1 {
		t.Fatalf("pending cards = %d, want the existing one only", got)
	}
	if _, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-2"); err == nil {
		t.Fatal("the duplicate call left an operation behind")
	}
	if gateway.submitCount != 0 {
		t.Fatalf("submits = %d", gateway.submitCount)
	}
	// The same call retried (restart) gets the same answer.
	retried, err := executor.Execute(ctx, withCall(command, "call-2", `{"prompt":"a fox"}`), nil)
	if err != nil || retried.AgentContent != again.AgentContent || pendingCards(t, repo, "session-a", time.Time{}) != 1 {
		t.Fatalf("retried duplicate: %+v %v", retried, err)
	}

	// A different input is a different thing and gets its own card.
	other, err := executor.Execute(ctx, withCall(command, "call-3", `{"prompt":"a wolf"}`), nil)
	if err != nil || !other.NoSubmission {
		t.Fatalf("different input: %+v %v", other, err)
	}
	if got := pendingCards(t, repo, "session-a", time.Time{}); got != 2 {
		t.Fatalf("pending cards after a different input = %d, want 2", got)
	}
}

// Only a card that is still pending and not expired absorbs a repeat.
func TestARepeatAfterTheCardWasDecidedOrExpiredCreatesANewCard(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	clock := now
	executor, command, _, repo := policyHarnessConfigured(t, sessionMode{mode: "manual"}, "create_model_3d_from_image", "image-to-3d",
		func(service *executiongrant.Service) {
			service.PendingTTL = time.Hour
			service.Clock = func() time.Time { return clock }
		})
	if _, err := executor.Execute(ctx, withCall(command, "call-1", `{"prompt":"a fox"}`), nil); err != nil {
		t.Fatal(err)
	}
	// Denied: the repeat is a new request.
	first, _ := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-1")
	if _, err := repo.DecideExecutionGrant(ctx, "owner-a", first.GrantRequestID, "owner-a", executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(ctx, withCall(command, "call-2", `{"prompt":"a fox"}`), nil); err != nil {
		t.Fatal(err)
	}
	second, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-2")
	if err != nil || !second.Detached {
		t.Fatalf("a repeat after a denial must get its own card: %+v %v", second, err)
	}
	// Expired: the repeat is a new request too.
	clock = now.Add(2 * time.Hour)
	if _, err := executor.Execute(ctx, withCall(command, "call-3", `{"prompt":"a fox"}`), nil); err != nil {
		t.Fatal(err)
	}
	third, err := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-3")
	if err != nil || !third.Detached {
		t.Fatalf("a repeat after expiry must get its own card: %+v %v", third, err)
	}
}

// Defect 1 (race): a Stop that lands between the turn check and the detach must
// not leave a detached card nobody can finish.
func TestStopLandingBeforeTheDetachLeavesTheOperationUndetached(t *testing.T) {
	ctx := context.Background()
	_, command, gateway, repo := policyHarness(t, sessionMode{mode: "manual"}, "create_model_3d_from_image", "image-to-3d")
	service := &executiongrant.Service{Grants: repo, Prepared: repo}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{},
		Operations: stopBeforeDetach{OperationStore: repo, repo: repo}, Grants: service, Chat: chatSchema(t),
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(ctx, command, nil)
	if err == nil && !result.NoSubmission {
		t.Fatalf("a stopped turn carried on: %+v", result)
	}
	op, loadErr := repo.LoadAgentOperation(ctx, "owner-a", "session-a", "turn-a", "call-a")
	if loadErr != nil || op.Detached {
		t.Fatalf("operation = %+v %v", op, loadErr)
	}
	if gateway.submitCount != 0 {
		t.Fatal("submitted")
	}
}

// stopBeforeDetach presses Stop at the last moment before the detach statement.
type stopBeforeDetach struct {
	generationtool.OperationStore
	repo *sqlite.Repository
}

func (s stopBeforeDetach) DetachAgentOperation(ctx context.Context, op generationtool.Operation) (generationtool.Operation, bool, error) {
	if _, _, err := s.repo.RequestTurnStop(ctx, op.OwnerID, op.SessionID, []string{"running"}, time.Now()); err != nil {
		return generationtool.Operation{}, false, err
	}
	return s.OperationStore.DetachAgentOperation(ctx, op)
}

func chatSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Chat
}

// The key is explicit: these fields decide whether two requests are "the same
// thing", and nothing else does.
func TestApprovalRequestKeyIdentity(t *testing.T) {
	base := generation.PreparedInput{
		ID: "prepared-1", OwnerID: "owner-a", SessionID: "session-a", Capability: generation.ImageTo3D,
		OfferingID: "meshy/image-to-3d", OfferingRevision: "r1", InputHash: "hash-1",
		NormalizedInput: []byte(`{"prompt":"a fox","image":"asset-1","count":1}`),
		Assets: []generation.PreparedInputAsset{{Parameter: "image", ID: "asset-1", Role: "reference",
			SHA256: "aa", Size: 10, MediaType: "image/png"}},
		Dependencies: []generation.PreparedInputDependency{{Parameter: "source", TaskID: "task-1"}},
	}
	key := generationtool.ApprovalRequestKey("create_model_3d_from_image", base)
	if key == "" {
		t.Fatal("empty key")
	}
	same := map[string]func(*generation.PreparedInput){
		"new prepared id, hash and time": func(p *generation.PreparedInput) {
			p.ID, p.InputHash, p.CreatedAt = "prepared-2", "hash-2", time.Now()
		},
		"input keys in another order": func(p *generation.PreparedInput) {
			p.NormalizedInput = []byte(`{"count":1,"image":"asset-1","prompt":"a fox"}`)
		},
		"same picture uploaded again (new asset id)": func(p *generation.PreparedInput) {
			p.NormalizedInput = []byte(`{"prompt":"a fox","image":"asset-2","count":1}`)
			p.Assets = []generation.PreparedInputAsset{{Parameter: "image", ID: "asset-2", Role: "reference",
				SHA256: "aa", Size: 10, MediaType: "image/png"}}
		},
	}
	for name, change := range same {
		next := base
		change(&next)
		if got := generationtool.ApprovalRequestKey("create_model_3d_from_image", next); got != key {
			t.Errorf("%s changed the key", name)
		}
	}
	different := map[string]func(*generation.PreparedInput){
		"prompt": func(p *generation.PreparedInput) {
			p.NormalizedInput = []byte(`{"prompt":"a wolf","image":"asset-1","count":1}`)
		},
		"parameter": func(p *generation.PreparedInput) {
			p.NormalizedInput = []byte(`{"prompt":"a fox","image":"asset-1","count":2}`)
		},
		"offering":          func(p *generation.PreparedInput) { p.OfferingID = "tripo/image-to-3d" },
		"offering revision": func(p *generation.PreparedInput) { p.OfferingRevision = "r2" },
		"capability":        func(p *generation.PreparedInput) { p.Capability = generation.TextTo3D },
		"source bytes": func(p *generation.PreparedInput) {
			p.Assets = []generation.PreparedInputAsset{{Parameter: "image", ID: "asset-1", Role: "reference",
				SHA256: "bb", Size: 10, MediaType: "image/png"}}
		},
		"source role": func(p *generation.PreparedInput) {
			p.Assets = []generation.PreparedInputAsset{{Parameter: "image", ID: "asset-1", Role: "style",
				SHA256: "aa", Size: 10, MediaType: "image/png"}}
		},
		"source task": func(p *generation.PreparedInput) {
			p.Dependencies = []generation.PreparedInputDependency{{Parameter: "source", TaskID: "task-2"}}
		},
		"scene context": func(p *generation.PreparedInput) { p.SceneID = "scene-1" },
	}
	for name, change := range different {
		next := base
		change(&next)
		if got := generationtool.ApprovalRequestKey("create_model_3d_from_image", next); got == key {
			t.Errorf("%s did not change the key", name)
		}
	}
	if generationtool.ApprovalRequestKey("create_model_3d", base) == key {
		t.Error("another tool shares the key")
	}
}

// Calls of one turn run concurrently: whichever way they interleave, one thing
// gets one card.
func TestConcurrentIdenticalCallsCreateOneCard(t *testing.T) {
	ctx := context.Background()
	executor, command, _, repo := policyHarness(t, sessionMode{mode: "manual"}, "create_model_3d_from_image", "image-to-3d")
	var wait sync.WaitGroup
	for index := range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := executor.Execute(ctx, withCall(command, fmt.Sprintf("call-%d", index), `{"prompt":"a fox"}`), nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if got := pendingCards(t, repo, "session-a", time.Time{}); got != 1 {
		t.Fatalf("pending cards = %d, want 1", got)
	}
}

// An automatic approval has no card, so there is nothing to merge into.
func TestAutomaticApprovalIsNeverMergedIntoAPendingCard(t *testing.T) {
	ctx := context.Background()
	mode := "manual"
	executor, command, gateway, repo := policyHarness(t, switchableMode{mode: &mode}, "create_model_3d_from_image", "image-to-3d")
	if _, err := executor.Execute(ctx, withCall(command, "call-1", `{"prompt":"a fox"}`), nil); err != nil {
		t.Fatal(err)
	}
	mode = "auto"
	result, err := executor.Execute(ctx, withCall(command, "call-2", `{"prompt":"a fox"}`), nil)
	if err != nil || result.AgentContent == chatSchema(t).Agent.ConsentDuplicateContent || gateway.submitCount != 1 {
		t.Fatalf("auto call: %+v submits=%d err=%v", result, gateway.submitCount, err)
	}
	if got := pendingCards(t, repo, "session-a", time.Time{}); got != 1 {
		t.Fatalf("pending cards = %d", got)
	}
}

// Another session asking for the same thing is not a duplicate.
func TestTheSameInputInAnotherSessionGetsItsOwnCard(t *testing.T) {
	ctx := context.Background()
	executor, command, _, repo := policyHarness(t, sessionMode{mode: "manual"}, "create_model_3d_from_image", "image-to-3d")
	if _, err := executor.Execute(ctx, withCall(command, "call-1", `{"prompt":"a fox"}`), nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-b", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-b", SessionID: "session-b", Role: "user", Content: "go", CreatedAt: now},
		chat.Message{ID: "turn-b", SessionID: "session-b", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session-b", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-b", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-b", AssistantMessageID: "turn-b",
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	other := withCall(command, "call-1", `{"prompt":"a fox"}`)
	other.SessionID, other.AssistantMessageID = "session-b", "turn-b"
	if _, err := executor.Execute(ctx, other, nil); err != nil {
		t.Fatal(err)
	}
	if pendingCards(t, repo, "session-a", time.Time{}) != 1 || pendingCards(t, repo, "session-b", time.Time{}) != 1 {
		t.Fatal("each session must keep its own card")
	}
}
