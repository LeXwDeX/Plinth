package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// Use the real Generation freezer and persisted Grant reader: a hand-built
// PreparedInput cannot authenticate a native quote's input hash.
func TestMultiviewNativeQuoteBoundToRealPreparedGrant(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &raw); err != nil {
		t.Fatal(err)
	}
	for _, item := range raw["offerings"].([]any) {
		o := item.(map[string]any)
		if o["id"] == "tripo/image-to-multiview" {
			o["enabled"] = true
		}
	}
	body, _ := json.Marshal(raw)
	c, err := catalog.Load(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	h := newConfiguredHarness(t, mock.New(), nil, func(deps *generation.Dependencies) { deps.Catalog = c })
	ctx := context.Background()
	h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, "owner", "session")
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	asset, err := h.inputs.Upload(ctx, "owner", bytes.NewReader(picture.Bytes()), "image/png", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"image": string(asset.ID)})
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "session", Capability: generation.ImageToMultiview, OfferingID: "tripo/image-to-multiview", Input: input})
	if err != nil || prepared.NativeQuote == nil || prepared.NativeQuote.TotalAmount != "10" {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	principal := executiongrant.Principal{OwnerID: "owner", ActorID: "owner", Source: executiongrant.HumanBrowser}
	reader := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	grant, err := reader.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: "session", OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: "multiview", OperationID: "multiview"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := h.repository.GetExecutionPreparedSnapshot(ctx, "owner", string(prepared.ID))
	if err != nil || snapshot.NativeQuote == nil || snapshot.NativeQuote.TotalAmount != "10" || grant.PreparedInputHash != prepared.InputHash {
		t.Fatalf("grant/snapshot lost native quote: %+v %+v %v", grant, snapshot, err)
	}
	grant, err = reader.Decide(ctx, principal, grant.RequestID, executiongrant.Approve)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{OwnerID: "owner", SessionID: "session", PreparedInputID: prepared.ID, IdempotencyKey: "multiview", OperationID: "multiview", AuthorizationRef: grant.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.module.Get(ctx, "owner", accepted.TaskID)
	if err != nil || task.NativeQuote == nil || task.NativeQuote.TotalAmount != "10" || len(task.Offering.Outputs) != 4 {
		t.Fatalf("task lost approved quote/slots: %+v %v", task, err)
	}
	prepared.NativeQuote.TotalAmount = "11"
	if err := generation.VerifyPreparedPricingHash(prepared); err == nil {
		t.Fatal("tampered quote reused frozen input hash")
	}
}
