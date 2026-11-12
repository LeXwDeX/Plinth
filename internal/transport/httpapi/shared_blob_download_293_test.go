package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// sharedGLBTripoAdapter delivers a Tripo full-format task the way the real flow does
// for its GLTF export: the converted.gltf slot and the model slot carry one and the
// same GLB (#293).
type sharedGLBTripoAdapter struct {
	*httpMeshyFormatFixtureAdapter
}

func (a *sharedGLBTripoAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	observation, err := a.httpMeshyFormatFixtureAdapter.Observe(ctx, request)
	if err != nil || observation.Phase != generation.UpstreamCompleted || request.PostSuccess ||
		request.Offering.ID != "tripo/tripo-v3.1-full/text-to-3d" {
		return observation, err
	}
	var model *generation.ArtifactClaim
	for index := range observation.Artifacts {
		if observation.Artifacts[index].Role == "model" {
			model = &observation.Artifacts[index]
		}
	}
	if model == nil {
		return observation, errors.New("the fixture delivered no model claim")
	}
	for index := range observation.Artifacts {
		claim := &observation.Artifacts[index]
		if claim.Role == "converted_model" && claim.Variant == "converted.gltf" {
			claim.Inline, claim.MediaType = model.Inline, model.MediaType
		}
	}
	return observation, nil
}

// Two slots over one blob stay two deliverables: each is read by its own asset ID
// under the owner's scope, and is named and typed by its own record, not by the file.
func TestSlotsSharingOneBlobDownloadIndependently(t *testing.T) {
	var root string
	adapter := &sharedGLBTripoAdapter{&httpMeshyFormatFixtureAdapter{Adapter: mock.New()}}
	module, handler, _, _ := newHTTPHarnessWithCatalogAndProviders(t, nil, nil, nil, nil, nil, nil,
		map[string]generation.ProviderPort{"tripo.v3": adapter},
		func(t *testing.T, database string) { root = filepath.Dir(database) })
	task := createSucceededTripoModelTask(t, module, "owner-a")
	slot := func(role, variant string) generation.Asset {
		t.Helper()
		for _, asset := range task.Assets {
			if asset.Role == role && asset.Variant == variant {
				return asset
			}
		}
		t.Fatalf("no %s/%q asset in %+v", role, variant, task.Assets)
		return generation.Asset{}
	}
	model, gltf := slot("model", ""), slot("converted_model", "converted.gltf")
	if model.ID == gltf.ID || model.StorageKey != gltf.StorageKey || model.SHA256 != gltf.SHA256 {
		t.Fatalf("the slots are not two records over one blob: model=%+v gltf=%+v", model, gltf)
	}

	// One stored file for the pair.
	copies := 0
	err := filepath.WalkDir(filepath.Join(root, "storage", "blobs"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && entry.Type().IsRegular() && filepath.Base(path) == filepath.Base(model.StorageKey) {
			copies++
		}
		return walkErr
	})
	if err != nil || copies != 1 {
		t.Fatalf("the shared blob is stored %d times (err=%v)", copies, err)
	}

	var shared []byte
	for _, asset := range []generation.Asset{model, gltf} {
		target := "/api/v1/tasks/" + string(task.ID) + "/assets/" + string(asset.ID)
		response := modelDeliveryRequest(handler, http.MethodGet, target, "owner-a")
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("%s/%s: %d %v", asset.Role, asset.Variant, response.Code, response.Header())
		}
		if shared == nil {
			shared = response.Body.Bytes()
		} else if !bytes.Equal(shared, response.Body.Bytes()) {
			t.Fatalf("%s/%s returned other bytes than its twin", asset.Role, asset.Variant)
		}
		// Both records are GLB files for the Schema's format table, so the name and
		// the type come out as that format's, per record.
		if got := response.Header().Get("Content-Type"); got != "model/gltf-binary" {
			t.Fatalf("%s/%s content type = %q", asset.Role, asset.Variant, got)
		}
		disposition, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
		if err != nil || disposition != "attachment" || params["filename"] != "model-"+string(task.ID)+".glb" {
			t.Fatalf("%s/%s disposition = %q (err=%v)", asset.Role, asset.Variant, response.Header().Get("Content-Disposition"), err)
		}
		if etag := response.Header().Get("ETag"); etag != `"`+asset.SHA256+`"` {
			t.Fatalf("%s/%s ETag = %q", asset.Role, asset.Variant, etag)
		}
		// Sharing the file does not widen access: another owner gets nothing from either record.
		if foreign := modelDeliveryRequest(handler, http.MethodGet, target, "owner-b"); foreign.Code != http.StatusNotFound || bytes.Contains(foreign.Body.Bytes(), shared) {
			t.Fatalf("%s/%s foreign read = %d", asset.Role, asset.Variant, foreign.Code)
		}
	}
}
