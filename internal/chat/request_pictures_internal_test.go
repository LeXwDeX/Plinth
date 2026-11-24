package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents/inlinepictures"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func texturedPNG(t *testing.T, size int, seed uint32) string {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, size, size))
	state := seed
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			state = state*1664525 + 1013904223
			grain := uint8(state>>24) % 8
			picture.SetNRGBA(x, y, color.NRGBA{uint8(x*255/size) + grain, uint8(y*255/size) + grain, 128 + grain, 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

// #555: every picture the plan keeps is in the declared request format, and
// the vision byte budget counts the encoded bytes, not the original PNG's: a
// bound that three PNGs overflow holds the three JPEGs.
func TestVisionPlanCountsTheEncodedPictureBytes(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 3, 100, "high"),
	}, 10, 100000)
	originals := map[string]string{}
	var pngBytes int64
	for index, url := range []string{"/b1", "/b2", "/b3"} {
		originals[url] = texturedPNG(t, 256, uint32(index+1))
		pngBytes += int64(len(originals[url]))
	}
	encoder, err := inlinepictures.New(inlinepictures.Policy{
		Format: inlinepictures.FormatJPEG, Quality: 95,
		Background: color.RGBA{255, 255, 255, 255}, MaximumPixels: 1 << 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	var jpegBytes int64
	for _, original := range originals {
		jpegBytes += int64(len(encoder.URL(original)))
	}
	if jpegBytes*2 > pngBytes {
		t.Fatalf("JPEG %d bytes is not far below PNG %d bytes", jpegBytes, pngBytes)
	}
	schema.Context.Vision.MaximumBytes = (jpegBytes + pngBytes) / 2
	orchestrator := visionTestOrchestrator(schema, originals)
	orchestrator.pictures = encoder

	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{})

	if len(plan.Picks[3]) != 3 || len(plan.Dropped) != 0 {
		t.Fatalf("picks = %d dropped = %v (bound %d, jpeg %d, png %d)",
			len(plan.Picks[3]), plan.Dropped, schema.Context.Vision.MaximumBytes, jpegBytes, pngBytes)
	}
	for _, pick := range plan.Picks[3] {
		if !strings.HasPrefix(pick.URL, "data:image/jpeg;base64,") {
			t.Fatalf("a picked picture is not JPEG: %.30s", pick.URL)
		}
	}
	if plan.Spend.Bytes != jpegBytes {
		t.Fatalf("spend = %d, want the JPEG total %d", plan.Spend.Bytes, jpegBytes)
	}
	// The encodings were cached: the plan asked for each picture again.
	if encoder.CacheHits() < 3 {
		t.Fatalf("cache hits = %d", encoder.CacheHits())
	}
}

// A tool picture (show_picture, a render, a crop) and a picture already given
// as a data URI are encoded too.
func TestDataURIPicturesAreEncodedBeforeTheyAreSent(t *testing.T) {
	encoder, err := inlinepictures.New(inlinepictures.Policy{
		Format: inlinepictures.FormatJPEG, Quality: 95,
		Background: color.RGBA{255, 255, 255, 255}, MaximumPixels: 1 << 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	original := texturedPNG(t, 64, 9)
	orchestrator := &Orchestrator{pictures: encoder}
	url, ok := orchestrator.inlineVisionURL(context.Background(), "owner", "session", visionCandidate{url: original})
	if !ok || !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("inline data URI = %.30s, %v", url, ok)
	}
	orchestrator.visionInline = func(context.Context, generation.OwnerID, string) (string, bool) { return original, true }
	url, ok = orchestrator.inlineVisionURL(context.Background(), "owner", "session", visionCandidate{url: "/asset"})
	if !ok || !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("inlined asset = %.30s, %v", url, ok)
	}
}
