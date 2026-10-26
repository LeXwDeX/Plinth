package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	_ "modernc.org/sqlite"
)

// #324: a Tripo segmentation Task was charged 90 (40 base + five fixed exports at 10), and
// its plan, the Task detail and credits_charged all said 90, while the administration record
// read the bare `estimated_credits` of the frozen Offering and said 40. The record must be
// the frozen Offering's estimate of the frozen input, the number the user side shows.
func TestAdministrationRecordsEstimateTheFrozenInputNotTheBarePrice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "estimate.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	at := time.Date(2026, time.October, 22, 1, 0, 0, 0, time.UTC)
	segment, _ := json.Marshal(map[string]any{
		"id": "tripo/geometry/segment", "provider_id": "tripo", "model_id": "tripo-segment", "capability": "mesh-segmentation",
		"estimated_credits": 40, "credits_metered": true,
		"parameters": []map[string]any{{"name": "output_formats", "type": "string-array", "fixed": true,
			"default": []string{"FBX", "OBJ", "STL", "USDZ", "3MF"}}},
		"cost": map[string]any{"base_credits": 40, "extra_credits": 10, "selection_parameter": "output_formats",
			"consent_parameter": "authorize_extra_credits"},
	})
	images, _ := json.Marshal(map[string]any{
		"id": "image/batch", "provider_id": "meshy", "model_id": "batch", "capability": "text-to-image",
		"estimated_credits": 6, "credits_metered": true, "estimated_credits_unit_parameter": "n",
	})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, task := range []struct{ id, capability, offering, input string }{
		{"task-segment", "mesh-segmentation", string(segment), `{"input":"task-source","output_formats":["FBX","OBJ","STL","USDZ","3MF"],"authorize_extra_credits":true}`},
		{"task-images", "text-to-image", string(images), `{"prompt":"cat","n":3}`},
	} {
		if _, err := db.Exec(`INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash, state,
			progress, version, created_at, updated_at, finished_at) VALUES (?, 'owner', ?, ?, ?, ?, 'SUCCEEDED', 100, 1, ?, ?, ?)`,
			task.id, task.capability, task.offering, task.input, task.id+"-hash",
			at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]int{"task-segment": 90, "task-images": 18}

	records, _, err := repository.QueryAdministrationGenerations(context.Background(), administration.GenerationQuery{Page: 1, PageSize: 10})
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v err=%v", records, err)
	}
	for _, record := range records {
		if record.EstimatedCredits != want[record.TaskID] {
			t.Errorf("record %s estimated %d; want %d", record.TaskID, record.EstimatedCredits, want[record.TaskID])
		}
	}
	detail, found, err := repository.GetAdministrationGeneration(context.Background(), "task-segment")
	if err != nil || !found || detail.Ledger.EstimatedCredits != 90 {
		t.Fatalf("detail = %+v found=%v err=%v", detail.Ledger, found, err)
	}
	buckets, err := repository.QueryAdministrationUsage(context.Background(), administration.UsageQuery{Since: at.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, bucket := range buckets {
		total += bucket.EstimatedCredits
	}
	if total != 108 {
		t.Fatalf("usage total = %d; want 90 + 18 (%+v)", total, buckets)
	}
}
