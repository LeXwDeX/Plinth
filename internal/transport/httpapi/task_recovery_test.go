package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskRecoveryHTTPRequiresOwnedAttestedSource(t *testing.T) {
	t.Parallel()
	handler, cookie := retryBrowser(t)
	for _, tt := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"no receipt", http.MethodGet, "/api/v1/tasks/failed-retryable/recovery", "", http.StatusOK},
		{"other owner", http.MethodGet, "/api/v1/tasks/failed-foreign/recovery", "", http.StatusNotFound},
		{"unknown task", http.MethodGet, "/api/v1/tasks/missing/recovery", "", http.StatusNotFound},
		{"forged digest", http.MethodPost, "/api/v1/tasks/failed-retryable/recovery/prepare", `{"receipt_digest":"` + strings.Repeat("a", 64) + `"}`, http.StatusConflict},
		{"arbitrary URL", http.MethodPost, "/api/v1/tasks/failed-retryable/recovery/prepare", `{"receipt_digest":"x","url":"https://example.test/model.glb"}`, http.StatusBadRequest},
		{"other owner prepare", http.MethodPost, "/api/v1/tasks/failed-foreign/recovery/prepare", `{"receipt_digest":"x"}`, http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := grantTestRequest(t, handler, cookie, tt.method, tt.path, tt.body, "")
			if r.Code != tt.status {
				t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
			}
			if tt.name == "no receipt" && !strings.Contains(r.Body.String(), `"eligible":false`) {
				t.Fatal("unattested source marked eligible")
			}
			for _, secret := range []string{"storage_key", "upstream_job", "opaque", "recovery:"} {
				if strings.Contains(r.Body.String(), secret) {
					t.Fatalf("private detail leaked: %s", secret)
				}
			}
		})
	}
}

