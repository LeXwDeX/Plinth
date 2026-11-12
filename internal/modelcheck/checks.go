// Package modelcheck evaluates the acceptance thresholds of a production
// plan checkpoint (#251) against a saved model's inspection report. The
// thresholds come from the purpose budget and the frozen production brief and
// are frozen in the approved plan; the Agent only names the checks. The same
// report fields and pass/fail/unknown rules are used by the read-only
// inspect_model usage check (#103), so a checkpoint and a manual inspection
// cannot disagree about the same file.
package modelcheck

import (
	"math"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// Check ids a plan or a checkpoint may name. They match
// application.chat.model_review vocabulary constants.
const (
	CheckTriangles         = "triangles"
	CheckUV                = "uv"
	CheckWatertight        = "watertight"
	CheckSkin              = "skin"
	CheckTextureSize       = "texture_size"
	CheckPivotBottomCenter = "pivot_bottom_center"
	CheckYUp               = "y_up"
	CheckPlausibleScale    = "plausible_scale"
	CheckDebris            = "debris"
	CheckDegenerate        = "degenerate"
)

// Statuses of one evaluated check.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusUnknown = "unknown"
)

// Known reports whether id names a check the server can evaluate. Checkpoint
// and acceptance declarations are refused for any other id.
func Known(id string) bool {
	switch id {
	case CheckTriangles, CheckUV, CheckWatertight, CheckSkin, CheckTextureSize,
		CheckPivotBottomCenter, CheckYUp, CheckPlausibleScale, CheckDebris, CheckDegenerate:
		return true
	}
	return false
}

// KnownIDs returns every check id in a stable order.
func KnownIDs() []string {
	return []string{
		CheckTriangles, CheckUV, CheckWatertight, CheckSkin, CheckTextureSize,
		CheckPivotBottomCenter, CheckYUp, CheckPlausibleScale, CheckDebris, CheckDegenerate,
	}
}

// Result is the evaluation of one check. Actual carries the measured value;
// Detail is a stable, non-secret reason for an unknown result.
type Result struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Actual any    `json:"actual"`
	Detail string `json:"detail,omitempty"`
}

// Failed reports whether any result failed. Unknown results never block a
// plan: the checkpoint records them and the next step may run.
func Failed(results []Result) bool {
	for _, result := range results {
		if result.Status == StatusFail {
			return true
		}
	}
	return false
}

// Evaluate judges report against the frozen checks. A check without a usable
// bound, or a report that could not read the model, is unknown rather than a
// pass, so infrastructure trouble is visible and never blocks the plan.
func Evaluate(report modelinspect.Report, checks []budget.PlanCheck) []Result {
	results := make([]Result, 0, len(checks))
	for _, check := range checks {
		results = append(results, evaluate(report, check))
	}
	return results
}

func evaluate(report modelinspect.Report, check budget.PlanCheck) Result {
	readError := warned(report, "read_error")
	switch check.Check {
	case CheckTriangles:
		values := []int64{report.Triangles}
		value := float64(report.Triangles)
		if readError || (check.Minimum == nil && check.Maximum == nil) {
			return Result{Check: check.Check, Status: StatusUnknown, Actual: values[0], Detail: unknownDetail(readError)}
		}
		if check.Minimum != nil && value < *check.Minimum || check.Maximum != nil && value > *check.Maximum {
			return Result{Check: check.Check, Status: StatusFail, Actual: values[0]}
		}
		return Result{Check: check.Check, Status: StatusPass, Actual: values[0]}
	case CheckUV:
		return boolean(report, check, report.UV.AllHaveUV, check.Required)
	case CheckWatertight:
		if readError || report.Triangles == 0 || warned(report, "invalid_indices", "invalid_positions") {
			return Result{Check: check.Check, Status: StatusUnknown, Actual: report.Topology.Watertight, Detail: unknownDetail(readError)}
		}
		return boolean(report, check, report.Topology.Watertight, check.Required)
	case CheckSkin:
		return boolean(report, check, len(report.Skins) > 0, check.Required)
	case CheckTextureSize:
		actual := maxTextureEdge(report)
		if check.Maximum == nil {
			return Result{Check: check.Check, Status: StatusUnknown, Actual: actual}
		}
		status := StatusPass
		if float64(actual) > *check.Maximum {
			status = StatusFail
		}
		return Result{Check: check.Check, Status: status, Actual: actual}
	case CheckPivotBottomCenter:
		if check.MaximumRatio == nil || readError || bboxDiagonal(report) <= 0 {
			return Result{Check: check.Check, Status: StatusUnknown, Actual: 0.0}
		}
		offset := report.BBox.BottomCenterOffset
		actual := math.Sqrt(offset[0]*offset[0]+offset[1]*offset[1]+offset[2]*offset[2]) / bboxDiagonal(report)
		status := StatusPass
		if actual > *check.MaximumRatio {
			status = StatusFail
		}
		return Result{Check: check.Check, Status: status, Actual: actual}
	case CheckYUp:
		if check.MaximumRatio == nil || readError {
			return Result{Check: check.Check, Status: StatusUnknown, Actual: ""}
		}
		status, actual := YUp(report.BBox.Size, *check.MaximumRatio, check.Required)
		return Result{Check: check.Check, Status: status, Actual: actual}
	case CheckPlausibleScale:
		actual := longestEdge(report)
		if check.Minimum == nil && check.Maximum == nil || readError || actual <= 0 {
			return Result{Check: check.Check, Status: StatusUnknown, Actual: actual}
		}
		status := StatusPass
		if check.Minimum != nil && actual < *check.Minimum || check.Maximum != nil && actual > *check.Maximum {
			status = StatusFail
		}
		return Result{Check: check.Check, Status: status, Actual: actual}
	case CheckDebris:
		return ratio(report, check, report.Topology.DebrisTriangles)
	case CheckDegenerate:
		return ratio(report, check, report.Topology.DegenerateTriangles)
	}
	return Result{Check: check.Check, Status: StatusUnknown, Detail: "unknown_check"}
}

