package imageinspect

// Appearance comparison of two views of one subject (#283). The geometry
// checks of Compare see where the subject sits in the frame; these see what it
// looks like: its dominant colours, how many detached parts it has, and whether
// two opposite views are mirror images of each other. Like the rest of the
// package they read decoded pixels, never meaning, and every bound comes from
// the caller (the Schema), so a zero bound disables its check.

import (
	"fmt"
	"image"
	"math"
	"sort"
	"strings"
)

// Names of the checks this file produces. They are stable wire values the
// Schema words (three_view.consistency.reasons).
const (
	CheckPaletteDistance   = "palette_distance"
	CheckPartCountDelta    = "part_count_delta"
	CheckMirrorOverlap     = "mirror_overlap"
	CheckMirrorOrientation = "mirror_orientation"
	CheckMainComponent     = "main_component"
)

// maximumColourDistance is the RGB distance between black and white, the scale
// the palette distance is normalised by.
var maximumColourDistance = math.Sqrt(3) * 255

// colourLevels is the number of steps per channel the palette bins the subject
// pixels into before it merges the bins into dominant colours.
const colourLevels = 16

// maximumPartGrid bounds the longest side of the grid the parts are counted
// on, so a large picture costs linear time and bounded memory; pieces closer
// than one cell count as one part.
const maximumPartGrid = 256

// AppearanceOptions are the measuring parameters of MeasureAppearance.
type AppearanceOptions struct {
	// SubjectThreshold is the colour distance from the background that makes a
	// pixel subject, the measure of SubjectSegments.
	SubjectThreshold float64
	// PaletteColours bounds the dominant colours kept (heaviest first), and
	// PaletteMinShare is the share of the subject a colour needs to be one.
	PaletteColours  int
	PaletteMinShare float64
	// PaletteMergeDistance is the RGB distance under which two colours count as
	// one: shading must not split a garment into several colours.
	PaletteMergeDistance float64
	// PartMinShare is the share of the subject's pixels a connected piece needs
	// to count as a part.
	PartMinShare float64
	// NormalizedSide is the edge of the square the silhouette is scaled to for
	// the mirror comparison; zero skips the silhouette.
	NormalizedSide int
}

// PaletteColour is one dominant colour of a subject: the mean colour of the
// pixels merged into it and their share of the subject.
type PaletteColour struct {
	RGB   [3]float64 `json:"rgb"`
	Share float64    `json:"share"`
}

// Appearance is the deterministic look of one picture's subject.
type Appearance struct {
	// HasSubject is false when no pixel passed the subject threshold.
	HasSubject bool `json:"has_subject"`
	// Ratio is the subject's share of the picture.
	Ratio float64 `json:"ratio"`
	// Palette lists the dominant colours, heaviest first.
	Palette []PaletteColour `json:"palette,omitempty"`
	// Parts counts the connected pieces of the subject that hold at least
	// PartMinShare of it: the body and each detached accessory.
	Parts int `json:"parts"`
	// Side and Silhouette are the subject cropped to its box and scaled to a
	// Side x Side square, row-major; the mirror comparison reads them.
	Side       int    `json:"side,omitempty"`
	Silhouette []bool `json:"-"`
}

// MeasureAppearance reduces one decoded picture to its Appearance.
func MeasureAppearance(picture *image.NRGBA, options AppearanceOptions) Appearance {
	subject := SubjectSegments(picture, options.SubjectThreshold)
	appearance := Appearance{HasSubject: subject.HasSubject, Ratio: subject.Ratio}
	if !subject.HasSubject {
		return appearance
	}
	appearance.Palette = dominantColours(picture, subject, options)
	appearance.Parts = subjectParts(subject, options.PartMinShare)
	if options.NormalizedSide > 0 {
		appearance.Side = options.NormalizedSide
		appearance.Silhouette = normalizeMask(subject, options.NormalizedSide)
	}
	return appearance
}

