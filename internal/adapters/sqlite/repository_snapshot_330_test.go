package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const snapshotStamp330 = "2026-10-23T00:00:00Z"

// aggregateReadGate330 holds the aggregate reader at the asset query so the
// test can commit a delivery completion in between. Without one read snapshot
// the result mixes pre-commit Task metadata with post-commit assets.
type aggregateReadGate330 struct {
	hitOnce     sync.Once
	releaseOnce sync.Once
	hit         chan struct{}
	release     chan struct{}
}

func newAggregateReadGate330() *aggregateReadGate330 {
	return &aggregateReadGate330{hit: make(chan struct{}), release: make(chan struct{})}
}

func (g *aggregateReadGate330) blockOnAssets(query string) {
	if g == nil || !strings.Contains(query, "FROM generation_assets") {
		return
	}
	g.hitOnce.Do(func() { close(g.hit) })
	<-g.release
}

func (g *aggregateReadGate330) open() {
	g.releaseOnce.Do(func() { close(g.release) })
}

type aggregateReadConnector330 struct {
	driver driver.Driver
	dsn    string
	gate   *aggregateReadGate330
}

func (c *aggregateReadConnector330) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &aggregateReadConn330{Conn: conn, gate: c.gate}, nil
}

func (c *aggregateReadConnector330) Driver() driver.Driver { return c.driver }

// aggregateReadConn330 deliberately wraps the driver.Conn interface instead of
// exposing modernc's QueryerContext, so database/sql always prepares queries and
// this gate sees the asset read in both the old (db) and new (tx) paths.
type aggregateReadConn330 struct {
	driver.Conn
	gate *aggregateReadGate330
}

func (c *aggregateReadConn330) Prepare(query string) (driver.Stmt, error) {
	c.gate.blockOnAssets(query)
	return c.Conn.Prepare(query)
}

func (c *aggregateReadConn330) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.gate.blockOnAssets(query)
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *aggregateReadConn330) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func openGatedAggregateRepository330(t *testing.T, path string, gate *aggregateReadGate330) *Repository {
	t.Helper()
	proxy, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	drv := proxy.Driver()
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	dsn, _, err := normalizeDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(&aggregateReadConnector330{driver: drv, dsn: dsn, gate: gate})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return &Repository{db: db, clock: func() time.Time { return time.Now().UTC() }}
}

func seedAggregateSnapshotTask330(t *testing.T, r *Repository) {
	t.Helper()
	offering := `{"id":"local/generated-glb/to-gltf","provider_id":"local","capability":"format-conversion"}`
	if _, err := r.db.Exec(`INSERT INTO generation_tasks
		(id, owner_id, session_id, capability, offering_json, input_json, request_hash,
		 idempotency_key, state, progress, current_attempt_id, version, created_at, updated_at, finished_at)
		VALUES ('failed', 'owner-a', NULL, 'format-conversion', ?, '{"source_asset":"input"}', 'hash', 'key',
		 'FAILED', 100, 'attempt-a', 1, ?, ?, ?)`,
		offering, snapshotStamp330, snapshotStamp330, snapshotStamp330); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_attempts
		(id, task_id, ordinal, offering_json, state, submission_key,
		 upstream_provider_id, upstream_job_id, opaque_json, started_at, updated_at, finished_at)
		VALUES ('attempt-a', 'failed', 1, ?, 'FAILED', 'submit-a', 'provider-x', 'remote-a',
		        '[{"identifier":"job-a"}]', ?, ?, ?)`,
		offering, snapshotStamp330, snapshotStamp330, snapshotStamp330); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_artifact_claims
		(attempt_id, claim_id, role, variant, locator, media_type)
		VALUES ('attempt-a', 'claim-a', 'model', 'glb', '', 'model/gltf-binary')`); err != nil {
		t.Fatal(err)
	}
}

func commitAggregateSnapshotDelivery330(t *testing.T, r *Repository) {
	t.Helper()
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	offering := `{"id":"local/generated-glb/to-gltf","provider_id":"local","capability":"format-conversion"}`
	if _, err := tx.ExecContext(ctx, `UPDATE generation_tasks
		SET state = 'SUCCEEDED', progress = 100, current_attempt_id = 'attempt-b',
		    version = 2, updated_at = ?, finished_at = ?
		WHERE id = 'failed'`, snapshotStamp330, snapshotStamp330); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO generation_attempts
		(id, task_id, ordinal, offering_json, state, submission_key,
		 started_at, updated_at, finished_at)
		VALUES ('attempt-b', 'failed', 2, ?, 'DELIVERY_COMPLETED',
		        'delivery-completion:attempt-a', ?, ?, ?)`,
		offering, snapshotStamp330, snapshotStamp330, snapshotStamp330); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO generation_assets
		(id, task_id, attempt_id, role, variant, required, ordinal, storage_key,
		 media_type, size_bytes, sha256, created_at, delivery_profile)
		VALUES ('asset-b', 'failed', 'attempt-b', 'model', 'glb', 1, 1, 'stored/asset-b',
		        'model/gltf-binary', 1, ?, ?, '')`,
		strings.Repeat("a", 64), snapshotStamp330); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func checkSnapshotTask330(t *testing.T, task generation.Task) {
	t.Helper()
	if task.State != generation.TaskFailed {
		t.Fatalf("task state = %s; want the pre-commit FAILED task", task.State)
	}
	if len(task.Assets) != 0 {
		t.Fatalf("assets = %+v; want the pre-commit empty asset set", task.Assets)
	}
}

