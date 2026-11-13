package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// The shipped background removal declares no source-side rule (#299); the
// test-local copy of it does. The rule is generic Schema and kernel machinery,
// so its tests run on an Offering of their own, not on a shipped one.
const (
	shippedMattingOffering generation.OfferingID = "matting/default/background-removal"
	sourceSideOffering     generation.OfferingID = catalogtest.SourceSideOfferingID
)

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
	h        *harness
	session  string
	offering generation.OfferingID
}

// newSourceSideFixture is a harness whose Catalog also holds the test-local
// Offering that declares default_by_source_side on birefnet_process_res (1024 up
// to a long side of 1024, 2048 above); its sdmatte_process_res declares none.
func newSourceSideFixture(t *testing.T) *sourceSideFixture {
	t.Helper()
	product := catalogtest.LoadWithSourceSideOffering(t)
	return newSourceSideFixtureOn(t, sourceSideOffering, func(dependencies *generation.Dependencies) {
		dependencies.Catalog = product
	})
}

// newShippedFixture is a harness on the shipped Catalog.
func newShippedFixture(t *testing.T) *sourceSideFixture {
	t.Helper()
	return newSourceSideFixtureOn(t, shippedMattingOffering, nil)
}

func newSourceSideFixtureOn(
	t *testing.T,
	offering generation.OfferingID,
	configure func(*generation.Dependencies),
) *sourceSideFixture {
	t.Helper()
	h := newConfiguredHarness(t, mock.New(), nil, configure)
	ctx := context.Background()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a",
		OwnerName: "Owner", Title: "matting", CreatedAt: h.clock.Now(), UpdatedAt: h.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	return &sourceSideFixture{h: h, session: "session-a", offering: offering}
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
		OfferingID: f.offering, Input: encoded,
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

// resolutionsCase is one request for a background removal: the sizes of its
// pictures, the parameters the caller gives, and the two process resolutions the
// frozen input is expected to hold.
type resolutionsCase struct {
	name     string
	sizes    [][2]int
	extra    map[string]any
	birefnet float64
	sdmatte  float64
}

// run freezes the case on the fixture and checks the two resolutions.
func (test resolutionsCase) run(t *testing.T, f *sourceSideFixture) {
	t.Helper()
	var sources []generation.AssetID
	for _, size := range test.sizes {
		sources = append(sources, f.upload(t, pictureOf(t, size[0], size[1])))
	}
	_, frozen := f.prepare(t, sources, test.extra)
	if frozen["birefnet_process_res"] != test.birefnet || frozen["sdmatte_process_res"] != test.sdmatte {
		t.Fatalf("frozen resolutions = %v / %v, want %v / %v",
			frozen["birefnet_process_res"], frozen["sdmatte_process_res"], test.birefnet, test.sdmatte)
	}
}

// #284: a parameter that declares default_by_source_side takes, when the caller
// left it out, the value of the tier the long side of the pictures falls in; a
// parameter that declares none keeps its static default whatever the pictures.
// The Offering here is the test-local one, whose birefnet_process_res declares
// the rule (1024 up to a long side of 1024, 2048 above) and whose
// sdmatte_process_res does not. The kernel measures the stored pictures and
// applies the tiers to the parameters the caller left out, so a value the caller
// gives is always the caller's.
func TestSourceSideDefaultsFollowThePictureSize(t *testing.T) {
	for _, test := range []resolutionsCase{
		{"a small picture keeps the static size", [][2]int{{800, 600}}, nil, 1024, 1024},
		{"a long side of exactly 1024 is still the first tier", [][2]int{{1024, 1024}}, nil, 1024, 1024},
		{"one pixel over takes the next tier", [][2]int{{1025, 10}}, nil, 2048, 1024},
		{"the long side counts, not the width", [][2]int{{16, 3000}}, nil, 2048, 1024},
		{"a very large picture takes the open-ended tier", [][2]int{{4096, 64}}, nil, 2048, 1024},
		{"a 2048 picture: only the parameter with the rule leaves its static default", [][2]int{{2048, 2048}}, nil, 2048, 1024},
		{"a batch is sized by its largest picture", [][2]int{{800, 600}, {2000, 1500}, {640, 480}}, nil, 2048, 1024},
		{"a batch of small pictures stays at the static size", [][2]int{{800, 600}, {1000, 700}}, nil, 1024, 1024},
		{"a value the caller gave wins; the other keeps its static default",
			[][2]int{{2000, 1500}}, map[string]any{"birefnet_process_res": 1536}, 1536, 1024},
		{"a caller value equal to the static default still wins",
			[][2]int{{2000, 1500}}, map[string]any{"birefnet_process_res": 1024}, 1024, 1024},
		{"a caller value for the parameter without a rule is kept",
			[][2]int{{2048, 2048}}, map[string]any{"sdmatte_process_res": 2048}, 2048, 2048},
		{"a lowered caller value for the parameter without a rule is kept",
			[][2]int{{2048, 2048}}, map[string]any{"sdmatte_process_res": 768}, 2048, 768},
		{"both given by the caller are kept as given",
			[][2]int{{2000, 1500}}, map[string]any{"birefnet_process_res": 4096, "sdmatte_process_res": 512}, 4096, 512},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.run(t, newSourceSideFixture(t))
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
	if frozen["birefnet_process_res"] != float64(2048) || frozen["sdmatte_process_res"] != float64(1024) {
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
	if taskInput["birefnet_process_res"] != float64(2048) || taskInput["sdmatte_process_res"] != float64(1024) {
		t.Fatalf("task input = %s", task.Input)
	}
}

// An Offering that declares no source-side rule keeps its input exactly as
// Normalize gave it, whatever size its pictures have.
func TestSourceSideDefaultsLeaveOtherOfferingsAlone(t *testing.T) {
	f := newShippedFixture(t)
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

// #299: the shipped background removal declares no source-side rule, so its two
// working resolutions are the static 1024 whatever the pictures are, which is
// what Plinth sent before #284. A 2048x2048 picture ran the matting service's
// shared GPU out of memory on 2026-11-13, first with both resolutions at 2048 and
// then with SDMatte at 1024 and BiRefNet at 2048 (out of memory in the coarse
// stage). A value the caller gives is still the caller's, up to the service's
// range, even one the rule used to choose.
func TestShippedMattingResolutionsStayAt1024WhateverThePictureSize(t *testing.T) {
	for _, test := range []resolutionsCase{
		{"a small picture", [][2]int{{800, 600}}, nil, 1024, 1024},
		{"a long side of exactly 1024", [][2]int{{1024, 1024}}, nil, 1024, 1024},
		{"one pixel over 1024", [][2]int{{1025, 10}}, nil, 1024, 1024},
		{"the long side counts, not the width", [][2]int{{16, 3000}}, nil, 1024, 1024},
		{"a 2048 picture", [][2]int{{2048, 2048}}, nil, 1024, 1024},
		{"the batch of the live runs that ran the GPU out of memory", [][2]int{{2048, 2048}, {16, 16}}, nil, 1024, 1024},
		{"a very large picture", [][2]int{{4096, 64}}, nil, 1024, 1024},
		{"a batch of mixed sizes", [][2]int{{800, 600}, {2000, 1500}, {640, 480}}, nil, 1024, 1024},
		{"a coarse resolution the caller gives is kept",
			[][2]int{{2048, 2048}}, map[string]any{"birefnet_process_res": 1536}, 1536, 1024},
		{"the coarse 2048 of the old rule is the caller's to ask for",
			[][2]int{{2048, 2048}}, map[string]any{"birefnet_process_res": 2048}, 2048, 1024},
		{"a fine resolution up to the service's 2048 is the caller's to ask for",
			[][2]int{{2048, 2048}}, map[string]any{"sdmatte_process_res": 2048}, 1024, 2048},
		{"both at the service's maximums",
			[][2]int{{2048, 2048}}, map[string]any{"birefnet_process_res": 4096, "sdmatte_process_res": 2048}, 4096, 2048},
		{"both lowered to the service's minimum",
			[][2]int{{2048, 2048}}, map[string]any{"birefnet_process_res": 512, "sdmatte_process_res": 512}, 512, 512},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.run(t, newShippedFixture(t))
		})
	}
}

// The static values are what the Task freezes at submit too: a 2048x2048 source
// prepared and submitted on the shipped Offering holds 1024 for both.
func TestShippedMattingTaskFreezesTheStaticResolutions(t *testing.T) {
	f := newShippedFixture(t)
	ctx := context.Background()
	source := f.upload(t, pictureOf(t, 2048, 2048))
	prepared, frozen := f.prepare(t, []generation.AssetID{source}, nil)
	if frozen["birefnet_process_res"] != float64(1024) || frozen["sdmatte_process_res"] != float64(1024) {
		t.Fatalf("frozen = %v", frozen)
	}
	accepted, err := f.h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, f.h.repository, prepared, "shipped-matting-prepared"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.h.module.Get(ctx, "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var taskInput map[string]any
	if err := json.Unmarshal(task.Input, &taskInput); err != nil {
		t.Fatal(err)
	}
	if taskInput["birefnet_process_res"] != float64(1024) || taskInput["sdmatte_process_res"] != float64(1024) {
		t.Fatalf("task input = %s", task.Input)
	}
}
