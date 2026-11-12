package threeview

import (
	"image"
	"image/color"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

func blockPicture(width, height, x0, y0, x1, y1 int, fill color.NRGBA) *image.NRGBA {
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			if x >= x0 && x < x1 && y >= y0 && y < y1 {
				picture.SetNRGBA(x, y, fill)
			}
		}
	}
	return picture
}

// Every check the decision can report has words in the default Schema: the
// Agent reads the reasons of a redraw, and a check named only by its code
// would reach it as a bare identifier (#283).
func TestEveryAutomaticCheckHasWordsInTheDefaultSchema(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	reasons := c.Application().Chat.ThreeView.Consistency.Reasons
	emitted := map[string]bool{}
	note := func(checks []imageinspect.Check) {
		for _, check := range checks {
			emitted[check.Name] = true
		}
	}
	report := imageinspect.Report{Width: 64, Height: 64, SubjectRatio: 0.3, BackgroundSpread: 99, TouchesEdge: true,
		SubjectBBox: [4]int{4, 4, 40, 60}, ComponentCount: 3, LargestComponentRatio: 0.5}
	note(report.Review(imageinspect.Checklist{RequirePlainBackground: true, MaxBackgroundSpread: 1, MinSubjectRatio: 0.01,
		MaxComponents: 1, RequireEdgeClear: true}))
	note([]imageinspect.Check{imageinspect.MainComponentCheck(report, 0.6)})
	other := report
	other.SubjectRatio, other.SubjectBBox = 0.5, [4]int{4, 10, 40, 60}
	note(imageinspect.Compare(report, other, imageinspect.Consistency{
		MaxSubjectRatioDelta: 0.01, MaxBackgroundSpread: 1, MaxBBoxHeightDelta: 0.01}))
	options := imageinspect.AppearanceOptions{SubjectThreshold: 32, PaletteColours: 4, PaletteMinShare: 0.03,
		PaletteMergeDistance: 48, PartMinShare: 0.01, NormalizedSide: 16}
	first := imageinspect.MeasureAppearance(blockPicture(64, 64, 10, 10, 30, 60, color.NRGBA{R: 50, G: 80, B: 160, A: 255}), options)
	second := imageinspect.MeasureAppearance(blockPicture(64, 64, 20, 10, 50, 60, color.NRGBA{R: 170, G: 50, B: 50, A: 255}), options)
	note(imageinspect.CompareAppearance(first, second, imageinspect.AppearanceLimits{MaxPaletteDistance: 0.1, MaxPartCountDelta: 1}))
	note(imageinspect.CompareMirror(first, second, imageinspect.MirrorLimits{MinOverlap: 0.5, MaxOrientationMargin: 0.1}))
	for _, name := range []string{"nonblank", "background", "subject_size", "edge", "components", "main_component",
		"subject_size_delta", "silhouette_height_delta", "palette_distance", "part_count_delta", "mirror_overlap",
		"mirror_orientation"} {
		if !emitted[name] {
			t.Errorf("the check %s was not produced; the list in this test is stale", name)
		}
	}
	for name := range emitted {
		if reasons[name] == "" {
			t.Errorf("the check %s has no words in three_view.consistency.reasons", name)
		}
	}
}

func TestAReasonWithoutWordsFallsBackToTheCheckAndItsMeasurement(t *testing.T) {
	service := &Service{schema: catalog.ChatThreeViewSchema{Consistency: catalog.ChatThreeViewConsistencySchema{
		Reasons: map[string]string{"mirror_orientation": "faces the {partner}'s way ({actual}; {expected})"}}}}
	worded := viewCheck{Check: imageinspect.Check{Name: "mirror_orientation", Actual: "1.00 / 0.41", Expected: "0.10"}, Partner: "Left"}
	if got := service.reasonText(worded); got != "faces the Left's way (1.00 / 0.41; 0.10)" {
		t.Fatalf("worded reason = %q", got)
	}
	bare := viewCheck{Check: imageinspect.Check{Name: "palette_distance", Actual: "0.189"}}
	if got := service.reasonText(bare); got != "palette_distance (0.189)" {
		t.Fatalf("unworded reason = %q", got)
	}
}

// The frame follows the subject's box plus a margin on every side (#283).
func TestFrameShape(t *testing.T) {
	for _, test := range []struct {
		name          string
		width, height int
		margin        float64
		want          float64
	}{
		{"a tall character", 32, 160, 0.08, 57.6 / 185.6},
		{"a square object", 100, 100, 0.08, 1},
		{"a wide animal", 160, 40, 0.1, 192.0 / 72.0},
		{"no margin", 24, 48, 0, 0.5},
	} {
		if got := frameShape(test.width, test.height, test.margin); got < test.want-1e-9 || got > test.want+1e-9 {
			t.Errorf("%s: frame shape = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestTheFrameStaysInsideTheBoundsOfTheSubjectType(t *testing.T) {
	service := &Service{schema: catalog.ChatThreeViewSchema{
		DefaultSubjectType: "generic",
		SubjectTypes: []catalog.ChatThreeViewSubjectType{
			{ID: "generic"},
			{ID: "humanoid", FrameMinimumRatio: 0.66, FrameMaximumRatio: 1},
			{ID: "quadruped", FrameMinimumRatio: 1},
		}}}
	for _, test := range []struct {
		subject string
		ratio   float64
		want    float64
	}{
		{"", 0.3, 0.3},
		{"generic", 2.5, 2.5},
		{"humanoid", 0.3, 0.66},
		{"humanoid", 0.8, 0.8},
		{"humanoid", 2, 1},
		{"quadruped", 0.7, 1},
		{"quadruped", 1.6, 1.6},
		{"unknown", 0.4, 0.4},
	} {
		if got := service.boundFrameRatio(test.ratio, test.subject); got != test.want {
			t.Errorf("%q at %v = %v, want %v", test.subject, test.ratio, got, test.want)
		}
	}
}
