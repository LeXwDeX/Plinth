package imageinspect

import (
	"testing"
)

func report(subjectRatio, spread float64, bbox [4]int, height int) Report {
	return Report{Width: 64, Height: height, SubjectRatio: subjectRatio, BackgroundSpread: spread, SubjectBBox: bbox}
}

func statusOf(checks []Check, name string) string {
	for _, check := range checks {
		if check.Name == name {
			return check.Status
		}
	}
	return ""
}

func TestCompareAcceptsTheSameView(t *testing.T) {
	reference := report(0.30, 2, [4]int{20, 8, 43, 55}, 64)
	candidate := reference
	checks := Compare(reference, candidate, Consistency{MaxSubjectRatioDelta: 0.05, MaxBackgroundSpread: 20, MaxBBoxHeightDelta: 0.05})
	if Failed(checks) {
		t.Fatalf("identical views failed: %+v", checks)
	}
	for _, name := range []string{"nonblank", "background", "subject_size_delta", "silhouette_height_delta"} {
		if statusOf(checks, name) != StatusPass {
			t.Fatalf("%s did not pass: %+v", name, checks)
		}
	}
}

func TestCompareRejectsEachInconsistentProperty(t *testing.T) {
	reference := report(0.30, 2, [4]int{20, 8, 43, 55}, 64)
	limits := Consistency{MaxSubjectRatioDelta: 0.05, MaxBackgroundSpread: 20, MaxBBoxHeightDelta: 0.05}
	for name, candidate := range map[string]Report{
		"subject size":    report(0.60, 2, [4]int{20, 8, 43, 55}, 64),
		"background":      report(0.30, 40, [4]int{20, 8, 43, 55}, 64),
		"silhouette size": report(0.30, 2, [4]int{20, 16, 43, 47}, 64),
		"blank":           report(0, 0, [4]int{}, 64),
	} {
		checks := Compare(reference, candidate, limits)
		if !Failed(checks) {
			t.Fatalf("%s: inconsistent view passed: %+v", name, checks)
		}
	}
}

func TestCompareZeroLimitsDisableOnlyTheirCheck(t *testing.T) {
	reference := report(0.30, 2, [4]int{20, 8, 43, 55}, 64)
	checks := Compare(reference, report(0.90, 40, [4]int{0, 0, 63, 63}, 64), Consistency{})
	if len(checks) != 1 || checks[0].Name != "nonblank" || checks[0].Status != StatusPass {
		t.Fatalf("zero limits should keep only nonblank: %+v", checks)
	}
}
