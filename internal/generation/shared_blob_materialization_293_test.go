package generation_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #293: a Provider may fulfil two slots of one task from the same file. Tripo's
// pre-completion flow does it for a GLTF export: its self-contained GLB is both
// the converted.gltf slot and the model slot, so the two claims carry one locator.
// Materialization writes one asset record per slot; the CAS keeps one blob.

// sharedLocatorAdapter completes at once with two claims that name the same file.
type sharedLocatorAdapter struct {
	body []byte
}

func (a sharedLocatorAdapter) Start(_ context.Context, request generation.StartRequest) (generation.Observation, error) {
	locator := "https://assets.example.test/" + string(request.AttemptID) + "/gltf.glb"
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Job:   &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: "shared-" + string(request.AttemptID)},
		Artifacts: []generation.ArtifactClaim{
			{ID: string(request.AttemptID) + ":convert:GLTF", Role: "converted_model", Variant: "converted.gltf", Locator: locator, MediaType: "model/gltf-binary"},
			{ID: string(request.AttemptID) + ":model", Role: "model", Locator: locator, MediaType: "model/gltf-binary"},
		},
	}, nil
}

func (sharedLocatorAdapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

// OpenAsset serves the shared file for the claims that name it; the harness adds
// inline fixture claims for the other formats, which carry their own bytes.
func (a sharedLocatorAdapter) OpenAsset(_ context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	body := request.Claim.Inline
	if request.Claim.Locator != "" {
		body = a.body
	}
	return io.NopCloser(bytes.NewReader(body)), generation.AssetMetadata{MediaType: request.Claim.MediaType, Size: int64(len(body))}, nil
}

func slotAsset(t *testing.T, assets []generation.Asset, role, variant string) generation.Asset {
	t.Helper()
	for _, asset := range assets {
		if asset.Role == role && asset.Variant == variant {
			return asset
		}
	}
	t.Fatalf("no %s/%q asset in %+v", role, variant, assets)
	return generation.Asset{}
}

// storedBlobFiles lists the regular files of the harness's CAS (its root sits
// beside the database).
func storedBlobFiles(t *testing.T, harness *harness) []string {
	t.Helper()
	root := filepath.Join(filepath.Dir(harness.dbPath), "assets")
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "blobs"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestTwoSlotsFromOneFileMaterializeOverOneStoredBlob(t *testing.T) {
	shared := validGLB("tripo gltf conversion")
	harness := newHarness(t, sharedLocatorAdapter{body: shared}, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"a brass robot","authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		detail := ""
		if task.Failure != nil {
			detail = task.Failure.SafeDetail + " / " + task.Failure.UpstreamExcerpt
		}
		events, _ := harness.module.Events(context.Background(), "owner-a", accepted.TaskID, 0, 100)
		t.Fatalf("state = %s, failure = %q, events = %v", task.State, detail, eventTypes(events))
	}

	model := slotAsset(t, task.Assets, "model", "")
	converted := slotAsset(t, task.Assets, "converted_model", "converted.gltf")

	// Two records with their own identity, one stored file.
	if model.ID == converted.ID || !model.Required || !converted.Required {
		t.Fatalf("slot records: model=%+v converted=%+v", model, converted)
	}
	if model.StorageKey != converted.StorageKey || model.SHA256 != converted.SHA256 ||
		model.Size != converted.Size || model.Size != int64(len(shared)) {
		t.Fatalf("slots did not share a blob: model=%+v converted=%+v", model, converted)
	}
	distinct := map[string]struct{}{}
	for _, asset := range task.Assets {
		distinct[asset.StorageKey] = struct{}{}
	}
	if len(task.Assets) <= len(distinct) {
		t.Fatalf("expected fewer blobs than assets: %d assets over %d keys", len(task.Assets), len(distinct))
	}
	files := storedBlobFiles(t, harness)
	if len(files) != len(distinct) {
		t.Fatalf("%d stored files for %d distinct keys: %v", len(files), len(distinct), files)
	}
	copies := 0
	for _, file := range files {
		if file == model.StorageKey {
			copies++
		}
	}
	if copies != 1 {
		t.Fatalf("shared blob %s is stored %d times", model.StorageKey, copies)
	}

	// Each slot downloads on its own, by its own asset ID, with the bytes it was given.
	for _, asset := range []generation.Asset{model, converted} {
		content, err := harness.module.OpenAsset(context.Background(), "owner-a", task.ID, asset.ID)
		if err != nil {
			t.Fatalf("open %s/%s: %v", asset.Role, asset.Variant, err)
		}
		data, readErr := io.ReadAll(content.Body)
		_ = content.Body.Close()
		if readErr != nil || !bytes.Equal(data, shared) {
			t.Fatalf("%s/%s read %d bytes (err=%v)", asset.Role, asset.Variant, len(data), readErr)
		}
		if content.Role != asset.Role || content.Variant != asset.Variant || content.MediaType != "model/gltf-binary" || content.SHA256 != model.SHA256 {
			t.Fatalf("%s/%s content = role %q variant %q type %q sha %q", asset.Role, asset.Variant, content.Role, content.Variant, content.MediaType, content.SHA256)
		}
	}

	// The reference snapshot maintenance reads lists both records under the one key,
	// so the key stays referenced while either of them is.
	records, err := sqlite.BlobReferenceRecords(context.Background(), harness.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	holders := map[string]bool{}
	for _, record := range records {
		if record.StorageKey == model.StorageKey {
			holders[record.SourceID] = true
		}
	}
	if len(holders) != 2 || !holders[string(model.ID)] || !holders[string(converted.ID)] {
		t.Fatalf("records referencing %s = %v, want both slots", model.StorageKey, holders)
	}
	references, err := sqlite.BlobReferences(context.Background(), harness.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	options := local.MaintenanceOptions{
		Root: filepath.Join(filepath.Dir(harness.dbPath), "assets"), MinimumAge: time.Hour, Now: time.Now().Add(48 * time.Hour),
		ReferenceBasis: "fixture",
		References:     func(context.Context) (map[string]struct{}, error) { return references, nil },
	}
	report, err := local.Maintain(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.BlobCount != len(distinct) || report.OrphanCount != 0 {
		t.Fatalf("maintenance on the live snapshot: %+v", report)
	}
	// Were every record gone, the shared blob would be one candidate, not two.
	options.References = func(context.Context) (map[string]struct{}, error) { return map[string]struct{}{}, nil }
	report, err = local.Maintain(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	listed := 0
	for _, candidate := range report.Candidates {
		if candidate.StorageKey == model.StorageKey {
			listed++
		}
	}
	if report.OrphanCount != len(distinct) || listed != 1 {
		t.Fatalf("maintenance without references: orphans %d (want %d), shared blob listed %d times", report.OrphanCount, len(distinct), listed)
	}
}
