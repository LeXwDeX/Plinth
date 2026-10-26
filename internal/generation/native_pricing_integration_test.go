package generation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func nativePriceCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	var raw map[string]any
	_ = json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &raw)
	for _, v := range raw["offerings"].([]any) {
		o := v.(map[string]any)
		if o["provider_id"] == "openai" {
			o["credits_metered"] = true
		}
		if o["id"] != "openai/gpt-image-2/text-to-image" {
			continue
		}
		o["credits_metered"] = true
		o["estimated_credits"] = 0
		delete(o, "estimated_credits_unit_parameter")
		o["native_pricing"] = &schema.NativePricing{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", Components: []schema.NativePriceComponent{{ID: "fixed_quality", Label: "Highest quality", Amount: "2.5"}}, ConsentParameter: "native_consent", MaxAuthorizedExtraAmount: "2.5"}
		o["parameters"] = append(o["parameters"].([]any), map[string]any{"name": "native_consent", "type": "boolean", "default": false})
	}
	body, _ := json.Marshal(raw)
	c, err := catalog.Load(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type nativeQuoteDriftCatalog struct {
	*catalog.Catalog
	field string
}

func (c *nativeQuoteDriftCatalog) Normalize(cap generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	o, n, e := c.Catalog.Normalize(cap, id, input)
	if e != nil || o.NativePricing == nil {
		return o, n, e
	}
	switch c.field {
	case "base":
		o.NativePricing.BaseAmount = "0.6"
	case "unit":
		o.NativePricing.Unit = "other.credit"
	case "label":
		o.NativePricing.UnitLabel = "Other units"
	case "component":
		o.NativePricing.Components[0].Amount = "2.4"
	case "revision":
		o.Revision = "2026-10-16.213.1"
	}
	return o, n, e
}
func TestNativeQuoteSQLiteFreezesGrantAndTaskAtomically(t *testing.T) {
	ctx := context.Background()
	c := nativePriceCatalog(t)
	drift := &nativeQuoteDriftCatalog{Catalog: c}
	h := newConfiguredHarness(t, mock.New(), nil, func(d *generation.Dependencies) { d.Catalog = drift })
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "s", OwnerID: "o", OwnerName: "Owner", Title: "native", CreatedAt: h.clock.now, UpdatedAt: h.clock.now}); err != nil {
		t.Fatal(err)
	}
	cmd := generation.PrepareInputCommand{OwnerID: "o", SessionID: "s", Capability: generation.TextToImage, OfferingID: "openai/gpt-image-2/text-to-image", Input: json.RawMessage(`{"prompt":"x","native_consent":true}`)}
	if _, err := h.module.Submit(ctx, generation.SubmitCommand{OwnerID: "o", SessionID: "s", Capability: cmd.Capability, OfferingID: cmd.OfferingID, Input: cmd.Input, IdempotencyKey: "no-grant"}); !generation.IsCode(err, generation.ErrForbidden) {
		t.Fatalf("native unapproved direct submission: %v", err)
	}
	p, err := h.module.PrepareInput(ctx, cmd)
	if err != nil || p.NativeQuote == nil || p.NativeQuote.TotalAmount != "3" || p.ExtraCredits != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	reader := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: "o", ActorID: "o", Source: executiongrant.HumanBrowser}
	g, err := reader.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: "s", OfferingID: string(p.OfferingID), OfferingRevision: p.OfferingRevision, PreparedInputID: string(p.ID), PreparedInputHash: p.InputHash, IdempotencyKey: "native", OperationID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	g, err = reader.Decide(ctx, principal, g.RequestID, executiongrant.Approve)
	if err != nil {
		t.Fatal(err)
	}
	submit := generation.SubmitPreparedCommand{OwnerID: "o", SessionID: "s", PreparedInputID: p.ID, IdempotencyKey: "native", OperationID: "native", AuthorizationRef: g.RequestID}
	for _, field := range []string{"base", "unit", "label", "component"} {
		drift.field = field
		if _, err := h.module.SubmitPrepared(ctx, submit); !generation.IsCode(err, generation.ErrConflict) {
			t.Fatalf("%s price drift reused grant: %v", field, err)
		}
		current, e := h.repository.GetExecutionGrant(ctx, "o", g.RequestID)
		if e != nil || current.Status != executiongrant.Approved {
			t.Fatalf("drift claimed grant %v %v", current, e)
		}
	}
	drift.field = ""
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER refuse_native BEFORE INSERT ON generation_tasks WHEN NEW.native_quote_json IS NOT NULL BEGIN SELECT RAISE(ABORT,'injected quote failure'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err = h.module.SubmitPrepared(ctx, submit); err == nil {
		t.Fatal("injected admission succeeded")
	}
	still, err := h.repository.GetExecutionGrant(ctx, "o", g.RequestID)
	if err != nil || still.Status != executiongrant.Approved {
		t.Fatalf("partial grant claim: %+v %v", still, err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM generation_tasks`).Scan(&n)
	if n != 0 {
		t.Fatal("partial Task persisted")
	}
	_, _ = db.Exec(`DROP TRIGGER refuse_native`)
	accepted, err := h.module.SubmitPrepared(ctx, submit)
	if err != nil {
		t.Fatal(err)
	}
	drift.field = "base"
	if replay, e := h.module.SubmitPrepared(ctx, submit); e != nil || replay.TaskID != accepted.TaskID {
		t.Fatalf("accepted replay changed after price update: %+v %v", replay, e)
	}
	drift.field = ""
	second, err := sqlite.Open(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	task, err := second.Get(ctx, "o", accepted.TaskID)
	if err != nil || task.NativeQuote == nil || task.NativeQuote.TotalAmount != "3" {
		t.Fatalf("restart lost quote %+v %v", task.NativeQuote, err)
	}
	summary, err := second.GetTaskSummary(ctx, generation.OwnTasks("o"), accepted.TaskID)
	if err != nil || summary.NativeQuote == nil || summary.NativeQuote.TotalAmount != "3" {
		t.Fatalf("summary lost quote %+v %v", summary.NativeQuote, err)
	}
	snapshot, err := second.GetExecutionPreparedSnapshot(ctx, "o", string(p.ID))
	if err != nil || snapshot.NativeQuote == nil || snapshot.NativeQuote.ExtraAmount != "2.5" {
		t.Fatalf("grant snapshot lost quote %+v %v", snapshot, err)
	}
	if _, err = db.Exec(`DROP TRIGGER generation_prepared_inputs_immutable`); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(p)
	if _, err = db.Exec(`UPDATE generation_prepared_inputs SET payload_json=json_remove(payload_json,'$.native_quote') WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = second.GetExecutionPreparedSnapshot(ctx, "o", string(p.ID)); err == nil {
		t.Fatal("missing native quote degraded to legacy grant")
	}
	if _, err = second.GetPreparedInput(ctx, "o", p.ID); err == nil {
		t.Fatal("missing native quote degraded to legacy prepared")
	}
	if _, err = db.Exec(`UPDATE generation_prepared_inputs SET payload_json=? WHERE id=?`, string(original), p.ID); err != nil {
		t.Fatal(err)
	}

	if _, err = db.Exec(`UPDATE generation_prepared_inputs SET payload_json=json_set(payload_json,'$.native_quote.total_amount','4') WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = second.GetExecutionPreparedSnapshot(ctx, "o", string(p.ID)); err == nil {
		t.Fatal("corrupt grant quote accepted")
	}
}

func TestTripoHNativeQuoteRequiresMatchingRevisionGrant(t *testing.T) {
	ctx := context.Background()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	drift := &nativeQuoteDriftCatalog{Catalog: c}
	h := newConfiguredHarness(t, mock.New(), nil, func(d *generation.Dependencies) { d.Catalog = drift })
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "h-session", OwnerID: "h-owner", OwnerName: "Owner", Title: "H", CreatedAt: h.clock.now, UpdatedAt: h.clock.now}); err != nil {
		t.Fatal(err)
	}
	command := generation.PrepareInputCommand{OwnerID: "h-owner", SessionID: "h-session", Capability: generation.TextTo3D, OfferingID: "tripo/tripo-v3.1-full/text-to-3d", Input: json.RawMessage(`{"prompt":"a brass compass","authorize_extra_credits":true}`)}
	prepared, err := h.module.PrepareInput(ctx, command)
	if err != nil || prepared.NativeQuote == nil || prepared.NativeQuote.TotalAmount != "90" || prepared.NativeQuote.Unit != "tripo.credit" {
		t.Fatalf("prepared H quote=%+v err=%v", prepared, err)
	}
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: string(command.OwnerID), ActorID: string(command.OwnerID), Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal, SessionID: command.SessionID, OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash, IdempotencyKey: "h-native", OperationID: "h-native"})
	if err != nil {
		t.Fatal(err)
	}
	grant, err = grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve)
	if err != nil {
		t.Fatal(err)
	}
	submit := generation.SubmitPreparedCommand{OwnerID: command.OwnerID, SessionID: command.SessionID, PreparedInputID: prepared.ID, IdempotencyKey: "h-native", OperationID: "h-native", AuthorizationRef: grant.RequestID}
	drift.field = "revision"
	if _, err := h.module.SubmitPrepared(ctx, submit); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("changed revision reused H Grant: %v", err)
	}
	drift.field = ""
	accepted, err := h.module.SubmitPrepared(ctx, submit)
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.repository.Get(ctx, command.OwnerID, accepted.TaskID)
	if err != nil || task.NativeQuote == nil || task.NativeQuote.TotalAmount != "90" {
		t.Fatalf("frozen H Task quote=%+v err=%v", task.NativeQuote, err)
	}
}
