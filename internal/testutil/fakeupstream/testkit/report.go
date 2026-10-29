package testkit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// AssertCommittedAlignmentReport checks the committed alignment report of one fake version: it must name the
// version's official base, carry a check date and real (paid) official requests, and hold no unexplained
// difference. officialBase is the version's OfficialBase constant; path is its alignment-report.json.
func AssertCommittedAlignmentReport(t *testing.T, path, officialBase string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Official  string `json:"official_base"`
		CheckedAt string `json:"checked_at"`
		Requests  int    `json:"official_requests"`
		Paid      int    `json:"official_paid_requests"`
		Diffs     []struct {
			Note string `json:"note"`
		} `json:"diffs"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Official != officialBase || report.CheckedAt == "" || report.Requests == 0 || report.Paid == 0 {
		t.Fatalf("report header incomplete: %+v", report)
	}
	for _, diff := range report.Diffs {
		if strings.TrimSpace(diff.Note) == "" {
			t.Fatalf("report holds an unexplained difference")
		}
	}
}
