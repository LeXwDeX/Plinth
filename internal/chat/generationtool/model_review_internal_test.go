package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// TestModelReviewStateBoundsConcurrentRenders holds every slot the Schema
// declares and shows that a waiting render gives up with its context.
// TestModelReviewLimitsCarryTextureBudgets proves the Schema's cumulative
// texture caps reach the parser and renderer; leaving them unmapped would keep
// the per-image cache unbounded in production (#103 review P2).
func TestModelReviewLimitsCarryTextureBudgets(t *testing.T) {
	limits := modelReviewLimits(catalog.ChatModelReviewSchema{
		Inspect: catalog.ChatModelInspectSchema{Limits: catalog.ChatModelReviewLimits{
			MaxTexturePixels: 100, MaxTotalTexturePixels: 200, MaxTextureBytes: 300,
		}},
	})
	if limits.MaxTexturePixels != 100 || limits.MaxTotalTexturePixels != 200 || limits.MaxTextureBytes != 300 {
		t.Fatalf("mapped limits = %+v", limits)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared := product.Application().Chat.ModelReview.Inspect.Limits
	if declared.MaxTotalTexturePixels <= 0 || declared.MaxTextureBytes <= 0 {
		t.Fatalf("default Catalog declares no cumulative texture budget: %+v", declared)
	}
}

// TestModelReviewRenderCallsCountOnlySuccessfulCalls keeps failed or refused
// renders and comparisons from spending the reply's render slots: a summary
// with no views is not a rendered call, while a summary-less record keeps the
// old counting (it may predate summaries) (#103 review C7).
func TestModelReviewRenderCallsCountOnlySuccessfulCalls(t *testing.T) {
	executor := &Executor{chat: catalog.ChatSchema{AgentTools: []catalog.ChatAgentToolSchema{
		{Name: "render_model_views", Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "model.render"}},
		{Name: "compare_models", Execution: catalog.ChatToolExecutionSchema{ImplementationKey: "model.compare"}},
	}}}
	command := chat.ToolCommand{EarlierCalls: []chat.ToolCall{
		{Name: "render_model_views", Summary: json.RawMessage(`{"tool":"render","views":["front"]}`)},
		{Name: "render_model_views", Summary: json.RawMessage(`{"tool":"render","failed_views":["front"]}`)},
		// A refusal has no summary; the record cannot say whether it predates
		// summaries, so it keeps counting as before.
		{Name: "render_model_views"},
		{Name: "inspect_model", Summary: json.RawMessage(`{"tool":"inspect"}`)},
		{Name: "compare_models", Summary: json.RawMessage(`{"tool":"compare","views":["front"]}`)},
		{Name: "compare_models", Summary: json.RawMessage(`{"tool":"compare","failed_views":["front"]}`)},
	}}
	if used := executor.modelReviewRenderCalls(command); used != 3 {
		t.Fatalf("render calls = %d, want 3 (successful render, summary-less record, successful compare)", used)
	}
}

func TestModelReviewStateBoundsConcurrentRenders(t *testing.T) {
	state := newModelReviewState(catalog.ChatModelReviewSchema{
		Render: catalog.ChatModelRenderSchema{MaxConcurrentRenders: 2},
	})
	ctx := context.Background()
	if err := state.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := state.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := state.acquire(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("a third render obtained a slot, err = %v", err)
	}
	state.release()
	if err := state.acquire(ctx); err != nil {
		t.Fatalf("a freed slot was not reusable: %v", err)
	}
	state.release()
	state.release()
}

