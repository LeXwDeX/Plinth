package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
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
	cache.put("a", &modelReviewEntry{fileBytes: 10})
	cache.put("b", &modelReviewEntry{fileBytes: 20})
	cache.put("c", &modelReviewEntry{fileBytes: 30})
	if cache.get("a") != nil {
		t.Fatal("the third model did not evict the least recently used one")
	}
	// Read b without touching c, so c stays the least recently used entry.
	if len(cache.entries) != 2 || cache.entries["c"] == nil || cache.get("b") == nil || cache.bytes != 50 {
		t.Fatalf("cache = %v bytes=%d", cache.entries, cache.bytes)
	}
	// Reading b made c the least recently used; adding d evicts c, not b.
	cache.put("d", &modelReviewEntry{fileBytes: 40})
	if cache.entries["c"] != nil || cache.get("b") == nil || cache.get("d") == nil || cache.bytes != 60 {
		t.Fatalf("after d: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
	// One file above the byte budget never enters the cache.
	cache.put("huge", &modelReviewEntry{fileBytes: 101})
	if cache.get("huge") != nil || cache.bytes != 60 {
		t.Fatalf("an oversized entry was cached: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
	// Replacing a key updates its bytes instead of counting them twice.
	cache.put("b", &modelReviewEntry{fileBytes: 25})
	if cache.get("b") == nil || cache.bytes != 65 {
		t.Fatalf("replaced entry: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
	// The byte budget can bind before the entry count: with room for two
	// entries but only 50 bytes, adding f evicts d and b to fit f alone.
	cache.maxBytes = 50
	cache.put("f", &modelReviewEntry{fileBytes: 30})
	if cache.get("b") != nil || cache.get("d") != nil || cache.get("f") == nil || cache.bytes != 30 {
		t.Fatalf("byte eviction: entries=%v bytes=%d", cache.entries, cache.bytes)
	}
}

// An entry is charged what it holds, and recharged when Inspect or Render
// grow it: growth evicts the least recently used entry, and an entry that
// alone outgrows the budget leaves the cache (#120 item 2).
func TestModelReviewCacheRechargesGrownEntries(t *testing.T) {
	cache := &modelReviewCache{maxModels: 2, maxBytes: 100, entries: map[string]*modelReviewEntry{}}
	a := newModelReviewEntry("a", nil, 10)
	b := newModelReviewEntry("b", nil, 20)
	cache.put("a", a)
	cache.put("b", b)
	if cache.bytes != 30 {
		t.Fatalf("bytes = %d, want 30", cache.bytes)
	}
	a.reportBytes.Store(60)
	cache.refresh(a)
	if cache.bytes != 90 || cache.get("a") == nil || cache.get("b") == nil {
		t.Fatalf("after a grew: bytes=%d entries=%v", cache.bytes, cache.entries)
	}
	// Reading a and b above made a the least recently used entry again.
	b.reportBytes.Store(50)
	cache.refresh(b)
	if cache.entries["a"] != nil || cache.bytes != 70 {
		t.Fatalf("after b grew: bytes=%d entries=%v, want a evicted", cache.bytes, cache.entries)
	}
	b.reportBytes.Store(200)
	cache.refresh(b)
	if len(cache.entries) != 0 || cache.bytes != 0 || len(cache.order) != 0 {
		t.Fatalf("an entry above the budget stayed cached: bytes=%d entries=%v", cache.bytes, cache.entries)
	}
	// Recharging an entry the cache no longer holds changes nothing.
	cache.refresh(a)
	if cache.bytes != 0 {
		t.Fatalf("refreshing an evicted entry charged %d bytes", cache.bytes)
	}
}

// Concurrent first loads of one key share a single load: the second caller
// gets the running load and, once it ends, its entry or parse error. A load
// that ended with neither lets the next caller start a new one.
func TestModelReviewCacheSharesConcurrentFirstLoads(t *testing.T) {
	cache := &modelReviewCache{maxModels: 2, maxBytes: 100, entries: map[string]*modelReviewEntry{}}
	entry, running, started := cache.begin("k")
	if entry != nil || running != nil || started == nil {
		t.Fatal("the first caller did not start the load")
	}
	_, running, second := cache.begin("k")
	if running != started || second != nil {
		t.Fatal("a concurrent caller started a second load")
	}
	parsed := newModelReviewEntry("k", nil, 10)
	cache.finish("k", started, parsed, nil)
	select {
	case <-running.done:
	default:
		t.Fatal("finishing the load did not release its waiters")
	}
	if running.entry != parsed || cache.get("k") != parsed {
		t.Fatal("the waiter or the cache did not receive the parsed entry")
	}
	if entry, _, _ := cache.begin("k"); entry != parsed {
		t.Fatal("a later caller did not get the cached entry")
	}

	_, _, failed := cache.begin("unreadable")
	_, waiting, _ := cache.begin("unreadable")
	cache.finish("unreadable", failed, nil, nil)
	<-waiting.done
	if waiting.entry != nil || waiting.parseErr != nil {
		t.Fatal("a read failure was shared as a result")
	}
	if _, _, retry := cache.begin("unreadable"); retry == nil {
		t.Fatal("after a failed read the next caller could not start a new load")
	}
}

// Inspection holds one of the state's slots, so it waits behind renders and
// gives up with its context; a cancelled inspection stores no report, and
// the next call computes it (#120 item 2).
func TestModelReviewInspectWaitsForASlotAndHonoursItsContext(t *testing.T) {
	state := newModelReviewState(catalog.ChatModelReviewSchema{
		Render: catalog.ChatModelRenderSchema{MaxConcurrentRenders: 1},
		Cache:  catalog.ChatModelReviewCacheSchema{MaxModels: 1, MaxBytes: 1 << 20},
	})
	entry := newModelReviewEntry("k", nil, 0)
	if err := state.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := entry.inspect(waiting, state, modelinspect.InspectOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("inspect without a free slot: err = %v", err)
	}
	if entry.report != nil {
		t.Fatal("an inspection that never ran stored a report")
	}
	state.release()
	report, err := entry.inspect(context.Background(), state, modelinspect.InspectOptions{})
	if err != nil || len(report.Warnings) != 1 || report.Warnings[0] != "nil_model" {
		t.Fatalf("inspect with a free slot = %+v, %v", report, err)
	}
	if entry.reportBytes.Load() <= 0 {
		t.Fatal("the stored report is not charged to the entry")
	}
	// A caller waiting for another call's inspection also gives up with its
	// own context.
	entry.inspecting <- struct{}{}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := entry.inspect(cancelled, state, modelinspect.InspectOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("inspect while another call holds the entry: err = %v", err)
	}
	<-entry.inspecting
	if len(state.slots) != 0 {
		t.Fatalf("%d slots still held", len(state.slots))
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
