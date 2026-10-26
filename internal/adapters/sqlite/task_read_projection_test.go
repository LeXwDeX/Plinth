package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"path/filepath"
	"strings"
	"testing"
)

func seedReadProjectionTask(t *testing.T, r *Repository, id string, input string) {
	t.Helper()
	_, err := r.db.Exec(`INSERT INTO generation_tasks(id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES (?,?,?,?,?,?,'RUNNING',1,?,?)`, id, "owner", "text-to-image", `{"id":"offering","provider_id":"provider","model_id":"model","parameters":[{"name":"custom_prompt","semantic":"prompt"}],"provider_options":{"huge":"`+strings.Repeat("x", 256<<10)+`"}}`, input, "hash-"+id, "2026-09-04T00:00:00Z", "2026-09-04T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
}
func TestTaskListReadsOnlyPresentationSummary(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "summary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedReadProjectionTask(t, r, "task", `{"custom_prompt":"keep this","large_image":"`+strings.Repeat("x", 1<<20)+`"}`)
	page, err := r.List(context.Background(), generation.OwnTasks("owner"), generation.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 {
		t.Fatalf("page: %+v", page)
	}
	task := page.Tasks[0]
	encoded, _ := json.Marshal(task.Offering)
	if len(task.Input) > 100 || len(encoded) > 2048 {
		t.Fatalf("list loaded unused payloads: input=%d offering=%d", len(task.Input), len(encoded))
	}
	if !strings.Contains(string(task.Input), "keep this") || task.Offering.ID != "offering" {
		t.Fatalf("summary lost presentation contract: %+v", task)
	}
	full, err := r.Get(context.Background(), "owner", "task")
	if err != nil || len(full.Input) < 1<<20 {
		t.Fatalf("detail lost input: %d %v", len(full.Input), err)
	}
}
func TestBatchTaskReadDoesNotResurrectTombstones(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "deleted.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedReadProjectionTask(t, r, "deleted", `{}`)
	if _, err := r.db.Exec(`INSERT INTO generation_task_tombstones(task_id,actor_id,deleted_at) VALUES ('deleted','owner','2026-09-04T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	tasks, err := r.loadTasksByID(context.Background(), []generation.TaskID{"deleted"})
	if err != nil || len(tasks) != 0 {
		t.Fatalf("deleted task returned: %d %v", len(tasks), err)
	}
}

func TestTaskReadProjectionPlans(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	order := `CASE WHEN state IN ('RUNNING', 'MATERIALIZING') THEN 0 ELSE 1 END, created_at DESC, id DESC`
	for _, tc := range []struct {
		name, where, index string
		args               []any
	}{
		{"owner", "owner_id = ?", "generation_tasks_owner_active_created", []any{"owner"}},
		{"admin", "1 = 1", "generation_tasks_active_created", nil},
		{"owner capability", "owner_id = ? AND capability = ?", "generation_tasks_owner_capability_active_created", []any{"owner", "text-to-image"}},
		{"substring search", "owner_id = ? AND id LIKE ?", "generation_tasks_owner_active_created", []any{"owner", "%task%"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := r.db.Query("EXPLAIN QUERY PLAN SELECT id FROM generation_tasks WHERE "+tc.where+" AND NOT EXISTS (SELECT 1 FROM generation_task_tombstones WHERE task_id=generation_tasks.id) ORDER BY "+order+" LIMIT 20", tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			joined := strings.Join(plan, "; ")
			t.Log(joined)
			if strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, tc.index) {
				t.Fatalf("unexpected task list plan: %s", joined)
			}
		})
	}
	idSQL, args := administrationGenerationIDsSQL(administration.GenerationQuery{Page: 1, PageSize: 20}, false)
	rows, err := r.db.Query("EXPLAIN QUERY PLAN "+idSQL, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		t.Log("admin ledger:", detail)
	}
	if strings.Contains(strings.Join(plan, ";"), "TEMP B-TREE") {
		t.Fatal("administration identifiers still require temporary sorting")
	}

}

func TestTaskSummaryTracksChangedInputs(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "update.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedReadProjectionTask(t, r, "task", `{"custom_prompt":"before"}`)
	if _, err := r.db.Exec(`UPDATE generation_tasks SET input_json='{"custom_prompt":"after","irrelevant":"blob"}' WHERE id='task'`); err != nil {
		t.Fatal(err)
	}
	task, err := r.GetTaskSummary(context.Background(), generation.OwnTasks("owner"), "task")
	if err != nil || string(task.Input) != `{"custom_prompt":"after"}` {
		t.Fatalf("stale summary: %s %v", task.Input, err)
	}
	if _, err := r.GetTaskSummary(context.Background(), generation.OwnTasks("other"), "task"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("cross-owner summary: %v", err)
	}
	if version, err := r.GetTaskVersion(context.Background(), generation.OwnTasks("owner"), "task"); err != nil || version != 1 {
		t.Fatalf("version=%d error=%v", version, err)
	}
}

func TestTaskListSnapshotDoesNotMixConcurrentDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	seedReadProjectionTask(t, r, "task", `{}`)
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM generation_tasks").Scan(&total); err != nil {
		t.Fatal(err)
	}
	ids, err := r.listTaskIDs(ctx, tx, []string{"1=1"}, nil, "created_at DESC, id DESC", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.db.Exec(`INSERT INTO generation_task_tombstones(task_id,actor_id,deleted_at) VALUES ('task','owner','2026-09-04T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	tasks, err := r.loadTaskSummaries(ctx, tx, ids)
	if err != nil || total != 1 || len(tasks) != 1 {
		t.Fatalf("snapshot inconsistent: total=%d tasks=%d err=%v", total, len(tasks), err)
	}
	tx.Rollback()
	page, err := r.List(ctx, generation.OwnTasks("owner"), generation.ListQuery{Limit: 10})
	if err != nil || page.Total != 0 || len(page.Tasks) != 0 {
		t.Fatalf("next snapshot retains tombstone: %+v %v", page, err)
	}
}

func TestRepositoryPaginationRejectsUnsafeLimitAndNeverOverflows(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "bounds.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedReadProjectionTask(t, r, "task", `{}`)
	for _, q := range []generation.ListQuery{{Limit: 0}, {Limit: -1}, {Limit: int(^uint(0) >> 1)}, {Limit: 1, Offset: -1}} {
		if _, err := r.List(context.Background(), generation.OwnTasks("owner"), q); err == nil {
			t.Fatalf("accepted unsafe query: %+v", q)
		}
	}
	offset := generation.PageOffset(int(^uint(0)>>1), 100)
	page, err := r.List(context.Background(), generation.OwnTasks("owner"), generation.ListQuery{Limit: 100, Offset: offset})
	if err != nil || len(page.Tasks) != 0 || page.Total != 1 {
		t.Fatalf("saturated page: %+v %v", page, err)
	}
	records, total, err := r.QueryAdministrationGenerations(context.Background(), administration.GenerationQuery{Page: int(^uint(0) >> 1), PageSize: 100})
	if err != nil || len(records) != 0 || total != 1 {
		t.Fatalf("saturated admin page: %d %d %v", len(records), total, err)
	}
}

// TestLegacyOfferingSnapshotDecodesWithoutResourceContractFields 覆盖
// §3.4：历史 offering_json（outputs 无 variant/delivery，minimum 恒存在）
// 经仓库读回后保持旧语义：Minimum 解码为显式 1、Delivery/Variant 为空，
// 快照字节零改写。
func TestLegacyOfferingSnapshotDecodesWithoutResourceContractFields(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "legacy-offering.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	offeringJSON := `{"id":"legacy-offering","provider_id":"meshy","model_id":"meshy-7","parameters":[],"outputs":[{"role":"model","minimum":1,"media_types":["model/gltf-binary"]}]}`
	_, err = r.db.Exec(
		`INSERT INTO generation_tasks(id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at)
		 VALUES ('legacy-snapshot-task','owner','text-to-3d',?,?,'hash','SUCCEEDED',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		offeringJSON,
		`{"prompt":"history"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := r.Get(context.Background(), "owner", "legacy-snapshot-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Offering.Outputs) != 1 {
		t.Fatalf("outputs = %#v", task.Offering.Outputs)
	}
	output := task.Offering.Outputs[0]
	if output.Minimum == nil || *output.Minimum != 1 ||
		output.Delivery != "" || output.Variant != "" ||
		output.VariantPrefix != "" || output.Group != "" {
		t.Fatalf("legacy output decoded = %#v", output)
	}
}
