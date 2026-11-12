package imageinspect

import (
	"image"
	"image/color"
	"math"
	"strings"
	"testing"
)

// shippedRequirements mirror the thresholds the default Catalog declares in
// image_review.usage, so the fixtures below are judged the way the product
// judges them.
func shippedRequirements() Requirements {
	return Requirements{
		Tiling: TilingLimits{Segments: 8, MaxSeamRatio: 2.5, MinSeamDelta: 6},
		Alpha:  AlphaLimits{MinTransparentRatio: 0.005, MaxBackgroundSpread: 32},
		Fringe: FringeLimits{BandRatio: 0.002, LightLuma: 225, LumaGap: 60, MaxRatio: 0.12},
	}
}

func checkNamed(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("no %q check in %+v", name, checks)
	return Check{}
}

func inspectFor(t *testing.T, data []byte, requirements Requirements) Report {
	t.Helper()
	report, err := InspectFor(data, Limits{}, requirements)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// #282: the picture's own pixels are compared with the purpose's minimum short
// edge. Below it fails with the stable fix code; at it, and above it, passes.
func TestShortEdgeBelowTheMinimumFails(t *testing.T) {
	requirements := shippedRequirements()
	requirements.MinShortEdge = 96
	paint := func(x, y int) color.NRGBA { return color.NRGBA{R: 200, G: 120, B: 40, A: 255} }

	below := inspectFor(t, encodePNG(t, 160, 64, paint), requirements)
	if below.ShortEdge != 64 {
		t.Fatalf("short edge = %d, want 64", below.ShortEdge)
	}
	check := checkNamed(t, below.ReviewRequirements(requirements), "short_edge")
	if check.Status != StatusFail || check.Fix != "raise_resolution" ||
		!strings.Contains(check.Actual, "64 px (160 x 64)") || !strings.Contains(check.Expected, "96") {
		t.Fatalf("below the minimum: %+v", check)
	}

	for name, size := range map[string][2]int{"exactly the minimum": {96, 200}, "above the minimum": {128, 128}} {
		report := inspectFor(t, encodePNG(t, size[0], size[1], paint), requirements)
		if got := checkNamed(t, report.ReviewRequirements(requirements), "short_edge"); got.Status != StatusPass {
			t.Fatalf("%s: %+v", name, got)
		}
	}
}

func TestRequirementsAskForNothingByDefault(t *testing.T) {
	report := inspectFor(t, encodePNG(t, 32, 32, func(x, y int) color.NRGBA { return color.NRGBA{A: 255} }), Requirements{})
	if checks := report.ReviewRequirements(Requirements{}); len(checks) != 0 {
		t.Fatalf("a zero Requirements produced checks: %+v", checks)
	}
	if report.Tiling != nil || report.EdgeFringe != nil {
		t.Fatalf("a zero Requirements measured pixels it was not asked about: %+v", report)
	}
}

// periodic is a texture whose period divides the picture size, so its opposite
// edges are neighbours in the infinite tiling.
func periodic(x, y int) color.NRGBA {
	phase := func(value, amplitude float64, period float64) uint8 {
		return uint8(128 + amplitude*math.Sin(2*math.Pi*value/period))
	}
	return color.NRGBA{
		R: phase(float64(x), 100, 16),
		G: phase(float64(y), 100, 16),
		B: phase(float64(x+y), 60, 16),
		A: 255,
	}
}

// #287: a tileable texture passes; a texture whose edges do not meet is flagged
// with the position of the seam.
func TestTilingAcceptsAContinuousTexture(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Tileable = true
	report := inspectFor(t, encodePNG(t, 128, 128, periodic), requirements)
	if report.Tiling == nil || len(report.Tiling.LeftRight) != 8 || len(report.Tiling.TopBottom) != 8 {
		t.Fatalf("tiling = %+v", report.Tiling)
	}
	check := checkNamed(t, report.ReviewRequirements(requirements), "seamless")
	if check.Status != StatusPass {
		t.Fatalf("a periodic texture was flagged: %+v (%+v)", check, report.Tiling)
	}
}

func TestTilingFlagsAGradientOnBothAxes(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Tileable = true
	ramp := func(x, y int) color.NRGBA { return color.NRGBA{R: uint8(2 * x), G: uint8(2 * y), B: 100, A: 255} }
	report := inspectFor(t, encodePNG(t, 128, 128, ramp), requirements)
	check := checkNamed(t, report.ReviewRequirements(requirements), "seamless")
	if check.Status != StatusFail || check.Fix != "regenerate_tileable" ||
		!strings.Contains(check.Actual, "left/right seam at y 0-127") ||
		!strings.Contains(check.Actual, "top/bottom seam at x 0-127") {
		t.Fatalf("a gradient was not flagged on both axes: %+v", check)
	}
}

func TestTilingNamesWhereTheSeamBreaks(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Tileable = true
	// The texture is continuous except that rows 40..79 of the last column are
	// painted white, so only that stretch of the left/right seam is a step.
	patched := func(x, y int) color.NRGBA {
		if x == 127 && y >= 40 && y <= 79 {
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		return periodic(x, y)
	}
	report := inspectFor(t, encodePNG(t, 128, 128, patched), requirements)
	check := checkNamed(t, report.ReviewRequirements(requirements), "seamless")
	if check.Status != StatusFail || !strings.Contains(check.Actual, "left/right seam at y 32-79") {
		t.Fatalf("the broken stretch was not named: %+v (%+v)", check, report.Tiling)
	}
	if strings.Contains(check.Actual, "top/bottom") {
		t.Fatalf("the intact seam was reported: %+v", check)
	}
}

func TestTilingFlatTextureWithATinyOffsetPasses(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Tileable = true
	// A flat colour with a 3-level step between the edges is below the
	// MinSeamDelta floor: invisible, so not a seam.
	flat := func(x, y int) color.NRGBA {
		if x == 127 {
			return color.NRGBA{R: 103, G: 103, B: 103, A: 255}
		}
		return color.NRGBA{R: 100, G: 100, B: 100, A: 255}
	}
	report := inspectFor(t, encodePNG(t, 128, 128, flat), requirements)
	if check := checkNamed(t, report.ReviewRequirements(requirements), "seamless"); check.Status != StatusPass {
		t.Fatalf("an invisible offset was flagged: %+v", check)
	}
}

func TestTilingNeedsTwoPixelsPerAxis(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Tileable = true
	report := inspectFor(t, encodePNG(t, 1, 1, func(x, y int) color.NRGBA { return color.NRGBA{A: 255} }), requirements)
	if check := checkNamed(t, report.ReviewRequirements(requirements), "seamless"); check.Status != StatusUnknown {
		t.Fatalf("a one-pixel picture was judged: %+v", check)
	}
	// A report that never measured seams cannot pass a tiling requirement.
	if check := checkNamed(t, Report{}.ReviewRequirements(requirements), "seamless"); check.Status != StatusUnknown {
		t.Fatalf("an unmeasured report passed: %+v", check)
	}
}

// disc paints a picture around its centre: paint receives each pixel's
// distance from the centre in pixels, so a fixture is a list of rings.
func disc(size int, paint func(distance float64) color.NRGBA) func(x, y int) color.NRGBA {
	centre := float64(size) / 2
	return func(x, y int) color.NRGBA {
		return paint(math.Hypot(float64(x)+0.5-centre, float64(y)+0.5-centre))
	}
}

var (
	darkRed = color.NRGBA{R: 60, G: 20, B: 20, A: 255}
	white   = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
)

// #284: a cut-out whose edge keeps a light rim of its old background is flagged;
// a clean edge, a wide deliberate outline and a light subject are not.
func TestFringeFlagsAWhiteRimAndAcceptsCleanEdges(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Transparency = TransparencyRequired
	cases := []struct {
		name string
		want string
		draw func(distance float64) color.NRGBA
	}{
		{"white rim two pixels wide", StatusFail, func(d float64) color.NRGBA {
			switch {
			case d <= 60:
				return darkRed
			case d <= 62:
				return white
			}
			return color.NRGBA{}
		}},
		{"clean soft edge of the subject's own colour", StatusPass, func(d float64) color.NRGBA {
			switch {
			case d <= 60:
				return darkRed
			case d <= 62:
				edge := darkRed
				edge.A = uint8(255 * (62 - d) / 2)
				return edge
			}
			return color.NRGBA{}
		}},
		{"deliberate white outline ten pixels wide", StatusPass, func(d float64) color.NRGBA {
			switch {
			case d <= 50:
				return darkRed
			case d <= 60:
				return white
			}
			return color.NRGBA{}
		}},
		{"light subject", StatusPass, func(d float64) color.NRGBA {
			if d <= 60 {
				return white
			}
			return color.NRGBA{}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := inspectFor(t, encodePNG(t, 200, 200, disc(200, test.draw)), requirements)
			checks := report.ReviewRequirements(requirements)
			if got := checkNamed(t, checks, "alpha"); got.Status != StatusPass {
				t.Fatalf("a real cut-out failed the alpha check: %+v", got)
			}
			got := checkNamed(t, checks, "edge_fringe")
			if got.Status != test.want || got.Fix != "clean_edges" {
				t.Fatalf("edge_fringe = %+v (report %+v)", got, report.EdgeFringe)
			}
		})
	}
}

func TestFringeNeedsAReferenceBand(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Transparency = TransparencyRequired
	// A three-pixel line has no pixel further than two bands from transparency.
	line := func(x, y int) color.NRGBA {
		if y >= 98 && y <= 100 {
			return darkRed
		}
		return color.NRGBA{}
	}
	report := inspectFor(t, encodePNG(t, 200, 200, line), requirements)
	if check := checkNamed(t, report.ReviewRequirements(requirements), "edge_fringe"); check.Status != StatusUnknown {
		t.Fatalf("an unmeasurable edge was judged: %+v", check)
	}
}

// #284: a picture that claims a transparent background but has no alpha channel
// is flagged: a baked-in checkerboard, a flat opaque RGBA file, or an alpha
// channel that is opaque everywhere but a few stray pixels.
func TestAlphaFlagsPicturesWithoutARealChannel(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Transparency = TransparencyRequired
	checker := func(x, y int) color.NRGBA {
		if x >= 60 && x < 140 && y >= 60 && y < 140 {
			return darkRed
		}
		if (x/8+y/8)%2 == 0 {
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		return color.NRGBA{R: 204, G: 204, B: 204, A: 255}
	}
	strays := func(x, y int) color.NRGBA {
		if (x == 3 && y == 3) || (x == 90 && y == 4) || (x == 150 && y == 120) {
			return color.NRGBA{}
		}
		return darkRed
	}
	for name, paint := range map[string]func(x, y int) color.NRGBA{
		"baked-in checkerboard": checker,
		"alpha only on strays":  strays,
		"opaque everywhere":     func(x, y int) color.NRGBA { return darkRed },
	} {
		t.Run(name, func(t *testing.T) {
			report := inspectFor(t, encodePNG(t, 200, 200, paint), requirements)
			checks := report.ReviewRequirements(requirements)
			if got := checkNamed(t, checks, "alpha"); got.Status != StatusFail || got.Fix != "remove_background" {
				t.Fatalf("alpha = %+v (report %+v)", got, report)
			}
			if got := checkNamed(t, checks, "edge_fringe"); got.Status != StatusUnknown {
				t.Fatalf("a picture with no real alpha had its edge judged: %+v", got)
			}
		})
	}
}

func TestAlphaAcceptsARealChannel(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Transparency = TransparencyRequired
	report := inspectFor(t, encodePNG(t, 200, 200, disc(200, func(d float64) color.NRGBA {
		if d <= 60 {
			return darkRed
		}
		return color.NRGBA{}
	})), requirements)
	if got := checkNamed(t, report.ReviewRequirements(requirements), "alpha"); got.Status != StatusPass {
		t.Fatalf("a real cut-out failed: %+v", got)
	}
}

// A purpose that takes a solid or a transparent background accepts a plain
// opaque one but still flags a busy opaque background.
func TestAlphaAllowedTakesAPlainSolidBackground(t *testing.T) {
	requirements := shippedRequirements()
	requirements.Transparency = TransparencyAllowed
	plain := func(x, y int) color.NRGBA {
		if x >= 60 && x < 140 && y >= 60 && y < 140 {
			return darkRed
		}
		return color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	}
	busy := func(x, y int) color.NRGBA {
		if x >= 60 && x < 140 && y >= 60 && y < 140 {
			return darkRed
		}
		if (x/8+y/8)%2 == 0 {
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		return color.NRGBA{R: 160, G: 160, B: 160, A: 255}
	}
	report := inspectFor(t, encodePNG(t, 200, 200, plain), requirements)
	checks := report.ReviewRequirements(requirements)
	if got := checkNamed(t, checks, "alpha"); got.Status != StatusPass {
		t.Fatalf("a plain solid background was refused: %+v", got)
	}
	if got := checkNamed(t, checks, "edge_fringe"); got.Status != StatusUnknown {
		t.Fatalf("an opaque picture had its edge judged: %+v", got)
	}
	report = inspectFor(t, encodePNG(t, 200, 200, busy), requirements)
	if got := checkNamed(t, report.ReviewRequirements(requirements), "alpha"); got.Status != StatusFail {
		t.Fatalf("a busy opaque background passed: %+v (spread %.1f)", got, report.BackgroundSpread)
	}
}

func TestDistanceToTransparencyIgnoresThePictureBorder(t *testing.T) {
	// Row: opaque x4 then transparent. The left end touches the picture border,
	// which is not transparency, so its distance is the distance to the hole.
	picture := image.NewNRGBA(image.Rect(0, 0, 5, 1))
	for x := 0; x < 4; x++ {
		picture.SetNRGBA(x, 0, darkRed)
	}
	got := distanceToTransparency(picture, 10)
	want := []uint8{4, 3, 2, 1, 0}
	for index, value := range want {
		if got[index] != value {
			t.Fatalf("distances = %v, want %v", got, want)
		}
	}
}
