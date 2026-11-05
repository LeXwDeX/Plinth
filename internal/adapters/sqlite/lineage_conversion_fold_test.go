package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// #330 review of !341: the fold of format conversions starts when the
// upgraded binary first opens the database, and that moment never moves:
// conversions made before it keep their version numbers.
func TestConversionFoldStartsWhenTheUpgradeFirstRuns330(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fold.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := r.ConversionFoldSince(ctx)
	if err != nil || time.Since(created) > time.Minute || time.Until(created) > time.Second {
		t.Fatalf("a new database folds from its creation: %v %v", created, err)
	}
	// A database from before 0068, as an existing deployment has it.
	for _, statement := range []string{`DELETE FROM go_schema_migrations WHERE version >= 68`, `DROP TABLE lineage_conversion_fold`, `DROP TABLE budget_reservations`, `DROP TABLE production_budgets`, `ALTER TABLE execution_grants DROP COLUMN budget_eligible`, `ALTER TABLE processing_plans DROP COLUMN origin`, `DROP INDEX IF EXISTS generation_attempts_delivery_completion`, `ALTER TABLE generation_attempts DROP COLUMN delivery_completion_json`, `ALTER TABLE generation_attempts DROP COLUMN rate_limit_queued`, `ALTER TABLE generation_attempts DROP COLUMN last_observation_queue_json`, `ALTER TABLE agent_generation_operations DROP COLUMN reference_source`, `ALTER TABLE skill_run_steps DROP COLUMN replacement_asset_id`, `ALTER TABLE go_chat_messages DROP COLUMN multiview`, `ALTER TABLE go_chat_sessions DROP COLUMN brief`, `ALTER TABLE go_chat_sessions DROP COLUMN explicit_model_3d`, `ALTER TABLE chat_choices DROP COLUMN kind`} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	beforeUpgrade := time.Now().UTC()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	since, err := upgraded.ConversionFoldSince(ctx)
	if err != nil || since.Before(beforeUpgrade.Add(-time.Second)) {
		t.Fatalf("the fold starts at the upgrade: %v (upgrade at %v) %v", since, beforeUpgrade, err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if again, err := reopened.ConversionFoldSince(ctx); err != nil || !again.Equal(since) {
		t.Fatalf("the start moved on reopen: %v -> %v %v", since, again, err)
	}
}
