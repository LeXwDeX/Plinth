package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// Freeze advised changes and a synthetic native price in the same real SQLite admission.
func TestNativeAdvisedFrozenIntersection(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &raw); err != nil {
		t.Fatal(err)
	}
	for _, entry := range raw["offerings"].([]any) {
		o := entry.(map[string]any)
		if o["provider_id"] == "openai" {
			o["credits_metered"] = true
		}
		if o["id"] != "openai/gpt-image-2/text-to-image" {
			continue
		}
		o["estimated_credits"] = 0
		delete(o, "estimated_credits_unit_parameter")
		o["native_pricing"] = map[string]any{"version": 1, "unit": "hyper3d.credit", "unit_label": "Hyper3D credits", "base_amount": "0.5", "components": []any{map[string]any{"id": "quality", "label": "Highest quality", "amount": "2.5", "when": map[string]any{"parameter": "quality", "operator": "equals", "value": "max"}}}, "consent_parameter": "native_consent", "max_authorized_extra_amount": "2.5"}
		o["parameters"] = append(o["parameters"].([]any), map[string]any{"name": "native_consent", "type": "boolean", "default": false})
	}
	body, _ := json.Marshal(raw)
	c, err := catalog.Load(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	h := newConfiguredHarness(t, mock.New(), nil, func(d *generation.Dependencies) { d.Catalog = c })
	ctx := context.Background()
	if _, err = h.repository.CreateSession(ctx, chat.Session{ID: "native-advised", OwnerID: "owner", OwnerName: "Owner", Title: "test", CreatedAt: h.clock.now, UpdatedAt: h.clock.now}); err != nil {
		t.Fatal(err)
	}
	cmd := generation.PrepareInputCommand{OwnerID: "owner", SessionID: "native-advised", Capability: generation.TextToImage, OfferingID: "openai/gpt-image-2/text-to-image", Input: json.RawMessage(`{"prompt":"x","quality":"low"}`)}
	if _, err = h.module.PrepareInput(ctx, cmd); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("unexplained adjustment accepted: %v", err)
	}
	cmd.ParameterReasons = map[string]string{"quality": "Draft composition"}
	first, err := h.module.PrepareInput(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if first.NativeQuote == nil || first.NativeQuote.TotalAmount != "0.5" || first.NativeQuote.ExtraAmount != "0" || len(first.ParameterAdjustments) != 1 {
		t.Fatalf("quote/adjustment intersection lost: %+v", first)
	}
	highest := cmd
	highest.Input = json.RawMessage(`{"prompt":"x","native_consent":true}`)
	highest.ParameterReasons = nil
	full, err := h.module.PrepareInput(ctx, highest)
	if err != nil || full.NativeQuote == nil || full.NativeQuote.TotalAmount != "3" || len(full.ParameterAdjustments) != 0 {
		t.Fatalf("highest/default quote mismatch: %+v %v", full, err)
	}
	principal := executiongrant.Principal{OwnerID: "owner", ActorID: "owner", Source: executiongrant.HumanBrowser}
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: cmd.SessionID, OfferingID: string(first.OfferingID), OfferingRevision: first.OfferingRevision, PreparedInputID: string(first.ID), PreparedInputHash: first.InputHash, IdempotencyKey: "native-advised", OperationID: "native-advised"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	cmd.ParameterReasons["quality"] = "Changed creative intent"
	second, err := h.module.PrepareInput(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if second.InputHash == first.InputHash || second.NativeQuote == nil || second.NativeQuote.TotalAmount != first.NativeQuote.TotalAmount {
		t.Fatal("reason did not bind independently of price")
	}
	submit := generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: cmd.SessionID, PreparedInputID: second.ID, IdempotencyKey: "native-advised", OperationID: "native-advised", AuthorizationRef: grant.RequestID}
	if _, err = h.module.SubmitPrepared(ctx, submit); !generation.IsCode(err, generation.ErrForbidden) {
		t.Fatalf("old grant authorized changed reason: %v", err)
	}
	submit.PreparedInputID = first.ID
	accepted, err := h.module.SubmitPrepared(ctx, submit)
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.module.Get(ctx, "owner", accepted.TaskID)
	if err != nil || task.NativeQuote == nil || task.NativeQuote.TotalAmount != "0.5" {
		t.Fatalf("task quote mismatch: %+v %v", task.NativeQuote, err)
	}
	stored, err := h.module.GetPreparedInput(ctx, "owner", first.ID)
	if err != nil || len(stored.ParameterAdjustments) != 1 || stored.ParameterAdjustments[0].Reason != "Draft composition" || stored.NativeQuote == nil || stored.NativeQuote.TotalAmount != "0.5" {
		t.Fatalf("frozen contracts drifted: %+v %v", stored, err)
	}
	replay, err := h.module.SubmitPrepared(ctx, submit)
	if err != nil || replay.TaskID != accepted.TaskID || !replay.Replayed {
		t.Fatalf("accepted replay: %+v %v", replay, err)
	}
}
