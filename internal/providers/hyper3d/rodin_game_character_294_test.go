package hyper3d_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #294 follow-up: the one paid Rodin run of the game-character request (2026-11-13,
// dev-test, main 94a63a35; session purpose game_character, platform pc_console),
// from now on covered by the fake. The Offering was hyper3d/rodin-gen-2.5/text-to-3d
// and its frozen input a prompt, mesh_mode=Quad, quality_override=10000,
// texture_mode=high, TAPose=true, quad_normal=true and the extra-credit consent;
// the Catalog's template added tier=Gen-2.5-Extreme-High. The service charged 1
// credit (0.5 for the generation, 0.5 for the tier, nothing for Quad, the pose
// switch or the bake), took about 160 seconds and delivered a PBR GLB (9,437,496
// bytes, one mesh, about 21,800 triangles for 10,000 quads, no skin, three
// embedded PNG images of 2048x2048, bounding box 1.363 x 1.889 x 0.475) and a
// WebP preview.
//
// The raw upstream bodies were not kept, so this drives the real Adapter with the
// shipped Offering and that frozen input against the Hyper3D fake and checks what
// the run showed at the level the fake can carry: the form as sent, the receipt's
// charge against the Catalog's quote, the claims the Offering declares (a model and
// a preview, not the extra mesh the Extreme-High tier also lists) and the bytes
// the Adapter and Plinth's own checks accept. What the shared fixture does not
// reproduce (the triangle count, and the number and edge of the textures) is
// recorded in the fake's endpoint note, not asserted here.
//
// Nothing here leaves the process: the Adapter's client reaches the fake through
// the fixture's host routes and refuses every other host.

const (
	rodinAPIHost  = "api.hyper3d.com"
	rodinFileHost = "file.hyper3d.com"

	gameCharacterOffering = "hyper3d/rodin-gen-2.5/text-to-3d"
	gameCharacterPrompt   = "a game character standing in an A pose"
	// gameCharacterInput is the frozen input of the run, as the purpose's
	// overrides produce it on the run's platform (pc_console; a session without a
	// platform and the phone class get 8000 faces since the budget follow-up, and
	// the Catalog's tests hold every platform's count to its triangle budget).
	gameCharacterInput = `{"prompt":"` + gameCharacterPrompt + `","mesh_mode":"Quad","quality_override":10000,"texture_mode":"high","TAPose":true,"quad_normal":true,"authorize_extra_credits":true}`
)

// submissionRecorder keeps what the Adapter sent: every Gen-2.5 submission's
// multipart parts in wire order and the host of every request.
type submissionRecorder struct {
	inner http.RoundTripper

	mu          sync.Mutex
	submissions [][]formPart
	hosts       []string
}

func (r *submissionRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.hosts = append(r.hosts, request.URL.Hostname())
	r.mu.Unlock()
	if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/rodin") {
		return r.inner.RoundTrip(request)
	}
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return nil, err
	}
	parts, err := parseSubmission(request.Header.Get("Content-Type"), raw)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.submissions = append(r.submissions, parts)
	r.mu.Unlock()
	forwarded := request.Clone(request.Context())
	forwarded.Body = io.NopCloser(bytes.NewReader(raw))
	return r.inner.RoundTrip(forwarded)
}