// dominantColours bins the subject pixels, then merges the bins into colours:
// the heaviest bin seeds a colour and every lighter bin within the merge
// distance of a colour joins it. The order is by weight with ties broken by
// bin index, so the result is deterministic.
func dominantColours(picture *image.NRGBA, subject Subject, options AppearanceOptions) []PaletteColour {
	var counts [colourLevels * colourLevels * colourLevels]int
	var sums [colourLevels * colourLevels * colourLevels][3]float64
	const step = 256 / colourLevels
	for y := 0; y < subject.Height; y++ {
		row := picture.Pix[y*picture.Stride:]
		for x := 0; x < subject.Width; x++ {
			if !subject.Mask[y*subject.Width+x] {
				continue
			}
			red, green, blue := row[4*x], row[4*x+1], row[4*x+2]
			bin := (int(red)/step)*colourLevels*colourLevels + (int(green)/step)*colourLevels + int(blue)/step
			counts[bin]++
			sums[bin][0] += float64(red)
			sums[bin][1] += float64(green)
			sums[bin][2] += float64(blue)
		}
	}
	order := make([]int, 0, 256)
	for bin, count := range counts {
		if count > 0 {
			order = append(order, bin)
		}
	}
	sort.Slice(order, func(a, b int) bool {
		if counts[order[a]] != counts[order[b]] {
			return counts[order[a]] > counts[order[b]]
		}
		return order[a] < order[b]
	})
	type cluster struct {
		weight float64
		sum    [3]float64
	}
	var clusters []cluster
	for _, bin := range order {
		weight := float64(counts[bin])
		centre := [3]float64{sums[bin][0] / weight, sums[bin][1] / weight, sums[bin][2] / weight}
		joined := false
		for index := range clusters {
			mean := [3]float64{clusters[index].sum[0] / clusters[index].weight,
				clusters[index].sum[1] / clusters[index].weight, clusters[index].sum[2] / clusters[index].weight}
			if colourDistanceBetween(mean, centre) <= options.PaletteMergeDistance {
				clusters[index].weight += weight
				for channel := range clusters[index].sum {
					clusters[index].sum[channel] += sums[bin][channel]
				}
				joined = true
				break
			}
		}
		if !joined {
			clusters = append(clusters, cluster{weight: weight, sum: sums[bin]})
		}
	}
	sort.SliceStable(clusters, func(a, b int) bool { return clusters[a].weight > clusters[b].weight })
	limit := max(options.PaletteColours, 1)
	total := float64(subject.Pixels)
	palette := make([]PaletteColour, 0, min(limit, len(clusters)))
	for _, item := range clusters {
		share := item.weight / total
		// The heaviest colour is always kept: a subject has at least one.
		if len(palette) == limit || (len(palette) > 0 && share < options.PaletteMinShare) {
			break
		}
		palette = append(palette, PaletteColour{
			RGB:   [3]float64{item.sum[0] / item.weight, item.sum[1] / item.weight, item.sum[2] / item.weight},
			Share: share,
		})
	}
	return palette
}

// subjectParts counts the 4-connected pieces of the subject on a coarse grid
// whose cells hold at least minShare of the subject's cells.
func subjectParts(subject Subject, minShare float64) int {
	width, height := subject.Width, subject.Height
	scale := 1
	if longest := max(width, height); longest > maximumPartGrid {
		scale = (longest + maximumPartGrid - 1) / maximumPartGrid
	}
	gridWidth, gridHeight := (width+scale-1)/scale, (height+scale-1)/scale
	grid := make([]bool, gridWidth*gridHeight)
	cells := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			cell := (y/scale)*gridWidth + x/scale
			if subject.Mask[y*width+x] && !grid[cell] {
				grid[cell] = true
				cells++
			}
		}
	}
	if cells == 0 {
		return 0
	}
	minimum := max(1, int(math.Ceil(float64(cells)*minShare)))
	seen := make([]bool, len(grid))
	stack := make([]int, 0, 64)
	parts := 0
	for start := range grid {
		if !grid[start] || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		size := 0
		for len(stack) > 0 {
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			x, y := at%gridWidth, at/gridWidth
			for _, next := range [4][2]int{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
				if next[0] < 0 || next[0] >= gridWidth || next[1] < 0 || next[1] >= gridHeight {
					continue
				}
				neighbour := next[1]*gridWidth + next[0]
				if grid[neighbour] && !seen[neighbour] {
					seen[neighbour] = true
					stack = append(stack, neighbour)
				}
			}
		}
		if size >= minimum {
			parts++
		}
	}
	return parts
}

