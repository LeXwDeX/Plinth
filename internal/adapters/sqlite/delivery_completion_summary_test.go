package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #330 P2: List and GetTaskSummary must expose DeliveryCompleted. Chat lists a
// session's model summaries and binds every next step from them; without the
// flag a custom Schema's upstream-task route is offered for a Task that only
// has locally completed assets, and the kernel later rejects that dependency.
func TestTaskSummaryKeepsLocalDeliveryCompletion330(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s", "owner", chat.Session{})
	stamp := "2026-10-23T00:00:00Z"
	offering := `{"id":"local/generated-glb/to-gltf","revision":"1","provider_id":"local","capability":"format-conversion"}`
	if _, err := r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, session_id, capability, offering_json, input_json, request_hash, idempotency_key,
  state, progress, current_attempt_id, version, created_at, updated_at, finished_at)
 VALUES ('completed','owner','s','format-conversion',?,'{"source_asset":"asset-input"}','hash','key',
  'SUCCEEDED',100,'completion',2,?,?,?)`, offering, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_attempts
 (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at, finished_at, delivery_completion_json)
 VALUES ('completion','completed',2,?,'DELIVERY_COMPLETED','delivery-completion:original',?,?,?,'{"kind":"plan_retry"}')`,
		offering, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_assets
 (id, task_id, attempt_id, role, variant, required, ordinal, storage_key, media_type, size_bytes, sha256, created_at, delivery_profile)
 VALUES ('asset','completed','completion','model','glb',0,1,'key/asset','model/glb',100,?,?,'')`,
		strings.Repeat("a", 64), stamp); err != nil {
		t.Fatal(err)
	}

	page, err := r.List(ctx, generation.OwnTasks("owner"), generation.ListQuery{
		SessionID: "s", States: []generation.TaskState{generation.TaskSucceeded}, Limit: 10,
	})
	if err != nil || len(page.Tasks) != 1 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	summary := page.Tasks[0]
	if !summary.DeliveryCompleted {
		t.Fatal("list summary lost DeliveryCompleted")
	}
	if detail, err := r.GetTaskSummary(ctx, generation.OwnTasks("owner"), "completed"); err != nil || !detail.DeliveryCompleted {
		t.Fatalf("summary detail = %+v, %v; want DeliveryCompleted", detail, err)
	}

	upstream := generation.Offering{ID: "target/upstream", ProviderID: "local",
		Parameters: []generation.Parameter{{Name: "source_task", Type: "task",
			Semantic: schema.SemanticSourceTask, SameProvider: true,
			TaskCapabilities: []generation.Capability{generation.FormatConversion}}}}
	if _, err := generation.BindModelSource(upstream, summary, "", nil); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("upstream route err = %v; want ErrInvalidInput", err)
	}

	assets := generation.Offering{ID: "target/assets", ProviderID: "local",
		Parameters: []generation.Parameter{{Name: "source_task", Type: "task",
			Semantic: schema.SemanticSourceTask, TaskSourceKind: schema.TaskSourceAssets,
			TaskOfferings:    []generation.OfferingID{"local/generated-glb/to-gltf"},
			TaskCapabilities: []generation.Capability{generation.FormatConversion},
			MediaTypes:       []string{"model/glb"}, Extensions: []string{".glb"}, MaxBytes: 1000}}}
	binding, err := generation.BindModelSource(assets, summary, "", nil)
	if err != nil || binding.AssetID != "asset" || binding.Value != "completed" {
		t.Fatalf("asset route = %+v, %v; want the saved asset", binding, err)
	}
}
