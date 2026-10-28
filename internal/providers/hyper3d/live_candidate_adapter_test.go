package hyper3d_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// namedDownloads gives the files of a replayed download list back the names
// the sample had to sanitize to opaque ids. The sample keeps each file's URL
// path, so the extension decides: the first GLB carries the declared
// "_pbr.glb" suffix, any later GLB a plain ".glb", the WebP "preview.webp".
type namedDownloads struct {
	inner http.RoundTripper
	named int
}

func (n *namedDownloads) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := n.inner.RoundTrip(request)
	if err != nil || !strings.HasSuffix(request.URL.Path, "/download") {
		return response, err
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	var list struct {
		List []map[string]string `json:"list"`
	}
	if json.Unmarshal(body, &document) != nil || json.Unmarshal(body, &list) != nil {
		return nil, io.ErrUnexpectedEOF
	}
	pbr := false
	for _, file := range list.List {
		switch {
		case strings.HasSuffix(file["url"], ".glb") && !pbr:
			file["name"] += "_pbr.glb"
			pbr = true
		case strings.HasSuffix(file["url"], ".glb"):
			file["name"] += ".glb"
		case strings.HasSuffix(file["url"], ".webp"):
			file["name"] = "preview.webp"
		}
		n.named++
	}
	document["list"], _ = json.Marshal(list.List)
	body, _ = json.Marshal(document)
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

// The reviewed Rodin sample drives the real Adapter through the Generation
// Port: Start with its durable dispatch checkpoint, every Observe the kernel
// would make, then the downloads of the claimed files. A request the sample
// does not contain fails the replay.
func TestRodinLiveCandidateDrivesTheAdapterThroughStartObserveAndOpenAsset(t *testing.T) {
	exchanges, policy := loadRodinLiveCandidate(t)
	replay, err := wirefixture.NewReplayWithMedia(exchanges, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	transport := &namedDownloads{inner: replay}

	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("hyper3d/rodin-gen-2.5/text-to-3d")
	if !ok {
		t.Fatal("missing Rodin Offering")
	}
	var config hyper3d.Config
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == offering.AdapterKey {
			config = hyper3d.Config{
				BaseURL: "https://api.hyper3d.com/api/v2", APIKey: "replay-credential",
				AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix,
				StatusMapping: connection.StatusMapping, HTTPClient: &http.Client{Transport: transport},
			}
		}
	}
	port, err := hyper3d.New(config)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"prompt":"A simple solid blue cube. Plain flat blue surfaces, sharp edges, no text, no patterns, no logo, minimal geometric shape.","authorize_extra_credits":true}`)
	_, input, err := product.Normalize(generation.TextTo3D, offering.ID, raw)
	if err != nil {
		t.Fatal(err)
	}

	checkpointer := &recordingCheckpointer{}
	started, err := port.Start(generation.WithDispatchCheckpointer(context.Background(), checkpointer), generation.StartRequest{
		OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: offering, Input: input,
	})
	if err != nil || started.Job == nil || started.Dispatch == nil || checkpointer.calls.Load() != 1 {
		t.Fatalf("start = %+v, %v (checkpoints %d)", started, err, checkpointer.calls.Load())
	}

	observation, polls := started, 0
	for observation.Phase != generation.UpstreamCompleted {
		if polls++; polls > 200 {
			t.Fatalf("the sample never completed; last phase %s", observation.Phase)
		}
		observation, err = port.Observe(context.Background(), generation.ObserveRequest{
			TaskID: "task", AttemptID: "attempt", Offering: offering, Input: input, Job: *started.Job, Opaque: observation.Opaque,
		})
		if err != nil {
			t.Fatalf("observe %d: %v", polls, err)
		}
	}
	if polls != 69 {
		t.Fatalf("observations = %d, want the 69 recorded status polls", polls)
	}

	// The required model and the background thumbnail are claimed, in the order
	// the sample downloads them; the third listed file matches no declaration.
	if len(observation.Artifacts) != 2 {
		t.Fatalf("claims = %+v", observation.Artifacts)
	}
	for index, want := range []struct{ role, mediaType, recipe string }{
		{"model", "model/gltf-binary", "glb-triangle"}, {"thumbnail", "image/webp", "webp-1x1"},
	} {
		claim := observation.Artifacts[index]
		body, metadata, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: offering, Claim: claim})
		if err != nil || claim.Role != want.role || metadata.MediaType != want.mediaType {
			t.Fatalf("claim %d = %+v, metadata %+v, %v", index, claim, metadata, err)
		}
		got, err := io.ReadAll(body)
		_ = body.Close()
		_, expected, recipeErr := wirefixture.SyntheticMedia(want.recipe)
		if err != nil || recipeErr != nil || !bytes.Equal(got, expected) {
			t.Fatalf("claim %d delivered %d bytes (%v), want recipe %s", index, len(got), err, want.recipe)
		}
	}
	if transport.named != 3 {
		t.Fatalf("named %d downloaded files, want the 3 listed", transport.named)
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}
