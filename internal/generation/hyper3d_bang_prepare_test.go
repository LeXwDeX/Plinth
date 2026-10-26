package generation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type bangPreparationAdapter struct {
	generation.ProviderPort
	starts int
}

func (a *bangPreparationAdapter) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	a.starts++
	return a.ProviderPort.Start(ctx, req)
}

func TestHyper3DBangPrepareRequiresSameSessionRodinSource(t *testing.T) {
	raw, err := os.ReadFile("../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// Only this test's catalog enables the candidate. The source lifecycle rows
	// below are local fixtures; this test proves preparation, not live delivery.
	for _, entry := range doc["offerings"].([]any) {
		offering := entry.(map[string]any)
		if offering["id"] == "hyper3d/bang/rodin-task" {
			offering["enabled"] = true
		}
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	adapter := &bangPreparationAdapter{ProviderPort: mock.New()}
	registry := providers.NewRegistry()
	if err := registry.Register("hyper3d.rodin.v1", adapter); err != nil {
		t.Fatal(err)
	}
	h := newConfiguredHarness(t, adapter, nil, func(d *generation.Dependencies) {
		d.Catalog, d.Providers = taskCatalog, registry
	})
	ctx := context.Background()
	const owner = generation.OwnerID("bang-owner")
	const sourceSession = "bang-source-session"
	for _, session := range []string{sourceSession, "bang-other-session"} {
		h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, string(owner), session)
	}
	h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, "bang-other-owner", "bang-other-owner-session")

	source, ok := taskCatalog.Get("hyper3d/rodin-gen-2.5/text-to-3d")
	if !ok {
		t.Fatal("Rodin fixture offering missing")
	}
	offeringJSON, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := source.EvaluateNativeQuote(json.RawMessage(`{"prompt":"a wooden chair"}`))
	if err != nil {
		t.Fatal(err)
	}
	quoteJSON, err := json.Marshal(quote)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const taskID = "successful-rodin-source"
	const attemptID = "successful-rodin-source-attempt"
	const stamp = "2026-10-21T00:00:00Z"
	_, err = db.Exec(`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,current_attempt_id,version,created_at,updated_at,finished_at,native_quote_json,native_quote_required) VALUES (?,?,?,?,?,?,'fixture-source-hash','SUCCEEDED',?,1,?,?,?,?,1)`, taskID, owner, sourceSession, "text-to-3d", string(offeringJSON), `{"prompt":"a wooden chair"}`, attemptID, stamp, stamp, stamp, string(quoteJSON))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,upstream_provider_id,upstream_job_id,started_at,updated_at,finished_at) VALUES (?,?,1,?,'UPSTREAM_SUCCEEDED','fixture-source-key','hyper3d','fixture-native-rodin-job',?,?,?)`, attemptID, taskID, string(offeringJSON), stamp, stamp, stamp)
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(who generation.OwnerID, session string) (generation.PreparedInput, error) {
		return h.module.PrepareInput(ctx, generation.PrepareInputCommand{
			OwnerID: who, SessionID: session, Capability: "mesh-segmentation", OfferingID: "hyper3d/bang/rodin-task",
			Input: json.RawMessage(`{"source_task":"` + taskID + `"}`),
		})
	}
	if _, err := prepare(owner, "bang-other-session"); err == nil {
		t.Error("same-owner source from another session was accepted")
	}
	prepared, err := prepare(owner, sourceSession)
	if err != nil {
		t.Fatalf("same-session Rodin source rejected: %v", err)
	}
	if len(prepared.Dependencies) != 1 || prepared.Dependencies[0].TaskID != taskID || prepared.Dependencies[0].SourceSessionID != sourceSession {
		t.Error("source task and session were not frozen together")
	}
	if _, err := prepare("bang-other-owner", "bang-other-owner-session"); err == nil {
		t.Error("cross-owner source was accepted")
	}
	if adapter.starts != 0 {
		t.Fatalf("preparation dispatched %d provider starts", adapter.starts)
	}
}
