package imageinspect

import "image"

// Transparent-edge rim measurement (#284). A cut-out made from a picture with a
// light background often keeps a thin rim of that background colour along its
// edge: it is invisible on white and shows as a white outline on a dark or
// coloured interface. The measurement looks only at pixels of the subject
// (alpha at least subjectAlpha) near its boundary with transparency:
//
//   - the edge band is the subject pixels within `band` pixels of a
//     transparent pixel, weighted by their alpha;
//   - the reference band is the subject pixels further in (more than 2 bands
//     and at most 4 bands from transparency), which show what the subject
//     itself looks like next to that edge;
//   - an edge pixel is rim when it is light (luma at least LightLuma) and
//     brighter than the reference band of its tile of the picture by LumaGap.
//
// A light outline the artist drew deliberately (a sticker border) is wider than
// the band, so its reference band is light too and it is not counted. Dark rims
// and coloured backgrounds are not measured; the report says so through the
// numbers it gives, never by a verdict. Thresholds live in the Schema.

// subjectAlpha is the alpha from which a pixel belongs to the subject. It is
// the same threshold the subject mask uses.
const subjectAlpha = 16

// fringeTiles is the grid the reference brightness is taken on, per axis, so a
// subject that is dark on one side and light on the other is compared with its
// own neighbourhood.
const fringeTiles = 8

// minTileReference is the number of reference pixels a tile needs to be used on
// its own; sparser tiles fall back to the whole picture's reference.
const minTileReference = 4

// maxFringeBand bounds the band width so the distance map fits one byte.
const maxFringeBand = 60

// FringeReport is the measured transparent-edge rim.
type FringeReport struct {
	// Measured is false when the picture has no usable edge: no subject
	// boundary at all, or a subject too thin to have a reference band.
	Measured bool `json:"measured"`
	// BandPixels is the width of the edge band in pixels.
	BandPixels int `json:"band_pixels"`
	// EdgePixels is the number of subject pixels in the edge band.
	EdgePixels int `json:"edge_pixels"`
	// FringeRatio is the alpha-weighted share of the edge band that is rim, in
	// [0, 1].
	FringeRatio float64 `json:"fringe_ratio"`
}

// FringeProbe holds the measurement parameters; the verdict threshold is
// FringeLimits.MaxRatio.
type FringeProbe struct {
	// BandRatio is the band width as a share of the picture's longer edge.
	BandRatio float64
	// LightLuma is the luma (0..255) from which an edge pixel is light.
	LightLuma float64
	// LumaGap is how much brighter than the reference an edge pixel must be.
	LumaGap float64
}

func measureFringe(picture *image.NRGBA, probe FringeProbe) *FringeReport {
	width, height := picture.Bounds().Dx(), picture.Bounds().Dy()
	band := int(float64(max(width, height))*probe.BandRatio + 0.5)
	band = max(1, min(band, maxFringeBand))
	report := &FringeReport{BandPixels: band}
	distance := distanceToTransparency(picture, 4*band+1)

	outer := uint8(4 * band)
	inner := uint8(2 * band)
	tileWidth := (width + fringeTiles - 1) / fringeTiles
	tileHeight := (height + fringeTiles - 1) / fringeTiles
	var tileSum [fringeTiles * fringeTiles]float64
	var tileCount [fringeTiles * fringeTiles]int
	var totalSum float64
	var totalCount int
	for y := 0; y < height; y++ {
		row := picture.Pix[y*picture.Stride:]
		for x := 0; x < width; x++ {
			depth := distance[y*width+x]
			if depth <= inner || depth > outer {
				continue
			}
			luma := pixelLuma(row[4*x:])
			tile := (y/tileHeight)*fringeTiles + x/tileWidth
			tileSum[tile] += luma
			tileCount[tile]++
			totalSum += luma
			totalCount++
		}
	}
	if totalCount == 0 {
		return report
	}
	globalReference := totalSum / float64(totalCount)

	var edgeWeight, rimWeight float64
	for y := 0; y < height; y++ {
		row := picture.Pix[y*picture.Stride:]
		for x := 0; x < width; x++ {
			depth := distance[y*width+x]
			if depth < 1 || int(depth) > band {
				continue
			}
			weight := float64(row[4*x+3]) / 255
			report.EdgePixels++
			edgeWeight += weight
			tile := (y/tileHeight)*fringeTiles + x/tileWidth
			reference := globalReference
			if tileCount[tile] >= minTileReference {
				reference = tileSum[tile] / float64(tileCount[tile])
			}
			luma := pixelLuma(row[4*x:])
			if luma >= probe.LightLuma && luma-reference >= probe.LumaGap {
				rimWeight += weight
			}
		}
	}
	if report.EdgePixels == 0 || edgeWeight == 0 {
		return report
	}
	report.Measured = true
	report.FringeRatio = roundRatio(rimWeight / edgeWeight)
	return report
}

// pixelLuma is the Rec. 601 brightness of a straight (non-premultiplied)
// pixel, 0..255.
func pixelLuma(pixel []byte) float64 {
	return 0.299*float64(pixel[0]) + 0.587*float64(pixel[1]) + 0.114*float64(pixel[2])
}

// distanceToTransparency gives every subject pixel its 4-neighbour distance to
// the nearest pixel outside the subject, capped at limit; other pixels are 0.
// The picture's own border is not transparency: a subject that runs off the
// picture has no edge there.
func distanceToTransparency(picture *image.NRGBA, limit int) []uint8 {
	width, height := picture.Bounds().Dx(), picture.Bounds().Dy()
	capped := uint8(min(limit, 255))
	distance := make([]uint8, width*height)
	subject := func(x, y int) bool { return picture.Pix[y*picture.Stride+4*x+3] >= subjectAlpha }
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if !subject(x, y) {
				continue
			}
			best := capped
			if x > 0 {
				best = min(best, distance[y*width+x-1]+1)
			}
			if y > 0 {
				best = min(best, distance[(y-1)*width+x]+1)
			}
			distance[y*width+x] = best
		}
	}
	for y := height - 1; y >= 0; y-- {
		for x := width - 1; x >= 0; x-- {
			if !subject(x, y) {
				continue
			}
			best := distance[y*width+x]
			if x+1 < width {
				best = min(best, distance[y*width+x+1]+1)
			}
			if y+1 < height {
				best = min(best, distance[(y+1)*width+x]+1)
			}
			distance[y*width+x] = best
		}
	}
	return distance
}
