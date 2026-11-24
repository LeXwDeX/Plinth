package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// historicalSummaryTriggersSQL are the summary triggers a database has before
// 0113, taken from 0020 itself so the test cannot drift from what production
// databases hold.
func historicalSummaryTriggersSQL(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("migrations", "0020_task_read_projections.sql"))
	if err != nil {
		t.Fatal(err)
	}
	triggers := regexp.MustCompile(`(?s)CREATE TRIGGER generation_task_summary_[a-z]+ .*?\nEND;`).FindAllString(string(raw), -1)
	if len(triggers) != 2 {
		t.Fatalf("0020 summary triggers = %d", len(triggers))
	}
	return triggers
}

// #517: 0113 makes the task summary projection carry the frozen Offering's
// credits_metered. Rows written before it are backfilled from the task, and
// both triggers keep new and changed tasks in step; running it again changes
// nothing.
func TestMigration0113BackfillsAndSyncsTheSummaryCreditsMetered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summaries.db")
	r, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	// Put the database back where 0112 left it: the 0020 triggers and no
	// ledger row for 0113.
	setup := []string{`DELETE FROM go_schema_migrations WHERE version = 113`,
		`DROP TRIGGER generation_task_summary_insert`, `DROP TRIGGER generation_task_summary_update`}
	setup = append(setup, historicalSummaryTriggersSQL(t)...)
	for _, statement := range setup {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	insert := func(db *Repository, id, offering string) {
		t.Helper()
		if _, err := db.db.Exec(`INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash, state,
			progress, version, created_at, updated_at) VALUES (?, 'owner-a', 'text-to-3d', ?, '{"prompt":"barrel"}', ?, 'FAILED', 0, 1,
			'2026-11-24T00:00:00Z', '2026-11-24T00:00:00Z')`, id, offering, id+"-hash"); err != nil {
			t.Fatal(err)
		}
	}
	const metered = `{"id":"meshy/text-to-3d","provider_id":"meshy","model_id":"m","credits_metered":true,"parameters":[{"name":"prompt","semantic":"prompt"}]}`
	const unmetered = `{"id":"openai/image","provider_id":"openai","model_id":"i","parameters":[{"name":"prompt","semantic":"prompt"}]}`
	insert(r, "task-old-metered", metered)
	insert(r, "task-old-unmetered", unmetered)
	var carried int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM generation_task_summaries WHERE json_extract(offering_json, '$.credits_metered') IS NOT NULL`).Scan(&carried); err != nil || carried != 0 {
		t.Fatalf("the historical triggers already carry the flag: %d err=%v", carried, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var head int
	if err := upgraded.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head != MigrationHead() || head < 113 {
		t.Fatalf("head = %d err=%v", head, err)
	}
	meteredOf := func(id string) bool {
		t.Helper()
		task, err := upgraded.GetTaskSummary(context.Background(), generation.OwnTasks("owner-a"), generation.TaskID(id))
		if err != nil {
			t.Fatal(err)
		}
		return task.Offering.CreditsMetered
	}
	if !meteredOf("task-old-metered") || meteredOf("task-old-unmetered") {
		t.Fatalf("backfill: metered=%v unmetered=%v", meteredOf("task-old-metered"), meteredOf("task-old-unmetered"))
	}
	// New and changed tasks: both triggers write the flag.
	insert(upgraded, "task-new-metered", metered)
	insert(upgraded, "task-new-unmetered", unmetered)
	if !meteredOf("task-new-metered") || meteredOf("task-new-unmetered") {
		t.Fatal("the insert trigger does not carry credits_metered")
	}
	if _, err := upgraded.db.Exec(`UPDATE generation_tasks SET offering_json = ? WHERE id = 'task-new-unmetered'`, metered); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.db.Exec(`UPDATE generation_tasks SET offering_json = ? WHERE id = 'task-old-metered'`, unmetered); err != nil {
		t.Fatal(err)
	}
	if !meteredOf("task-new-unmetered") || meteredOf("task-old-metered") {
		t.Fatal("the update trigger does not keep credits_metered in step")
	}
	// The rest of the summary is what 0020 wrote: the identity and the prompt.
	task, err := upgraded.GetTaskSummary(context.Background(), generation.OwnTasks("owner-a"), "task-new-metered")
	if err != nil || task.Offering.ID != "meshy/text-to-3d" || task.Offering.ProviderID != "meshy" || len(task.Offering.Parameters) != 1 {
		t.Fatalf("summary = %+v err=%v", task.Offering, err)
	}

	// Running the backfill again writes nothing.
	raw, err := os.ReadFile(filepath.Join("migrations", "0113_task_summary_credits_metered.sql"))
	if err != nil {
		t.Fatal(err)
	}
	backfill := regexp.MustCompile(`(?s)UPDATE generation_task_summaries.*?;`).FindString(string(raw))
	result, err := upgraded.db.Exec(backfill)
	if err != nil {
		t.Fatal(err)
	}
	if changed, _ := result.RowsAffected(); changed != 0 {
		t.Fatalf("a second backfill changed %d rows", changed)
	}
}
