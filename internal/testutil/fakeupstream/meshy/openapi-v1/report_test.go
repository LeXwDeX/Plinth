package meshyopenapiv1

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// The committed alignment report must show the version aligned with the real
// API: no unexplained difference, and real requests behind it.
func TestAlignmentReportIsCommittedAndClean(t *testing.T) {
	testkit.AssertCommittedAlignmentReport(t, "testdata/alignment-report.json", OfficialBase)
}
