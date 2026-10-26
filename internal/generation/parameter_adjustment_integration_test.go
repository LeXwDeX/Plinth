package generation_test

import (
	"context"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"testing"
	"time"
)

func TestAdvisedInputRequiresReasonAndBindsApprovalToActualValues(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	now := time.Now().UTC()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cmd := generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage, OfferingID: "openai/gpt-image-2/text-to-image", Input: json.RawMessage(`{"prompt":"paint","quality":"low"}`)}
	if _, err := h.module.PrepareInput(ctx, cmd); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("unexplained change accepted: %v", err)
	}
	cmd.ParameterReasons = map[string]string{"quality": "先验证草稿构图"}
	first, err := h.module.PrepareInput(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ParameterAdjustments) != 1 || string(first.ParameterAdjustments[0].Value) != `"low"` || string(first.ParameterAdjustments[0].RecommendedValue) != `"max"` {
		t.Fatalf("adjustment=%+v", first.ParameterAdjustments)
	}
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: "session-a", OfferingID: string(first.OfferingID), OfferingRevision: first.OfferingRevision, PreparedInputID: string(first.ID), PreparedInputHash: first.InputHash, IdempotencyKey: "advised", OperationID: "advised-op"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	cmd.ParameterReasons["quality"] = "另一项创作理由"
	second, err := h.module.PrepareInput(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if second.InputHash == first.InputHash {
		t.Fatal("new reason reused old approval identity")
	}
	bad := generation.SubmitPreparedCommand{OwnerID: "owner-a", SessionID: "session-a", PreparedInputID: second.ID, IdempotencyKey: "advised", OperationID: "advised-op", AuthorizationRef: grant.RequestID}
	if _, err = h.module.SubmitPrepared(ctx, bad); !generation.IsCode(err, generation.ErrForbidden) {
		t.Fatalf("old grant authorized revised reason: %v", err)
	}
	stored, err := h.module.GetPreparedInput(ctx, "owner-a", first.ID)
	if err != nil || stored.ParameterAdjustments[0].Reason != "先验证草稿构图" {
		t.Fatalf("old frozen input changed: %+v %v", stored, err)
	}
	bad.PreparedInputID = first.ID
	accepted, err := h.module.SubmitPrepared(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.module.Get(ctx, "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err = json.Unmarshal(task.Input, &actual); err != nil {
		t.Fatal(err)
	}
	if actual["quality"] != "low" {
		t.Fatalf("approved value lost: %v", actual)
	}
	if _, err = h.module.GetPreparedInput(ctx, "other-owner", first.ID); err == nil {
		t.Fatal("cross-owner frozen reason visible")
	}

	forced := generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextTo3D, OfferingID: "tripo/tripo-v3.1-full/text-to-3d", Input: json.RawMessage(`{"prompt":"parts","generate_parts":true,"authorize_extra_credits":true}`)}
	forcedPrepared, err := h.module.PrepareInput(ctx, forced)
	if err != nil || len(forcedPrepared.ParameterAdjustments) != 0 {
		t.Fatalf("forced effective recommendation needs invented reason: %+v %v", forcedPrepared.ParameterAdjustments, err)
	}
}
