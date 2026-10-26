package tripo_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// The uploaded picture is named from its verified media type. A type Tripo's
// upload does not take is refused before the upload instead of travelling as
// a guessed ".png".
func TestImageUploadFilenameFollowsVerifiedMediaType(t *testing.T) {
	uploads := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/files":
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			for _, file := range request.MultipartForm.File["file"] {
				uploads = append(uploads, file.Filename)
			}
			writeEnvelope(writer, map[string]string{"file_token": "file-token-name"})
		case "/generation/image-to-model":
			writeEnvelope(writer, map[string]string{"task_id": "tripo-image-name"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	adapter, err := tripo.New(tripo.Config{BaseURL: upstream.URL, APIKey: "tripo-key", HTTPClient: upstream.Client(), InputAssets: memoryInputAssets{content: []byte("picture")}})
	if err != nil {
		t.Fatal(err)
	}
	start := func(mediaType string) error {
		_, err := adapter.Start(context.Background(), generation.StartRequest{
			AttemptID: "attempt-name", SubmissionKey: "submission-name",
			Offering: generation.Offering{
				ProviderID: "tripo", Capability: generation.ImageTo3D,
				ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/image-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
			},
			Input:       []byte(`{"reference_assets":["asset-input"]}`),
			InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "asset-input", MediaType: mediaType}}},
		})
		return err
	}
	if err := start("image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 1 || uploads[0] != "input.jpg" {
		t.Fatalf("uploads = %q", uploads)
	}
	err = start("image/gif")
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil ||
		providerErr.Failure.Class != generation.FailureInvalidInput ||
		!strings.Contains(providerErr.Failure.SafeDetail, "扩展名") {
		t.Fatalf("unsupported picture must be refused with a reason: %v", err)
	}
	if len(uploads) != 1 {
		t.Fatalf("refused picture reached the upload: %q", uploads)
	}
}
