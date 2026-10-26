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
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type tripoGeometryCountingAdapter struct {
	generation.ProviderPort
	starts int
}

func (a *tripoGeometryCountingAdapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	a.starts++
	return a.ProviderPort.Start(ctx, request)
}

func (a *tripoGeometryCountingAdapter) ResolveDependencyBinding(generation.DependencyBindingRequest) (json.RawMessage, error) {
	return json.RawMessage(`{"fixture":"successful-tripo-segment"}`), nil
}

// A test-only catalog enables the disabled candidate Offerings so PrepareInput
// can exercise their dependency contract. The shipped Product Schema remains
// disabled; no candidate Task is ever submitted or dispatched.
func tripoGeometryPrepareCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile("../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	enabledForPrepare := map[string]bool{
		"tripo/geometry/segment":   true,
		"tripo/geometry/complete":  true,
		"tripo/geometry/quick-cap": true,
	}
	legacyModels := doc["application"].(map[string]any)["provider_discovery"].(map[string]any)["legacy_models"].([]any)
	legacyModelIDs := make(map[string]bool, len(legacyModels))
	for _, value := range legacyModels {
		legacyModelIDs[value.(map[string]any)["model_id"].(string)] = true
	}
	for _, value := range doc["offerings"].([]any) {
		offering := value.(map[string]any)
		if enabledForPrepare[offering["id"].(string)] {
			offering["enabled"] = true
			modelID := offering["model_id"].(string)
			if !legacyModelIDs[modelID] {
				legacyModels = append(legacyModels, map[string]any{
					"model_id": modelID, "provider_key": offering["provider_id"], "recommended": false,
				})
				legacyModelIDs[modelID] = true
			}
		}
	}
	doc["application"].(map[string]any)["provider_discovery"].(map[string]any)["legacy_models"] = legacyModels
	updated, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	result, err := catalog.Load(bytes.NewReader(updated))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTripoGeometryCompletePrepareBindsSuccessfulSegmentToSameSession(t *testing.T) {
	for _, candidate := range []struct {
		id         string
		capability generation.Capability
	}{
		{"tripo/geometry/complete", generation.Capability("mesh-completion")},
		{"tripo/geometry/quick-cap", generation.Capability("mesh-completion")},
	} {
		t.Run(candidate.id, func(t *testing.T) {
			taskCatalog := tripoGeometryPrepareCatalog(t)
			adapter := &tripoGeometryCountingAdapter{ProviderPort: mock.New()}
			h := newConfiguredHarness(t, adapter, nil, func(d *generation.Dependencies) {
				d.Catalog = taskCatalog
			})
			ctx := context.Background()
			const owner = generation.OwnerID("geometry-owner")
			const sourceSession = "geometry-source-session"
			h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, string(owner), sourceSession)
			segmentID := seedSuccessfulTripoSegmentTask(t, h, taskCatalog, string(owner), sourceSession)

			// The fixture is a successful native Tripo segment Task. Only PrepareInput
			// is exercised for the completion candidates, which remain unpublished.
			beforeStartCalls := adapter.starts
			targetInput := []byte(`{"source_task":"` + string(segmentID) + `","authorize_extra_credits":true}`)
			for _, session := range []string{"geometry-other-session", sourceSession} {
				h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, string(owner), session)
			}
			h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, "geometry-other-owner", "geometry-other-owner-session")

			if _, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
				OwnerID: owner, SessionID: "geometry-other-session", Capability: candidate.capability,
				OfferingID: generation.OfferingID(candidate.id), Input: targetInput,
			}); err == nil {
				t.Fatal("same-owner source from a different session was accepted")
			}
			prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
				OwnerID: owner, SessionID: sourceSession, Capability: candidate.capability,
				OfferingID: generation.OfferingID(candidate.id), Input: targetInput,
			})
			if err != nil {
				t.Fatalf("same-session source rejected: %v", err)
			}
			if len(prepared.Dependencies) != 1 || prepared.Dependencies[0].TaskID != segmentID || prepared.Dependencies[0].SourceSessionID != sourceSession {
				t.Fatalf("source dependency was not frozen with its session: %+v", prepared.Dependencies)
			}
			if _, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
				OwnerID: "geometry-other-owner", SessionID: "geometry-other-owner-session", Capability: candidate.capability,
				OfferingID: generation.OfferingID(candidate.id), Input: targetInput,
			}); err == nil {
				t.Fatal("cross-owner source was accepted")
			}
			if got := adapter.starts; got != beforeStartCalls {
				t.Fatalf("PrepareInput dispatched a provider start: before=%d after=%d", beforeStartCalls, got)
			}
		})
	}
}

func seedSuccessfulTripoSegmentTask(t *testing.T, h *harness, taskCatalog *catalog.Catalog, owner, session string) generation.TaskID {
	t.Helper()
	offering, ok := taskCatalog.Get("tripo/geometry/segment")
	if !ok {
		t.Fatal("Tripo segment source Offering missing from fixture catalog")
	}
	offeringJSON, err := json.Marshal(offering)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const taskID generation.TaskID = "successful-tripo-segment-source"
	const attemptID = "successful-tripo-segment-source-attempt"
	const stamp = "2026-10-21T00:00:00Z"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,current_attempt_id,version,created_at,updated_at,finished_at) VALUES (?,?,?,?,?,?,'fixture-source-hash','SUCCEEDED',?,1,?,?,?)`, []any{taskID, owner, session, "mesh-segmentation", string(offeringJSON), `{"source_task":"fixture-model"}`, attemptID, stamp, stamp, stamp}},
		{`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,upstream_provider_id,upstream_job_id,started_at,updated_at,finished_at) VALUES (?,?,1,?,'UPSTREAM_SUCCEEDED','fixture-source-key','tripo','fixture-segment-job',?,?,?)`, []any{attemptID, taskID, string(offeringJSON), stamp, stamp, stamp}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return taskID
}
