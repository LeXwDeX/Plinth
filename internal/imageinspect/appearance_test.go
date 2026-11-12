package imageinspect

import (
	"image"
	"image/color"
	"testing"
)

type lookBlock struct {
	x0, y0, x1, y1 int // [x0, x1) x [y0, y1)
	fill           color.NRGBA
}

var (
	coatBlue   = color.NRGBA{R: 50, G: 80, B: 160, A: 255}
	coatRed    = color.NRGBA{R: 170, G: 50, B: 50, A: 255}
	skinTone   = color.NRGBA{R: 230, G: 190, B: 160, A: 255}
	hairBrown  = color.NRGBA{R: 70, G: 45, B: 30, A: 255}
	bootsDark  = color.NRGBA{R: 40, G: 40, B: 50, A: 255}
	steelGrey  = color.NRGBA{R: 150, G: 155, B: 165, A: 255}
	petOrange  = color.NRGBA{R: 225, G: 120, B: 40, A: 255}
	whiteField = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
)

// paintLook draws the blocks on an opaque white picture.
func paintLook(width, height int, blocks []lookBlock) *image.NRGBA {
	return subjectPicture(width, height, func(x, y int) color.NRGBA {
		result := whiteField
		for _, item := range blocks {
			if x >= item.x0 && x < item.x1 && y >= item.y0 && y < item.y1 {
				result = item.fill
			}
		}
		return result
	})
}

// mirrorLook is the same scene seen from the opposite side.
func mirrorLook(blocks []lookBlock, width int) []lookBlock {
	result := make([]lookBlock, len(blocks))
	for index, item := range blocks {
		result[index] = lookBlock{width - item.x1, item.y0, width - item.x0, item.y1, item.fill}
	}
	return result
}

// lookCharacter is a character with a coat, a head, boots and two detached
// accessories: a floating sword on one side and a pet on the other. The body
// is centred, the accessories are not, so the silhouette is not symmetric.
func lookCharacter(head color.NRGBA, coat color.NRGBA) []lookBlock {
	return []lookBlock{
		{40, 30, 88, 120, coat},
		{52, 8, 76, 30, head},
		{44, 120, 84, 150, bootsDark},
		{100, 40, 107, 110, steelGrey},
		{8, 125, 30, 148, petOrange},
	}
}

const lookWidth, lookHeight = 128, 160

var lookOptions = AppearanceOptions{
	SubjectThreshold: 32, PaletteColours: 6, PaletteMinShare: 0.03, PaletteMergeDistance: 48,
	PartMinShare: 0.01, NormalizedSide: 64,
}

func measureLook(blocks []lookBlock) Appearance {
	return MeasureAppearance(paintLook(lookWidth, lookHeight, blocks), lookOptions)
}

func failedCheckNames(checks []Check) []string {
	var names []string
	for _, check := range checks {
		if check.Status == StatusFail {
			names = append(names, check.Name)
		}
	}
	return names
}

func TestMeasureAppearanceReadsColoursAndParts(t *testing.T) {
	appearance := measureLook(lookCharacter(skinTone, coatBlue))
	if !appearance.HasSubject || appearance.Parts != 3 {
		t.Fatalf("appearance = %+v, want a body, a sword and a pet", appearance)
	}
	if len(appearance.Palette) < 3 || appearance.Palette[0].RGB != [3]float64{50, 80, 160} {
		t.Fatalf("the coat must be the dominant colour: %+v", appearance.Palette)
	}
	closeTo(t, appearance.Palette[0].Share, 4320.0/7044.0, 0.001, "coat share")
	again := measureLook(lookCharacter(skinTone, coatBlue))
	if len(again.Palette) != len(appearance.Palette) || again.Parts != appearance.Parts {
		t.Fatal("the measureLook is not deterministic")
	}
	for index := range appearance.Palette {
		if appearance.Palette[index] != again.Palette[index] {
			t.Fatalf("palette differs at %d: %+v vs %+v", index, appearance.Palette[index], again.Palette[index])
		}
	}
}

