package hyper3d_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A Task output carries no original filename. Its multipart part must still be
// named from the asset's verified media type (Hyper3D reads the format from the
// filename), never from the host's mime table or a generic upload.bin.
func TestModelSourceWithoutOriginalFilenameUploadsVerifiedExtension(t *testing.T) {
	for _, id := range []generation.OfferingID{"hyper3d/bang/local-model", "hyper3d/texture/local-model"} {
		for name, model := range map[string]generation.InputAsset{
			"task output":         {ID: "model-owned", OwnerID: "owner", MediaType: "model/gltf-binary", Role: "model", Variant: "glb"},
			"name without suffix": {ID: "model-owned", OwnerID: "owner", MediaType: "model/gltf-binary", OriginalFilename: "scan"},
			"mismatched suffix":   {ID: "model-owned", OwnerID: "owner", MediaType: "model/gltf-binary", OriginalFilename: "scan.bin"},
		} {
			t.Run(string(id)+"/"+name, func(t *testing.T) {
				step := nativeCandidate(t, id)
				calls := 0
				var modelName, imageName string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return
					}
					if files := r.MultipartForm.File["model"]; len(files) == 1 {
						modelName = files[0].Filename
					}
					if files := r.MultipartForm.File["image"]; len(files) == 1 {
						imageName = files[0].Filename
					}
					_, _ = io.WriteString(w, `{"uuid":"created","jobs":{"subscription_key":"subscription"}}`)
				}))
				defer server.Close()
				request := generation.StartRequest{
					OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: step,
					Input: json.RawMessage(`{"source_asset":"model-owned","reference_image":"image-owned"}`),
					InputAssets: map[string][]generation.InputAsset{
						"source_asset":    {model},
						"reference_image": {{ID: "image-owned", OwnerID: "owner", MediaType: "image/png"}},
					},
				}
				port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
				result, err := port.Start(context.Background(), request)
				if err != nil || result.Job == nil || calls != 1 {
					t.Fatalf("submission: %+v %v calls=%d", result, err, calls)
				}
				if !strings.HasSuffix(modelName, ".glb") || !strings.HasSuffix(imageName, ".png") {
					t.Fatalf("filenames model=%q image=%q", modelName, imageName)
				}
			})
		}
	}
}

// When the verified media type has no extension the Offering declares, the
// request is refused before any upstream call with a reason naming the gap.
func TestModelSourceWithoutDeclaredExtensionIsRefusedBeforeDispatch(t *testing.T) {
	step := nativeCandidate(t, "hyper3d/texture/local-model")
	for index := range step.Parameters {
		if step.Parameters[index].Name == "source_asset" {
			parameter := &step.Parameters[index]
			parameter.MediaTypes = append(append([]string(nil), parameter.MediaTypes...), "model/obj")
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"uuid":"created","jobs":{"subscription_key":"subscription"}}`)
	}))
	defer server.Close()
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	result, err := port.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: step,
		Input: json.RawMessage(`{"source_asset":"model-owned","reference_image":"image-owned"}`),
		InputAssets: map[string][]generation.InputAsset{
			"source_asset":    {{ID: "model-owned", OwnerID: "owner", MediaType: "model/obj", Role: "model", Variant: "obj"}},
			"reference_image": {{ID: "image-owned", OwnerID: "owner", MediaType: "image/png"}},
		},
	})
	if err == nil || result.Job != nil || calls != 0 {
		t.Fatalf("undeclared extension reached upstream: %+v %v calls=%d", result, err, calls)
	}
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Failure == nil ||
		providerErr.Failure.Class != generation.FailureInvalidInput ||
		!strings.Contains(providerErr.Failure.SafeDetail, "扩展名") {
		t.Fatalf("refusal must name the missing extension: %v", err)
	}
}