func TestRodinGameCharacterRunAgainstTheFake(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get(generation.OfferingID(gameCharacterOffering))
	if !ok {
		t.Fatalf("missing Offering %s", gameCharacterOffering)
	}
	// The Schema takes the run's values, and freezes them.
	_, frozen, err := product.Normalize(offering.Capability, offering.ID, json.RawMessage(gameCharacterInput))
	if err != nil {
		t.Fatalf("the Schema refused the run's input: %v", err)
	}
	quote, err := offering.EvaluateNativeQuote(frozen)
	if err != nil {
		t.Fatal(err)
	}

	fixture := testkit.New(t, fakeupstream.Config{ArtifactOrigin: "https://" + rodinFileHost}, hyper3dapiv2.Version())
	recorder := &submissionRecorder{inner: fixture.RouteHosts(rodinAPIHost, rodinFileHost)}
	var port *hyper3d.Adapter
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID != "hyper3d" {
			continue
		}
		port, err = hyper3d.New(hyper3d.Config{
			BaseURL: "https://" + rodinAPIHost + hyper3dapiv2.Prefix, APIKey: "synthetic-key",
			AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix,
			StatusMapping: connection.StatusMapping, ErrorMapping: connection.ErrorMapping,
			HTTPClient: &http.Client{Transport: recorder},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if port == nil {
		t.Fatal("no hyper3d connection")
	}

	checkpointer := &recordingCheckpointer{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), checkpointer)
	started, err := port.Start(ctx, generation.StartRequest{
		OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: offering, Input: frozen,
	})
	if err != nil || started.Job == nil || started.Dispatch == nil || checkpointer.calls.Load() != 1 {
		t.Fatalf("start = %+v, %v (checkpoints %d)", started, err, checkpointer.calls.Load())
	}

	// The form as sent: the template's fields, the purpose's overrides and the two
	// switches, each once; the consent flag stays on this side of the wire.
	wanted := []string{
		"geometry_file_format=glb", "material=PBR", "mesh_mode=Quad", "prompt=" + gameCharacterPrompt, "quad_normal=true",
		"quality=high", "quality_override=10000", "TAPose=true", "texture_mode=high", "tier=Gen-2.5-Extreme-High",
	}
	recorder.mu.Lock()
	submissions := append([][]formPart(nil), recorder.submissions...)
	recorder.mu.Unlock()
	if len(submissions) != 1 {
		t.Fatalf("the fake received %d submissions, want 1", len(submissions))
	}
	sent := partStrings(submissions[0])
	slices.Sort(sent)
	slices.Sort(wanted)
	if !slices.Equal(sent, wanted) {
		t.Fatalf("form as sent = %v\nwant           = %v", sent, wanted)
	}

	// The receipt's charge is kept as private billing evidence and agrees with the
	// Catalog's frozen quote for these inputs (1 credit, as the run was charged).
	var receipt struct {
		ConsumedCredit json.Number `json:"consumed_credit"`
	}
	if err := json.Unmarshal(started.Opaque, &receipt); err != nil || receipt.ConsumedCredit == "" {
		t.Fatalf("checkpoint %s carries no consumed_credit: %v", started.Opaque, err)
	}
	consumed, err := receipt.ConsumedCredit.Float64()
	if err != nil {
		t.Fatal(err)
	}
	frozenTotal, err := strconv.ParseFloat(quote.TotalAmount, 64)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != 1 || consumed != frozenTotal {
		t.Fatalf("receipt consumed %v, frozen quote %s: both should be the 1 credit of the run", consumed, quote.TotalAmount)
	}

	// Every observation the kernel would make, until the files are claimed.
	observation, sawWaiting, sawRunning, polls := started, false, false, 0
	for observation.Phase != generation.UpstreamCompleted {
		if polls++; polls > 40 {
			t.Fatalf("the fake never completed; last phase %s", observation.Phase)
		}
		observation, err = port.Observe(context.Background(), generation.ObserveRequest{
			TaskID: "task", AttemptID: "attempt", Offering: offering, Input: frozen, Job: *started.Job, Opaque: observation.Opaque,
		})
		if err != nil {
			t.Fatalf("observe %d: %v", polls, err)
		}
		sawWaiting = sawWaiting || observation.Phase == generation.UpstreamAccepted
		sawRunning = sawRunning || observation.Phase == generation.UpstreamRunning
	}
	if !sawWaiting || !sawRunning {
		t.Fatalf("the sub-job flow showed waiting %v, running %v", sawWaiting, sawRunning)
	}

	// The Offering's slots: a required GLB model and a background WebP preview. The
	// Extreme-High tier's extra mesh is listed by the fake and is not claimed.
	if len(observation.Artifacts) != 2 {
		t.Fatalf("claims = %+v, want the model and the preview only", observation.Artifacts)
	}
	for index, want := range []struct{ role, variant, mediaType string }{
		{"model", "glb", "model/gltf-binary"}, {"thumbnail", "primary", "image/webp"},
	} {
		claim := observation.Artifacts[index]
		if claim.Role != want.role || claim.Variant != want.variant || claim.MediaType != want.mediaType {
			t.Fatalf("claim %d = %s/%s %s, want %s/%s %s", index, claim.Role, claim.Variant, claim.MediaType, want.role, want.variant, want.mediaType)
		}
		if !strings.HasPrefix(claim.Locator, "https://"+rodinFileHost+"/") || strings.Contains(claim.Locator, "model_export_raw") {
			t.Fatalf("claim %d locator %q is not the declared file on the file host", index, claim.Locator)
		}
		body, metadata, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: offering, Claim: claim})
		if err != nil {
			t.Fatalf("claim %d: %v", index, err)
		}
		data, readErr := io.ReadAll(body)
		_ = body.Close()
		if readErr != nil || metadata.MediaType != want.mediaType || len(data) == 0 {
			t.Fatalf("claim %d delivered %d bytes as %q: %v", index, len(data), metadata.MediaType, readErr)
		}
		// Plinth's own delivery check accepts both files.
		if err := testkit.DeliveryErr(t, data, want.role, want.variant, want.mediaType); err != nil {
			t.Fatalf("claim %d is refused by the delivery check: %v", index, err)
		}
		if want.role != "model" {
			continue
		}
		// What the inspect path reads from the model: one mesh, no skin, PNG textures.
		model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		report := modelinspect.Inspect(model, modelinspect.DefaultInspectOptions())
		if report.Meshes != 1 || len(report.Skins) != 0 || report.TextureCount < 1 {
			t.Fatalf("model: %d meshes, %d skins, %d textures", report.Meshes, len(report.Skins), report.TextureCount)
		}
		for _, material := range report.Materials {
			for _, slot := range material.Slots {
				if slot.Present && slot.MIME != "image/png" {
					t.Fatalf("texture slot %s is %q, want image/png", slot.Slot, slot.MIME)
				}
			}
		}
	}

	// Nothing left the fake.
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, host := range recorder.hosts {
		if host != rodinAPIHost && host != rodinFileHost {
			t.Fatalf("a request went to %q", host)
		}
	}
}