func TestShadingDoesNotSplitAColour(t *testing.T) {
	// A coat lit from the left: a gradient across the garment is one colour.
	picture := subjectPicture(lookWidth, lookHeight, func(x, y int) color.NRGBA {
		if x >= 40 && x < 88 && y >= 30 && y < 120 {
			return color.NRGBA{R: uint8(40 + (x-40)/2), G: uint8(70 + (x-40)/2), B: uint8(150 + (x-40)/2), A: 255}
		}
		return whiteField
	})
	appearance := MeasureAppearance(picture, lookOptions)
	if len(appearance.Palette) != 1 {
		t.Fatalf("a shaded garment became %d colours: %+v", len(appearance.Palette), appearance.Palette)
	}
}

func TestAppearanceAcceptsTheBackViewOfTheSameCharacter(t *testing.T) {
	front := measureLook(lookCharacter(skinTone, coatBlue))
	// From behind the face is hidden by hair, and the silhouette is mirrored.
	back := measureLook(mirrorLook(lookCharacter(hairBrown, coatBlue), lookWidth))
	limits := AppearanceLimits{MaxPaletteDistance: 0.1, MaxPartCountDelta: 1}
	checks := CompareAppearance(front, back, limits)
	if len(checks) != 2 || Failed(checks) {
		t.Fatalf("the same character seen from behind failed: %+v", checks)
	}
	mirror := CompareMirror(front, back, MirrorLimits{MinOverlap: 0.5, MaxOrientationMargin: 0.1})
	if len(mirror) != 2 || Failed(mirror) {
		t.Fatalf("a mirrored back view failed: %+v", mirror)
	}
}

func TestColourDriftIsInconsistent(t *testing.T) {
	front := measureLook(lookCharacter(skinTone, coatBlue))
	drifted := measureLook(mirrorLook(lookCharacter(hairBrown, coatRed), lookWidth))
	checks := CompareAppearance(front, drifted, AppearanceLimits{MaxPaletteDistance: 0.1, MaxPartCountDelta: 1})
	names := failedCheckNames(checks)
	if len(names) != 1 || names[0] != CheckPaletteDistance {
		t.Fatalf("a red coat on a blue character: failed %v of %+v", names, checks)
	}
	if checks[0].Actual == "" || checks[0].Expected == "" {
		t.Fatalf("the reason must say what was measured: %+v", checks[0])
	}
}

func TestAMissingAccessoryIsInconsistent(t *testing.T) {
	front := measureLook(lookCharacter(skinTone, coatBlue))
	bare := lookCharacter(hairBrown, coatBlue)[:3] // no sword, no pet
	candidate := measureLook(mirrorLook(bare, lookWidth))
	checks := CompareAppearance(front, candidate, AppearanceLimits{MaxPaletteDistance: 0.1, MaxPartCountDelta: 1})
	names := failedCheckNames(checks)
	if len(names) != 1 || names[0] != CheckPartCountDelta {
		t.Fatalf("a view that lost the sword and the pet: failed %v of %+v", names, checks)
	}
	// One accessory that merges with the body in a side view is within the tolerance.
	oneLost := lookCharacter(hairBrown, coatBlue)[:4] // the pet is missing
	checks = CompareAppearance(front, measureLook(mirrorLook(oneLost, lookWidth)),
		AppearanceLimits{MaxPaletteDistance: 0.1, MaxPartCountDelta: 1})
	if Failed(checks) {
		t.Fatalf("one missing part is inside the tolerance: %+v", checks)
	}
}

