package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type memoryInputs map[generation.AssetID][]byte

func (inputs memoryInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (inputs memoryInputs) Open(_ context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(inputs[asset.ID])), nil
}

func encodeTestPNG(t *testing.T, canvas image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func completeWithInputs(t *testing.T, adapter *Adapter, submission string, input any, assets map[string][]generation.InputAsset) []deliveredFile {
	t.Helper()
	payload, _ := json.Marshal(input)
	offering := imageOffering("image-to-image")
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task-" + generation.TaskID(submission), AttemptID: "attempt-" + generation.AttemptID(submission),
		SubmissionKey: submission, Offering: offering, Input: payload, InputAssets: assets,
	})
	if err != nil {
		t.Fatal(err)
	}
	observe := generation.ObserveRequest{TaskID: "task-" + generation.TaskID(submission), AttemptID: "attempt-" + generation.AttemptID(submission), Offering: offering, Job: *started.Job, Input: payload}
	var observation generation.Observation
	for range 4 {
		if observation, err = adapter.Observe(context.Background(), observe); err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			break
		}
	}
	return open(t, adapter, offering, observe, observation.Artifacts)
}

func TestMaskedEditChangesOnlyTheMaskedRegion(t *testing.T) {
	reference := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	mask := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			reference.SetNRGBA(x, y, color.NRGBA{uint8(x / 2), uint8(y / 2), 90, 255})
			mask.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 255})
		}
	}
	for y := 100; y < 180; y++ {
		for x := 100; x < 220; x++ {
			mask.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 0})
		}
	}
	inputs := memoryInputs{"ref": encodeTestPNG(t, reference), "mask": encodeTestPNG(t, mask)}
	adapter := NewWithOptions(Options{Inputs: inputs})
	delivered := completeWithInputs(t, adapter, "inpaint", map[string]any{"prompt": "改成金色花纹", "size": "1024x1536"}, map[string][]generation.InputAsset{
		"reference_assets": {{ID: "ref"}}, "mask": {{ID: "mask"}},
	})
	if len(delivered) != 1 {
		t.Fatalf("delivered %d files", len(delivered))
	}
	result, err := png.Decode(bytes.NewReader(delivered[0].content))
	if err != nil {
		t.Fatal(err)
	}
	if result.Bounds().Dx() != 400 || result.Bounds().Dy() != 300 {
		t.Fatalf("an inpaint keeps the reference size, got %v", result.Bounds())
	}
	changed, outside := 0, 0
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			same := color.NRGBAModel.Convert(result.At(x, y)) == color.NRGBAModel.Convert(reference.At(x, y))
			inside := x >= 100 && x < 220 && y >= 100 && y < 180
			switch {
			case inside && !same:
				changed++
			case !inside && !same:
				outside++
			}
		}
	}
	if outside != 0 {
		t.Fatalf("%d pixels outside the mask changed", outside)
	}
	if changed < 120*80*8/10 {
		t.Fatalf("only %d of %d masked pixels changed", changed, 120*80)
	}
}

