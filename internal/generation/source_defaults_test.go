package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

const mattingOffering generation.OfferingID = "matting/default/background-removal"

// pictureOf is a real PNG whose header gives the size; the pixels are empty so
// a large one stays small.
func pictureOf(t *testing.T, width, height int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

type sourceSideFixture struct {
	h       *harness
	session string
}

func newSourceSideFixture(t *testing.T) *sourceSideFixture {
	t.Helper()
	h := newHarness(t, mock.New(), nil)
	ctx := context.Background()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a",
		OwnerName: "Owner", Title: "matting", CreatedAt: h.clock.Now(), UpdatedAt: h.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	return &sourceSideFixture{h: h, session: "session-a"}
}

// upload stores the bytes as an input picture of owner-a.
func (f *sourceSideFixture) upload(t *testing.T, data []byte) generation.AssetID {
	t.Helper()
	asset, err := f.h.inputs.Upload(context.Background(), "owner-a", bytes.NewReader(data), "image/png", 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	return asset.ID
}

// prepare freezes a background removal of the pictures with the extra
// parameters and returns the frozen input's two process resolutions.
func (f *sourceSideFixture) prepare(t *testing.T, sources []generation.AssetID, extra map[string]any) (generation.PreparedInput, map[string]any) {
	t.Helper()
	input := map[string]any{"source_assets": sources}
	for name, value := range extra {
		input[name] = value
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := f.h.module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: f.session, Capability: generation.BackgroundRemove,
		OfferingID: mattingOffering, Input: encoded,
	})
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]any
	if err := json.Unmarshal(prepared.NormalizedInput, &frozen); err != nil {
		t.Fatal(err)
	}
	return prepared, frozen
}

// #284: a background removal's two working resolutions follow the long side of
// the pictures it is given. The Schema declares the tiers; the kernel measures
// the stored pictures and applies them to the parameters the caller left out.
func TestSourceSideDefaultsFollowThePictureSize(t *testing.T) {
	for _, test := range []struct {
		name     string
		sizes    [][2]int
		extra    map[string]any
		birefnet float64
		sdmatte  float64
	}{
		{"a small picture keeps the working size", [][2]int{{800, 600}}, nil, 1024, 1024},
		{"a long side of exactly 1024 is still small", [][2]int{{1024, 1024}}, nil, 1024, 1024},
		{"one pixel over takes the next tier", [][2]int{{1025, 10}}, nil, 2048, 2048},
		{"the long side counts, not the width", [][2]int{{16, 3000}}, nil, 2048, 2048},
		{"a very large picture takes the open-ended tier", [][2]int{{4096, 64}}, nil, 2048, 2048},
		{"a batch is sized by its largest picture", [][2]int{{800, 600}, {2000, 1500}, {640, 480}}, nil, 2048, 2048},
		{"a batch of small pictures stays small", [][2]int{{800, 600}, {1000, 700}}, nil, 1024, 1024},
		{"a value the caller gave wins; the other follows the picture",
			[][2]int{{2000, 1500}}, map[string]any{"birefnet_process_res": 1536}, 1536, 2048},
		{"a caller value equal to the static default still wins",
			[][2]int{{2000, 1500}}, map[string]any{"sdmatte_process_res": 1024}, 2048, 1024},
		{"both given by the caller are kept as given",
			[][2]int{{2000, 1500}}, map[string]any{"birefnet_process_res": 4096, "sdmatte_process_res": 512}, 4096, 512},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSourceSideFixture(t)
			var sources []generation.AssetID
			for _, size := range test.sizes {
				sources = append(sources, f.upload(t, pictureOf(t, size[0], size[1])))
			}
			_, frozen := f.prepare(t, sources, test.extra)
			if frozen["birefnet_process_res"] != test.birefnet || frozen["sdmatte_process_res"] != test.sdmatte {
				t.Fatalf("frozen resolutions = %v / %v, want %v / %v",
					frozen["birefnet_process_res"], frozen["sdmatte_process_res"], test.birefnet, test.sdmatte)
			}
		})
	}
}

// Pictures the kernel cannot measure leave the static default, which is the
// behaviour before the rule existed; so does one unreadable picture in a batch
// of otherwise large ones (it must not let the others choose a size it was not
// measured for).
func TestSourceSideDefaultsLeaveTheStaticDefaultWhenAPictureCannotBeMeasured(t *testing.T) {
	f := newSourceSideFixture(t)
	unreadable := f.upload(t, []byte("not a picture"))
	large := f.upload(t, pictureOf(t, 3000, 2000))
	for name, sources := range map[string][]generation.AssetID{
		"only an unreadable picture":   {unreadable},
		"unreadable among large ones":  {large, unreadable},
		"unreadable in front of large": {unreadable, large},
	} {
		_, frozen := f.prepare(t, sources, nil)
		if frozen["birefnet_process_res"] != float64(1024) || frozen["sdmatte_process_res"] != float64(1024) {
			t.Fatalf("%s: frozen resolutions = %v / %v, want the static 1024", name,
				frozen["birefnet_process_res"], frozen["sdmatte_process_res"])
		}
	}
}

// The value the rule chose is frozen with the prepared input: submitting it
// freezes the same value on the Task, and a Task never re-measures.
func TestSourceSideDefaultsAreFrozenWithThePreparedInput(t *testing.T) {
	f := newSourceSideFixture(t)
	ctx := context.Background()
	source := f.upload(t, pictureOf(t, 3000, 2000))
	prepared, frozen := f.prepare(t, []generation.AssetID{source}, nil)
	if frozen["birefnet_process_res"] != float64(2048) || frozen["sdmatte_process_res"] != float64(2048) {
		t.Fatalf("frozen = %v", frozen)
	}
	accepted, err := f.h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, f.h.repository, prepared, "source-side-prepared"))
	if err != nil {
		t.Fatalf("a prepared input with a derived default is refused at submit: %v", err)
	}
	task, err := f.h.module.Get(ctx, "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var taskInput map[string]any
	if err := json.Unmarshal(task.Input, &taskInput); err != nil {
		t.Fatal(err)
	}
	if taskInput["birefnet_process_res"] != float64(2048) || taskInput["sdmatte_process_res"] != float64(2048) {
		t.Fatalf("task input = %s", task.Input)
	}
}

// An Offering that declares no source-side rule keeps its input exactly as
// Normalize gave it, whatever size its pictures have.
func TestSourceSideDefaultsLeaveOtherOfferingsAlone(t *testing.T) {
	f := newSourceSideFixture(t)
	source := f.upload(t, pictureOf(t, 3000, 2000))
	input, _ := json.Marshal(map[string]any{"prompt": "fix the door", "reference_assets": []generation.AssetID{source}})
	prepared, err := f.h.module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: f.session, Capability: generation.ImageToImage,
		OfferingID: "openai/gpt-image-2/image-to-image", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]any
	if err := json.Unmarshal(prepared.NormalizedInput, &frozen); err != nil {
		t.Fatal(err)
	}
	if _, derived := frozen["birefnet_process_res"]; derived {
		t.Fatalf("frozen = %v", frozen)
	}
}
