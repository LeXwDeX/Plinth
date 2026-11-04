package modelinspect

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io"
	"sync"
	"testing"
)

// twoImageDocument embeds one 2x2 PNG per texture. The document has no
// geometry because these tests decode textures directly.
func twoImageDocument(t testing.TB, shared bool) *testGLB {
	t.Helper()
	b := newTestGLB()
	encode := func(pixel color.NRGBA) int {
		picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		for y := 0; y < 2; y++ {
			for x := 0; x < 2; x++ {
				picture.SetNRGBA(x, y, pixel)
			}
		}
		return b.addBufferView(encodePNG(t, picture), 0)
	}
	firstView := encode(color.NRGBA{R: 255, A: 255})
	secondView := firstView
	if !shared {
		secondView = encode(color.NRGBA{B: 255, A: 255})
	}
	firstImage := b.addImage(map[string]any{"bufferView": firstView, "mimeType": "image/png"})
	secondImage := firstImage
	if !shared {
		secondImage = b.addImage(map[string]any{"bufferView": secondView, "mimeType": "image/png"})
	}
	b.addTexture(map[string]any{"source": firstImage})
	b.addTexture(map[string]any{"source": secondImage})
	return b
}

// countingReaderAt counts reads per absolute offset so a test can prove one
// image payload was decoded once.
type countingReaderAt struct {
	reader io.ReaderAt
	mu     sync.Mutex
	calls  map[int64]int
}

func (c *countingReaderAt) ReadAt(data []byte, offset int64) (int, error) {
	c.mu.Lock()
	c.calls[offset]++
	c.mu.Unlock()
	return c.reader.ReadAt(data, offset)
}

func (c *countingReaderAt) count(offset int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[offset]
}

func TestDecodeTextureSharesOneDecodePerImage(t *testing.T) {
	raw := twoImageDocument(t, true).finish()
	model := mustParseModel(t, raw, DefaultLimits())
	view := model.bufferViews[model.Images[0].BufferView]
	counter := &countingReaderAt{reader: bytes.NewReader(raw), calls: map[int64]int{}}
	model.src = counter
	first, err := model.decodeTexture(0)
	if err != nil {
		t.Fatalf("decode first texture: %v", err)
	}
	second, err := model.decodeTexture(1)
	if err != nil {
		t.Fatalf("decode second texture: %v", err)
	}
	if first == nil || second == nil {
		t.Fatalf("textures decoded to nil: %v %v", first, second)
	}
	if len(model.textureCache.decoded) != 1 {
		t.Fatalf("decoded image cache entries = %d, want 1", len(model.textureCache.decoded))
	}
	if &first.pixels[0] != &second.pixels[0] {
		t.Fatal("textures sharing one image decoded separate pixel buffers")
	}
	if got := counter.count(view.offset); got != 1 {
		t.Fatalf("image payload read %d times, want 1", got)
	}
}

func TestDecodeTextureEnforcesTotalPixelBudget(t *testing.T) {
	model := mustParseModel(t, twoImageDocument(t, false).finish(), Limits{
		MaxTexturePixels:      16,
		MaxTotalTexturePixels: 4,
	})
	if _, err := model.decodeTexture(0); err != nil {
		t.Fatalf("first texture: %v", err)
	}
	_, err := model.decodeTexture(1)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("second texture error = %v, want ErrTooLarge", err)
	}
}

func TestDecodeTextureEnforcesTotalByteBudget(t *testing.T) {
	model := mustParseModel(t, twoImageDocument(t, false).finish(), Limits{
		MaxTexturePixels: 16,
		MaxTextureBytes:  16,
	})
	if _, err := model.decodeTexture(0); err != nil {
		t.Fatalf("first texture: %v", err)
	}
	_, err := model.decodeTexture(1)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("second texture error = %v, want ErrTooLarge", err)
	}
}

// A texture over the per-image limit still falls back to the material factor
// without charging the cumulative budget.
func TestDecodeTexturePerImageLimitDoesNotConsumeBudget(t *testing.T) {
	model := mustParseModel(t, twoImageDocument(t, false).finish(), Limits{
		MaxTexturePixels:      3,
		MaxTotalTexturePixels: 4,
		MaxTextureBytes:       16,
	})
	first, err := model.decodeTexture(0)
	if err != nil || first != nil {
		t.Fatalf("over-limit texture = %v, %v; want nil, nil", first, err)
	}
	if model.textureCache.pixels != 0 || model.textureCache.bytes != 0 {
		t.Fatalf("over-limit texture charged budget: pixels=%d bytes=%d",
			model.textureCache.pixels, model.textureCache.bytes)
	}
	// The fallback is cached per texture and per image.
	again, err := model.decodeTexture(0)
	if err != nil || again != nil {
		t.Fatalf("cached over-limit texture = %v, %v; want nil, nil", again, err)
	}
}
