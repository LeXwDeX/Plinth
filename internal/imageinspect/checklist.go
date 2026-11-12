package imageinspect

import "fmt"

// Checklist is a Schema-declared set of picture requirements. Zero values
// disable the corresponding check.
type Checklist struct {
	// RequireAlpha demands any transparency (the picture's background was
	// removed).
	RequireAlpha bool `json:"require_alpha,omitempty"`
	// RequirePlainBackground accepts either transparency or a border whose
	// colour spread is at most MaxBackgroundSpread.
	RequirePlainBackground bool    `json:"require_plain_background,omitempty"`
	MaxBackgroundSpread    float64 `json:"max_background_spread,omitempty"`
	MinSubjectRatio        float64 `json:"min_subject_ratio,omitempty"`
	MaxSubjectRatio        float64 `json:"max_subject_ratio,omitempty"`
	// RequireEdgeClear demands the subject not touch any border (not cropped).
	RequireEdgeClear bool `json:"require_edge_clear,omitempty"`
	// MaxComponents bounds the number of subject components (1 means a single
	// subject); zero disables the check.
	MaxComponents int `json:"max_components,omitempty"`
}

// Check is one deterministic verdict. Status is "pass", "fail" or "unknown"
// (the picture cannot show the property, for example the edge of a picture with
// no alpha channel); unknown is a fact, never a pass. Fix is a stable code the
// caller localizes (remove_background, crop_subject, single_subject,
// regenerate, raise_resolution, regenerate_tileable, clean_edges). Hint is the
// caller's actionable sentence for a failed check, from the Product Schema; this
// package never writes it.
type Check struct {
	Name     string `json:"check"`
	Status   string `json:"status"`
	Actual   string `json:"actual,omitempty"`
	Expected string `json:"expected,omitempty"`
	Fix      string `json:"fix,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusUnknown = "unknown"
)

// Review judges the report against a checklist.
func (r Report) Review(checklist Checklist) []Check {
	checks := make([]Check, 0, 5)
	nonblank := r.SubjectRatio > 0.01
	checks = append(checks, Check{Name: "nonblank", Status: status(nonblank),
		Actual: fmt.Sprintf("subject %.1f%%", r.SubjectRatio*100), Expected: "a visible subject", Fix: "regenerate"})
	if checklist.RequireAlpha || checklist.RequirePlainBackground {
		ok := r.HasAlpha
		expected := "a transparent background"
		if !checklist.RequireAlpha {
			ok = r.HasAlpha || r.BackgroundSpread <= checklist.MaxBackgroundSpread
			expected = fmt.Sprintf("transparency or border spread <= %.0f", checklist.MaxBackgroundSpread)
		}
		checks = append(checks, Check{Name: "background", Status: status(ok),
			Actual:   fmt.Sprintf("alpha=%t, border spread %.1f", r.HasAlpha, r.BackgroundSpread),
			Expected: expected, Fix: "remove_background"})
	}
	if checklist.MinSubjectRatio > 0 || checklist.MaxSubjectRatio > 0 {
		ok := (checklist.MinSubjectRatio == 0 || r.SubjectRatio >= checklist.MinSubjectRatio) &&
			(checklist.MaxSubjectRatio == 0 || r.SubjectRatio <= checklist.MaxSubjectRatio)
		checks = append(checks, Check{Name: "subject_size", Status: status(ok),
			Actual:   fmt.Sprintf("%.1f%%", r.SubjectRatio*100),
			Expected: fmt.Sprintf("%.0f%%..%.0f%%", checklist.MinSubjectRatio*100, checklist.MaxSubjectRatio*100),
			Fix:      "crop_subject"})
	}
	if checklist.RequireEdgeClear {
		checks = append(checks, Check{Name: "edge", Status: status(!r.TouchesEdge),
			Actual:   fmt.Sprintf("touches edge=%t", r.TouchesEdge),
			Expected: "the subject stays inside the frame", Fix: "crop_subject"})
	}
	if checklist.MaxComponents > 0 {
		checks = append(checks, Check{Name: "components", Status: status(r.ComponentCount <= checklist.MaxComponents),
			Actual:   fmt.Sprintf("%d component(s)", r.ComponentCount),
			Expected: fmt.Sprintf("<= %d", checklist.MaxComponents), Fix: "single_subject"})
	}
	return checks
}

func status(ok bool) string {
	if ok {
		return StatusPass
	}
	return StatusFail
}

// Failed reports whether any check failed.
func Failed(checks []Check) bool {
	for _, check := range checks {
		if check.Status == StatusFail {
			return true
		}
	}
	return false
}