func TestSwappedSidesAreInconsistent(t *testing.T) {
	front := measureLook(lookCharacter(skinTone, coatBlue))
	limits := MirrorLimits{MinOverlap: 0.5, MaxOrientationMargin: 0.1}
	// A back view that kept the front's layout instead of mirroring it: the
	// sword stays on the same side of the picture.
	swapped := measureLook(lookCharacter(hairBrown, coatBlue))
	checks := CompareMirror(front, swapped, limits)
	orientation := false
	for _, name := range failedCheckNames(checks) {
		orientation = orientation || name == CheckMirrorOrientation
	}
	if !orientation {
		t.Fatalf("an unmirrored back view passed the orientation check: %+v", checks)
	}
	// With the overlap floor out of the way, the orientation check alone finds it.
	orientationOnly := CompareMirror(front, swapped, MirrorLimits{MaxOrientationMargin: 0.1})
	if names := failedCheckNames(orientationOnly); len(orientationOnly) != 1 || len(names) != 1 || names[0] != CheckMirrorOrientation {
		t.Fatalf("orientation alone: %+v", orientationOnly)
	}
}

func TestMirrorChecksIgnoreScaleAndPosition(t *testing.T) {
	left := measureLook(lookCharacter(skinTone, coatBlue))
	// The opposite view drawn smaller and lower in a larger picture.
	shifted := make([]lookBlock, 0, 5)
	for _, item := range mirrorLook(lookCharacter(hairBrown, coatBlue), lookWidth) {
		shifted = append(shifted, lookBlock{item.x0 + 20, item.y0 + 30, item.x1 + 20, item.y1 + 30, item.fill})
	}
	right := MeasureAppearance(paintLook(lookWidth+40, lookHeight+60, shifted), lookOptions)
	checks := CompareMirror(left, right, MirrorLimits{MinOverlap: 0.5, MaxOrientationMargin: 0.1})
	if len(checks) != 2 || Failed(checks) {
		t.Fatalf("a translated mirror image failed: %+v", checks)
	}
}

func TestADifferentSilhouetteIsNotAMirrorImage(t *testing.T) {
	left := measureLook(lookCharacter(skinTone, coatBlue))
	// A short, wide figure instead of the character.
	other := measureLook([]lookBlock{{10, 100, 118, 150, coatBlue}})
	checks := CompareMirror(left, other, MirrorLimits{MinOverlap: 0.7, MaxOrientationMargin: 0.1})
	found := false
	for _, name := range failedCheckNames(checks) {
		found = found || name == CheckMirrorOverlap
	}
	if !found {
		t.Fatalf("a different silhouette passed the mirror overlap: %+v", checks)
	}
}

func TestASymmetricSubjectNeverFailsTheOrientationMargin(t *testing.T) {
	symmetric := []lookBlock{{30, 20, 98, 140, coatBlue}, {50, 4, 78, 20, skinTone}}
	reference, candidate := measureLook(symmetric), measureLook(symmetric)
	checks := CompareMirror(reference, candidate, MirrorLimits{MinOverlap: 0.9, MaxOrientationMargin: 0.01})
	if len(checks) != 2 || Failed(checks) {
		t.Fatalf("a symmetric silhouette must pass both ways: %+v", checks)
	}
}

func TestZeroLimitsDisableTheAppearanceChecks(t *testing.T) {
	front := measureLook(lookCharacter(skinTone, coatBlue))
	other := measureLook([]lookBlock{{10, 100, 118, 150, coatRed}})
	if checks := CompareAppearance(front, other, AppearanceLimits{}); len(checks) != 0 {
		t.Fatalf("zero limits produced checks: %+v", checks)
	}
	if checks := CompareMirror(front, other, MirrorLimits{}); len(checks) != 0 {
		t.Fatalf("zero mirror limits produced checks: %+v", checks)
	}
	if checks := CompareAppearance(front, Appearance{}, AppearanceLimits{MaxPaletteDistance: 0.1, MaxPartCountDelta: 1}); len(checks) != 0 {
		t.Fatalf("a picture without a subject has nothing to compare: %+v", checks)
	}
}

