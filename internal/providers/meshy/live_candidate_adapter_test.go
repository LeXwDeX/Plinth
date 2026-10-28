package meshy_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// The reviewed Meshy sample drives the real Adapter through the Generation
// Port: Start of the preview, every Observe the kernel would make (the preview
// finishing, the refine being submitted, the refine finishing), then the
// download of each claimed file. A request the sample does not contain fails
// the replay.
func TestMeshyLiveCandidateDrivesTheAdapterThroughStartObserveAndOpenAsset(t *testing.T) {
	exchanges, policy := loadLiveCandidate(t)
	replay, err := wirefixture.NewReplayWithMedia(exchanges, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	port := newMeshyAdapter(t, meshy.Config{
		BaseURL: "https://api.meshy.ai", APIKey: "replay-credential", HTTPClient: &http.Client{Transport: replay},
	})
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/meshy-7.1/text-to-3d")
	if !ok {
		t.Fatal("missing Meshy Offering")
	}
	input := normalizedInput(t, offering, `{"prompt":"A simple plain blue cube, clean flat surfaces, solid uniform blue color, no text, no logos, no patterns, minimal geometric object."}`)

	started, err := port.Start(context.Background(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: offering, Input: input,
	})
	if err != nil || started.Job == nil || started.Job.RemoteID != "id-1" {
		t.Fatalf("start = %+v, %v", started, err)
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
	if polls != 53 || len(observation.Artifacts) != 12 {
		t.Fatalf("observations = %d (want the recorded 35 preview + 18 refine polls), claims = %d (want 12)", polls, len(observation.Artifacts))
	}

	// The kernel downloads the required files before the background ones. The
	// sample records that order, so each claim is opened where its file was.
	opened := 0
	for _, exchange := range exchanges {
		if exchange.Response.Body.Encoding != wirefixture.PayloadMedia {
			continue
		}
		locator := "https://" + exchange.Request.Host + exchange.Request.Path
		for _, claim := range observation.Artifacts {
			if claim.Locator != locator {
				continue
			}
			body, metadata, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: offering, Claim: claim})
			if err != nil {
				t.Fatalf("claim %s/%s: %v", claim.Role, claim.Variant, err)
			}
			got, err := io.ReadAll(body)
			_ = body.Close()
			_, want, recipeErr := wirefixture.SyntheticMedia(exchange.Response.Body.Media.Recipe)
			if err != nil || recipeErr != nil || !bytes.Equal(got, want) || metadata.MediaType == "" {
				t.Fatalf("claim %s/%s delivered %d bytes (%v), want recipe %s", claim.Role, claim.Variant, len(got), err, exchange.Response.Body.Media.Recipe)
			}
			opened++
		}
	}
	if opened != len(observation.Artifacts) {
		t.Fatalf("opened %d of %d claims", opened, len(observation.Artifacts))
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}
