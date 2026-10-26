package sqlite

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #327 (from #324 note 99119): the usage statistics counted the estimate of a Meshy Retexture
// the upstream rejected with 404 (15, never charged), while the session header left it out. Both
// now use one rule: queued, running and succeeded Tasks count, and so do failed or canceled ones
// whose upstream finished or whose submission is ambiguous; a Task that failed or was canceled
// before its upstream finished adds nothing. It is still counted as a task (and as a failure).
func TestAdministrationUsageEstimatesOnlyTasksThatWereOrMayBeCharged(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	now := time.Date(2026, 10, 22, 6, 0, 0, 0, time.UTC)
	insert := func(id, owner, state, failure, phase string, credits int, quote string) {
		t.Helper()
		var failureJSON, quoteJSON any
		if failure != "" {
			failureJSON = failure
		}
		required := 0
		if quote != "" {
			quoteJSON, required = quote, 1
		}
		offering := `{"id":"o","provider_id":"p","model_id":"m","capability":"retexture","estimated_credits":` + strconv.Itoa(credits) + `}`
		if _, err := r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, idempotency_key, state, progress, failure_json,
  current_attempt_id, materialize_failures, version, created_at, updated_at, native_quote_json, native_quote_required)
 VALUES (?, ?, 'retexture', ?, '{}', ?, ?, ?, 0, ?, ?, 0, 1, '2026-10-22T05:00:00Z', '2026-10-22T05:00:00Z', ?, ?)`,
			id, owner, offering, "hash-"+id, "key-"+id, state, failureJSON, "attempt-"+id, quoteJSON, required); err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Exec(`INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key,
  started_at, updated_at, last_observation_phase) VALUES (?, ?, 1, ?, 'FAILED', ?, '2026-10-22T05:00:00Z', '2026-10-22T05:00:00Z', ?)`,
			"attempt-"+id, id, offering, "submission-"+id, phase); err != nil {
			t.Fatal(err)
		}
	}
	tripo := func(amount string) string {
		return `{"version":1,"unit":"tripo.credit","unit_label":"Tripo credits","base_amount":"` + amount +
			`","extra_amount":"0","total_amount":"` + amount + `"}`
	}
	// The same Tasks as the session total test (#324): 80 + 1 + 10 + 1000 count.
	insert("done", "owner", "SUCCEEDED", "", "COMPLETED", 80, "")
	insert("running", "owner", "RUNNING", "", "RUNNING", 1, "")
	insert("rejected", "owner", "FAILED", `{"class":"rejected","retryable":false,"detail":"x"}`, "", 15, "")
	insert("upstream-failed", "owner", "FAILED", `{"class":"provider","retryable":false,"detail":"x"}`, "FAILED", 100, "")
	insert("canceled", "owner", "CANCELED", "", "", 200, "")
	insert("stored-failed", "owner", "FAILED", `{"class":"integrity","retryable":false,"detail":"x"}`, "COMPLETED", 10, "")
	insert("ambiguous", "owner", "FAILED", `{"class":"provider","retryable":false,"ambiguous":true,"detail":"x"}`, "", 1000, "")
	// A Provider-native quote follows the same rule: the rejected 90 is left out.
	insert("tripo-done", "native", "SUCCEEDED", "", "COMPLETED", 0, tripo("105"))
	insert("tripo-rejected", "native", "FAILED", `{"class":"rejected","retryable":false,"detail":"x"}`, "", 0, tripo("90"))

	service, err := administration.NewUsageService(administration.UsageServiceConfig{
		Repository: r, Now: func() time.Time { return now }, DefaultDays: 7,
		DatePattern: `^\d{4}-\d{2}-\d{2}$`, DateLayout: "2006-01-02", Timezone: "UTC",
		SucceededStates: []string{"SUCCEEDED"}, FailedStates: []string{"FAILED", "CANCELED"},
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := service.Summarize(ctx, administration.UsageRequest{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	byUser := map[string]administration.UsageSummary{}
	for _, summary := range summaries {
		byUser[summary.UserID] = summary
	}
	legacy := byUser["owner"]
	if legacy.EstimatedCredits != 1091 || legacy.TotalTasks != 7 || legacy.SucceededTasks != 1 || legacy.FailedTasks != 5 {
		t.Fatalf("owner usage = %+v; want 1091 credits over 7 tasks (1 succeeded, 5 failed or canceled)", legacy)
	}
	if legacy.ByType["retexture"] != 1091 {
		t.Fatalf("owner by type = %v; want retexture 1091", legacy.ByType)
	}
	native := byUser["native"]
	if native.TotalTasks != 2 || native.FailedTasks != 1 || len(native.PriceTotals) != 1 ||
		native.PriceTotals[0] != (schema.NativeAmount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "105"}) {
		t.Fatalf("native usage = %+v; want 105 Tripo credits over 2 tasks", native)
	}
}