// YUp judges the up axis from a bounding box (#280). The box alone cannot
// tell a lying humanoid from a standing quadruped, a car or a table: all of
// them are wider than tall. So the rule depends on what the purpose says about
// the subject:
//
//   - Y is the longest edge: pass.
//   - The shortest edge is at most flatRatio of the longest: the box is flat
//     and has no readable up axis, unknown ("flat").
//   - upright (the purpose's subject stands, such as a humanoid character) and
//     Y is strictly the shortest edge: the subject lies on its back or front,
//     fail.
//   - Otherwise unknown: a wide subject is a normal shape, not an error.
//
// actual names the longest axis ("x", "y" or "z"), "flat", or "" when the box
// is empty.
func YUp(size [3]float64, flatRatio float64, upright bool) (string, string) {
	longest := max(size[0], max(size[1], size[2]))
	if longest <= 0 {
		return StatusUnknown, ""
	}
	if min(size[0], min(size[1], size[2])) <= flatRatio*longest {
		return StatusUnknown, "flat"
	}
	actual := "z"
	switch {
	case size[1] >= size[0] && size[1] >= size[2]:
		return StatusPass, "y"
	case size[0] >= size[2]:
		actual = "x"
	}
	if upright && size[1] < size[0] && size[1] < size[2] {
		return StatusFail, actual
	}
	return StatusUnknown, actual
}

func boolean(report modelinspect.Report, check budget.PlanCheck, actual, required bool) Result {
	if !required {
		return Result{Check: check.Check, Status: StatusUnknown, Actual: actual}
	}
	status := StatusPass
	if !actual {
		status = StatusFail
	}
	return Result{Check: check.Check, Status: status, Actual: actual}
}

func ratio(report modelinspect.Report, check budget.PlanCheck, affected int64) Result {
	actual := 0.0
	if check.MaximumRatio == nil || warned(report, "read_error") || report.Triangles <= 0 {
		return Result{Check: check.Check, Status: StatusUnknown, Actual: actual}
	}
	actual = float64(affected) / float64(report.Triangles)
	status := StatusPass
	if actual > *check.MaximumRatio {
		status = StatusFail
	}
	return Result{Check: check.Check, Status: status, Actual: actual}
}

func maxTextureEdge(report modelinspect.Report) int {
	value := 0
	for _, material := range report.Materials {
		for _, slot := range material.Slots {
			if !slot.Present {
				continue
			}
			value = max(value, max(slot.Width, slot.Height))
		}
	}
	return value
}

func bboxDiagonal(report modelinspect.Report) float64 {
	size := report.BBox.Size
	return math.Sqrt(size[0]*size[0] + size[1]*size[1] + size[2]*size[2])
}

func longestEdge(report modelinspect.Report) float64 {
	size := report.BBox.Size
	return max(size[0], max(size[1], size[2]))
}

func shortestEdge(report modelinspect.Report) float64 {
	size := report.BBox.Size
	return min(size[0], min(size[1], size[2]))
}

func warned(report modelinspect.Report, names ...string) bool {
	for _, warning := range report.Warnings {
		for _, name := range names {
			if warning == name {
				return true
			}
		}
	}
	return false
}

func unknownDetail(readError bool) string {
	if readError {
		return "read_error"
	}
	return "not_declared"
}
