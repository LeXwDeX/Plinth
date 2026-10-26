package input_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func inputModelPolicy() modelinput.Policy {
	return modelinput.Policy{ProfileID: modelinput.ProfileID, Revision: modelinput.ProfileRevision, Limits: modelinput.Limits{MaxBytes: 10 << 20, MaxJSONBytes: 2 << 20, MaxDepth: 32, MaxElements: 100000, MaxVertices: 1000000, MaxTriangles: 1000000, MaxNodes: 10000, MaxWork: 64 << 20, MaxImagePixels: 1000000, MaxTotalImagePixels: 2000000}}
}
func inputFileCount(t *testing.T, root string) int {
	t.Helper()
	n := 0
	if err := filepath.WalkDir(root, func(_ string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			n++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestUploadValidatorRunsBeforeCASAndRegistration(t *testing.T) {
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo := &uploadRepository{}
	calls := 0
	service, err := inputassets.NewWithConfig(repo, store, inputassets.Config{UploadValidator: func(_ context.Context, f *os.File, upload generation.InputAssetUpload) error {
		calls++
		if upload.OriginalFilename != "private.glb" {
			t.Fatal("upload metadata lost")
		}
		return &modelinput.Error{Kind: modelinput.Unsupported, Reason: "animation"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	before := inputFileCount(t, root)
	_, err = service.UploadAsset(context.Background(), "owner", generation.InputAssetUpload{Body: bytes.NewReader(modelinputtest.Triangle(modelinputtest.Options{})), OriginalFilename: "private.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 10 << 20})
	var validation *modelinput.Error
	if !errors.As(err, &validation) || validation.Kind != modelinput.Unsupported {
		t.Fatalf("typed validation rejected incorrectly: %v", err)
	}
	if calls != 1 || repo.creates != 0 || inputFileCount(t, root) != before {
		t.Fatalf("calls=%d creates=%d; failed validation wrote files", calls, repo.creates)
	}
}
func TestOwnedUploadReadsRealSQLiteUploadOnly(t *testing.T) {
	root := t.TempDir()
	repo, err := sqlite.Open(filepath.Join(root, "input.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, err := inputassets.NewWithConfig(repo, store, inputassets.Config{UploadValidator: func(ctx context.Context, f *os.File, _ generation.InputAssetUpload) error {
		_, err := modelinput.ValidateGLB(ctx, f, inputModelPolicy())
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	asset, err := service.UploadAsset(context.Background(), "owner", generation.InputAssetUpload{Body: bytes.NewReader(raw), OriginalFilename: "triangle.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 10 << 20})
	if err != nil {
		t.Fatal(err)
	}
	actual, body, err := service.OpenOwnedUpload(context.Background(), "owner", asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil || !bytes.Equal(raw, got) || actual.SHA256 != asset.SHA256 || actual.Size != int64(len(raw)) {
		t.Fatal("real upload bytes or identity differ")
	}
	if _, body, err := service.OpenOwnedUpload(context.Background(), "foreign", asset.ID); err == nil || body != nil {
		t.Fatal("foreign upload opened")
	}
}
func TestOwnedUploadNeverUsesTaskOutputFallback(t *testing.T) {
	store, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo := &uploadRepository{}
	service, err := inputassets.New(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, body, err := service.OpenOwnedUpload(context.Background(), "owner", "task-output-id"); err == nil || body != nil {
		t.Fatal("Task output relabeled as native upload")
	}
	if repo.loads != 0 || repo.gets != 1 {
		t.Fatalf("wrong identity port: gets=%d fallback=%d", repo.gets, repo.loads)
	}
}

type uploadRepository struct{ creates, gets, loads int }

func (r *uploadRepository) CreateInputAsset(context.Context, generation.InputAsset) error {
	r.creates++
	return nil
}
func (r *uploadRepository) GetInputAsset(context.Context, generation.AssetID) (generation.InputAsset, error) {
	r.gets++
	return generation.InputAsset{}, &generation.Error{Code: generation.ErrNotFound}
}
func (r *uploadRepository) LoadInputAssets(context.Context, generation.OwnerID, []generation.AssetID) (map[generation.AssetID]generation.InputAsset, error) {
	r.loads++
	return map[generation.AssetID]generation.InputAsset{"task-output-id": {ID: "task-output-id", OwnerID: "owner"}}, nil
}

func TestUploadValidatorPreservesDomainError(t *testing.T) {
	store, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cause := &modelinput.Error{Kind: modelinput.Unsupported, Reason: "uri"}
	expected := &generation.Error{Code: generation.ErrorCode("unsupported_asset"), Operation: "validate_upload", Detail: "profile_unsupported", Cause: cause}
	repo := &uploadRepository{}
	service, err := inputassets.NewWithConfig(repo, store, inputassets.Config{UploadValidator: func(context.Context, *os.File, generation.InputAssetUpload) error {
		return fmt.Errorf("validator wrapper: %w", expected)
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UploadAsset(context.Background(), "owner", generation.InputAssetUpload{Body: bytes.NewReader([]byte("model")), MediaType: "model/gltf-binary", MaxBytes: 100})
	if err != expected {
		t.Fatalf("domain error replaced: %v", err)
	}
	var typed *modelinput.Error
	if !errors.As(err, &typed) || typed != cause || repo.creates != 0 {
		t.Fatalf("cause or write boundary lost: %v", err)
	}
}
