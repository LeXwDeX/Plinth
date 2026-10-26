package imaging

import (
	"image"
	"image/color"
	"testing"
)

func TestFitWithinPreservesAspectRatioWithoutEnlargement(t *testing.T) {
	for name, testCase := range map[string]struct {
		width      int
		height     int
		maximum    int
		wantWidth  int
		wantHeight int
	}{
		"landscape": {
			width: 1024, height: 512, maximum: 256,
			wantWidth: 256, wantHeight: 128,
		},
		"portrait": {
			width: 300, height: 900, maximum: 256,
			wantWidth: 85, wantHeight: 256,
		},
		"small": {
			width: 100, height: 50, maximum: 256,
			wantWidth: 100, wantHeight: 50,
		},
	} {
		t.Run(name, func(t *testing.T) {
			gotWidth, gotHeight := FitWithin(
				testCase.width,
				testCase.height,
				testCase.maximum,
			)
			if gotWidth != testCase.wantWidth ||
				gotHeight != testCase.wantHeight {
				t.Fatalf(
					"FitWithin = %dx%d, want %dx%d",
					gotWidth,
					gotHeight,
					testCase.wantWidth,
					testCase.wantHeight,
				)
			}
		})
	}
}

func TestResizeBilinearSamplesAllFourChannels(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	source.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	source.SetNRGBA(1, 1, color.NRGBA{
		R: 255, G: 255, B: 255, A: 255,
	})

	result := ResizeBilinear(source, 1, 1)
	got := color.NRGBAModel.Convert(result.At(0, 0)).(color.NRGBA)
	if got.R < 126 || got.R > 129 ||
		got.G < 126 || got.G > 129 ||
		got.B < 126 || got.B > 129 ||
		got.A != 255 {
		t.Fatalf("bilinear center = %#v", got)
	}
}
