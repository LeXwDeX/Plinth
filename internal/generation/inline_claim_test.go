package generation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

type inlineImageAdapter struct {
	starts int
	opens  int
	data   []byte
}

func validInlineImage(t *testing.T, extraBytes int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	imageValue := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageValue.Set(0, 0, color.RGBA{R: 42, G: 84, B: 126, A: 255})
	if err := png.Encode(&encoded, imageValue); err != nil {
		t.Fatal(err)
	}
	if extraBytes == 0 {
		return encoded.Bytes()
	}
	// A large, valid ancillary chunk exercises inline-to-CAS storage without
	// representing arbitrary bytes as a malformed PNG.
	base := encoded.Bytes()
	chunkData := append([]byte("fixture\x00"), bytes.Repeat([]byte("image-payload"), extraBytes/len("image-payload")+1)...)
	chunkData = chunkData[:extraBytes+len("fixture\x00")]
	chunkType := []byte("tEXt")
	chunk := make([]byte, 8+len(chunkData)+4)
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(chunkData)))
	copy(chunk[4:8], chunkType)
	copy(chunk[8:], chunkData)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return append(append([]byte{}, base[:len(base)-12]...), append(chunk, base[len(base)-12:]...)...)
}

func (a *inlineImageAdapter) Start(_ context.Context, r generation.StartRequest) (generation.Observation, error) {
	a.starts++
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Artifacts: []generation.ArtifactClaim{{ID: "image", Role: "image", MediaType: "image/png", Inline: a.data}}}, nil
}
func (a *inlineImageAdapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	panic("unexpected observe")
}
func (a *inlineImageAdapter) OpenAsset(_ context.Context, r generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	a.opens++
	return io.NopCloser(bytes.NewReader(r.Claim.Inline)), generation.AssetMetadata{MediaType: r.Claim.MediaType, Size: int64(len(r.Claim.Inline))}, nil
}
func submitInlineImage(t *testing.T, h *harness) generation.TaskID {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{OwnerID: "inline", Capability: generation.TextToImage, OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"large image"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}
func TestNewInlineImageClaimPersistsOnlyCASReference(t *testing.T) {
	adapter := &inlineImageAdapter{data: validInlineImage(t, 1<<19)}
	h := newHarness(t, adapter, nil)
	id := submitInlineImage(t, h)
	h.reconcile(t)
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Claims) != 1 || len(aggregate.Claims[0].Inline) != 0 {
		t.Fatalf("SQLite contains inline image: claims=%d bytes=%d", len(aggregate.Claims), len(aggregate.Claims[0].Inline))
	}
	h.reconcile(t)
	task, err := h.module.Get(context.Background(), "inline", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded || adapter.opens != 0 {
		t.Fatalf("state=%s provider opens=%d", task.State, adapter.opens)
	}
	content, err := h.module.OpenAsset(context.Background(), "inline", id, task.Assets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	got, err := io.ReadAll(content.Body)
	if err != nil || !bytes.Equal(got, adapter.data) {
		t.Fatal("CAS content differs from provider output")
	}
}
func TestInlineCASWriteFailureNeverResubmitsPaidTask(t *testing.T) {
	adapter := &inlineImageAdapter{data: validInlineImage(t, 0)}
	h := newHarness(t, adapter, failingAssetStore{})
	id := submitInlineImage(t, h)
	h.reconcile(t)
	h.reconcile(t)
	task, err := h.module.Get(context.Background(), "inline", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskFailed || adapter.starts != 1 {
		t.Fatalf("state=%s upstream starts=%d", task.State, adapter.starts)
	}
}

// Run each subtest separately with /usr/bin/time -l to compare peak process RSS.
// This retains the old inline representation only as a recovery/measurement fixture.
func TestInlineClaimStorageFootprint(t *testing.T) {
	for _, mode := range []string{"direct", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "generation.db")
			repository, err := sqlite.Open(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			store, err := local.New(filepath.Join(root, "assets"))
			if err != nil {
				t.Fatal(err)
			}
			catalogValue, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			payload := validInlineImage(t, 1<<20)
			adapter := &inlineImageAdapter{data: payload}
			registry := providers.NewRegistry()
			if err := registry.Register("openai-images.v1", adapter); err != nil {
				t.Fatal(err)
			}
			module, err := generation.New(generation.Dependencies{Repository: repository, Catalog: catalogValue, Providers: registry, Assets: store})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = module.Close() })
			h := &harness{module: module, repository: repository, clock: &fakeClock{now: time.Now().UTC()}}
			accepted, err := h.submitApprovedWithModule(t, context.Background(), module, generation.SubmitCommand{OwnerID: "inline", Capability: generation.TextToImage, OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"footprint"}`)})
			if err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if mode == "direct" {
				_, err = module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{})
			} else {
				aggregate, loadErr := repository.Load(context.Background(), accepted.TaskID)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				now := time.Now().UTC()
				task := aggregate.Task
				task.Version++
				task.State = generation.TaskMaterializing
				task.CurrentAttemptID = "legacy-attempt"
				task.UpdatedAt = now
				task.NextActionAt = now
				attempt := &generation.Attempt{ID: "legacy-attempt", TaskID: task.ID, Ordinal: 1, Offering: task.Offering, State: generation.AttemptUpstreamSucceeded, SubmissionKey: "legacy", StartedAt: now, UpdatedAt: now, FinishedAt: &now}
				err = repository.Commit(context.Background(), generation.Commit{ExpectedVersion: aggregate.Task.Version, Task: task, Attempt: attempt, ReplaceClaims: true, Claims: []generation.ArtifactClaim{{ID: "legacy-image", Role: "image", MediaType: "image/png", Inline: payload}}})
			}
			if err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			db, err := sql.Open("sqlite", databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var inlineBytes, referenceBytes int64
			if err := db.QueryRow(`SELECT COALESCE(SUM(length(inline_data)),0),COALESCE(SUM(length(stored_asset_json)),0) FROM generation_artifact_claims`).Scan(&inlineBytes, &referenceBytes); err != nil {
				t.Fatal(err)
			}
			var pages, pageSize int64
			if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
				t.Fatal(err)
			}
			var walSize int64
			if info, err := os.Stat(databasePath + "-wal"); err == nil {
				walSize = info.Size()
			}
			t.Logf("mode=%s payload=%d inline=%d reference=%d sqlite_pages_bytes=%d wal_bytes=%d allocated_bytes=%d", mode, len(payload), inlineBytes, referenceBytes, pages*pageSize, walSize, after.TotalAlloc-before.TotalAlloc)
			if mode == "direct" {
				if inlineBytes != 0 || referenceBytes == 0 {
					t.Fatal("new claim stored binary payload instead of reference")
				}
				aggregate, err := repository.Load(context.Background(), accepted.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				refs, err := sqlite.BlobReferences(context.Background(), databasePath)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := refs[aggregate.Claims[0].StoredAsset.StorageKey]; !ok {
					t.Fatal("pending CAS claim missing from garbage-collection references")
				}
			} else if inlineBytes != int64(len(payload)) {
				t.Fatal("legacy fixture did not preserve inline bytes")
			}
			if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{}); err != nil {
				t.Fatal(err)
			}
			final, err := module.Get(context.Background(), "inline", accepted.TaskID)
			if err != nil || final.State != generation.TaskSucceeded {
				t.Fatalf("recovery=%s err=%v", final.State, err)
			}
		})
	}
}
