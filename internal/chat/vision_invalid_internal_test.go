package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func encodedPNG(t *testing.T, width, height int) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

type refusingAdapter struct {
	refuse   func(request agents.Request) error
	requests []agents.Request
}

func (adapter *refusingAdapter) Stream(_ context.Context, request agents.Request, _ func(agents.Delta) error) (agents.Completion, error) {
	adapter.requests = append(adapter.requests, request)
	if err := adapter.refuse(request); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{Content: "ok"}, nil
}

func (adapter *refusingAdapter) Complete(ctx context.Context, request agents.Request) (agents.Completion, error) {
	return adapter.Stream(ctx, request, nil)
}

func invalidPictureRequest(urls ...string) agents.Request {
	parts := []agents.ContentPart{{Kind: "text", Text: "look"}}
	for _, url := range urls {
		parts = append(parts, agents.ContentPart{Kind: "image_url", URL: url})
	}
	return agents.Request{Model: "m", Messages: []agents.Message{{Role: "user", Parts: parts}}}
}

func invalidPictureOrchestrator(adapter agents.Adapter) *Orchestrator {
	vision := catalog.ChatVisionSchema{InvalidPicture: catalog.ChatVisionInvalidPictureSchema{
		MinimumEdgePixels: 33, MaximumAspectRatio: 200, MaximumExcluded: 8,
	}}
	return &Orchestrator{
		agent:  adapter,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		schema: catalog.ChatSchema{
			Agent:   catalog.ChatAgentSchema{InvalidImageNote: "invalid note"},
			Context: catalog.ChatContextSchema{TextPartKind: "text", ImagePartKind: "image_url", Vision: &vision},
		},
	}
}

func invalidRefusal() error {
	return errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions"}}, &agents.StatusError{Status: 400})
}

