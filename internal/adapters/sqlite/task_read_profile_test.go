package sqlite

import (
	"context"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// Run explicitly to record latency/allocation evidence for a 60 MiB data set.
func TestTaskReadProjectionProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("large-row profile")
	}
	r, err := Open(filepath.Join(t.TempDir(), "profile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	input := `{"custom_prompt":"profile prompt","image":"` + strings.Repeat("x", 1<<20) + `"}`
	for i := range 48 {
		seedReadProjectionTask(t, r, fmt.Sprintf("task-%03d", i), input)
	}
	ctx := context.Background()
	query := generation.ListQuery{Limit: 20, ActiveFirst: true}
	fullPage := func() error {
		var total int
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_tasks WHERE owner_id='owner' AND NOT EXISTS(SELECT 1 FROM generation_task_tombstones WHERE task_id=generation_tasks.id)`).Scan(&total); err != nil {
			return err
		}
		ids, err := r.listTaskIDs(ctx, r.db, []string{"owner_id='owner'"}, nil, `CASE WHEN state IN ('RUNNING','MATERIALIZING') THEN 0 ELSE 1 END,created_at DESC,id DESC`, 21, 0)
		if err != nil {
			return err
		}
		ids = ids[:20]
		args := make([]any, len(ids))
		marks := make([]string, len(ids))
		for i, id := range ids {
			args[i] = id
			marks[i] = "?"
		}
		rows, err := r.db.QueryContext(ctx, taskSelect+" WHERE id IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return err
		}
		tasks := []generation.Task{}
		for rows.Next() {
			task, err := scanTask(rows)
			if err != nil {
				rows.Close()
				return err
			}
			tasks = append(tasks, task)
		}
		rows.Close()
		for _, task := range tasks {
			if _, err := r.loadAssets(ctx, r.db, task.ID); err != nil {
				return err
			}
		}
		return nil
	}
	profile := func(name string, fn func() error) uint64 {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		durations := make([]time.Duration, 30)
		for i := range durations {
			start := time.Now()
			if err := fn(); err != nil {
				t.Fatal(err)
			}
			durations[i] = time.Since(start)
		}
		runtime.ReadMemStats(&after)
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		allocated := (after.TotalAlloc - before.TotalAlloc) / uint64(len(durations))
		t.Logf("%s: rows=48 input=1MiB offering=256KiB page=20 samples=30 P95=%s P99=%s alloc/op=%d bytes", name, durations[28], durations[29], allocated)
		return allocated
	}
	before := profile("previous full rows", fullPage)
	after := profile("summary rows", func() error { _, err := r.List(ctx, generation.OwnTasks("owner"), query); return err })
	if after >= before/10 {
		t.Fatalf("list allocations not reduced: before=%d after=%d", before, after)
	}
	profile("version poll", func() error { _, err := r.GetTaskVersion(ctx, generation.OwnTasks("owner"), "task-000"); return err })
	source, err := r.administrationGenerationSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	profile("previous admin full-row sort", func() error {
		var total int
		if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+source+")").Scan(&total); err != nil {
			return err
		}
		rows, err := r.db.QueryContext(ctx, `SELECT `+administrationGenerationColumns+` FROM (`+source+`) ORDER BY created_unix DESC,task_id DESC LIMIT 20`)
		if err != nil {
			return err
		}
		var tasks []string
		for rows.Next() {
			row, err := scanAdministrationGeneration(rows)
			if err != nil {
				rows.Close()
				return err
			}
			tasks = append(tasks, row.record.TaskID)
		}
		rows.Close()
		for _, id := range tasks {
			if _, err := r.loadAdministrationGenerationAssets(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
	profile("indexed admin identifiers", func() error {
		_, _, err := r.QueryAdministrationGenerations(ctx, administration.GenerationQuery{Page: 1, PageSize: 20})
		return err
	})

}
