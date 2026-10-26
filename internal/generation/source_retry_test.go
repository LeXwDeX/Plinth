package generation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #326: running a step again (a failed plan step's retry, a paused step's
// continue, a failed chat step's regenerate) is judged by one rule: bind the
// source as today's rules do and compare with what the earlier attempt froze.
func TestJudgeSourceRetry(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	withFile := modelSourceOffering(expiringTaskParameter(),
		Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset})
	taskOnly := modelSourceOffering(expiringTaskParameter())
	tooSmall := modelSourceOffering(expiringTaskParameter(),
		Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, MaxBytes: 1})
	namedTask := json.RawMessage(`{"source_task":"task-1"}`)
	namedFile := json.RawMessage(`{"source_asset":"a1"}`)
	fresh, expired := agedSource(time.Hour, now), agedSource(9*24*time.Hour, now)
	gone := agedSource(time.Hour, now)
	gone.UpstreamJobGone = true
	for name, tc := range map[string]struct {
		offering Offering
		source   Task
		frozen   json.RawMessage
		want     SourceRetry
	}{
		"fresh job, as before":                 {withFile, fresh, namedTask, SourceRetryAsBefore},
		"expired job, saved file now":          {withFile, expired, namedTask, SourceRetrySavedFile},
		"gone job, saved file now":             {withFile, gone, namedTask, SourceRetrySavedFile},
		"already the saved file":               {withFile, expired, namedFile, SourceRetryAsBefore},
		"nothing frozen yet":                   {withFile, expired, nil, SourceRetryAsBefore},
		"expired job, no file route":           {taskOnly, expired, namedTask, SourceRetryExpired},
		"expired job, the file does not fit":   {tooSmall, expired, namedTask, SourceRetryExpired},
		"another provider's result never fits": {taskOnly, func() Task { task := fresh; task.Offering.ProviderID = "other"; return task }(), namedTask, SourceRetryIncompatible},
	} {
		t.Run(name, func(t *testing.T) {
			got := JudgeSourceRetryAt(tc.offering, tc.source, "", tc.frozen, now)
			if got != tc.want {
				t.Fatalf("judgment = %q; want %q", got, tc.want)
			}
			if runnable := got.Runnable(); runnable != (tc.want == SourceRetryAsBefore || tc.want == SourceRetrySavedFile) {
				t.Fatalf("%q runnable = %v", got, runnable)
			}
		})
	}
}
