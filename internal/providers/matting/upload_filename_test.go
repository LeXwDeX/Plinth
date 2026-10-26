package matting_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The uploaded part's extension follows the verified media type from the
// project table, so BMP and TIFF are named correctly on hosts without a mime
// table, and an unmapped type is refused before the paid create.
func TestUploadExtensionFollowsVerifiedMediaType(t *testing.T) {
	var filenames []string
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		hits++
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		for _, files := range r.MultipartForm.File {
			for _, file := range files {
				filenames = append(filenames, file.Filename)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"batch_id": "batch-upload-name"})
	}))
	defer upstream.Close()
	adapter := newAdapter(t, upstream, memoryInputs{content: []byte("input-image")})
	offering := defaultOffering()
	offering.Parameters[0].Extensions = []string{"png", "jpg", "jpeg", "webp", "bmp", "tiff"}
	request := func(mediaTypes ...string) generation.StartRequest {
		assets := []generation.InputAsset{}
		ids := []string{}
		for index, mediaType := range mediaTypes {
			id := generation.AssetID("asset-" + string(rune('a'+index)))
			ids = append(ids, string(id))
			assets = append(assets, generation.InputAsset{ID: id, MediaType: mediaType})
		}
		input, _ := json.Marshal(map[string]any{"source_assets": ids})
		return generation.StartRequest{AttemptID: "att-upload-name", SubmissionKey: "sub-upload-name", Offering: offering, Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": assets}}
	}

	if _, err := adapter.Start(context.Background(), request("image/bmp", "image/tiff", "image/jpeg")); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(filenames, ",")
	for _, suffix := range []string{".bmp", ".tiff", ".jpg"} {
		if !strings.Contains(joined, suffix) {
			t.Fatalf("filenames %q lack %s", joined, suffix)
		}
	}
	if strings.Contains(joined, ".bin") {
		t.Fatalf("generic extension leaked: %q", joined)
	}

	_, err := adapter.Start(context.Background(), request("image/x-portable-pixmap"))
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil ||
		providerErr.Failure.Class != generation.FailureInvalidInput ||
		!strings.Contains(providerErr.Failure.SafeDetail, "扩展名") {
		t.Fatalf("unmapped type must be refused with a reason: %v", err)
	}
	if hits != 1 {
		t.Fatalf("create hits = %d; want only the mapped request", hits)
	}
}