func TestPaletteDistance(t *testing.T) {
	blue := []PaletteColour{{RGB: [3]float64{50, 80, 160}, Share: 0.6}, {RGB: [3]float64{230, 190, 160}, Share: 0.4}}
	red := []PaletteColour{{RGB: [3]float64{170, 50, 50}, Share: 0.6}, {RGB: [3]float64{230, 190, 160}, Share: 0.4}}
	if distance := PaletteDistance(blue, blue); distance != 0 {
		t.Fatalf("the same palette = %v", distance)
	}
	if PaletteDistance(blue, red) != PaletteDistance(red, blue) {
		t.Fatal("the palette distance is not symmetric")
	}
	if distance := PaletteDistance(blue, red); distance < 0.1 || distance > 0.3 {
		t.Fatalf("a blue coat against a red one = %v", distance)
	}
	// Other proportions of the same colours barely matter.
	reweighted := []PaletteColour{{RGB: [3]float64{50, 80, 160}, Share: 0.9}, {RGB: [3]float64{230, 190, 160}, Share: 0.1}}
	if distance := PaletteDistance(blue, reweighted); distance != 0 {
		t.Fatalf("same colours, other shares = %v", distance)
	}
	if PaletteDistance(nil, blue) != 0 || PaletteDistance(blue, nil) != 0 {
		t.Fatal("an empty palette has nothing to compare")
	}
}

func TestPartsRespectTheMinimumShareAndTheGap(t *testing.T) {
	options := lookOptions
	options.PartMinShare = 0.05
	body := lookBlock{20, 20, 80, 120, coatBlue}
	twoSeparate := paintLook(lookWidth, lookHeight, []lookBlock{body, {90, 60, 110, 100, steelGrey}})
	if parts := MeasureAppearance(twoSeparate, options).Parts; parts != 2 {
		t.Fatalf("a body and a detached piece = %d parts", parts)
	}
	touching := paintLook(lookWidth, lookHeight, []lookBlock{body, {80, 60, 110, 100, steelGrey}})
	if parts := MeasureAppearance(touching, options).Parts; parts != 1 {
		t.Fatalf("a piece that touches the body = %d parts", parts)
	}
	speck := paintLook(lookWidth, lookHeight, []lookBlock{body, {100, 20, 103, 23, steelGrey}})
	if parts := MeasureAppearance(speck, options).Parts; parts != 1 {
		t.Fatalf("a speck under the minimum share = %d parts", parts)
	}
	// On a large picture the grid joins pieces closer than one cell.
	large := subjectPicture(1024, 1024, func(x, y int) color.NRGBA {
		switch {
		case x >= 100 && x < 500 && y >= 100 && y < 900:
			return coatBlue
		case x >= 502 && x < 700 && y >= 300 && y < 700: // a 2-pixel gap, finer than a 4-pixel cell
			return steelGrey
		}
		return whiteField
	})
	if parts := MeasureAppearance(large, options).Parts; parts != 1 {
		t.Fatalf("pieces closer than a grid cell = %d parts", parts)
	}
}

func TestMainComponentCheckAcceptsAccessoriesButNotSeveralFigures(t *testing.T) {
	withAccessories := Report{ComponentCount: 3, LargestComponentRatio: 0.86}
	if check := MainComponentCheck(withAccessories, 0.6); check.Status != StatusPass || check.Name != CheckMainComponent {
		t.Fatalf("a character with a sword and a pet = %+v", check)
	}
	twoFigures := Report{ComponentCount: 2, LargestComponentRatio: 0.5}
	if check := MainComponentCheck(twoFigures, 0.6); check.Status != StatusFail {
		t.Fatalf("two equal figures = %+v", check)
	}
	if check := MainComponentCheck(Report{ComponentCount: 1, LargestComponentRatio: 1}, 0.6); check.Status != StatusPass {
		t.Fatalf("one piece = %+v", check)
	}
	if check := MainComponentCheck(Report{}, 0.6); check.Status != StatusPass {
		t.Fatalf("a picture without a subject is the blank check's business: %+v", check)
	}
}
