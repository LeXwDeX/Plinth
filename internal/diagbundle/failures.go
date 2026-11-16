package diagbundle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The failure summary answers "what failed, and what did the upstream say"
// without the work itself: the task's failure class, the upstream HTTP status
// and error code, and a short excerpt of the upstream's error text. The excerpt
// was sanitized by the kernel when it was stored (generation.UpstreamExcerpt:
// echoes of the prompt, URLs, tokens, e-mail addresses removed); it goes through
// that sanitizer once more here with the task's own prompt as the echo to
// strip, is cut shorter, and is scrubbed like any free text. The database is
// read-only, the input of a task is only read to learn which of its words must
// not appear, and nothing of it is written.

type failuresDocument struct {
	Tasks   []failureTask  `json:"tasks"`
	Summary failureSummary `json:"summary"`
}

type failureTask struct {
	TaskID              string           `json:"task_id"`
	Owner               string           `json:"owner"`
	Capability          string           `json:"capability"`
	OfferingID          string           `json:"offering_id"`
	ProviderID          string           `json:"provider_id"`
	CreatedAt           string           `json:"created_at"`
	FinishedAt          string           `json:"finished_at"`
	MaterializeFailures int              `json:"materialize_failures,omitempty"`
	Failure             failureDetail    `json:"failure"`
	Attempts            []failureAttempt `json:"attempts,omitempty"`
}

type failureDetail struct {
	Class           string `json:"class"`
	Retryable       bool   `json:"retryable"`
	Ambiguous       bool   `json:"ambiguous,omitempty"`
	Detail          string `json:"detail,omitempty"`
	UpstreamStatus  int    `json:"upstream_status,omitempty"`
	UpstreamCode    string `json:"upstream_code,omitempty"`
	UpstreamExcerpt string `json:"upstream_excerpt,omitempty"`
}

type failureAttempt struct {
	Ordinal        int    `json:"ordinal"`
	State          string `json:"state"`
	ProviderID     string `json:"provider_id,omitempty"`
	Class          string `json:"class,omitempty"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
	UpstreamCode   string `json:"upstream_code,omitempty"`
}

// failureSummary counts the summarized tasks, to read the pattern at a glance.
type failureSummary struct {
	ByClass          map[string]int `json:"by_class"`
	ByUpstreamStatus map[string]int `json:"by_upstream_status"`
	ByUpstreamCode   map[string]int `json:"by_upstream_code"`
}

type failureResult struct {
	status    string
	reason    string
	data      []byte
	entries   int
	available int
	truncated bool
}

// collectFailures reads the failed tasks of the time range, newest first, and
// keeps as many as fit the budget.
func collectFailures(ctx context.Context, sources Sources, since, until time.Time, budget int64, scrubber *Scrubber) (failureResult, error) {
	var result failureResult
	if sources.DatabasePath == "" {
		result.status, result.reason = StatusUnavailable, ReasonNotConfigured
		return result, nil
	}
	read, err := sqlite.ReadDiagnosticFailures(ctx, sources.DatabasePath, since, until, failedTaskLimit)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.status, result.reason = StatusUnavailable, ReasonUnreadable
		if errors.Is(err, os.ErrNotExist) {
			result.reason = ReasonNotFound
		}
		return result, nil
	}
	result.available = read.Available
	// Everything the tasks tell about their people and prompts is registered
	// before any text is scrubbed, so a log line read later is covered too.
	for _, task := range read.Tasks {
		scrubber.AddIdentity(task.OwnerID)
		for _, echo := range task.Echoes {
			scrubber.AddPrivate(echo)
		}
	}
	tasks := make([]failureTask, 0, len(read.Tasks))
	for _, task := range read.Tasks {
		tasks = append(tasks, scrubFailureTask(scrubber, task))
	}
	// Newest first: when the budget is short the oldest tasks go.
	for {
		document := failuresDocument{Tasks: tasks, Summary: summarizeFailures(tasks)}
		data, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return result, err
		}
		if int64(len(data)) <= budget || len(tasks) == 0 {
			result.data, result.entries = append(data, '\n'), len(tasks)
			break
		}
		tasks = tasks[:len(tasks)-1]
		result.truncated = true
	}
	if result.available > result.entries {
		result.truncated = true
	}
	result.status = StatusIncluded
	if result.entries == 0 {
		result.status = StatusEmpty
	}
	return result, nil
}

func scrubFailureTask(scrubber *Scrubber, task sqlite.DiagnosticFailedTask) failureTask {
	excerpt := generation.UpstreamExcerpt(task.Failure.UpstreamExcerpt, excerptRunes, task.Echoes...)
	out := failureTask{
		TaskID:              scrubber.Text(task.ID),
		Owner:               scrubber.Pseudonym(task.OwnerID),
		Capability:          scrubber.Text(task.Capability),
		OfferingID:          scrubber.Text(task.OfferingID),
		ProviderID:          scrubber.Text(task.ProviderID),
		CreatedAt:           formatTimestamp(task.CreatedAt),
		FinishedAt:          formatTimestamp(task.FinishedAt),
		MaterializeFailures: task.MaterializeFailures,
		Failure: failureDetail{
			Class:           scrubber.Text(string(task.Failure.Class)),
			Retryable:       task.Failure.Retryable,
			Ambiguous:       task.Failure.Ambiguous,
			Detail:          scrubber.Text(task.Failure.SafeDetail),
			UpstreamStatus:  task.Failure.UpstreamStatus,
			UpstreamCode:    scrubber.Text(task.Failure.UpstreamCode),
			UpstreamExcerpt: scrubber.Text(excerpt),
		},
	}
	for _, attempt := range task.Attempts {
		out.Attempts = append(out.Attempts, failureAttempt{
			Ordinal:        attempt.Ordinal,
			State:          scrubber.Text(attempt.State),
			ProviderID:     scrubber.Text(attempt.ProviderID),
			Class:          scrubber.Text(attempt.FailureClass),
			UpstreamStatus: attempt.UpstreamStatus,
			UpstreamCode:   scrubber.Text(attempt.UpstreamCode),
		})
	}
	return out
}

func summarizeFailures(tasks []failureTask) failureSummary {
	summary := failureSummary{ByClass: map[string]int{}, ByUpstreamStatus: map[string]int{}, ByUpstreamCode: map[string]int{}}
	for _, task := range tasks {
		summary.ByClass[task.Failure.Class]++
		if task.Failure.UpstreamStatus != 0 {
			summary.ByUpstreamStatus[strconv.Itoa(task.Failure.UpstreamStatus)]++
		}
		if task.Failure.UpstreamCode != "" {
			summary.ByUpstreamCode[task.Failure.UpstreamCode]++
		}
	}
	return summary
}