func TestAggregateReadIsOneSnapshotAcrossDeliveryCompletion330(t *testing.T) {
	loaders := []struct {
		name  string
		load  func(context.Context, *Repository) (generation.Aggregate, error)
		check func(*testing.T, generation.Aggregate)
	}{
		{
			name: "Load",
			load: func(ctx context.Context, r *Repository) (generation.Aggregate, error) {
				return r.Load(ctx, "failed")
			},
		},
		{
			name: "LoadByUpstreamJob",
			load: func(ctx context.Context, r *Repository) (generation.Aggregate, error) {
				return r.LoadByUpstreamJob(ctx, "provider-x", "remote-a")
			},
		},
		{
			name: "Get",
			load: func(ctx context.Context, r *Repository) (generation.Aggregate, error) {
				task, err := r.Get(ctx, "owner-a", "failed")
				return generation.Aggregate{Task: task}, err
			},
			check: func(t *testing.T, aggregate generation.Aggregate) {
				checkSnapshotTask330(t, aggregate.Task)
				if aggregate.Task.CurrentAttemptID != "attempt-a" {
					t.Fatalf("current attempt id = %s; want attempt-a", aggregate.Task.CurrentAttemptID)
				}
				if string(aggregate.Task.ProviderData) != `[{"identifier":"job-a"}]` {
					t.Fatalf("provider data = %s; want attempt-a data", aggregate.Task.ProviderData)
				}
				if aggregate.Task.DeliveryCompleted {
					t.Fatal("DeliveryCompleted = true for the pre-commit failed task")
				}
			},
		},
	}
	for _, loader := range loaders {
		t.Run(loader.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.db")
			seed, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			seedAggregateSnapshotTask330(t, seed)
			if err := seed.db.Close(); err != nil {
				t.Fatal(err)
			}
			writer, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })

			gate := newAggregateReadGate330()
			// Deferred opens run before t.Cleanup, so a failed writer commit
			// unblocks the reader before the reader's db.Close cleanup.
			defer gate.open()
			reader := openGatedAggregateRepository330(t, path, gate)
			type loadResult struct {
				aggregate generation.Aggregate
				err       error
			}
			done := make(chan loadResult, 1)
			go func() {
				aggregate, err := loader.load(context.Background(), reader)
				done <- loadResult{aggregate: aggregate, err: err}
			}()
			select {
			case <-gate.hit:
			case <-time.After(10 * time.Second):
				t.Fatal("aggregate read did not reach the asset query")
			}
			commitAggregateSnapshotDelivery330(t, writer)
			gate.open()
			result := <-done
			if result.err != nil {
				t.Fatal(result.err)
			}
			if loader.check != nil {
				loader.check(t, result.aggregate)
				return
			}
			checkSnapshotTask330(t, result.aggregate.Task)
			if result.aggregate.CurrentAttempt == nil || result.aggregate.CurrentAttempt.ID != "attempt-a" {
				t.Fatalf("current attempt = %+v; want attempt-a", result.aggregate.CurrentAttempt)
			}
			if len(result.aggregate.Claims) != 1 || result.aggregate.Claims[0].ID != "claim-a" {
				t.Fatalf("claims = %+v; want the pre-commit claim-a set", result.aggregate.Claims)
			}
		})
	}
}

func TestAggregateLoadPreservesNotFoundAndTombstone330(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	if _, err := r.Load(ctx, "missing"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("missing Load error = %v", err)
	}
	if _, err := r.LoadByUpstreamJob(ctx, "provider-x", "missing"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("missing LoadByUpstreamJob error = %v", err)
	}
	if _, err := r.Get(ctx, "owner-a", "missing"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("missing Get error = %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Get(cancelled, "owner-a", "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Get error = %v", err)
	}
	seedAggregateSnapshotTask330(t, r)
	if _, err := r.Get(ctx, "owner-b", "failed"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("cross-owner Get error = %v", err)
	}
	if _, err := r.db.Exec(`INSERT INTO generation_task_tombstones
		(task_id, actor_id, deleted_at) VALUES ('failed', 'owner-a', ?)`, snapshotStamp330); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Load(ctx, "failed"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("tombstoned Load error = %v", err)
	}
	if _, err := r.LoadByUpstreamJob(ctx, "provider-x", "remote-a"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("tombstoned LoadByUpstreamJob error = %v", err)
	}
	if _, err := r.Get(ctx, "owner-a", "failed"); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("tombstoned Get error = %v", err)
	}
}
