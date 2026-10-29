package meshyopenapiv1

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The committed alignment report must show the version aligned with the real
// API: no unexplained difference, and real requests behind it.
func TestAlignmentReportIsCommittedAndClean(t *testing.T) {
	raw, err := os.ReadFile("testdata/alignment-report.json")
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
	if report.Official != OfficialBase || report.CheckedAt == "" || report.Requests == 0 || report.Paid == 0 {
		t.Fatalf("report header incomplete: %+v", report)
	}
	for _, diff := range report.Diffs {
		if strings.TrimSpace(diff.Note) == "" {
			t.Fatalf("report holds an unexplained difference")
		}
	}
}
