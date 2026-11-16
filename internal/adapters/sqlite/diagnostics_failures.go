package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Read-only failure projection for the diagnostics export (#234). The export
// runs next to a live service (docker exec), so the database is opened with
// mode=ro and query_only: no migration, no journal change, no write of any
// kind. It is not immutable=1: that would hide the WAL frames of a running
// service, which hold the most recent failures.

const (
	// diagnosticInputBytes bounds the input_json read per Task. Only the
	// free-text values of the input are used, to strip their echoes from the
	// bundle; a larger input is not read at all.
	diagnosticInputBytes = 256 << 10
	// diagnosticAttemptsPerTask keeps the latest Attempts of a Task.
	diagnosticAttemptsPerTask = 10
	// diagnosticAttemptChunk keeps the IN list of the Attempt query short.
	diagnosticAttemptChunk = 400
)

// DiagnosticAttempt is what an Attempt contributes to a failure summary: how
// it ended, never its upstream job or checkpoint.
type DiagnosticAttempt struct {
	Ordinal        int
	State          string
	ProviderID     string
	FailureClass   string
	UpstreamStatus int
	UpstreamCode   string
}

// DiagnosticFailedTask is the read-only projection of one FAILED Task.
// Failure.UpstreamExcerpt is already sanitized by the kernel when it is
// persisted; the exporter sanitizes it once more.
type DiagnosticFailedTask struct {
	ID                  string
	OwnerID             string
	Capability          string
	OfferingID          string
	ProviderID          string
	CreatedAt           time.Time
	FinishedAt          time.Time
	MaterializeFailures int
	Failure             generation.Failure
	Attempts            []DiagnosticAttempt
	// Echoes are the free-text values of the Task's frozen input (prompts).
	// They exist only so the exporter can remove them from everything it
	// writes; they are never part of a bundle.
	Echoes []string
}

// DiagnosticFailures is the newest failed Tasks in a time range and how many
// failed in total in that range.
type DiagnosticFailures struct {
	Available int
	Tasks     []DiagnosticFailedTask
}

// ReadDiagnosticFailures reads the FAILED Tasks that finished (or, when no
// finish time was recorded, were last updated) between since and until,
// newest first, at most limit of them. The time test uses julianday(), so the
// trimmed fractional zeros of RFC 3339 text do not change the order.
func ReadDiagnosticFailures(ctx context.Context, databasePath string, since, until time.Time, limit int) (DiagnosticFailures, error) {
	if limit <= 0 {
		return DiagnosticFailures{}, errors.New("诊断读取需要正的条数上限")
	}
	db, err := openDiagnosticDatabase(databasePath)
	if err != nil {
		return DiagnosticFailures{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DiagnosticFailures{}, err
	}
	defer tx.Rollback()
	lower, upper := formatTime(since), formatTime(until)
	const inRange = `state = 'FAILED' AND julianday(COALESCE(finished_at, updated_at)) BETWEEN julianday(?) AND julianday(?)`
	var result DiagnosticFailures
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM generation_tasks WHERE `+inRange, lower, upper).Scan(&result.Available); err != nil {
		return DiagnosticFailures{}, fmt.Errorf("统计失败任务: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, owner_id, capability, offering_json,
		       CASE WHEN length(input_json) <= ? THEN input_json ELSE '' END,
		       created_at, finished_at, updated_at, materialize_failures, failure_json
		FROM generation_tasks
		WHERE `+inRange+`
		ORDER BY julianday(COALESCE(finished_at, updated_at)) DESC, id DESC
		LIMIT ?`, diagnosticInputBytes, lower, upper, limit)
	if err != nil {
		return DiagnosticFailures{}, fmt.Errorf("读取失败任务: %w", err)
	}
	defer rows.Close()
	index := map[string]int{}
	for rows.Next() {
		var task DiagnosticFailedTask
		var offeringJSON, inputJSON, createdAt, updatedAt string
		var finishedAt, failureJSON sql.NullString
		if err := rows.Scan(&task.ID, &task.OwnerID, &task.Capability, &offeringJSON, &inputJSON,
			&createdAt, &finishedAt, &updatedAt, &task.MaterializeFailures, &failureJSON); err != nil {
			return DiagnosticFailures{}, err
		}
		if task.CreatedAt, err = parseTime(createdAt); err != nil {
			return DiagnosticFailures{}, err
		}
		finished := updatedAt
		if finishedAt.Valid && finishedAt.String != "" {
			finished = finishedAt.String
		}
		if task.FinishedAt, err = parseTime(finished); err != nil {
			return DiagnosticFailures{}, err
		}
		var offering generation.Offering
		if json.Unmarshal([]byte(offeringJSON), &offering) == nil {
			task.OfferingID = string(offering.ID)
			task.ProviderID = offering.ProviderID
			if inputJSON != "" {
				task.Echoes = generation.InputEchoes(offering, json.RawMessage(inputJSON))
			}
		}
		task.Failure = decodeDiagnosticFailure(failureJSON)
		index[task.ID] = len(result.Tasks)
		result.Tasks = append(result.Tasks, task)
	}
	if err := rows.Err(); err != nil {
		return DiagnosticFailures{}, err
	}
	rows.Close()
	if err := readDiagnosticAttempts(ctx, tx, &result, index); err != nil {
		return DiagnosticFailures{}, err
	}
	return result, nil
}

func readDiagnosticAttempts(ctx context.Context, tx *sql.Tx, result *DiagnosticFailures, index map[string]int) error {
	ids := make([]string, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		ids = append(ids, task.ID)
	}
	for start := 0; start < len(ids); start += diagnosticAttemptChunk {
		chunk := ids[start:min(start+diagnosticAttemptChunk, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT task_id, ordinal, state, COALESCE(upstream_provider_id, ''), failure_json
			FROM generation_attempts
			WHERE task_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)
			ORDER BY task_id, ordinal`, args...)
		if err != nil {
			return fmt.Errorf("读取失败任务的尝试: %w", err)
		}
		for rows.Next() {
			var taskID string
			var attempt DiagnosticAttempt
			var failureJSON sql.NullString
			if err := rows.Scan(&taskID, &attempt.Ordinal, &attempt.State, &attempt.ProviderID, &failureJSON); err != nil {
				rows.Close()
				return err
			}
			failure := decodeDiagnosticFailure(failureJSON)
			attempt.FailureClass, attempt.UpstreamStatus, attempt.UpstreamCode = string(failure.Class), failure.UpstreamStatus, failure.UpstreamCode
			position := index[taskID]
			task := &result.Tasks[position]
			task.Attempts = append(task.Attempts, attempt)
			if len(task.Attempts) > diagnosticAttemptsPerTask {
				task.Attempts = task.Attempts[len(task.Attempts)-diagnosticAttemptsPerTask:]
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

// decodeDiagnosticFailure reads a stored failure; a missing or unreadable one
// is the zero value, which the export shows as "no recorded failure".
func decodeDiagnosticFailure(raw sql.NullString) generation.Failure {
	var failure generation.Failure
	if !raw.Valid || strings.TrimSpace(raw.String) == "" || strings.TrimSpace(raw.String) == "null" {
		return failure
	}
	if err := json.Unmarshal([]byte(raw.String), &failure); err != nil {
		return generation.Failure{}
	}
	return failure
}

// openDiagnosticDatabase opens an existing database file read-only. Nothing is
// created, migrated or checkpointed, and the connection refuses writes even if
// the file mode would allow them.
//
// While a service has the database open its -wal and -shm files exist, and a
// plain read-only open reads through them (the newest failures may still be
// only in the WAL). When neither exists no connection has the database open,
// nothing is in a WAL, and a plain open would create both files: an export run
// as another user would leave files the service cannot reopen at its next
// start. That case is opened immutable instead, which touches nothing.
func openDiagnosticDatabase(databasePath string) (*sql.DB, error) {
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("数据库不是普通文件")
	}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(1)"}}
	if !sidecarExists(absolute+"-wal") && !sidecarExists(absolute+"-shm") {
		query.Set("immutable", "1")
	}
	dsn := url.URL{Scheme: "file", Path: absolute, RawQuery: query.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func sidecarExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
