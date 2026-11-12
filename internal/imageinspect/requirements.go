package imageinspect

import (
	"fmt"
	"image"
	"strings"
)

// Requirements are the delivery requirements of one 2D purpose that a picture's
// pixels can prove or refute: a minimum resolution (#282), seamless tiling
// (#287) and a transparent background with clean edges (#284). The values come
// from the Product Schema (image2d_usage requirements and image_review.usage
// thresholds); this package holds only the generic measurement and judgement.
// A zero Requirements asks for nothing.
type Requirements struct {
	// MinShortEdge is the smallest accepted min(width, height) in pixels; zero
	// disables the check.
	MinShortEdge int
	// Tileable demands that opposite edges continue into each other.
	Tileable bool
	Tiling   TilingLimits
	// Transparency says what the purpose asks of the background.
	Transparency TransparencyMode
	Alpha        AlphaLimits
	Fringe       FringeLimits
}

// TransparencyMode is what a purpose asks of the picture's background.
type TransparencyMode string

const (
	// TransparencyOff does not judge the background's transparency.
	TransparencyOff TransparencyMode = ""
	// TransparencyAllowed accepts a real alpha channel or a plain solid
	// background (a purpose that takes either).
	TransparencyAllowed TransparencyMode = "allowed"
	// TransparencyRequired demands a real alpha channel (the person asked for a
	// transparent background, or the purpose is transparent only).
	TransparencyRequired TransparencyMode = "required"
)

// TilingLimits judge the seam measurement. A segment of a seam is a break when
// its edge difference is at least MinSeamDelta (0..255) and more than
// MaxSeamRatio times the difference between neighbouring pixels inside it.
// Segments is the number of stretches each seam is divided into.
type TilingLimits struct {
	Segments     int
	MaxSeamRatio float64
	MinSeamDelta float64
}

// AlphaLimits judge the alpha channel. MinTransparentRatio is the share of
// fully transparent pixels a channel must show to count as real (an RGBA file
// whose alpha is opaque everywhere, or nearly so, is not a cut-out).
// MaxBackgroundSpread is the border colour spread up to which TransparencyAllowed
// takes an opaque background as plain.
type AlphaLimits struct {
	MinTransparentRatio float64
	MaxBackgroundSpread float64
}

// FringeLimits judge the transparent-edge rim. BandRatio, LightLuma and
// LumaGap parameterize the measurement (see FringeProbe); MaxRatio is the
// largest accepted share of the edge band that is rim.
type FringeLimits struct {
	BandRatio float64
	LightLuma float64
	LumaGap   float64
	MaxRatio  float64
}

// measure fills the report fields that need the decoded pixels. Only the
// measurements a requirement will judge are made.
func (r *Report) measure(picture *image.NRGBA, requirements Requirements) {
	if requirements.Tileable {
		r.Tiling = measureTiling(picture, requirements.Tiling.Segments)
	}
	if requirements.Transparency != TransparencyOff && r.HasAlpha {
		r.EdgeFringe = measureFringe(picture, FringeProbe{
			BandRatio: requirements.Fringe.BandRatio,
			LightLuma: requirements.Fringe.LightLuma,
			LumaGap:   requirements.Fringe.LumaGap,
		})
	}
}

// ReviewRequirements judges the report against one purpose's requirements. The
// report must come from InspectFor with the same requirements, or the pixel
// checks cannot be judged and read as unknown. Names are stable wire values:
// short_edge, seamless, alpha, edge_fringe. Fix names the stable repair code.
func (r Report) ReviewRequirements(requirements Requirements) []Check {
	checks := make([]Check, 0, 4)
	if requirements.MinShortEdge > 0 {
		checks = append(checks, Check{Name: "short_edge",
			Status:   status(r.ShortEdge >= requirements.MinShortEdge),
			Actual:   fmt.Sprintf("%d px (%d x %d)", r.ShortEdge, r.Width, r.Height),
			Expected: fmt.Sprintf("short edge >= %d px", requirements.MinShortEdge),
			Fix:      "raise_resolution"})
	}
	if requirements.Tileable {
		checks = append(checks, r.seamlessCheck(requirements.Tiling))
	}
	if requirements.Transparency != TransparencyOff {
		usesAlpha := r.usesAlpha(requirements.Alpha)
		checks = append(checks, r.alphaCheck(requirements, usesAlpha))
		checks = append(checks, r.fringeCheck(requirements.Fringe, usesAlpha))
	}
	return checks
}

// usesAlpha says the alpha channel is real: enough pixels are fully
// transparent. A picture with stray semi-transparent pixels only, or an RGBA
// file with no transparency, is not a cut-out.
func (r Report) usesAlpha(limits AlphaLimits) bool {
	return r.HasAlpha && r.TransparentRatio >= limits.MinTransparentRatio
}

