package matting_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	mattingapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/matting/api-v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

func shippedOffering(t *testing.T) (*catalog.Catalog, generation.Offering) {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := loaded.Get("matting/default/background-removal")
	if !ok {
		t.Fatal("background-removal offering missing")
	}
	return loaded, offering
}

func resolutionParameter(offering generation.Offering, name string) (generation.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

// #284: the range the Schema declares for each process resolution is the range
// the service validates, as the fake enforces it. A Schema that lets a caller
// ask for more than the service takes would fail the Task after it is accepted;
// one that stops below it would waste what the service can do.
func TestShippedResolutionRangesAreTheServicesRanges(t *testing.T) {
	_, offering := shippedOffering(t)
	for _, limit := range mattingapiv1.ResolutionLimits() {
		parameter, ok := resolutionParameter(offering, limit.Field)
		if !ok {
			t.Fatalf("the Offering has no %s parameter", limit.Field)
		}
		if parameter.Minimum == nil || int64(*parameter.Minimum) != limit.Min ||
			parameter.Maximum == nil || int64(*parameter.Maximum) != limit.Max {
			t.Fatalf("%s declares %v..%v, the service takes %d..%d", limit.Field, parameter.Minimum, parameter.Maximum, limit.Min, limit.Max)
		}
		if !parameter.Integer {
			t.Fatalf("%s is an integer on the service", limit.Field)
		}
		values := []json.RawMessage{parameter.Default}
		if rule := parameter.DefaultBySourceSide; rule != nil {
			for _, tier := range rule.Tiers {
				values = append(values, tier.Value)
			}
		}
		for _, raw := range values {
			var value int64
			if err := json.Unmarshal(raw, &value); err != nil || value < limit.Min || value > limit.Max {
				t.Fatalf("%s default or tier %s is outside the service's %d..%d", limit.Field, raw, limit.Min, limit.Max)
			}
		}
	}
}

// startAgainstTheFake submits one picture through the real adapter to the fake
// with the given resolutions in the Offering's own input shape.
func startAgainstTheFake(t *testing.T, f *testkit.Fixture, resolutions map[string]any) error {
	t.Helper()
	loaded, offering := shippedOffering(t)
	adapter, err := matting.New(matting.Config{
		BaseURL: f.Server.URL + "/matting", APIKey: "matting-key", HTTPClient: f.Server.Client(),
		InputAssets: memoryInputs{content: []byte("picture")},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"source_assets": []string{"asset-1"}}
	for name, value := range resolutions {
		input[name] = value
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	normalized, normalizeErr := normalizeForOffering(loaded, offering, encoded)
	if normalizeErr != nil {
		return normalizeErr
	}
	_, err = adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt-1", SubmissionKey: fmt.Sprintf("submission-%v", resolutions), Offering: offering, Input: normalized,
		InputAssets: map[string][]generation.InputAsset{"source_assets": {{ID: "asset-1", MediaType: "image/png"}}},
	})
	return err
}

func normalizeForOffering(loaded *catalog.Catalog, offering generation.Offering, input json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := loaded.Normalize(offering.Capability, offering.ID, input)
	return normalized, err
}

// Every value the Schema lets a request carry reaches the service and is
// accepted: the static 1024 defaults, the edges of both ranges, and the 2048 a
// caller can raise either resolution to. The Schema never chooses 2048 by itself:
// the shared GPU ran out of memory on a 2048x2048 picture with it (#299).
func TestEverySchemaAllowedResolutionIsAcceptedByTheService(t *testing.T) {
	f := testkit.New(t, fakeupstream.Config{}, mattingapiv1.Version())
	for name, resolutions := range map[string]map[string]any{
		"the declared defaults":             {},
		"both at the minimum":               {"birefnet_process_res": 512, "sdmatte_process_res": 512},
		"both at the maximum":               {"birefnet_process_res": 4096, "sdmatte_process_res": 2048},
		"both raised to 2048 by the caller": {"birefnet_process_res": 2048, "sdmatte_process_res": 2048},
		"only the coarse step":              {"birefnet_process_res": 3072},
		"only the fine step":                {"sdmatte_process_res": 1536},
	} {
		if err := startAgainstTheFake(t, f, resolutions); err != nil {
			t.Fatalf("%s: the service refused what the Schema allows: %v", name, err)
		}
	}
}

// A value past the Schema's maximum never reaches the service: Normalize
// refuses it first. Were the Schema to drift past the service, the fake's 422
// is what the next test shows the adapter reporting.
func TestAValueBeyondTheSchemaRangeIsRefusedBeforeTheService(t *testing.T) {
	f := testkit.New(t, fakeupstream.Config{}, mattingapiv1.Version())
	for name, resolutions := range map[string]map[string]any{
		"coarse above its maximum": {"birefnet_process_res": 4097},
		"fine above its maximum":   {"sdmatte_process_res": 2049},
		"coarse below its minimum": {"birefnet_process_res": 511},
		"a fraction":               {"sdmatte_process_res": 1024.5},
	} {
		if err := startAgainstTheFake(t, f, resolutions); err == nil {
			t.Fatalf("%s reached the service", name)
		}
	}
}

// If the Schema ever allowed more than the service takes, the 422 the service
// gives is a final refusal, never a retry: the same request would be refused again.
func TestTheServicesRangeRefusalIsFinal(t *testing.T) {
	f := testkit.New(t, fakeupstream.Config{}, mattingapiv1.Version())
	_, offering := shippedOffering(t)
	adapter, err := matting.New(matting.Config{
		BaseURL: f.Server.URL + "/matting", APIKey: "matting-key", HTTPClient: f.Server.Client(),
		InputAssets: memoryInputs{content: []byte("picture")},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Built by hand, the way a Schema that had drifted past the service would send it.
	_, err = adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt-2", SubmissionKey: "submission-drift", Offering: offering,
		Input:       json.RawMessage(`{"source_assets":["asset-1"],"birefnet_process_res":8192,"sdmatte_process_res":1024}`),
		InputAssets: map[string][]generation.InputAsset{"source_assets": {{ID: "asset-1", MediaType: "image/png"}}},
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil {
		t.Fatalf("Start error = %v, want a provider failure", err)
	}
	if providerErr.Failure.Retryable || providerErr.Failure.Ambiguous {
		t.Fatalf("the service's 422 is final: %+v", providerErr.Failure)
	}
}
