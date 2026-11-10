package imageinspect

import "fmt"

// Consistency bounds how far one picture may differ from the reference view of
// the same subject (#243). Zero fields disable the corresponding comparison.
// It is deterministic, like Review: it compares decoded pixels, never meaning.
type Consistency struct {
	// MaxSubjectRatioDelta bounds the absolute difference of the subject's
	// share of the picture: a view cropped closer or further than the reference
	// fails.
	MaxSubjectRatioDelta float64 `json:"max_subject_ratio_delta,omitempty"`
	// MaxBackgroundSpread is the plainness a candidate background must show,
	// the same measure as Checklist.MaxBackgroundSpread. A transparent
	// background passes. Zero disables the check.
	MaxBackgroundSpread float64 `json:"max_background_spread,omitempty"`
	// MaxBBoxHeightDelta bounds the absolute difference of the subject box's
	// height as a share of the picture. It keeps the silhouette at the same
	// scale in every view even when the subject's width changes (a left view
	// is narrower than the front view).
	MaxBBoxHeightDelta float64 `json:"max_bbox_height_delta,omitempty"`
}

// Compare judges a candidate view against the reference view. The checks are
// independent: the caller can report every reason one view has to be redone.
// Names are stable wire values; Fix names the stable repair code.
func Compare(reference, candidate Report, limits Consistency) []Check {
	checks := make([]Check, 0, 4)
	checks = append(checks, Check{Name: "nonblank", Status: status(candidate.SubjectRatio > 0.01),
		Actual:   fmt.Sprintf("subject %.1f%%", candidate.SubjectRatio*100),
		Expected: "a visible subject", Fix: "regenerate"})
	if limits.MaxBackgroundSpread > 0 {
		ok := candidate.HasAlpha || candidate.BackgroundSpread <= limits.MaxBackgroundSpread
		expected := fmt.Sprintf("transparency or border spread <= %.0f", limits.MaxBackgroundSpread)
		checks = append(checks, Check{Name: "background", Status: status(ok),
			Actual:   fmt.Sprintf("alpha=%t, border spread %.1f", candidate.HasAlpha, candidate.BackgroundSpread),
			Expected: expected, Fix: "regenerate"})
	}
	if limits.MaxSubjectRatioDelta > 0 {
		delta := absolute(candidate.SubjectRatio - reference.SubjectRatio)
		checks = append(checks, Check{Name: "subject_size_delta", Status: status(delta <= limits.MaxSubjectRatioDelta),
			Actual:   fmt.Sprintf("%.1f percentage points (reference %.1f%%)", delta*100, reference.SubjectRatio*100),
			Expected: fmt.Sprintf("within %.1f percentage points", limits.MaxSubjectRatioDelta*100), Fix: "regenerate"})
	}
	if limits.MaxBBoxHeightDelta > 0 {
		delta := absolute(silhouetteHeight(candidate) - silhouetteHeight(reference))
		checks = append(checks, Check{Name: "silhouette_height_delta", Status: status(delta <= limits.MaxBBoxHeightDelta),
			Actual:   fmt.Sprintf("%.1f percentage points", delta*100),
			Expected: fmt.Sprintf("within %.1f percentage points", limits.MaxBBoxHeightDelta*100), Fix: "regenerate"})
	}
	return checks
}

// silhouetteHeight is the subject box's height as a share of the picture; 0
// when the picture has no subject.
func silhouetteHeight(report Report) float64 {
	if report.Height <= 0 || report.SubjectRatio <= 0 || report.SubjectBBox == [4]int{} {
		return 0
	}
	return float64(report.SubjectBBox[3]-report.SubjectBBox[1]+1) / float64(report.Height)
}

func absolute(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