func TestModelReviewCacheEvictsByModelCountAndBytes(t *testing.T) {
	cache := &modelReviewCache{maxModels: 2, maxBytes: 100, entries: map[string]*modelReviewEntry{}}
	cache.put("a", &modelReviewEntry{size: 10})
	cache.put("b", &modelReviewEntry{size: 20})
	cache.put("c", &modelReviewEntry{size: 30})
	if cache.get("a") != nil {
		t.Fatal("the third model did not evict the least recently used one")
	}
	// Read b without touching c, so c stays the least recently used entry.
	if len(cache.entries) != 2 || cache.entries["c"] == nil || cache.get("b") == nil || cache.bytes != 50 {
		t.Fatalf("cache = %v bytes=%d", cache.entries, cache.bytes)
	}
	// Reading b made c the least recently used; adding d evicts c, not b.
	cache.put("d", &modelReviewEntry{size: 40})
	if cache.entries["c"] != nil || cache.get("b") == nil || cache.get("d") == nil || cache.bytes != 60 {
		t.Fatalf("after d: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
	// One file above the byte budget never enters the cache.
	cache.put("huge", &modelReviewEntry{size: 101})
	if cache.get("huge") != nil || cache.bytes != 60 {
		t.Fatalf("an oversized entry was cached: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
	// Replacing a key updates its bytes instead of counting them twice.
	cache.put("b", &modelReviewEntry{size: 25})
	if cache.get("b") == nil || cache.bytes != 65 {
		t.Fatalf("replaced entry: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
	// The byte budget can bind before the entry count: with room for two
	// entries but only 50 bytes, adding f evicts d and b to fit f alone.
	cache.maxBytes = 50
	cache.put("f", &modelReviewEntry{size: 30})
	if cache.get("b") != nil || cache.get("d") != nil || cache.get("f") == nil || cache.bytes != 30 {
		t.Fatalf("byte eviction: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
}

func TestModelReviewDownsampleAveragesEachBlock(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	// Each 2x2 block averages to one of the four expected colours below.
	rows := [4][4]color.NRGBA{
		{{A: 255}, {R: 20, A: 255}, {G: 40, A: 255}, {R: 20, G: 40, A: 255}},
		{{A: 255}, {R: 20, A: 255}, {G: 40, A: 255}, {R: 20, G: 40, A: 255}},
		{{R: 100, G: 100, B: 100, A: 255}, {R: 120, G: 100, B: 100, A: 255},
			{R: 100, G: 140, B: 100, A: 255}, {R: 120, G: 140, B: 100, A: 255}},
		{{R: 100, G: 100, B: 100, A: 255}, {R: 120, G: 100, B: 100, A: 255},
			{R: 100, G: 140, B: 100, A: 255}, {R: 120, G: 140, B: 100, A: 255}},
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			source.SetNRGBA(x, y, rows[y][x])
		}
	}
	output := modelReviewDownsample(source, 2)
	if output.Bounds().Dx() != 2 || output.Bounds().Dy() != 2 {
		t.Fatalf("output = %v", output.Bounds())
	}
	want := [2][2]color.NRGBA{
		{{R: 10, A: 255}, {R: 10, G: 40, A: 255}},
		{{R: 110, G: 100, B: 100, A: 255}, {R: 110, G: 140, B: 100, A: 255}},
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			if got := output.NRGBAAt(x, y); got != want[y][x] {
				t.Fatalf("block (%d,%d) = %v, want %v", x, y, got, want[y][x])
			}
		}
	}
}

func TestModelReviewSideBySideKeepsBothPicturesAndTheGap(t *testing.T) {
	left := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	right := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			left.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
			right.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
		}
	}
	output := modelReviewSideBySide(left, right)
	if output.Bounds().Dx() != 4+modelReviewCompareGap+4 || output.Bounds().Dy() != 4 {
		t.Fatalf("output = %v", output.Bounds())
	}
	if output.NRGBAAt(0, 0) != (color.NRGBA{R: 255, A: 255}) ||
		output.NRGBAAt(4+modelReviewCompareGap, 0) != (color.NRGBA{G: 255, A: 255}) {
		t.Fatalf("pictures moved: left=%v right=%v", output.NRGBAAt(0, 0), output.NRGBAAt(4+modelReviewCompareGap, 0))
	}
	if output.NRGBAAt(4, 0) != (color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff}) {
		t.Fatalf("gap = %v", output.NRGBAAt(4, 0))
	}
}