// #522: a refusal of a picture the upstream does not name is first answered
// without the pictures the Schema's bounds show to be unreadable, so a good
// picture beside a strip keeps travelling.
func TestInvalidRefusalStripsTheUnreadablePictureFirst(t *testing.T) {
	strip, good := encodedPNG(t, 300, 1), encodedPNG(t, 64, 64)
	adapter := &refusingAdapter{refuse: func(request agents.Request) error {
		for _, message := range request.Messages {
			for _, part := range message.Parts {
				if part.URL == strip {
					return invalidRefusal()
				}
			}
		}
		return nil
	}}
	orchestrator := invalidPictureOrchestrator(adapter)
	_, sent, err := orchestrator.streamAgent(context.Background(), ProcessTurn{}, invalidPictureRequest(strip, good), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(adapter.requests) != 2 {
		t.Fatalf("requests = %d, want the refused one and one retry", len(adapter.requests))
	}
	var urls []string
	for _, part := range sent.Messages[0].Parts {
		if part.URL != "" {
			urls = append(urls, part.URL)
		}
	}
	if !slices.Equal(urls, []string{good}) {
		t.Fatalf("pictures kept = %d, want only the good one", len(urls))
	}
}

// #522: when the refusal remains after the unreadable pictures are gone, or the
// request has none to point at, every picture goes, in at most two retries.
func TestInvalidRefusalEndsWithEveryPictureStripped(t *testing.T) {
	strip, other := encodedPNG(t, 300, 1), encodedPNG(t, 64, 64)
	adapter := &refusingAdapter{refuse: func(request agents.Request) error {
		if len(requestPictureKeys(request)) > 0 {
			return invalidRefusal()
		}
		return nil
	}}
	orchestrator := invalidPictureOrchestrator(adapter)
	_, sent, err := orchestrator.streamAgent(context.Background(), ProcessTurn{}, invalidPictureRequest(strip, other), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(adapter.requests) != 3 || len(requestPictureKeys(sent)) != 0 {
		t.Fatalf("requests = %d, pictures left = %d, want 3 requests ending without pictures",
			len(adapter.requests), len(requestPictureKeys(sent)))
	}
}

// #522: the session remembers only pictures known to be bad: the ones the
// Schema's bounds name, or the only picture; nothing after a count refusal.
func TestRefusedPictureKeysAreOnlyTheConfirmedOnes(t *testing.T) {
	every := []string{"a", "b", "c"}
	dimensions := errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions"}}, &agents.StatusError{Status: 400})
	count := errors.Join(&agents.ImageInvalidity{Reasons: []string{"dimensions", "count"}}, &agents.StatusError{Status: 400})
	if got := refusedPictureKeys(every, map[string]bool{"b": true}, dimensions); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("confirmed b: got %v", got)
	}
	if got := refusedPictureKeys(every, nil, dimensions); len(got) != 0 {
		t.Fatalf("nothing confirmed: got %v", got)
	}
	if got := refusedPictureKeys(every, map[string]bool{"b": true}, count); len(got) != 0 {
		t.Fatalf("count refusal: got %v", got)
	}
}

// #522: a failure of another kind during the retry ends it and is returned.
func TestInvalidRetryStopsOnAnotherFailure(t *testing.T) {
	picture := encodedPNG(t, 64, 64)
	calls := 0
	adapter := &refusingAdapter{refuse: func(agents.Request) error {
		calls++
		if calls == 1 {
			return invalidRefusal()
		}
		return &agents.StatusError{Status: 500}
	}}
	orchestrator := invalidPictureOrchestrator(adapter)
	_, _, err := orchestrator.streamAgent(context.Background(), ProcessTurn{}, invalidPictureRequest(picture), nil)
	var status *agents.StatusError
	if !errors.As(err, &status) || status.Status != 500 || calls != 2 {
		t.Fatalf("err = %v after %d calls, want the 500 of the retry", err, calls)
	}
}

// The size of a picture is read from the start of a large data URI, and a
// payload that is not a picture is never judged.
func TestDataURIPictureSize(t *testing.T) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewGray(image.Rect(0, 0, 70, 20))); err != nil {
		t.Fatal(err)
	}
	// A picture with a long tail after its header: the header alone decides.
	padded := append(buffer.Bytes(), bytes.Repeat([]byte{0}, maximumPictureHeaderBytes*2)...)
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(padded)
	if width, height, ok := dataURIPictureSize(url); !ok || width != 70 || height != 20 {
		t.Fatalf("size = %dx%d ok=%v, want 70x20", width, height, ok)
	}
	for _, unreadable := range []string{
		"https://example.test/a.png", "data:image/png;base64,", "data:image/png;base64,!!!!",
		"data:image/png,not-base64", "data:image/png;base64,Y29uZmlndXJlZA==",
	} {
		if _, _, ok := dataURIPictureSize(unreadable); ok {
			t.Fatalf("%q was read as a picture", unreadable)
		}
		if pictureTooSmall(catalog.ChatVisionInvalidPictureSchema{MinimumEdgePixels: 33, MaximumAspectRatio: 200}, unreadable) {
			t.Fatalf("%q was judged", unreadable)
		}
	}
}

func TestAppendVisionExclusions(t *testing.T) {
	got := AppendVisionExclusions([]string{"a", "b", "c"}, []string{"b", "", "d"}, 3)
	if !slices.Equal(got, []string{"c", "b", "d"}) {
		t.Fatalf("got %v, want [c b d]: a repeated key moves to the end and the oldest is forgotten", got)
	}
	if got := AppendVisionExclusions(nil, []string{"a"}, 0); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("got %v", got)
	}
	if DecodeVisionExclusions("") == nil || len(DecodeVisionExclusions("not json")) != 0 ||
		!slices.Equal(DecodeVisionExclusions(`["a","b"]`), []string{"a", "b"}) {
		t.Fatal("a stored list must decode and an unreadable one must remember nothing")
	}
	encoded, err := EncodeVisionExclusions(nil)
	if err != nil || strings.TrimSpace(encoded) != "[]" {
		t.Fatalf("encoded = %q, %v", encoded, err)
	}
}
