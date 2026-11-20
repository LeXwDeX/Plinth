package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #406: a new session takes the Schema's default approval mode (auto); a
// session that already exists keeps the mode it stored. The tests build the
// session service with the shipped Schema's policy, so the default they see is
// the one a deployment runs with.

func shippedApprovalPolicy406(t *testing.T) approvalpolicy.Policy {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	declared := application.Chat.ApprovalPolicy
	manual := make([]string, 0, len(declared.ManualApprovalCapabilities))
	for _, capability := range declared.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	known := make([]string, 0, len(application.Capabilities))
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	return approvalpolicy.New(declared.DefaultMode, declared.Modes, manual, known)
}

func shippedApprovalService406(t *testing.T, repository chat.Repository) *chat.Service {
	t.Helper()
	policy := shippedApprovalPolicy406(t)
	return newChatServiceWith(t, repository, func(config *chat.ServiceConfig) { config.ApprovalPolicy = policy })
}

func TestNewSessionTakesTheSchemaDefaultApprovalMode_406(t *testing.T) {
	ctx := context.Background()
	policy := shippedApprovalPolicy406(t)
	if policy.DefaultMode != "auto" {
		t.Fatalf("the shipped Schema's default approval mode = %q, want auto", policy.DefaultMode)
	}
	repository := newMemoryChatRepository()
	service := shippedApprovalService406(t, repository)
	session, err := service.CreateSession(ctx, "owner-a", "Owner A", "")
	if err != nil {
		t.Fatal(err)
	}
	if session.ApprovalMode != policy.DefaultMode {
		t.Fatalf("new session mode = %q, want the Schema default %q", session.ApprovalMode, policy.DefaultMode)
	}
	stored, found, err := repository.Session(ctx, "owner-a", session.ID)
	if err != nil || !found || stored.ApprovalMode != policy.DefaultMode {
		t.Fatalf("stored mode = %q found=%v err=%v", stored.ApprovalMode, found, err)
	}
	// What the grant policy and the HTTP projection read is the same value.
	if mode, err := service.SessionApprovalMode(ctx, "owner-a", session.ID); err != nil || mode != "auto" {
		t.Fatalf("session approval mode = %q err=%v", mode, err)
	}
	if got := service.NormalizeApprovalMode(session.ApprovalMode); got != "auto" {
		t.Fatalf("projected mode = %q", got)
	}
}

// A session created before the default changed keeps what it stored, whatever
// the Schema says now; a stored value the Schema does not offer reads as the
// strict mode, never as the new default.
func TestExistingSessionsKeepTheirStoredApprovalMode_406(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryChatRepository()
	for id, mode := range map[string]string{
		"legacy-budget": "budget", "legacy-manual": "manual", "legacy-auto": "auto",
		"legacy-empty": "", "legacy-unknown": "yolo",
	} {
		if _, err := repository.CreateSession(ctx, chat.Session{ID: id, OwnerID: "owner-a", ApprovalMode: mode}); err != nil {
			t.Fatal(err)
		}
	}
	service := shippedApprovalService406(t, repository)
	want := map[string]string{
		"legacy-budget": "budget", "legacy-manual": "manual", "legacy-auto": "auto",
		"legacy-empty": "manual", "legacy-unknown": "manual",
	}
	check := func(when string) {
		t.Helper()
		for id, mode := range want {
			got, err := service.SessionApprovalMode(ctx, "owner-a", id)
			if err != nil || got != mode {
				t.Fatalf("%s: %s reads %q err=%v, want %q", when, id, got, err, mode)
			}
		}
	}
	check("before a new session exists")
	fresh, err := service.CreateSession(ctx, "owner-a", "Owner A", "")
	if err != nil || fresh.ApprovalMode != "auto" {
		t.Fatalf("new session = %+v err=%v", fresh, err)
	}
	check("after a new session was created")
	// A switch reports the stored mode as the previous one, not the default.
	_, previous, err := service.SetApprovalMode(ctx, "owner-a", "legacy-budget", "manual")
	if err != nil || previous != "budget" {
		t.Fatalf("previous mode = %q err=%v, want budget", previous, err)
	}
}

// The Agent of a default session is told it is in the automatic mode and gets
// nothing that sends it to a production plan: the budget block (which says to
// propose a plan first) belongs to the budget mode only, and the approval
// block does not mention the plan tool.
func TestDefaultSessionContextHasNoPlanSteering_406(t *testing.T) {
	system := approvalPromptSystem(t, approvalPromptCase{})
	for _, want := range []string{autoModeLabel, autoModeText} {
		if !strings.Contains(system, want) {
			t.Errorf("the system context of a new session omits %q", want)
		}
	}
	for _, absent := range []string{budgetModeLabel, budgetModeText, budgetBlockHead, noPlanStateText} {
		if strings.Contains(system, absent) {
			t.Errorf("the system context of a new session carries %q", absent)
		}
	}
	start := strings.Index(system, approvalBlockHead)
	if start < 0 {
		t.Fatal("the approval block is missing")
	}
	block := system[start:]
	// The block ends with the sentence about a mid-conversation switch.
	if end := strings.Index(block, "也不要道歉更正"); end > 0 {
		block = block[:end]
	}
	if strings.Contains(block, "propose_production_plan") {
		t.Errorf("the approval block of a default session names the plan tool: %s", block)
	}
	if !strings.Contains(block, "不需要制作计划") {
		t.Errorf("the approval block of a default session does not say a plan is not needed: %s", block)
	}
}