func TestTaskRecoveryHTTPPrepareApproveSubmitAndPrivateProjection(t *testing.T) {
	t.Parallel()
	for _, manual := range []bool{false, true} {
		name := "default policy"
		if manual {
			name = "explicit manual policy"
		}
		t.Run(name, func(t *testing.T) { testTaskRecoveryHTTPFlow(t, manual) })
	}
}
func testTaskRecoveryHTTPFlow(t *testing.T, manual bool) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var offerings []map[string]json.RawMessage
	if err = json.Unmarshal(document["offerings"], &offerings); err != nil {
		t.Fatal(err)
	}
	oldID := generation.OfferingID("tripo/frozen/text-to-3d")
	options := schema.RecoveryOptions{ReceiptParameter: "receipt_id", DigestParameter: "receipt_digest", Sources: []schema.RecoverySourceContract{{OfferingID: oldID, OfferingRevision: "original-r1", Slots: []schema.RecoverySlotMapping{{SourceRole: "model", Role: "model", Variant: "glb"}, {SourceRole: "converted_model", SourceVariant: "converted.gltf", Role: "converted_model", Variant: "glb"}}}}}
	for _, entry := range offerings {
		if string(entry["id"]) == `"local/existing-assets/recover"` {
			var outputs []json.RawMessage
			json.Unmarshal(entry["outputs"], &outputs)
			entry["outputs"], _ = json.Marshal(outputs[:2])
			entry["provider_options"], _ = json.Marshal(options)
		}
	}
	document["offerings"], _ = json.Marshal(offerings)
	raw, _ = json.Marshal(document)
	c, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := c.Get("local/existing-assets/recover")
	old := generation.Offering{ID: oldID, Revision: "original-r1", Capability: generation.TextTo3D, ProviderID: "tripo", ModelID: "frozen", Domain: generation.DomainModel3D, Outputs: append([]generation.AssetRequirement(nil), offering.Outputs...)}
	old.Outputs[0].Variant = ""
	old.Outputs[1].Variant = "converted.gltf"
	var receipt generation.RecoveryReceipt
	setup := func(t *testing.T, path string) {
		repo, err := sqlite.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		frozen, _ := json.Marshal(old)
		stamp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('recovery-session','owner-a','Owner','Recovery',?,?)`, []any{stamp, stamp}},
			{`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,current_attempt_id,version,created_at,updated_at,finished_at) VALUES ('recovery-source','owner-a','recovery-session','text-to-3d',?,'{}','old','FAILED','recovery-attempt',1,?,?,?)`, []any{string(frozen), stamp, stamp, stamp}},
			{`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,upstream_provider_id,upstream_job_id,opaque_json,started_at,updated_at,finished_at) VALUES ('recovery-attempt','recovery-source',1,?,'FAILED','old','tripo','private-remote','{"private":"checkpoint"}',?,?,?)`, []any{string(frozen), stamp, stamp, stamp}},
		} {
			if _, err = db.Exec(statement.query, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
		source, err := repo.ReadRecoverySource(ctx, "owner-a", "recovery-source")
		if err != nil {
			t.Fatal(err)
		}
		digest, _ := generation.RecoveryDigest(offering)
		receipt = generation.RecoveryReceipt{Source: source.Binding, ArchiveSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64), OfferingID: offering.ID, OfferingRevision: offering.Revision, OfferingSHA256: digest}
		store, err := local.New(filepath.Join(filepath.Dir(path), "storage"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		for i, out := range offering.Outputs {
			asset, err := store.Put(ctx, generation.AssetWrite{AssetID: generation.AssetID(out.Role), OwnerID: "owner-a", TaskID: "recovery-source", AttemptID: "recovery-attempt", Role: out.Role, Variant: out.Variant, Required: true, MediaType: out.MediaTypes[0], MaxBytes: out.MaxBytes, Body: bytes.NewReader(httpFixtureGLB("recovered model"))})
			if err != nil {
				t.Fatal(err)
			}
			receipt.Slots = append(receipt.Slots, generation.RecoverySlot{SourceRole: old.Outputs[i].Role, SourceVariant: old.Outputs[i].Variant, Role: out.Role, Variant: out.Variant, Required: true, StorageKey: asset.StorageKey, SHA256: asset.SHA256, Size: asset.Size, MediaType: asset.MediaType})
		}
		receipt, err = generation.SealRecoveryReceipt(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err = sqlite.CreateRecoveryReceiptFile(ctx, path, receipt, offering); err != nil {
			t.Fatal(err)
		}
	}
	module, handler, cookie, _ := grantTestBrowserWithCatalog(t, func(a *catalog.ApplicationSchema) {
		if manual {
			a.Chat.ApprovalPolicy.ManualApprovalCapabilities = append(a.Chat.ApprovalPolicy.ManualApprovalCapabilities, schema.AssetRecovery)
		}
	}, []string{"user"}, c, setup)
	eligible := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/tasks/recovery-source/recovery", "", "")
	if eligible.Code != http.StatusOK || !strings.Contains(eligible.Body.String(), `"eligible":true`) {
		t.Fatalf("eligibility: %d %s", eligible.Code, eligible.Body.String())
	}
	prepared := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/recovery-source/recovery/prepare", `{"receipt_digest":"`+receipt.Digest+`"}`, "")
	if prepared.Code != http.StatusCreated {
		t.Fatalf("prepare: %d %s", prepared.Code, prepared.Body.String())
	}
	var identity struct {
		ID string `json:"prepared_input_id"`
	}
	json.Unmarshal(prepared.Body.Bytes(), &identity)
	if identity.ID == "" {
		t.Fatal("no prepared identity")
	}
	key := generation.RecoveryIdempotencyKey(receipt.ID)
	grant := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants", `{"prepared_input_id":"`+identity.ID+`"}`, key)
	if grant.Code != http.StatusCreated {
		t.Fatalf("grant: %d %s", grant.Code, grant.Body.String())
	}
	var request struct {
		ID string `json:"request_id"`
	}
	json.Unmarshal(grant.Body.Bytes(), &request)
	body := `{"prepared_input_id":"` + identity.ID + `","authorization_request_id":"` + request.ID + `"}`
	if manual {
		pending := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", body, key)
		if pending.Code != http.StatusForbidden {
			t.Fatalf("pending accepted: %d %s", pending.Code, pending.Body.String())
		}
		decision := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+request.ID, `{"decision":"approve"}`, "")
		if decision.Code != http.StatusOK {
			t.Fatalf("decision: %d %s", decision.Code, decision.Body.String())
		}
	}
	accepted := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", body, key)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("submit: %d %s", accepted.Code, accepted.Body.String())
	}
	var task struct {
		ID string `json:"task_id"`
	}
	json.Unmarshal(accepted.Body.Bytes(), &task)
	if task.ID == "" {
		t.Fatal("no task identity")
	}
	if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "http-recovery", LeaseFor: time.Minute, BatchSize: 10}); err != nil {
		t.Fatal(err)
	}
	detail := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/tasks/"+task.ID, "", "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"source_task_id":"recovery-source"`) {
		t.Fatalf("detail: %d %s", detail.Code, detail.Body.String())
	}
	for _, response := range []string{eligible.Body.String(), prepared.Body.String(), detail.Body.String()} {
		for _, private := range []string{"storage_key", "private-remote", "checkpoint", "\"locator\"", "\"recovery:task_"} {
			if strings.Contains(response, private) {
				t.Fatalf("private projection leaked %q: %s", private, response)
			}
		}
	}
	repeated := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/recovery-source/recovery/prepare", `{"receipt_digest":"`+receipt.Digest+`"}`, "")
	if repeated.Code != http.StatusOK || !strings.Contains(repeated.Body.String(), task.ID) {
		t.Fatalf("repeat: %d %s", repeated.Code, repeated.Body.String())
	}
}

// The upstream refetch route (#295) is owner-scoped like recovery: another
// owner's or an unknown Task is 404, and a Task whose upstream result was never
// accepted is not refetchable (409 naming the reason, nothing created).
func TestTaskRefetchHTTPIsOwnerScoped(t *testing.T) {
	t.Parallel()
	handler, cookie := retryBrowser(t)
	for _, tt := range []struct {
		name, method, path string
		status             int
		contains           string
	}{
		{"own eligibility", http.MethodGet, "/api/v1/tasks/failed-retryable/refetch", http.StatusOK, `"reason":"not_applicable"`},
		{"other owner eligibility", http.MethodGet, "/api/v1/tasks/failed-foreign/refetch", http.StatusNotFound, ""},
		{"unknown eligibility", http.MethodGet, "/api/v1/tasks/missing/refetch", http.StatusNotFound, ""},
		{"own not refetchable", http.MethodPost, "/api/v1/tasks/failed-retryable/refetch", http.StatusConflict, `"reason":"not_applicable"`},
		{"other owner refetch", http.MethodPost, "/api/v1/tasks/failed-foreign/refetch", http.StatusNotFound, ""},
		{"unknown refetch", http.MethodPost, "/api/v1/tasks/missing/refetch", http.StatusNotFound, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := grantTestRequest(t, handler, cookie, tt.method, tt.path, "", "")
			if r.Code != tt.status || !strings.Contains(r.Body.String(), tt.contains) {
				t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
			}
			if strings.Contains(r.Body.String(), `"task_id":"`) || strings.Contains(r.Body.String(), "remote") {
				t.Fatalf("unexpected task or upstream detail: %s", r.Body.String())
			}
		})
	}
}
