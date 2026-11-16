package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Diagnostics export (#234): the failure projection reads a live database
// without ever writing to it.

const diagnosticOfferingJSON = `{"id":"meshy/meshy-7/text-to-3d","provider_id":"meshy","capability":"text_to_3d",` +
	`"parameters":[{"name":"prompt","type":"text","semantic":"prompt"},{"name":"art_style","type":"select"}]}`

type diagnosticFixture struct {
	repository *Repository
	path       string
}

func newDiagnosticFixture(t *testing.T) diagnosticFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "art-ai.db")
	repository, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return diagnosticFixture{repository: repository, path: path}
}

type diagnosticTask struct {
	id, owner, state, input string
	created, finished       time.Time
	updated                 time.Time
	failure                 string
}

func (f diagnosticFixture) insertTask(t *testing.T, task diagnosticTask) {
	t.Helper()
	if task.updated.IsZero() {
		task.updated = task.created
	}
	var finished any
	if !task.finished.IsZero() {
		finished = formatTime(task.finished)
	}
	var failure any
	if task.failure != "" {
		failure = task.failure
	}
	if task.input == "" {
		task.input = `{"prompt":"a tiny dragon wearing a red hat","art_style":"realistic"}`
	}
	if _, err := f.repository.db.Exec(`
		INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash, state, version,
			created_at, updated_at, finished_at, failure_json, materialize_failures)
		VALUES (?, ?, 'text_to_3d', ?, ?, 'hash', ?, 1, ?, ?, ?, ?, 2)`,
		task.id, task.owner, diagnosticOfferingJSON, task.input, task.state,
		formatTime(task.created), formatTime(task.updated), finished, failure); err != nil {
		t.Fatal(err)
	}
}

func (f diagnosticFixture) insertAttempt(t *testing.T, taskID string, ordinal int, state, failure string) {
	t.Helper()
	var failureValue any
	if failure != "" {
		failureValue = failure
	}
	if _, err := f.repository.db.Exec(`
		INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key, upstream_provider_id,
			upstream_job_id, failure_json, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'meshy', ?, ?, ?, ?)`,
		taskID+"-a"+strings.Repeat("x", ordinal), taskID, ordinal, diagnosticOfferingJSON, state, taskID+"-key"+string(rune('0'+ordinal)),
		"job-secret-id-"+taskID+string(rune('0'+ordinal)),
		failureValue, formatTime(time.Unix(1_700_000_000, 0)), formatTime(time.Unix(1_700_000_100, 0))); err != nil {
		t.Fatal(err)
	}
}