// PaletteDistance compares two dominant-colour sets on a 0..1 scale: each
// colour is matched with the nearest colour of the other set, the RGB distance
// is weighted by the colour's share and normalised by the black-white
// distance, and the larger of the two directions is the result. A colour that
// has no counterpart in the other view (a coat that turned red) raises it by
// its share; different proportions of the same colours (a face that shows only
// from the front) barely move it. An empty set has nothing to compare and
// gives 0.
func PaletteDistance(reference, candidate []PaletteColour) float64 {
	if len(reference) == 0 || len(candidate) == 0 {
		return 0
	}
	return math.Max(directedPaletteDistance(reference, candidate), directedPaletteDistance(candidate, reference))
}

func directedPaletteDistance(from, to []PaletteColour) float64 {
	total := 0.0
	for _, colour := range from {
		total += colour.Share
	}
	if total <= 0 {
		return 0
	}
	sum := 0.0
	for _, colour := range from {
		nearest := math.Inf(1)
		for _, other := range to {
			nearest = math.Min(nearest, colourDistanceBetween(colour.RGB, other.RGB))
		}
		sum += colour.Share / total * nearest / maximumColourDistance
	}
	return sum
}

func describePalette(palette []PaletteColour, limit int) string {
	parts := make([]string, 0, limit)
	for index, colour := range palette {
		if index == limit {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %.0f%%", colourHex(colour.RGB), colour.Share*100))
	}
	return strings.Join(parts, ", ")
}

// AppearanceLimits bound how far a view's look may differ from the reference
// view. A zero limit disables its comparison.
type AppearanceLimits struct {
	// MaxPaletteDistance bounds PaletteDistance.
	MaxPaletteDistance float64
	// MaxPartCountDelta bounds the difference of the part counts: a pet or a
	// weapon that exists in one view and not in the other.
	MaxPartCountDelta int
}

// CompareAppearance judges a candidate view against the reference view. The
// checks are independent, so the caller can report every reason one view has
// to be redone. A picture without a subject has nothing to compare; the
// geometry checks already fail it.
func CompareAppearance(reference, candidate Appearance, limits AppearanceLimits) []Check {
	if !reference.HasSubject || !candidate.HasSubject {
		return nil
	}
	checks := make([]Check, 0, 2)
	if limits.MaxPaletteDistance > 0 && len(reference.Palette) > 0 && len(candidate.Palette) > 0 {
		distance := PaletteDistance(reference.Palette, candidate.Palette)
		checks = append(checks, Check{Name: CheckPaletteDistance, Status: status(distance <= limits.MaxPaletteDistance),
			// Actual reads "distance; reference colours -> the view's colours".
			Actual: fmt.Sprintf("%.3f; %s -> %s", distance,
				describePalette(reference.Palette, 4), describePalette(candidate.Palette, 4)),
			Expected: fmt.Sprintf("%.3f", limits.MaxPaletteDistance), Fix: "regenerate"})
	}
	if limits.MaxPartCountDelta > 0 {
		delta := candidate.Parts - reference.Parts
		if delta < 0 {
			delta = -delta
		}
		checks = append(checks, Check{Name: CheckPartCountDelta, Status: status(delta <= limits.MaxPartCountDelta),
			// Actual reads "reference parts -> the view's parts".
			Actual:   fmt.Sprintf("%d -> %d", reference.Parts, candidate.Parts),
			Expected: fmt.Sprintf("%d", limits.MaxPartCountDelta), Fix: "regenerate"})
	}
	return checks
}

// MirrorLimits bound how far two opposite views may be from mirror images of
// each other. A zero limit disables its comparison.
type MirrorLimits struct {
	// MinOverlap is the least overlap of one silhouette with the mirrored
	// silhouette of the other.
	MinOverlap float64
	// MaxOrientationMargin is how much better the unmirrored silhouettes may
	// overlap than the mirrored ones. An orthographic view and the view from
	// the opposite side have mirrored silhouettes, so a view that overlaps the
	// other one better as it is than mirrored faces the same way: its left and
	// right are swapped. A symmetric silhouette overlaps both ways equally and
	// never fails this.
	MaxOrientationMargin float64
}

// CompareMirror judges whether the candidate is the mirror image of the
// reference view, as the left and right views (or the front and back views) of
// one subject are. Both appearances must come from MeasureAppearance with the
// same NormalizedSide; otherwise there is nothing to compare.
func CompareMirror(reference, candidate Appearance, limits MirrorLimits) []Check {
	if (limits.MinOverlap <= 0 && limits.MaxOrientationMargin <= 0) || !reference.HasSubject || !candidate.HasSubject ||
		reference.Side <= 0 || reference.Side != candidate.Side ||
		len(reference.Silhouette) != reference.Side*reference.Side || len(candidate.Silhouette) != len(reference.Silhouette) {
		return nil
	}
	mirrored := silhouetteOverlap(reference.Silhouette, candidate.Silhouette, reference.Side, true)
	plain := silhouetteOverlap(reference.Silhouette, candidate.Silhouette, reference.Side, false)
	checks := make([]Check, 0, 2)
	if limits.MinOverlap > 0 {
		checks = append(checks, Check{Name: CheckMirrorOverlap, Status: status(mirrored >= limits.MinOverlap),
			Actual:   fmt.Sprintf("%.2f", mirrored),
			Expected: fmt.Sprintf("%.2f", limits.MinOverlap), Fix: "regenerate"})
	}
	if limits.MaxOrientationMargin > 0 {
		margin := plain - mirrored
		checks = append(checks, Check{Name: CheckMirrorOrientation, Status: status(margin <= limits.MaxOrientationMargin),
			// Actual reads "unmirrored overlap / mirrored overlap".
			Actual:   fmt.Sprintf("%.2f / %.2f", plain, mirrored),
			Expected: fmt.Sprintf("%.2f", limits.MaxOrientationMargin), Fix: "regenerate"})
	}
	return checks
}

// silhouetteOverlap is the intersection over union of two side x side masks;
// flip mirrors the first one horizontally.
func silhouetteOverlap(first, second []bool, side int, flip bool) float64 {
	intersection, union := 0, 0
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			source := x
			if flip {
				source = side - 1 - x
			}
			inFirst, inSecond := first[y*side+source], second[y*side+x]
			if inFirst && inSecond {
				intersection++
			}
			if inFirst || inSecond {
				union++
			}
		}
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// MainComponentCheck judges that one piece holds most of the subject. A
// character with a floating weapon or a pet is one subject in several pieces
// and passes; several figures side by side (a contact sheet instead of one
// view) split the subject into comparable pieces and fail. A picture with at
// most one piece passes.
func MainComponentCheck(report Report, minimum float64) Check {
	ok := report.ComponentCount <= 1 || report.LargestComponentRatio >= minimum
	return Check{Name: CheckMainComponent, Status: status(ok),
		Actual: fmt.Sprintf("%.0f%% (%d components)",
			report.LargestComponentRatio*100, report.ComponentCount),
		Expected: fmt.Sprintf("%.0f%%", minimum*100), Fix: "single_subject"}
}
