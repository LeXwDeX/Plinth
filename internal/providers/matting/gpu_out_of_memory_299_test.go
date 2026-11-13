package matting_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/matting"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	mattingapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/matting/api-v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// realOutOfMemory is the error_message the live service gave on 2026-11-13 for a
// 2048x2048 picture (#299), character for character. It is written out so these
// tests hold the Schema's matcher and the fake against what the service really
// said, not against each other.
const realOutOfMemory = "GPU 显存不足，无法处理该图片（图片尺寸: 2048×2048）。建议：使用更大显存的 GPU，或降低 BIREFNET_PROCESS_RES / SDMATTE_PROCESS_RES 配置。"

// sizedPictures serves each input asset its own bytes, so a picture can have the
// size the test stands it in for (an empty PNG stays small however large).
type sizedPictures map[generation.AssetID][]byte

func (sizedPictures) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (pictures sizedPictures) Open(_ context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	data, ok := pictures[asset.ID]
	if !ok {
		return nil, fmt.Errorf("no picture %q", asset.ID)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func pngOf(t *testing.T, width, height int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// shippedMapping is the error vocabulary the shipped Catalog declares for the
// matting connection: the one the application hands the adapter.
func shippedMapping(t *testing.T) (*catalog.Catalog, *generation.ErrorMapping) {
	t.Helper()
	loaded, _ := shippedOffering(t)
	for _, connection := range loaded.Application().Runtime.ProviderConnections {
		if connection.ID == "matting" {
			if connection.ErrorMapping == nil {
				t.Fatal("the matting connection declares no error_mapping")
			}
			return loaded, connection.ErrorMapping
		}
	}
	t.Fatal("the Catalog has no matting connection")
	return nil, nil
}

// failedBatch asks the fake for a batch the service ends FAILED, with the given
// faults on individual pictures.
func failedBatch(spec string) map[string]string {
	headers := map[string]string{fakeupstream.ScenarioHeader: string(fakeupstream.ScenarioTaskFailed)}
	if spec != "" {
		headers[mattingapiv1.FileFaultsHeader] = spec
	}
	return headers
}

// observeSized submits pictures of the given sizes through the real adapter, with
// the shipped Offering and the given error vocabulary, to the shared matting fake
// asked for the given headers, and returns the observation the adapter makes once
// the batch has finished.
func observeSized(t *testing.T, sizes [][2]int, headers map[string]string, mapping *generation.ErrorMapping) generation.Observation {
	t.Helper()
	f := testkit.New(t, fakeupstream.Config{}, mattingapiv1.Version())
	pictures := sizedPictures{}
	ids := make([]string, len(sizes))
	assets := make([]generation.InputAsset, len(sizes))
	for index, size := range sizes {
		id := generation.AssetID(fmt.Sprintf("asset-%d", index+1))
		pictures[id] = pngOf(t, size[0], size[1])
		ids[index] = string(id)
		assets[index] = generation.InputAsset{ID: id, MediaType: "image/png"}
	}
	adapter, err := matting.New(matting.Config{
		BaseURL: f.Server.URL + "/matting", APIKey: "matting-key",
		HTTPClient:   &http.Client{Transport: requestHeaders{next: http.DefaultTransport, headers: headers}},
		InputAssets:  pictures,
		ErrorMapping: mapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, offering := shippedOffering(t)
	input, err := json.Marshal(map[string]any{"source_assets": ids})
	if err != nil {
		t.Fatal(err)
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt-299", SubmissionKey: "submission-299", Offering: offering,
		Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": assets},
	})
	if err != nil || started.Job == nil {
		t.Fatalf("the fake refused the batch: %+v %v", started, err)
	}
	var observed generation.Observation
	for range 8 {
		observed, err = adapter.Observe(context.Background(), generation.ObserveRequest{
			AttemptID: "attempt-299", Offering: offering, Job: *started.Job,
		})
		if err != nil {
			t.Fatal(err)
		}
		if observed.Phase == generation.UpstreamCompleted || observed.Phase == generation.UpstreamFailed {
			return observed
		}
	}
	t.Fatalf("the batch never finished: %+v", observed)
	return observed
}

// #299: a GPU that cannot hold the picture is the service running short of
// capacity, not the service refusing the request, so the failure is the
// Schema's capacity class and not "rejected". The same request would run out
// again, so it is never retried by itself. Run in the shapes the service can
// give the failure in: a batch it ended FAILED, and a batch it ended completed
// with every picture failed.
func TestGPUOutOfMemoryIsACapacityFailureThatIsNotRetried(t *testing.T) {
	_, mapping := shippedMapping(t)
	for name, test := range map[string]struct {
		sizes   [][2]int
		headers map[string]string
	}{
		"a batch the service ended FAILED, the reason on the only picture": {
			[][2]int{{2048, 2048}}, failedBatch("1:gpu_oom"),
		},
		"a batch the service ended FAILED, the reason on the second picture": {
			[][2]int{{16, 16}, {2048, 2048}}, failedBatch("2:gpu_oom"),
		},
		"a batch completed with every picture failed": {
			[][2]int{{2048, 2048}}, faults("1:gpu_oom"),
		},
		"a batch completed with a refused picture and the failed one": {
			[][2]int{{16, 16}, {2048, 2048}}, faults("1:rejected,2:gpu_oom"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			observed := observeSized(t, test.sizes, test.headers, mapping)
			failure := observed.Failure
			if observed.Phase != generation.UpstreamFailed || failure == nil {
				t.Fatalf("observation = %+v, want a failed one", observed)
			}
			if failure.Class != generation.FailureUpstreamUnavailable || failure.Retryable || failure.Ambiguous {
				t.Fatalf("failure = %+v, want a definite upstream_unavailable that is not retried", failure)
			}
			// The detail is the service's own text of the picture that ran out, not the
			// first message of the batch, which is not always the one that says why.
			if failure.SafeDetail != realOutOfMemory {
				t.Fatalf("detail = %q, want %q", failure.SafeDetail, realOutOfMemory)
			}
			// The kernel does not retry it either: the Offering lists nothing to retry.
			_, offering := shippedOffering(t)
			if offering.Retry.Allows(failure.Class, 2) {
				t.Fatalf("the Offering retries %s: %+v", failure.Class, offering.Retry)
			}
		})
	}
}

// A picture that ran out of memory next to pictures that were matted is a
// completed batch with a notice naming the missing one, as for any picture that
// did not come back (#284); it is not a failure of the Task.
func TestGPUOutOfMemoryOfOnePictureAmongMattedOnesIsACompletionWithANotice(t *testing.T) {
	_, mapping := shippedMapping(t)
	observed := observeSized(t, [][2]int{{2048, 2048}, {16, 16}}, faults("1:gpu_oom"), mapping)
	if observed.Phase != generation.UpstreamCompleted || observed.Failure != nil || len(observed.Artifacts) != 1 {
		t.Fatalf("observation = %+v, want a completion with the one matted picture", observed)
	}
	want := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{1}}}
	if !reflect.DeepEqual(observed.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", observed.Notices, want)
	}
}

// Only what the Schema declares is classified. A failure whose text no matcher
// hits stays the plain refusal it was, and an adapter handed no vocabulary
// classifies nothing, the out-of-memory text included: the words are the
// Schema's, not the adapter's.
func TestOnlyTheDeclaredMessagesAreClassified(t *testing.T) {
	_, shipped := shippedMapping(t)
	statusBound := &generation.ErrorMapping{MessageMatchers: []generation.ErrorMessageMatcher{{
		ContainsAny: []string{"显存不足"}, Class: string(generation.FailureUpstreamUnavailable),
		HTTPStatus: &schema.StatusRange{Min: 500, Max: 599},
	}}}
	for name, test := range map[string]struct {
		sizes   [][2]int
		headers map[string]string
		mapping *generation.ErrorMapping
		detail  string
	}{
		"a failure the Schema says nothing about":                {[][2]int{{16, 16}}, failedBatch(""), shipped, "Fake matting failure."},
		"pictures that all fail without a reason the Schema has": {[][2]int{{16, 16}, {32, 32}}, faults("1:failed,2:failed"), shipped, ""},
		"the out-of-memory text without a vocabulary":            {[][2]int{{2048, 2048}}, failedBatch("1:gpu_oom"), nil, realOutOfMemory},
		"the out-of-memory text with an empty vocabulary":        {[][2]int{{2048, 2048}}, failedBatch("1:gpu_oom"), &generation.ErrorMapping{}, realOutOfMemory},
		"a matcher bound to an HTTP status is not a hit":         {[][2]int{{2048, 2048}}, failedBatch("1:gpu_oom"), statusBound, realOutOfMemory},
	} {
		t.Run(name, func(t *testing.T) {
			observed := observeSized(t, test.sizes, test.headers, test.mapping)
			failure := observed.Failure
			if observed.Phase != generation.UpstreamFailed || failure == nil {
				t.Fatalf("observation = %+v, want a failed one", observed)
			}
			if failure.Class != generation.FailureRejected || failure.Retryable || failure.Ambiguous {
				t.Fatalf("failure = %+v, want the plain refusal", failure)
			}
			if test.detail != "" && failure.SafeDetail != test.detail {
				t.Fatalf("detail = %q, want %q", failure.SafeDetail, test.detail)
			}
		})
	}
}

// The person reads the class's own Schema text, which every Provider shares: the
// service is unavailable or short of resources, retry later, and an administrator
// is the way on when it keeps happening. It is not the refusal the failure used to
// read as, and it says nothing about pictures or resolutions, which would mislead
// whoever meets the same class from a Provider that has neither. What is specific
// to the picture stays where it already was, the service's own text in the
// failure's detail (the Agent's background status line and the logs), which no
// public surface carries. The category the Agent acts on is the capacity one, not a
// refusal that asks the person about their account.
func TestGPUOutOfMemoryReadsAsTheSharedCapacityText(t *testing.T) {
	loaded, mapping := shippedMapping(t)
	observed := observeSized(t, [][2]int{{2048, 2048}}, faults("1:gpu_oom"), mapping)
	if observed.Failure == nil {
		t.Fatalf("observation = %+v, want a failed one", observed)
	}
	class := observed.Failure.Class
	application := loaded.Application()
	localizer, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	key := "failure." + string(class)
	chinese := localizer.Message("zh-CN", key)
	english := localizer.Message("en", key)
	for _, want := range []string{"服务暂时不可用或资源不足", "稍后重试", "联系管理员"} {
		if !strings.Contains(chinese, want) {
			t.Errorf("the Chinese text %q does not say %q", chinese, want)
		}
	}
	for _, want := range []string{"temporarily unavailable or short of resources", "retry later", "contact an administrator"} {
		if !strings.Contains(english, want) {
			t.Errorf("the English text %q does not say %q", english, want)
		}
	}
	// Generic for every Provider: no advice about the picture, the resolution or the GPU.
	for _, banned := range []string{"图片", "分辨率", "显存", "GPU"} {
		if strings.Contains(chinese, banned) {
			t.Errorf("the Chinese text %q is specific to pictures (%q)", chinese, banned)
		}
	}
	for _, banned := range []string{"picture", "image", "resolution", "memory", "GPU"} {
		if strings.Contains(strings.ToLower(english), strings.ToLower(banned)) {
			t.Errorf("the English text %q is specific to pictures (%q)", english, banned)
		}
	}
	if strings.IndexFunc(english, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
		t.Errorf("the English text is not English: %q", english)
	}
	report := generation.ClassifyFailure(class, observed.Failure.Retryable, observed.Failure.Ambiguous)
	if report.Category != generation.CategoryUpstreamCapacity || report.Retryable || report.UserDecisionRequired {
		t.Fatalf("report = %+v, want the capacity category without an automatic retry", report)
	}
	// It is not the text of the refusal the failure used to be.
	if refusal := application.Content.Messages["failure."+string(generation.FailureRejected)]; chinese == refusal {
		t.Fatalf("the text is still the refusal's: %q", chinese)
	}
	// The service's own words are still what the failure carries.
	if observed.Failure.SafeDetail != realOutOfMemory {
		t.Fatalf("detail = %q, want the service's text %q", observed.Failure.SafeDetail, realOutOfMemory)
	}
}
