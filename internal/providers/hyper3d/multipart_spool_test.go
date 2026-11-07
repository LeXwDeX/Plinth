package hyper3d_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
)

type rodinMultipartObservation struct {
	scratchEntries int
	contentLength  int64
	rawBytes       int
	images         []string
}

// The Rodin body is spooled once (submitMultipart) and httpjson reuses that
// file (#191): during a five-image submission ScratchDir holds one scratch file,
// never two, and the upstream sees exactly the encoded bytes and their length.
func TestHyper3DMultipartFiveImagesSpoolOnce(t *testing.T) {
	scratch := t.TempDir()
	observed := make(chan rodinMultipartObservation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries, _ := os.ReadDir(scratch)
		raw, _ := io.ReadAll(r.Body)
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			t.Errorf("content type = %q (%v)", r.Header.Get("Content-Type"), err)
		}
		var images []string
		reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				t.Errorf("multipart part: %v", nextErr)
				break
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == "images" {
				images = append(images, string(data))
			}
		}
		observed <- rodinMultipartObservation{
			scratchEntries: len(entries), contentLength: r.ContentLength, rawBytes: len(raw), images: images,
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"uuid":"uuid-images","jobs":{"subscription_key":"images-key"}}`)
	}))
	t.Cleanup(server.Close)

	port, err := hyper3d.New(hyper3d.Config{
		BaseURL: server.URL + "/api/v2", APIKey: "test-key", StatusMapping: mapping(),
		HTTPClient: server.Client(), AllowHTTPAssets: true, InputAssets: imageInputs{},
		ScratchDir: scratch,
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var assets []generation.InputAsset
	var want []string
	for index := 0; index < 5; index++ {
		id := fmt.Sprintf("view-%d", index)
		ids = append(ids, id)
		assets = append(assets, generation.InputAsset{
			ID: generation.AssetID(id), MediaType: "image/png", OriginalFilename: id + ".png",
		})
		want = append(want, "image-"+id)
	}
	encoded, err := json.Marshal(map[string]any{"reference_assets": ids})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := port.Start(context.Background(), generation.StartRequest{
		AttemptID: "attempt", Offering: offering(generation.ImageTo3D), Input: encoded,
		InputAssets: map[string][]generation.InputAsset{"reference_assets": assets},
	}); err != nil {
		t.Fatal(err)
	}

	seen := <-observed
	if seen.scratchEntries != 1 {
		t.Fatalf("scratch files during the five-image submission = %d, want 1 (two before the reuse path)", seen.scratchEntries)
	}
	if seen.contentLength != int64(seen.rawBytes) {
		t.Fatalf("Content-Length = %d, got %d bytes", seen.contentLength, seen.rawBytes)
	}
	if len(seen.images) != len(want) {
		t.Fatalf("image parts = %d, want %d", len(seen.images), len(want))
	}
	for index := range want {
		if seen.images[index] != want[index] {
			t.Fatalf("image %d = %q, want %q", index, seen.images[index], want[index])
		}
	}
	if entries, _ := os.ReadDir(scratch); len(entries) != 0 {
		t.Fatalf("scratch files left behind: %v", entries)
	}
}