func TestReadDiagnosticFailuresProjectsFailedTasksInTheRange(t *testing.T) {
	f := newDiagnosticFixture(t)
	base := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	rateLimited := `{"class":"rate_limited","retryable":true,"detail":"upstream busy","upstream_status":429,"upstream_code":"rate_limit"}`
	rejected := `{"class":"rejected","retryable":false,"detail":"refused","upstream_status":402,"upstream_code":"no_credit","upstream_excerpt":"not enough credit for owner@example.com"}`
	f.insertTask(t, diagnosticTask{id: "task_old", owner: "owner-1", state: "FAILED", created: base.Add(-72 * time.Hour), finished: base.Add(-71 * time.Hour), failure: rejected})
	f.insertTask(t, diagnosticTask{id: "task_ok", owner: "owner-1", state: "SUCCEEDED", created: base, finished: base.Add(time.Minute)})
	f.insertTask(t, diagnosticTask{id: "task_first", owner: "owner-1", state: "FAILED", created: base, finished: base.Add(10 * time.Minute), failure: rejected})
	// No finish time recorded: the last update dates the failure.
	f.insertTask(t, diagnosticTask{id: "task_second", owner: "owner-2", state: "FAILED", created: base, updated: base.Add(30 * time.Minute), failure: rateLimited})
	f.insertTask(t, diagnosticTask{id: "task_running", owner: "owner-1", state: "RUNNING", created: base})
	f.insertAttempt(t, "task_first", 1, "FAILED", rateLimited)
	f.insertAttempt(t, "task_first", 2, "FAILED", rejected)

	got, err := ReadDiagnosticFailures(context.Background(), f.path, base.Add(-time.Hour), base.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Available != 2 || len(got.Tasks) != 2 {
		t.Fatalf("available=%d tasks=%d, want the two failures inside the range: %+v", got.Available, len(got.Tasks), got.Tasks)
	}
	if got.Tasks[0].ID != "task_second" || got.Tasks[1].ID != "task_first" {
		t.Fatalf("newest first expected, got %s then %s", got.Tasks[0].ID, got.Tasks[1].ID)
	}
	second, first := got.Tasks[0], got.Tasks[1]
	if !second.FinishedAt.Equal(base.Add(30 * time.Minute)) {
		t.Fatalf("a failure without finish time is dated by its last update: %v", second.FinishedAt)
	}
	if first.OwnerID != "owner-1" || first.Capability != "text_to_3d" || first.OfferingID != "meshy/meshy-7/text-to-3d" ||
		first.ProviderID != "meshy" || first.MaterializeFailures != 2 {
		t.Fatalf("first = %+v", first)
	}
	if first.Failure.Class != "rejected" || first.Failure.UpstreamStatus != 402 || first.Failure.UpstreamCode != "no_credit" ||
		first.Failure.Retryable || !strings.Contains(first.Failure.UpstreamExcerpt, "not enough credit") {
		t.Fatalf("failure = %+v", first.Failure)
	}
	wantAttempts := []DiagnosticAttempt{
		{Ordinal: 1, State: "FAILED", ProviderID: "meshy", FailureClass: "rate_limited", UpstreamStatus: 429, UpstreamCode: "rate_limit"},
		{Ordinal: 2, State: "FAILED", ProviderID: "meshy", FailureClass: "rejected", UpstreamStatus: 402, UpstreamCode: "no_credit"},
	}
	if !reflect.DeepEqual(first.Attempts, wantAttempts) {
		t.Fatalf("attempts = %+v, want %+v", first.Attempts, wantAttempts)
	}
	// Only the declared free-text parameter is an echo: the prompt, not the style.
	if !reflect.DeepEqual(first.Echoes, []string{"a tiny dragon wearing a red hat"}) {
		t.Fatalf("echoes = %q", first.Echoes)
	}
	if len(second.Attempts) != 0 {
		t.Fatalf("a Task without Attempts has none: %+v", second.Attempts)
	}
	// Nothing about the upstream job or the input leaves the reader as a field.
	encoded, err := json.Marshal(got.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("job-secret-id")) {
		t.Fatalf("the upstream job id was projected: %s", encoded)
	}
}

func TestReadDiagnosticFailuresRangeOrderAndLimit(t *testing.T) {
	f := newDiagnosticFixture(t)
	base := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	failure := `{"class":"transport","retryable":true,"detail":"network"}`
	// Trimmed fractional zeros: ".5" sorts after ".52" as text, but not in time.
	for id, finished := range map[string]time.Time{
		"task_a": base.Add(500 * time.Millisecond), "task_b": base.Add(520 * time.Millisecond),
		"task_c": base.Add(time.Second), "task_d": base.Add(2 * time.Hour), "task_e": base.Add(-time.Second),
	} {
		f.insertTask(t, diagnosticTask{id: id, owner: "owner-1", state: "FAILED", created: base, finished: finished, failure: failure})
	}
	got, err := ReadDiagnosticFailures(context.Background(), f.path, base, base.Add(time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Available != 3 || len(got.Tasks) != 2 {
		t.Fatalf("available=%d tasks=%d, want 3 in range and the newest 2", got.Available, len(got.Tasks))
	}
	if got.Tasks[0].ID != "task_c" || got.Tasks[1].ID != "task_b" {
		t.Fatalf("order = %s, %s; want task_c (+1s) then task_b (+520ms)", got.Tasks[0].ID, got.Tasks[1].ID)
	}
	if _, err := ReadDiagnosticFailures(context.Background(), f.path, base, base.Add(time.Hour), 0); err == nil {
		t.Fatal("a zero limit must be refused")
	}
}

func TestReadDiagnosticFailuresSkipsAnInputTooLargeToRead(t *testing.T) {
	f := newDiagnosticFixture(t)
	base := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	huge := `{"prompt":"` + strings.Repeat("p", diagnosticInputBytes+10) + `"}`
	f.insertTask(t, diagnosticTask{id: "task_huge", owner: "owner-1", state: "FAILED", created: base, finished: base, input: huge,
		failure: `{"class":"rejected","detail":"x"}`})
	got, err := ReadDiagnosticFailures(context.Background(), f.path, base.Add(-time.Hour), base.Add(time.Hour), 5)
	if err != nil || len(got.Tasks) != 1 {
		t.Fatalf("tasks=%d err=%v", len(got.Tasks), err)
	}
	if len(got.Tasks[0].Echoes) != 0 {
		t.Fatalf("an input over %d bytes must not be read: %d echoes", diagnosticInputBytes, len(got.Tasks[0].Echoes))
	}
}

// The reader works beside the running service and must leave the database
// files exactly as they are.
func TestReadDiagnosticFailuresNeverWritesTheDatabase(t *testing.T) {
	f := newDiagnosticFixture(t)
	base := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	for _, id := range []string{"task_1", "task_2", "task_3"} {
		f.insertTask(t, diagnosticTask{id: id, owner: "owner-1", state: "FAILED", created: base, finished: base.Add(time.Minute),
			failure: `{"class":"rejected","detail":"x"}`})
	}
	digest := func() map[string][sha256.Size]byte {
		sums := map[string][sha256.Size]byte{}
		for _, suffix := range []string{"", "-wal"} {
			data, err := os.ReadFile(f.path + suffix)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			sums[suffix] = sha256.Sum256(data)
		}
		return sums
	}
	before := digest()
	if _, err := ReadDiagnosticFailures(context.Background(), f.path, base.Add(-time.Hour), base.Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, digest()) {
		t.Fatal("reading the failures changed the database or its WAL")
	}
	// Even a statement the file mode would allow is refused by the connection.
	db, err := openDiagnosticDatabase(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`UPDATE generation_tasks SET state = 'RUNNING'`, `DELETE FROM generation_tasks`, `CREATE TABLE diagnostic_probe (x)`,
		`PRAGMA user_version = 7`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Errorf("the diagnostics connection accepted %q", statement)
		}
	}
	var state string
	if err := f.repository.db.QueryRow(`SELECT state FROM generation_tasks WHERE id = 'task_1'`).Scan(&state); err != nil || state != "FAILED" {
		t.Fatalf("state = %q, %v", state, err)
	}
}

// With no service holding the database open there is no -wal or -shm file, and
// a plain read-only open would create them: files owned by whoever ran the
// export, which the service might then fail to reopen. Nothing is created.
func TestReadDiagnosticFailuresCreatesNoFileForAStoppedDatabase(t *testing.T) {
	f := newDiagnosticFixture(t)
	base := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	f.insertTask(t, diagnosticTask{id: "task_1", owner: "owner-1", state: "FAILED", created: base, finished: base.Add(time.Minute),
		failure: `{"class":"rejected","detail":"x"}`})
	if err := f.repository.Close(); err != nil { // the last connection: the WAL is checkpointed and removed
		t.Fatal(err)
	}
	directory := filepath.Dir(f.path)
	listing := func() []string {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}
	before := listing()
	for _, name := range before {
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
			t.Fatalf("the fixture still has %s: not a stopped database", name)
		}
	}
	got, err := ReadDiagnosticFailures(context.Background(), f.path, base.Add(-time.Hour), base.Add(time.Hour), 5)
	if err != nil || len(got.Tasks) != 1 || got.Tasks[0].ID != "task_1" {
		t.Fatalf("read: %+v, %v", got, err)
	}
	if after := listing(); !reflect.DeepEqual(before, after) {
		t.Fatalf("the export created files: %v -> %v", before, after)
	}
}

func TestReadDiagnosticFailuresRefusesWhatIsNotADatabaseFile(t *testing.T) {
	directory := t.TempDir()
	now := time.Now()
	for name, path := range map[string]string{
		"missing":   filepath.Join(directory, "missing.db"),
		"directory": directory,
	} {
		if _, err := ReadDiagnosticFailures(context.Background(), path, now.Add(-time.Hour), now, 5); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	link := filepath.Join(directory, "link.db")
	target := filepath.Join(directory, "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadDiagnosticFailures(context.Background(), link, now.Add(-time.Hour), now, 5); err == nil {
		t.Error("a symbolic link was accepted as the database")
	}
	// A database without the task table is an error, not an empty answer.
	plain := filepath.Join(directory, "plain.db")
	db, err := sql.Open("sqlite", plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated (x)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := ReadDiagnosticFailures(context.Background(), plain, now.Add(-time.Hour), now, 5); err == nil {
		t.Error("a database without generation_tasks was accepted")
	}
}