func TestPlainEditKeepsTheCompositionOfASameRatioReference(t *testing.T) {
	reference := image.NewNRGBA(image.Rect(0, 0, 200, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 200; x++ {
			tone := color.NRGBA{200, 40, 40, 255}
			if x > 100 {
				tone = color.NRGBA{40, 40, 200, 255}
			}
			reference.SetNRGBA(x, y, tone)
		}
	}
	adapter := NewWithOptions(Options{Inputs: memoryInputs{"ref": encodeTestPNG(t, reference)}})
	delivered := completeWithInputs(t, adapter, "recolor", map[string]any{"prompt": "换成红黑配色", "size": "1024x1536"}, map[string][]generation.InputAsset{"reference_assets": {{ID: "ref"}}})
	width, height := decodeSize(t, delivered[0].content)
	if float64(width)/float64(height) < 0.66 || float64(width)/float64(height) > 0.67 {
		t.Fatalf("size %dx%d is not 2:3", width, height)
	}
	result, _ := png.Decode(bytes.NewReader(delivered[0].content))
	left := color.NRGBAModel.Convert(result.At(width/4, height/2)).(color.NRGBA)
	right := color.NRGBAModel.Convert(result.At(width*3/4, height/2)).(color.NRGBA)
	if left == right {
		t.Fatalf("the two halves of the reference collapsed into one colour: %v", left)
	}
	if left.R == 200 && left.G == 40 && left.B == 40 {
		t.Fatal("the reference colours were not changed")
	}
}

func TestSketchOfAnotherRatioFallsBackToAFreshImage(t *testing.T) {
	sketch := image.NewNRGBA(image.Rect(0, 0, 640, 360))
	adapter := NewWithOptions(Options{Inputs: memoryInputs{"ref": encodeTestPNG(t, sketch)}})
	delivered := completeWithInputs(t, adapter, "sketch", map[string]any{"prompt": "按草图重画", "size": "1024x1536"}, map[string][]generation.InputAsset{"reference_assets": {{ID: "ref"}}})
	if width, height := decodeSize(t, delivered[0].content); width != 1024 || height != 1536 {
		t.Fatalf("got %dx%d, want the requested 1024x1536", width, height)
	}
}

func TestWhiteSketchIsNotRecolouredAsABase(t *testing.T) {
	sketch := image.NewNRGBA(image.Rect(0, 0, 640, 360))
	for index := range sketch.Pix {
		sketch.Pix[index] = 255
	}
	for x := 0; x < 640; x++ {
		sketch.SetNRGBA(x, 240, color.NRGBA{20, 20, 20, 255})
	}
	adapter := NewWithOptions(Options{Inputs: memoryInputs{"ref": encodeTestPNG(t, sketch)}})
	delivered := completeWithInputs(t, adapter, "sketch-16-9", map[string]any{"prompt": "按草图的构图重画", "size": "2048x1152"}, map[string][]generation.InputAsset{"reference_assets": {{ID: "ref"}}})
	result, _ := png.Decode(bytes.NewReader(delivered[0].content))
	pale := 0
	for x := 0; x < result.Bounds().Dx(); x += 16 {
		pixel := color.NRGBAModel.Convert(result.At(x, 40)).(color.NRGBA)
		if pixel.R > 230 && pixel.G > 230 && pixel.B > 230 {
			pale++
		}
	}
	if pale > result.Bounds().Dx()/16/2 {
		t.Fatal("the result is still the white sketch page")
	}
}

func TestBackgroundRemovalKeepsTheSubjectOfTheReference(t *testing.T) {
	var promptHash, seed [32]byte
	promptHash[0], seed[0] = 40, 7
	source, err := renderImagePNG(imageSpec{Width: 768, Height: 1152, Seed: seed, PromptHash: promptHash, Label: "T2I 1/1 #abc 768x1152"})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewWithOptions(Options{Inputs: memoryInputs{"ref": source}})
	offering := imageOffering("background-removal")
	payload, _ := json.Marshal(map[string]any{"prompt": "去背景"})
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task-cutout", AttemptID: "attempt-cutout", SubmissionKey: "cutout", Offering: offering, Input: payload,
		InputAssets: map[string][]generation.InputAsset{"source_assets": {{ID: "ref"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observe := generation.ObserveRequest{TaskID: "task-cutout", AttemptID: "attempt-cutout", Offering: offering, Job: *started.Job, Input: payload}
	var observation generation.Observation
	for range 4 {
		if observation, err = adapter.Observe(context.Background(), observe); err != nil {
			t.Fatal(err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			break
		}
	}
	delivered := open(t, adapter, offering, observe, observation.Artifacts)
	result, err := png.Decode(bytes.NewReader(delivered[0].content))
	if err != nil {
		t.Fatal(err)
	}
	transparent, opaque := 0, 0
	for y := 0; y < 1152; y += 8 {
		for x := 0; x < 768; x += 8 {
			if _, _, _, alpha := result.At(x, y).RGBA(); alpha == 0 {
				transparent++
			} else {
				opaque++
			}
		}
	}
	total := transparent + opaque
	if transparent*100/total < 40 || opaque*100/total < 8 {
		t.Fatalf("transparent %d%%, opaque %d%%: the cutout lost the background or the subject", transparent*100/total, opaque*100/total)
	}
	if _, _, _, alpha := result.At(20, 600).RGBA(); alpha != 0 {
		t.Fatal("the left edge of the picture is still background")
	}
}

func TestDominantHueFollowsTheTorsoColour(t *testing.T) {
	reference := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for index := 0; index < len(reference.Pix); index += 4 {
		reference.Pix[index], reference.Pix[index+1], reference.Pix[index+2], reference.Pix[index+3] = 20, 200, 30, 255
	}
	hue, ok := dominantHue(reference)
	if !ok || hue < 100 || hue > 140 {
		t.Fatalf("hue %v ok=%v, want green", hue, ok)
	}
	for index := 0; index < len(reference.Pix); index += 4 {
		reference.Pix[index], reference.Pix[index+1], reference.Pix[index+2] = 128, 128, 128
	}
	if _, ok := dominantHue(reference); ok {
		t.Fatal("a grey torso must fall back to the hash colour")
	}
}
