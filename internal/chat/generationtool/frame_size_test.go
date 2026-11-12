package generationtool

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #283: a three-view frame of a given shape gets the legal size nearest to it
// on every image model, at the longer side of the model's default.
func TestFrameOutputSizesFollowTheShapeAtTheDefaultLongSide(t *testing.T) {
	pixels := pixelSizeOffering()
	ratios := ratioOffering()
	for _, test := range []struct {
		name     string
		offering generation.Offering
		ratio    float64
		want     map[string]string
	}{
		// The pixel model takes custom sizes on a 16-pixel grid; the longer side
		// of its default (2048) is kept.
		{"pixel model, a portrait the grid holds exactly", pixels, 1152.0 / 2048, map[string]string{"size": "1152x2048"}},
		{"pixel model, a portrait the grid only approaches", pixels, 0.5688, map[string]string{"size": "1168x2048"}},
		{"pixel model, square", pixels, 1, map[string]string{"size": "2048x2048"}},
		{"pixel model, wide", pixels, 1.4, map[string]string{"size": "2048x1456"}},
		{"ratio model, a tall character", ratios, 0.57, map[string]string{"aspect_ratio": "9:16"}},
		{"ratio model, a squarish character", ratios, 0.72, map[string]string{"aspect_ratio": "3:4"}},
		{"ratio model, a wide animal", ratios, 1.4, map[string]string{"aspect_ratio": "4:3"}},
		{"ratio model, square", ratios, 1, map[string]string{"aspect_ratio": "1:1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := FrameOutputSizes(test.offering, test.ratio)
			if len(got) != len(test.want) {
				t.Fatalf("sizes = %v, want %v", got, test.want)
			}
			for name, value := range test.want {
				if got[name] != value {
					t.Fatalf("%s = %q, want %q (all: %v)", name, got[name], value, got)
				}
			}
		})
	}
}

func TestFrameOutputSizesAreAlwaysLegal(t *testing.T) {
	pixels := pixelSizeOffering()
	constraint := pixels.Parameters[1].ImageSize
	for ratio := 0.34; ratio < 2.9; ratio += 0.01 {
		value := FrameOutputSizes(pixels, ratio)["size"]
		size, ok := parseSizeValue(value)
		if !ok || size.Width == 0 {
			t.Fatalf("ratio %.2f gave %q", ratio, value)
		}
		if err := constraint.Check(size.Width, size.Height); err != nil {
			t.Fatalf("ratio %.2f gave an illegal size %s: %v", ratio, value, err)
		}
		if math.Abs(math.Log(size.Ratio/ratio)) > 0.04 {
			t.Fatalf("ratio %.2f gave %s, whose shape %.3f is not close", ratio, value, size.Ratio)
		}
	}
}

func TestFrameOutputSizesLeaveWhatThePersonCannotChoose(t *testing.T) {
	fixed := ratioOffering()
	fixed.Parameters[1].Fixed = true
	if got := FrameOutputSizes(fixed, 0.57); len(got) != 0 {
		t.Fatalf("a fixed output size was changed: %v", got)
	}
	if got := FrameOutputSizes(generation.Offering{Parameters: []generation.Parameter{
		{Name: "prompt", Type: "text", Semantic: generation.SemanticPrompt}}}, 0.57); len(got) != 0 {
		t.Fatalf("an Offering without an output size got %v", got)
	}
	for _, ratio := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if got := FrameOutputSizes(pixelSizeOffering(), ratio); len(got) != 0 {
			t.Fatalf("ratio %v gave %v", ratio, got)
		}
	}
	// A parameter that declares its default as a bare ratio has no pixels to keep.
	tiered := ratioOffering()
	tiered.Parameters[1].Default = json.RawMessage(`"1:1"`)
	if got := FrameOutputSizes(tiered, 0.57); got["aspect_ratio"] != "9:16" {
		t.Fatalf("ratio-only default: %v", got)
	}
}