func (r Report) alphaCheck(requirements Requirements, usesAlpha bool) Check {
	check := Check{Name: "alpha", Fix: "remove_background",
		Actual: fmt.Sprintf("alpha=%t, %.1f%% fully transparent, border spread %.1f",
			r.HasAlpha, r.TransparentRatio*100, r.BackgroundSpread)}
	switch requirements.Transparency {
	case TransparencyRequired:
		check.Expected = fmt.Sprintf("a real alpha channel (>= %.1f%% fully transparent pixels)",
			requirements.Alpha.MinTransparentRatio*100)
		check.Status = status(usesAlpha)
	default:
		check.Expected = fmt.Sprintf("a real alpha channel, or a plain background (border spread <= %.0f)",
			requirements.Alpha.MaxBackgroundSpread)
		check.Status = status(usesAlpha || (!r.HasAlpha && r.BackgroundSpread <= requirements.Alpha.MaxBackgroundSpread))
	}
	return check
}

func (r Report) fringeCheck(limits FringeLimits, usesAlpha bool) Check {
	check := Check{Name: "edge_fringe", Fix: "clean_edges",
		Expected: fmt.Sprintf("light rim on <= %.1f%% of the edge", limits.MaxRatio*100)}
	switch {
	case !usesAlpha:
		check.Status = StatusUnknown
		check.Actual = "no real alpha channel, so there is no transparent edge to measure"
	case r.EdgeFringe == nil || !r.EdgeFringe.Measured:
		check.Status = StatusUnknown
		check.Actual = "the subject has no edge wide enough to measure"
	default:
		check.Status = status(r.EdgeFringe.FringeRatio <= limits.MaxRatio)
		check.Actual = fmt.Sprintf("light rim on %.1f%% of %d edge pixels (band %d px)",
			r.EdgeFringe.FringeRatio*100, r.EdgeFringe.EdgePixels, r.EdgeFringe.BandPixels)
	}
	return check
}

// seamlessCheck judges both seams. A failing check names every broken stretch
// as a pixel range, merged where segments touch, so the Agent can look at that
// part of the picture.
func (r Report) seamlessCheck(limits TilingLimits) Check {
	check := Check{Name: "seamless", Fix: "regenerate_tileable",
		Expected: fmt.Sprintf("edge difference <= %.1fx the difference between neighbouring pixels (or < %.0f)",
			limits.MaxSeamRatio, limits.MinSeamDelta)}
	if r.Tiling == nil {
		check.Status = StatusUnknown
		check.Actual = "seams were not measured"
		return check
	}
	if len(r.Tiling.LeftRight) == 0 && len(r.Tiling.TopBottom) == 0 {
		check.Status = StatusUnknown
		check.Actual = "the picture is too small to compare opposite edges"
		return check
	}
	var broken []string
	if ranges := brokenRanges(r.Tiling.LeftRight, limits); len(ranges) > 0 {
		broken = append(broken, "left/right seam at y "+strings.Join(ranges, ", "))
	}
	if ranges := brokenRanges(r.Tiling.TopBottom, limits); len(ranges) > 0 {
		broken = append(broken, "top/bottom seam at x "+strings.Join(ranges, ", "))
	}
	if len(broken) > 0 {
		check.Status = StatusFail
		check.Actual = strings.Join(broken, "; ") + fmt.Sprintf(" (worst %.1fx)", worstBrokenRatio(r.Tiling, limits))
		return check
	}
	check.Status = StatusPass
	check.Actual = "opposite edges continue into each other"
	return check
}

// seamRatio is how many times larger the edge difference of a segment is than
// the difference between neighbouring pixels inside it. The inside difference
// is floored at 1 (of 255) so a flat stretch does not make a tiny offset look
// infinite.
func seamRatio(segment SeamSegment) float64 {
	return segment.Delta / max(segment.Inside, 1)
}

func segmentBroken(segment SeamSegment, limits TilingLimits) bool {
	return segment.Delta >= limits.MinSeamDelta && seamRatio(segment) > limits.MaxSeamRatio
}

// brokenRanges lists the broken stretches of one seam as "from-to", merging
// neighbouring segments.
func brokenRanges(segments []SeamSegment, limits TilingLimits) []string {
	var ranges []string
	start, end, open := 0, 0, false
	flush := func() {
		if open {
			ranges = append(ranges, fmt.Sprintf("%d-%d", start, end))
			open = false
		}
	}
	for _, segment := range segments {
		if !segmentBroken(segment, limits) {
			flush()
			continue
		}
		if !open {
			start, open = segment.From, true
		}
		end = segment.To
	}
	flush()
	return ranges
}

// worstBrokenRatio is the largest seamRatio among the broken segments.
func worstBrokenRatio(report *TilingReport, limits TilingLimits) float64 {
	worst := 0.0
	for _, seam := range [][]SeamSegment{report.LeftRight, report.TopBottom} {
		for _, segment := range seam {
			if segmentBroken(segment, limits) {
				worst = max(worst, seamRatio(segment))
			}
		}
	}
	return roundMetric(worst)
}
