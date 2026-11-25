package imageinspect

import (
	"regexp"
	"testing"
)

// #632: a check's actual and expected values reach the Agent verbatim
// (inspect_image, the 3D input gate, the three-view reasons). They are
// measurements written with the report's own field names, numbers and
// operators, never an English phrase the Agent could copy into a Chinese reply.
func TestCheckValuesAreMeasurementsNotEnglishPhrases(t *testing.T) {
	machineToken := regexp.MustCompile(`[A-Za-z0-9_@#./{}=<>%|-]*[_@#{}0-9=<>%][A-Za-z0-9_@#./{}=<>%|-]*`)
	phrase := regexp.MustCompile(`[A-Za-z][A-Za-z'’]*(?:[ ,;:()/-]+[A-Za-z][A-Za-z'’]*){2,}`)

	cutout := Report{Width: 512, Height: 512, ShortEdge: 512, SubjectRatio: 0.3, HasAlpha: true,
		TransparentRatio: 0.4, BackgroundSpread: 40, TouchesEdge: true, ComponentCount: 3, LargestComponentRatio: 0.5,
		EdgeFringe: &FringeReport{Measured: true, BandPixels: 2, EdgePixels: 900, FringeRatio: 0.3},
		Tiling: &TilingReport{
			LeftRight: []SeamSegment{{From: 0, To: 63, Delta: 40, Inside: 2}, {From: 64, To: 127, Delta: 1, Inside: 2}},
			TopBottom: []SeamSegment{{From: 0, To: 63, Delta: 40, Inside: 2}},
		}}
	opaque := Report{Width: 64, Height: 64, ShortEdge: 64, SubjectRatio: 0.005, BackgroundSpread: 10}
	smooth := cutout
	smooth.Tiling = &TilingReport{LeftRight: []SeamSegment{{From: 0, To: 63, Delta: 1, Inside: 2}}}
	unmeasured := cutout
	unmeasured.EdgeFringe, unmeasured.Tiling = nil, nil
	tiny := cutout
	tiny.Tiling = &TilingReport{}

	checklist := Checklist{RequirePlainBackground: true, MaxBackgroundSpread: 32, MinSubjectRatio: 0.05,
		MaxSubjectRatio: 0.95, RequireEdgeClear: true, MaxComponents: 1, MinMainComponentRatio: 0.6}
	tiling := TilingLimits{Segments: 2, MaxSeamRatio: 2.5, MinSeamDelta: 6}
	alpha := AlphaLimits{MinTransparentRatio: 0.005, MaxBackgroundSpread: 32}
	fringe := FringeLimits{MaxRatio: 0.12}
	var checks []Check
	for _, report := range []Report{cutout, opaque, smooth, unmeasured, tiny} {
		checks = append(checks, report.Review(checklist)...)
		checks = append(checks, report.Review(Checklist{RequireAlpha: true})...)
		for _, transparency := range []TransparencyMode{TransparencyAllowed, TransparencyRequired} {
			checks = append(checks, report.ReviewRequirements(Requirements{MinShortEdge: 1024, Tileable: true,
				Tiling: tiling, Transparency: transparency, Alpha: alpha, Fringe: fringe})...)
		}
		checks = append(checks, Compare(cutout, report, Consistency{MaxSubjectRatioDelta: 0.05,
			MaxBackgroundSpread: 32, MaxBBoxHeightDelta: 0.05})...)
		checks = append(checks, EdgeCheck(report), MainComponentCheck(report, 0.6))
	}
	reference := Appearance{HasSubject: true, Ratio: 0.3, Parts: 2,
		Palette: []PaletteColour{{RGB: [3]float64{200, 30, 30}, Share: 0.7}}}
	candidate := Appearance{HasSubject: true, Ratio: 0.3, Parts: 4,
		Palette: []PaletteColour{{RGB: [3]float64{30, 30, 200}, Share: 0.7}}}
	checks = append(checks, CompareAppearance(reference, candidate,
		AppearanceLimits{MaxPaletteDistance: 0.1, MaxPartCountDelta: 1})...)

	if len(checks) < 40 {
		t.Fatalf("only %d checks were produced", len(checks))
	}
	for _, check := range checks {
		for field, value := range map[string]string{"actual": check.Actual, "expected": check.Expected} {
			if match := phrase.FindString(machineToken.ReplaceAllString(value, "|")); match != "" {
				t.Errorf("%s %s %s = %q carries the English phrase %q", check.Name, check.Status, field, value, match)
			}
		}
	}
}
